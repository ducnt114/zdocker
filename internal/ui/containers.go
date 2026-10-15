package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ducnt114/zdocker/internal/docker"
)

// ageRefreshInterval re-renders the AGE column (a UI timer; the daemon is
// not asked anything).
const ageRefreshInterval = 5 * time.Second

type (
	containersLoadedMsg struct {
		items []docker.Container
		gen   int
		err   error
	}
	ageTickMsg struct{}
)

var containerKeyMap = struct {
	Up, Down, Logs, All, Refresh, Group key.Binding
}{
	Group: key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "group by compose project")),
	// Refresh is the shared refreshKey (r).
	Logs:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "logs")),
	Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	All:     key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all/running")),
	Refresh: refreshKey,
}

// containersView is the table of containers.
type containersView struct {
	table   filterTable
	client  docker.Client
	items   map[string]docker.Container // by ID
	showAll bool                        // like `docker ps -a`
	loaded  bool
	// grouped shows containers under their Docker Compose project, with
	// a header row per project; collapsed projects show only the header.
	grouped   bool
	collapsed map[string]bool
	gen     int // bumped on every load; late results of older loads are dropped
	now     func() time.Time
}

func newContainersView(c docker.Client) containersView {
	return containersView{table: newFilterTable(), client: c, showAll: true, now: time.Now, collapsed: map[string]bool{}}
}

// load lists the containers off the UI goroutine.
func (v *containersView) load() tea.Cmd {
	v.gen++
	c, all, gen := v.client, v.showAll, v.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		items, err := c.ListContainers(ctx, all)
		return containersLoadedMsg{items: items, gen: gen, err: err}
	}
}

func ageTick() tea.Cmd {
	return tea.Tick(ageRefreshInterval, func(time.Time) tea.Msg { return ageTickMsg{} })
}

func (v containersView) Update(msg tea.Msg) (containersView, tea.Cmd) {
	switch msg := msg.(type) {
	case containersLoadedMsg:
		if msg.gen != v.gen {
			return v, nil // an older load finished after a newer one started
		}
		if msg.err != nil {
			return v, reportErr(msg.err)
		}
		v.items = make(map[string]docker.Container, len(msg.items))
		for _, c := range msg.items {
			v.items[c.ID] = c
		}
		v.loaded = true
		v.refresh() // keeps the selection by ID
		return v, nil

	case ageTickMsg:
		v.refresh()
		return v, ageTick()

	case tea.KeyMsg:
		if !v.table.Capturing() {
			switch {
			case key.Matches(msg, containerKeyMap.All):
				v.showAll = !v.showAll
				return v, v.load()
			case key.Matches(msg, containerKeyMap.Refresh):
				return v, v.load()
			case key.Matches(msg, containerKeyMap.Group):
				v.grouped = !v.grouped
				v.refresh()
				return v, nil
			case key.Matches(msg, containerKeyMap.Logs) && v.onHeader():
				// enter on a project header folds or unfolds it.
				k, _ := v.table.Selected()
				project := strings.TrimPrefix(k, groupKeyPrefix)
				v.collapsed[project] = !v.collapsed[project]
				v.refresh()
				return v, nil
			case key.Matches(msg, containerKeyMap.Logs):
				if c, ok := v.selected(); ok {
					return v, func() tea.Msg { return openLogsMsg{container: c} }
				}
				return v, nil
			case key.Matches(msg, inspectKey):
				if c, ok := v.selected(); ok {
					return v, func() tea.Msg { return openInspectMsg{kind: docker.KindContainer, id: c.ID, name: c.Name} }
				}
				return v, nil
			case key.Matches(msg, statsKey):
				if c, ok := v.selected(); ok {
					return v, func() tea.Msg { return openStatsMsg{container: c} }
				}
				return v, nil
			}
			if c, ok := v.selected(); ok {
				if a, ok := containerAction(msg, c); ok {
					return v, request(a)
				}
			}
		}
		var cmd tea.Cmd
		v.table, cmd = v.table.Update(msg)
		return v, cmd
	}
	return v, nil
}

// selected returns the container under the cursor.
func (v containersView) selected() (docker.Container, bool) {
	id, ok := v.table.Selected()
	if !ok {
		return docker.Container{}, false
	}
	c, ok := v.items[id]
	return c, ok
}

// groupKeyPrefix marks the keys of project header rows.
const groupKeyPrefix = "group:"

// noProject groups containers not started by Docker Compose.
const noProject = "(no project)"

func (v containersView) onHeader() bool {
	k, ok := v.table.Selected()
	return ok && strings.HasPrefix(k, groupKeyPrefix)
}

// refresh re-renders the rows (e.g. so AGE moves on).
func (v *containersView) refresh() {
	items := make([]docker.Container, 0, len(v.items))
	for _, c := range v.items {
		items = append(items, c)
	}
	slices.SortFunc(items, func(a, b docker.Container) int { return strings.Compare(a.Name, b.Name) })
	if v.grouped {
		v.table.SetRows(v.groupedRows(items))
	} else {
		v.table.SetRows(v.rows(items))
	}
}

// groupedRows puts containers under one header per compose project, the
// projects in alphabetical order and "(no project)" last.
func (v containersView) groupedRows(items []docker.Container) []tableRow {
	byProject := map[string][]docker.Container{}
	var projects []string
	for _, c := range items {
		p := c.Project
		if p == "" {
			p = noProject
		}
		if _, ok := byProject[p]; !ok {
			projects = append(projects, p)
		}
		byProject[p] = append(byProject[p], c)
	}
	slices.SortFunc(projects, func(a, b string) int {
		switch {
		case a == noProject:
			return 1
		case b == noProject:
			return -1
		}
		return strings.Compare(a, b)
	})

	var rows []tableRow
	for _, p := range projects {
		members := byProject[p]
		running, names := 0, []string{p}
		for _, c := range members {
			if c.State == "running" {
				running++
			}
			names = append(names, c.Name, c.Image)
		}
		arrow := "▾"
		if v.collapsed[p] {
			arrow = "▸"
		}
		style := headerRowStyle
		if running < len(members) && p != noProject {
			style = style.Foreground(healthStyles[pending].GetForeground()) // a service is down
		}
		rows = append(rows, tableRow{
			key:    groupKeyPrefix + p,
			cells:  []string{arrow + " " + p, "", "", fmt.Sprintf("%d/%d running", running, len(members)), "", ""},
			filter: strings.Join(names, " "), // a folded group still matches its members
			style:  style,
			group:  p,
			header: true,
		})
		if v.collapsed[p] {
			continue
		}
		for _, r := range v.rows(members) {
			r.group = p
			r.cells[0] = "  " + r.cells[0]
			rows = append(rows, r)
		}
	}
	return rows
}

// rows turns containers into table rows. AGE sorts by creation time and
// the filter searches name, image and compose project.
func (v containersView) rows(items []docker.Container) []tableRow {
	now := v.now()
	rows := make([]tableRow, len(items))
	for i, c := range items {
		rows[i] = tableRow{
			key:    c.ID,
			cells:  []string{c.Name, c.Image, c.State, c.Status, c.Ports, formatAge(now.Sub(c.Created))},
			sortBy: []string{5: ageSortKey(c.Created)},
			filter: c.Name + " " + c.Image + " " + c.Project,
			style:  healthStyles[containerHealth(c)],
		}
	}
	return rows
}

func (v *containersView) SetSize(w, h int) {
	v.table.SetSize(w, h)
	fixed := []tableColumn{
		{Title: "IMAGE", Width: 24},
		{Title: "STATE", Width: 10},
		{Title: "STATUS", Width: 28},
		{Title: "PORTS", Width: 26},
		{Title: "AGE", Width: 7},
	}
	used := 0
	for _, c := range fixed {
		used += c.Width + 2
	}
	v.table.SetColumns(append([]tableColumn{{Title: "NAME", Width: max(w-used-2-2, 16)}}, fixed...))
}

func (v containersView) View() string {
	scope := "all"
	if !v.showAll {
		scope = "running"
	}
	note := ""
	if !v.loaded {
		note = "loading…"
	} else if v.grouped {
		note = "grouped by compose project"
	}
	return v.table.View("Containers("+scope+")", note)
}

func (v containersView) capturingInput() bool { return v.table.Capturing() }

func (v containersView) keys(readOnly bool) []key.Binding {
	k := containerKeyMap
	b := []key.Binding{k.Up, k.Down, k.Logs, inspectKey, statsKey}
	if !readOnly {
		a := actionKeys
		b = append(b, a.Start, a.Stop, a.Restart, a.Remove)
	}
	b = append(b, k.All, k.Group, k.Refresh)
	return append(b, v.table.Keys()...)
}
