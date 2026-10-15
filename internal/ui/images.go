package ui

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/zdocker/internal/docker"
)

type imagesLoadedMsg struct {
	items []docker.Image
	gen   int
	err   error
}

// imagesView is the table of images (one row per tag).
type imagesView struct {
	table  tableModel
	client docker.Client
	items  []docker.Image
	loaded bool
	gen    int
	now    func() time.Time
}

func newImagesView(c docker.Client) imagesView {
	return imagesView{table: newTableModel(), client: c, now: time.Now}
}

func (v *imagesView) load() tea.Cmd {
	v.gen++
	c, gen := v.client, v.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		items, err := c.ListImages(ctx)
		return imagesLoadedMsg{items: items, gen: gen, err: err}
	}
}

// rowKey identifies a row: an image ID alone isn't unique (several tags).
func imageRowKey(i docker.Image) string { return i.ID + " " + i.Repository + ":" + i.Tag }

func (v imagesView) selected() (docker.Image, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.items) {
		return docker.Image{}, false
	}
	return v.items[i], true
}

func (v imagesView) selectedKey() string {
	if img, ok := v.selected(); ok {
		return imageRowKey(img)
	}
	return ""
}

func (v imagesView) Update(msg tea.Msg) (imagesView, tea.Cmd) {
	switch msg := msg.(type) {
	case imagesLoadedMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		if msg.err != nil {
			return v, reportErr(msg.err)
		}
		selected := v.selectedKey() // before replacing the items
		v.items, v.loaded = msg.items, true
		v.setRows(selected)
		return v, nil

	case ageTickMsg:
		v.setRows(v.selectedKey())
		return v, nil

	case tea.KeyMsg:
		if key.Matches(msg, containerKeyMap.Refresh) {
			return v, v.load()
		}
		var cmd tea.Cmd
		v.table, cmd = v.table.Update(msg)
		return v, cmd
	}
	return v, nil
}

func (v *imagesView) setRows(selected string) {
	now := v.now()
	rows := make([][]string, len(v.items))
	styles := make([]lipgloss.Style, len(v.items))
	cursor := 0
	for i, img := range v.items {
		inUse := "-"
		if img.Containers > 0 {
			inUse = strconv.Itoa(img.Containers)
		}
		rows[i] = []string{img.Repository, img.Tag, img.ShortID(), formatSize(img.Size), inUse, formatAge(now.Sub(img.Created))}
		// Unused images are the ones `docker image prune` would consider:
		// show them muted, dangling ones too.
		styles[i] = healthStyles[healthy]
		if img.Containers == 0 {
			styles[i] = healthStyles[finished]
		}
		if imageRowKey(img) == selected {
			cursor = i
		}
	}
	v.table.SetRows(rows, styles)
	v.table.SetCursor(cursor)
}

func (v *imagesView) SetSize(w, h int) {
	v.table.SetWidth(w)
	v.table.SetHeight(max(h-1, 1))
	fixed := []tableColumn{
		{Title: "TAG", Width: 20},
		{Title: "IMAGE ID", Width: 12},
		{Title: "SIZE", Width: 8},
		{Title: "IN USE", Width: 6},
		{Title: "AGE", Width: 7},
	}
	used := 0
	for _, c := range fixed {
		used += c.Width + 2
	}
	v.table.SetColumns(append([]tableColumn{{Title: "REPOSITORY", Width: max(w-used-2-2, 20)}}, fixed...))
}

func (v imagesView) View() string {
	var total int64
	seen := map[string]bool{}
	for _, img := range v.items {
		if !seen[img.ID] { // count each image once, not once per tag
			seen[img.ID] = true
			total += img.Size
		}
	}
	title := titleStyle.Render(fmt.Sprintf("Images[%d]", len(v.items)))
	if v.loaded {
		title += statusStyle.Render(fmt.Sprintf("  %d images, %s", len(seen), formatSize(total)))
	} else {
		title += statusStyle.Render("  loading…")
	}
	return lipgloss.JoinVertical(lipgloss.Left, title, v.table.View())
}

func (v imagesView) capturingInput() bool { return false }

func (v imagesView) keys() []key.Binding {
	k := containerKeyMap
	return []key.Binding{k.Up, k.Down, k.Refresh}
}
