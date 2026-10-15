package ui

import (
	"context"
	"strings"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/zdocker/internal/docker"
)

type (
	openInspectMsg struct {
		kind docker.Kind
		id   string
		name string
	}
	inspectLoadedMsg struct {
		id   string // which request this answers
		text string
		err  error
	}
)

var (
	inspectKey = key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "inspect"))
	refreshKey = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh"))
)

// inspectView shows `docker inspect` output, syntax-highlighted. Like
// kboba's detail view, it is a snapshot: r fetches it again.
type inspectView struct {
	viewport viewport.Model
	client   docker.Client
	kind     docker.Kind
	id, name string
	reqID    string
	loading  bool
}

func newInspectView() inspectView {
	vp := viewport.New(0, 0)
	vp.SetHorizontalStep(4) // JSON isn't wrapped: scroll sideways instead
	return inspectView{viewport: vp}
}

func (v *inspectView) open(c docker.Client, kind docker.Kind, id, name string) tea.Cmd {
	v.client, v.kind, v.id, v.name = c, kind, id, name
	v.viewport.SetContent("")
	v.viewport.GotoTop()
	v.viewport.SetXOffset(0)
	return v.load()
}

func (v *inspectView) load() tea.Cmd {
	v.loading = true
	v.reqID = string(v.kind) + ":" + v.id
	c, kind, id, reqID := v.client, v.kind, v.id, v.reqID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		text, err := c.Inspect(ctx, kind, id)
		if err == nil {
			text = highlightJSON(text) // in the Cmd, off the UI goroutine
		}
		return inspectLoadedMsg{id: reqID, text: text, err: err}
	}
}

// highlightJSON colours JSON for the terminal; on any error it returns the
// input unchanged (colour is a nicety, never a reason to fail).
func highlightJSON(src string) string {
	style := "github"
	if lipgloss.HasDarkBackground() {
		style = "monokai"
	}
	var b strings.Builder
	if err := quick.Highlight(&b, src, "json", "terminal256", style); err != nil {
		return src
	}
	return b.String()
}

func (v inspectView) Update(msg tea.Msg) (inspectView, tea.Cmd) {
	switch msg := msg.(type) {
	case inspectLoadedMsg:
		if msg.id != v.reqID {
			return v, nil // answer to a request we already left
		}
		v.loading = false
		if msg.err != nil {
			return v, reportErr(msg.err) // e.g. the container was removed
		}
		v.viewport.SetContent(msg.text)
		return v, nil
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, globalKeyMap.Back):
			return v, goBack
		case key.Matches(msg, refreshKey):
			return v, v.load()
		}
		var cmd tea.Cmd
		v.viewport, cmd = v.viewport.Update(msg)
		return v, cmd
	}
	return v, nil
}

func (v *inspectView) SetSize(w, h int) {
	v.viewport.Width = w
	v.viewport.Height = max(h-1, 1)
}

func (v inspectView) View() string {
	title := titleStyle.Render("Inspect " + string(v.kind) + " " + v.name)
	if v.loading {
		title += statusStyle.Render("  loading…")
	}
	return lipgloss.JoinVertical(lipgloss.Left, title, v.viewport.View())
}

func (v inspectView) capturingInput() bool { return false }

func (v inspectView) keys() []key.Binding {
	return []key.Binding{
		logsKeyMap.Up, logsKeyMap.Down, logsKeyMap.Scroll, refreshKey, globalKeyMap.Back,
	}
}
