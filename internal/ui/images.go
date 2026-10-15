package ui

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ducnt114/zdocker/internal/docker"
)

type imagesLoadedMsg struct {
	items []docker.Image
	gen   int
	err   error
}

// imagesView is the table of images (one row per tag).
type imagesView struct {
	table  filterTable
	client docker.Client
	items  map[string]docker.Image // by imageRowKey
	order  []docker.Image          // as listed, for re-rendering
	loaded bool
	gen    int
	now    func() time.Time
}

func newImagesView(c docker.Client) imagesView {
	return imagesView{table: newFilterTable(), client: c, now: time.Now}
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

// imageRowKey identifies a row: an image ID alone isn't unique (several
// tags give several rows).
func imageRowKey(i docker.Image) string { return i.ID + " " + i.Repository + ":" + i.Tag }

func (v imagesView) selected() (docker.Image, bool) {
	k, ok := v.table.Selected()
	if !ok {
		return docker.Image{}, false
	}
	img, ok := v.items[k]
	return img, ok
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
		v.order, v.loaded = msg.items, true
		v.items = make(map[string]docker.Image, len(msg.items))
		for _, img := range msg.items {
			v.items[imageRowKey(img)] = img
		}
		v.table.SetRows(v.rows())
		return v, nil

	case ageTickMsg:
		v.table.SetRows(v.rows())
		return v, nil

	case tea.KeyMsg:
		if !v.table.Capturing() {
			switch {
			case key.Matches(msg, containerKeyMap.Refresh):
				return v, v.load()
			case key.Matches(msg, inspectKey):
				if img, ok := v.selected(); ok {
					return v, func() tea.Msg { return openInspectMsg{kind: docker.KindImage, id: img.ID, name: img.Ref()} }
				}
				return v, nil
			}
		}
		var cmd tea.Cmd
		v.table, cmd = v.table.Update(msg)
		return v, cmd
	}
	return v, nil
}

// rows: SIZE sorts by bytes (not "187MB" vs "5kB"), AGE by creation time.
func (v imagesView) rows() []tableRow {
	now := v.now()
	rows := make([]tableRow, len(v.order))
	for i, img := range v.order {
		inUse := "-"
		if img.Containers > 0 {
			inUse = strconv.Itoa(img.Containers)
		}
		// Unused images are the ones `docker image prune -a` would remove:
		// show them muted.
		style := healthStyles[healthy]
		if img.Containers == 0 {
			style = healthStyles[finished]
		}
		rows[i] = tableRow{
			key:    imageRowKey(img),
			cells:  []string{img.Repository, img.Tag, img.ShortID(), formatSize(img.Size), inUse, formatAge(now.Sub(img.Created))},
			sortBy: []string{3: fmt.Sprintf("%020d", img.Size), 4: strconv.Itoa(img.Containers), 5: ageSortKey(img.Created)},
			filter: img.Repository + ":" + img.Tag + " " + img.ShortID(),
			style:  style,
		}
	}
	return rows
}

func (v *imagesView) SetSize(w, h int) {
	v.table.SetSize(w, h)
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
	for _, img := range v.order {
		if !seen[img.ID] { // count each image once, not once per tag
			seen[img.ID] = true
			total += img.Size
		}
	}
	note := "loading…"
	if v.loaded {
		note = fmt.Sprintf("%d images, %s", len(seen), formatSize(total))
	}
	return v.table.View("Images", note)
}

func (v imagesView) capturingInput() bool { return v.table.Capturing() }

func (v imagesView) keys() []key.Binding {
	k := containerKeyMap
	return append([]key.Binding{k.Up, k.Down, inspectKey, k.Refresh}, v.table.Keys()...)
}
