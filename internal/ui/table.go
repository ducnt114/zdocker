package ui

// Adapted from kboba (github.com/ducnt114/kboba, internal/ui/table.go).

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// tableModel is a small table component, written instead of using
// bubbles/table because we need per-row colours. bubbles/table measures
// cells with runewidth (counting ANSI escape codes as width) and wraps the
// selected row in one style, whose background the cells' own colour resets
// would cancel. Here rows stay plain text and styles are applied per cell
// at render time, so widths are always right.
type tableModel struct {
	cols   []tableColumn
	rows   [][]string
	styles []lipgloss.Style // optional foreground style per row
	cursor int
	offset int // index of the first visible row
	height int // total height, including the header and its border
	width  int
	keys   tableKeyMap
}

type tableColumn struct {
	Title string
	Width int
}

type tableKeyMap struct {
	Up, Down, PageUp, PageDown, HalfUp, HalfDown, Top, Bottom key.Binding
}

var (
	tableHeaderStyle   = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	tableCellStyle     = lipgloss.NewStyle().Padding(0, 1)
	tableSelectedStyle = lipgloss.NewStyle().Padding(0, 1).Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).Background(colorAccent)
	tableBorderStyle = lipgloss.NewStyle().Foreground(colorMuted)
)

// headerHeight is the header line plus the border line under it.
const headerHeight = 2

func newTableModel() tableModel {
	return tableModel{keys: tableKeyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k")),
		Down:     key.NewBinding(key.WithKeys("down", "j")),
		PageUp:   key.NewBinding(key.WithKeys("pgup", "b")),
		PageDown: key.NewBinding(key.WithKeys("pgdown", " ")),
		HalfUp:   key.NewBinding(key.WithKeys("ctrl+u")),
		HalfDown: key.NewBinding(key.WithKeys("ctrl+d")),
		Top:      key.NewBinding(key.WithKeys("home", "g")),
		Bottom:   key.NewBinding(key.WithKeys("end", "G")),
	}}
}

func (t tableModel) Columns() []tableColumn { return t.cols }
func (t tableModel) Cursor() int            { return t.cursor }

func (t *tableModel) SetColumns(cols []tableColumn) { t.cols = cols }
func (t *tableModel) SetWidth(w int)                { t.width = w }

func (t *tableModel) SetHeight(h int) {
	t.height = h
	t.SetCursor(t.cursor) // keep the cursor visible
}

// SetRows replaces the rows. styles may be nil, or hold one style per row
// (used for the text colour; the selected row has its own style).
func (t *tableModel) SetRows(rows [][]string, styles []lipgloss.Style) {
	t.rows, t.styles = rows, styles
	t.SetCursor(t.cursor)
}

// SetCursor moves the cursor (clamped) and scrolls so it stays visible.
func (t *tableModel) SetCursor(i int) {
	t.cursor = clamp(i, 0, len(t.rows)-1)
	body := t.bodyHeight()
	switch {
	case t.cursor < t.offset:
		t.offset = t.cursor
	case t.cursor >= t.offset+body:
		t.offset = t.cursor - body + 1
	}
	// Don't leave empty space at the bottom when rows were removed.
	t.offset = clamp(t.offset, 0, max(len(t.rows)-body, 0))
}

func (t tableModel) bodyHeight() int { return max(t.height-headerHeight, 1) }

func (t tableModel) Update(msg tea.Msg) (tableModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return t, nil
	}
	body := t.bodyHeight()
	switch {
	case key.Matches(k, t.keys.Up):
		t.SetCursor(t.cursor - 1)
	case key.Matches(k, t.keys.Down):
		t.SetCursor(t.cursor + 1)
	case key.Matches(k, t.keys.PageUp):
		t.SetCursor(t.cursor - body)
	case key.Matches(k, t.keys.PageDown):
		t.SetCursor(t.cursor + body)
	case key.Matches(k, t.keys.HalfUp):
		t.SetCursor(t.cursor - body/2)
	case key.Matches(k, t.keys.HalfDown):
		t.SetCursor(t.cursor + body/2)
	case key.Matches(k, t.keys.Top):
		t.SetCursor(0)
	case key.Matches(k, t.keys.Bottom):
		t.SetCursor(len(t.rows) - 1)
	}
	return t, nil
}

func (t tableModel) View() string {
	var b strings.Builder

	// Header and border.
	header := make([]string, len(t.cols))
	for i, c := range t.cols {
		header[i] = tableHeaderStyle.Render(fitCell(c.Title, c.Width))
	}
	b.WriteString(strings.Join(header, ""))
	b.WriteByte('\n')
	b.WriteString(tableBorderStyle.Render(strings.Repeat("─", max(t.width, 0))))

	// Visible rows only.
	end := min(t.offset+t.bodyHeight(), len(t.rows))
	for r := t.offset; r < end; r++ {
		style := tableCellStyle
		switch {
		case r == t.cursor:
			style = tableSelectedStyle
		case r < len(t.styles):
			style = t.styles[r].Padding(0, 1)
		}
		cells := make([]string, len(t.cols))
		for i, c := range t.cols {
			value := ""
			if i < len(t.rows[r]) {
				value = t.rows[r][i]
			}
			cells[i] = style.Render(fitCell(value, c.Width))
		}
		b.WriteByte('\n')
		b.WriteString(strings.Join(cells, ""))
	}
	return b.String()
}

// fitCell truncates s to width display cells (with an ellipsis) and pads
// it with spaces to exactly width. Width is measured ANSI- and
// Unicode-aware.
func fitCell(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = ansi.Truncate(s, width, "…")
	return s + strings.Repeat(" ", width-ansi.StringWidth(s))
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}
