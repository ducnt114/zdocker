package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// tableRow is one row given to a filterTable.
type tableRow struct {
	key    string   // stable identity (container ID…), to keep the selection
	cells  []string // what is displayed, one per column
	sortBy []string // optional: what to sort by, per column (default: cells)
	filter string   // text the "/" filter searches (case-insensitive)
	style  lipgloss.Style
}

// sortValue is what column col sorts by.
func (r tableRow) sortValue(col int) string {
	if col < len(r.sortBy) && r.sortBy[col] != "" {
		return r.sortBy[col]
	}
	if col < len(r.cells) {
		return r.cells[col]
	}
	return ""
}

var filterTableKeys = struct {
	Filter, Sort, Reverse key.Binding
}{
	Filter:  key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	Sort:    key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort column")),
	Reverse: key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "reverse sort")),
}

// filterTable is a tableModel plus what every list view needs: a "/"
// filter, sorting by any column (s, S) and a selection that follows the
// same row (by key) when the data changes. containersView and imagesView
// are thin wrappers around it.
type filterTable struct {
	table    tableModel
	input    textinput.Model
	cols     []tableColumn // titles without sort arrows
	all      []tableRow
	visible  []tableRow // filtered and sorted, in display order
	sortCol  string     // column title; "" = the order given to SetRows
	sortDesc bool
}

func newFilterTable() filterTable {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "filter"
	return filterTable{table: newTableModel(), input: ti}
}

// SetColumns sets the columns (widths already computed by the view).
func (t *filterTable) SetColumns(cols []tableColumn) {
	t.cols = cols
	t.applyColumns()
}

func (t *filterTable) applyColumns() {
	cols := slices.Clone(t.cols)
	for i := range cols {
		if cols[i].Title == t.sortCol {
			cols[i].Title += sortArrow(t.sortDesc)
		}
	}
	t.table.SetColumns(cols)
}

// SetRows replaces the data, keeping the cursor on the same key.
func (t *filterTable) SetRows(rows []tableRow) {
	selected, _ := t.Selected()
	t.all = rows
	t.rebuild(selected)
}

// rebuild filters, sorts and redraws, putting the cursor on selected.
func (t *filterTable) rebuild(selected string) {
	term := strings.ToLower(t.input.Value())
	t.visible = t.visible[:0]
	for _, r := range t.all {
		if term == "" || strings.Contains(strings.ToLower(r.filter), term) {
			t.visible = append(t.visible, r)
		}
	}
	if col := t.sortIndex(); col >= 0 {
		// Stable, and ties keep the given order: live updates don't make
		// equal rows jump around.
		slices.SortStableFunc(t.visible, func(a, b tableRow) int {
			va, vb := a.sortValue(col), b.sortValue(col)
			c := 0
			switch {
			case naturalLess(va, vb):
				c = -1
			case naturalLess(vb, va):
				c = 1
			}
			if t.sortDesc {
				c = -c
			}
			return c
		})
	} else if t.sortDesc {
		slices.Reverse(t.visible)
	}

	rows := make([][]string, len(t.visible))
	styles := make([]lipgloss.Style, len(t.visible))
	cursor := 0
	for i, r := range t.visible {
		rows[i], styles[i] = r.cells, r.style
		if r.key == selected {
			cursor = i
		}
	}
	t.table.SetRows(rows, styles)
	t.table.SetCursor(cursor)
}

func (t filterTable) sortIndex() int {
	return slices.IndexFunc(t.cols, func(c tableColumn) bool { return c.Title == t.sortCol })
}

// Selected returns the key of the row under the cursor.
func (t filterTable) Selected() (string, bool) {
	i := t.table.Cursor()
	if i < 0 || i >= len(t.visible) {
		return "", false
	}
	return t.visible[i].key, true
}

// Capturing reports whether the filter input has focus (every key goes
// to it, even "q").
func (t filterTable) Capturing() bool { return t.input.Focused() }

// Update handles the filter, sorting and cursor movement.
func (t filterTable) Update(msg tea.Msg) (filterTable, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return t, nil
	}
	selected, _ := t.Selected()
	if t.input.Focused() {
		switch k.Type {
		case tea.KeyEsc:
			t.input.SetValue("")
			t.input.Blur()
		case tea.KeyEnter:
			t.input.Blur()
		default:
			var cmd tea.Cmd
			t.input, cmd = t.input.Update(k)
			t.rebuild(selected)
			return t, cmd
		}
		t.rebuild(selected)
		return t, nil
	}
	switch {
	case key.Matches(k, filterTableKeys.Filter):
		return t, t.input.Focus()
	case key.Matches(k, globalKeyMap.Back) && t.input.Value() != "":
		t.input.SetValue("")
		t.rebuild(selected)
		return t, nil
	case key.Matches(k, filterTableKeys.Sort):
		titles := make([]string, len(t.cols))
		for i, c := range t.cols {
			titles[i] = c.Title
		}
		t.sortCol = nextSortColumn(titles, t.sortCol)
		t.applyColumns()
		t.rebuild(selected)
		return t, nil
	case key.Matches(k, filterTableKeys.Reverse):
		t.sortDesc = !t.sortDesc
		t.applyColumns()
		t.rebuild(selected)
		return t, nil
	}
	var cmd tea.Cmd
	t.table, cmd = t.table.Update(k)
	return t, cmd
}

// nextSortColumn cycles through the titles, then back to "" (default).
func nextSortColumn(titles []string, current string) string {
	for i, t := range titles {
		if t == current {
			if i+1 < len(titles) {
				return titles[i+1]
			}
			return ""
		}
	}
	return titles[0]
}

func sortArrow(desc bool) string {
	if desc {
		return "↓"
	}
	return "↑"
}

func (t *filterTable) SetSize(w, h int) {
	t.input.Width = w - 2
	t.table.SetWidth(w)
	t.table.SetHeight(max(h-1, 1)) // the title line
}

// View renders the title line (title, row count, note, filter state) and
// the table.
func (t filterTable) View(title, note string) string {
	line := titleStyle.Render(fmt.Sprintf("%s[%d]", title, len(t.visible)))
	if note != "" {
		line += statusStyle.Render("  " + note)
	}
	switch {
	case t.input.Focused():
		line += "  " + t.input.View()
	case t.input.Value() != "":
		line += statusStyle.Render("  /" + t.input.Value())
	}
	return lipgloss.JoinVertical(lipgloss.Left, line, t.table.View())
}

// Keys are the bindings to show in the help bar.
func (t filterTable) Keys() []key.Binding {
	b := []key.Binding{filterTableKeys.Filter, filterTableKeys.Sort, filterTableKeys.Reverse}
	if t.input.Value() != "" {
		b = append(b, key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")))
	}
	return b
}
