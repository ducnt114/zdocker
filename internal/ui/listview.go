package ui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ducnt114/zdocker/internal/docker"
)

// listSpec describes a simple list view: everything that differs between
// the volumes and networks tables. listView does the rest.
type listSpec[T any] struct {
	title   string
	load    func(ctx context.Context, c docker.Client) ([]T, error)
	key     func(T) string
	row     func(item T, now time.Time) tableRow // row.key is set from key()
	columns func(width int) []tableColumn
	note    func(items []T) string     // shown after the title
	inspect func(T) openInspectMsg     // i
	remove  func(T) (actionMsg, error) // D; error = refused before asking
}

// listLoadedMsg is generic too: listLoadedMsg[docker.Volume] and
// listLoadedMsg[docker.Network] are different types, so a type switch
// routes each to its own view.
type listLoadedMsg[T any] struct {
	items []T
	gen   int
	err   error
}

// listView is a live, filterable, sortable table of T.
type listView[T any] struct {
	spec   listSpec[T]
	table  filterTable
	client docker.Client
	items  map[string]T
	order  []T
	loaded bool
	gen    int
	now    func() time.Time
}

func newListView[T any](c docker.Client, spec listSpec[T]) listView[T] {
	return listView[T]{spec: spec, table: newFilterTable(), client: c, now: time.Now}
}

func (v *listView[T]) load() tea.Cmd {
	v.gen++
	c, gen, load := v.client, v.gen, v.spec.load
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		items, err := load(ctx, c)
		return listLoadedMsg[T]{items: items, gen: gen, err: err}
	}
}

func (v listView[T]) selected() (T, bool) {
	k, ok := v.table.Selected()
	item, found := v.items[k]
	return item, ok && found
}

func (v listView[T]) rows() []tableRow {
	now := v.now()
	rows := make([]tableRow, len(v.order))
	for i, item := range v.order {
		rows[i] = v.spec.row(item, now)
		rows[i].key = v.spec.key(item)
	}
	return rows
}

func (v listView[T]) Update(msg tea.Msg) (listView[T], tea.Cmd) {
	switch msg := msg.(type) {
	case listLoadedMsg[T]:
		if msg.gen != v.gen {
			return v, nil
		}
		if msg.err != nil {
			return v, reportErr(msg.err)
		}
		v.order, v.loaded = msg.items, true
		v.items = make(map[string]T, len(msg.items))
		for _, item := range msg.items {
			v.items[v.spec.key(item)] = item
		}
		v.table.SetRows(v.rows())
		return v, nil

	case ageTickMsg:
		v.table.SetRows(v.rows())
		return v, nil

	case tea.KeyMsg:
		if !v.table.Capturing() {
			item, ok := v.selected()
			switch {
			case key.Matches(msg, refreshKey):
				return v, v.load()
			case key.Matches(msg, inspectKey) && ok:
				m := v.spec.inspect(item)
				return v, func() tea.Msg { return m }
			case key.Matches(msg, actionKeys.Remove) && ok:
				a, err := v.spec.remove(item)
				if err != nil {
					return v, reportErr(err)
				}
				return v, request(a)
			}
		}
		var cmd tea.Cmd
		v.table, cmd = v.table.Update(msg)
		return v, cmd
	}
	return v, nil
}

func (v *listView[T]) SetSize(w, h int) {
	v.table.SetSize(w, h)
	v.table.SetColumns(v.spec.columns(w))
}

func (v listView[T]) View() string {
	note := "loading…"
	if v.loaded && v.spec.note != nil {
		note = v.spec.note(v.order)
	} else if v.loaded {
		note = ""
	}
	return v.table.View(v.spec.title, note)
}

func (v listView[T]) capturingInput() bool { return v.table.Capturing() }

func (v listView[T]) keys(readOnly bool) []key.Binding {
	b := []key.Binding{containerKeyMap.Up, containerKeyMap.Down, inspectKey}
	if !readOnly {
		b = append(b, actionKeys.Remove)
	}
	b = append(b, refreshKey)
	return append(b, v.table.Keys()...)
}

// flexColumns gives the first column whatever the fixed ones leave.
func flexColumns(width int, first string, fixed ...tableColumn) []tableColumn {
	used := 0
	for _, c := range fixed {
		used += c.Width + 2
	}
	return append([]tableColumn{{Title: first, Width: max(width-used-2-2, 16)}}, fixed...)
}
