package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	Up, Down, Logs, All, Refresh key.Binding
}{
	Logs:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "logs")),
	Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	All:     key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all/running")),
	Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
}

// containersView is the table of containers.
type containersView struct {
	table   tableModel
	client  docker.Client
	items   []docker.Container
	showAll bool // like `docker ps -a`
	loaded  bool
	gen     int // bumped on every load; late results of older loads are dropped
	width   int
	now     func() time.Time
}

func newContainersView(c docker.Client) containersView {
	return containersView{table: newTableModel(), client: c, showAll: true, now: time.Now}
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
		// Remember the selection by ID *before* replacing the items: the
		// cursor index means nothing in the new list.
		selected := v.selectedID()
		v.items, v.loaded = msg.items, true
		v.setRows(selected)
		return v, nil

	case ageTickMsg:
		v.refreshRows()
		return v, ageTick()

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, containerKeyMap.All):
			v.showAll = !v.showAll
			return v, v.load()
		case key.Matches(msg, containerKeyMap.Refresh):
			return v, v.load()
		case key.Matches(msg, containerKeyMap.Logs):
			if c, ok := v.selected(); ok {
				return v, func() tea.Msg { return openLogsMsg{container: c} }
			}
			return v, nil
		}
		var cmd tea.Cmd
		v.table, cmd = v.table.Update(msg)
		return v, cmd
	}
	return v, nil
}

// selected returns the container under the cursor.
func (v containersView) selected() (docker.Container, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.items) {
		return docker.Container{}, false
	}
	return v.items[i], true
}

func (v containersView) selectedID() string {
	c, _ := v.selected()
	return c.ID
}

// refreshRows re-renders the current items (e.g. to update AGE).
func (v *containersView) refreshRows() { v.setRows(v.selectedID()) }

// setRows renders the items, putting the cursor on selectedID if present.
func (v *containersView) setRows(selectedID string) {
	now := v.now()
	rows := make([][]string, len(v.items))
	styles := make([]lipgloss.Style, len(v.items))
	cursor := 0
	for i, c := range v.items {
		rows[i] = []string{c.Name, c.Image, c.State, c.Status, c.Ports, formatAge(now.Sub(c.Created))}
		styles[i] = healthStyles[containerHealth(c)]
		if c.ID == selectedID {
			cursor = i
		}
	}
	v.table.SetRows(rows, styles)
	v.table.SetCursor(cursor)
}

func (v *containersView) SetSize(w, h int) {
	v.width = w
	v.table.SetWidth(w)
	v.table.SetHeight(max(h-1, 1)) // one line for the title
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
	cols := append([]tableColumn{{Title: "NAME", Width: max(w-used-2-2, 16)}}, fixed...)
	v.table.SetColumns(cols)
}

func (v containersView) View() string {
	scope := "all"
	if !v.showAll {
		scope = "running"
	}
	title := titleStyle.Render(fmt.Sprintf("Containers(%s)[%d]", scope, len(v.items)))
	if !v.loaded {
		title += statusStyle.Render("  loading…")
	}
	return lipgloss.JoinVertical(lipgloss.Left, title, v.table.View())
}

func (v containersView) capturingInput() bool { return false }

func (v containersView) keys() []key.Binding {
	k := containerKeyMap
	return []key.Binding{k.Up, k.Down, k.Logs, k.All, k.Refresh}
}
