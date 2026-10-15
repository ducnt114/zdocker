package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ducnt114/zdocker/internal/docker"
)

// maxLogLines is how many log lines are kept in memory.
const maxLogLines = 5000

// stderrMark starts buffered stderr lines. It is a control character
// (unit separator) that log text practically never contains, and it is
// stripped before display and search.
const stderrMark = "\x1f"

var (
	stderrGutter = lipgloss.NewStyle().Foreground(colorError).Render("▍") + " "
	stdoutGutter = "  "
)

type (
	logStreamStartedMsg struct {
		lines <-chan docker.LogLine
		errs  <-chan error
		gen   int
		err   error
	}
	logLinesMsg struct {
		lines []docker.LogLine
		gen   int
	}
	logStreamEndedMsg struct {
		gen int
		err error
	}
	openLogsMsg struct{ container docker.Container }
)

var logsKeyMap = struct {
	Up, Down, Follow, Search, Next, Prev, Wrap, Timestamps, Scroll key.Binding
}{
	Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "scroll")),
	Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "scroll")),
	Follow:     key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "follow")),
	Search:     key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
	Next:       key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next match")),
	Prev:       key.NewBinding(key.WithKeys("N"), key.WithHelp("N", "prev match")),
	Wrap:       key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "wrap")),
	Timestamps: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "timestamps")),
	Scroll:     key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("←/→", "scroll")),
}

// logsView follows one container's logs. Structure and features (ring
// buffer, follow, search, wrap) come from kboba's logs view.
type logsView struct {
	viewport viewport.Model
	buf      ringBuffer

	client    docker.Client
	container docker.Container
	opts      docker.LogOptions

	follow, wrap, ended bool

	search      textinput.Model
	term        string
	matches     []int // absolute line numbers (see ringBuffer.dropped)
	current     int
	lineOffsets []int // viewport row where each buffered line starts

	lines  <-chan docker.LogLine
	errs   <-chan error
	cancel context.CancelFunc
	gen    int
}

func newLogsView() logsView {
	vp := viewport.New(0, 0)
	vp.KeyMap.PageDown = key.NewBinding(key.WithKeys("pgdown", " ")) // "f" is follow
	vp.SetHorizontalStep(4)
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "search"
	return logsView{viewport: vp, buf: newRingBuffer(maxLogLines), search: ti, current: -1}
}

func (v *logsView) open(c docker.Client, ctr docker.Container) tea.Cmd {
	v.client, v.container = c, ctr
	v.opts = docker.LogOptions{}
	v.term = ""
	v.search.SetValue("")
	return v.restart()
}

// restart (re)starts the stream from scratch (new container or options).
func (v *logsView) restart() tea.Cmd {
	v.stop()
	v.gen++
	v.buf.reset()
	v.follow, v.ended, v.current = true, false, -1
	v.render()

	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	c, id, opts, gen := v.client, v.container.ID, v.opts, v.gen
	return func() tea.Msg {
		lines, errs, err := c.ContainerLogs(ctx, id, opts)
		return logStreamStartedMsg{lines: lines, errs: errs, gen: gen, err: err}
	}
}

// stop cancels the stream: the HTTP connection closes and the reader
// goroutine ends, closing its channel.
func (v *logsView) stop() {
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
	v.lines, v.errs = nil, nil
}

func waitForLogLines(lines <-chan docker.LogLine, errs <-chan error, gen int) tea.Cmd {
	return func() tea.Msg {
		batch, ok := receiveBatch(lines, 1000)
		if ok {
			return logLinesMsg{lines: batch, gen: gen}
		}
		select {
		case err := <-errs:
			return logStreamEndedMsg{gen: gen, err: err}
		default:
			return logStreamEndedMsg{gen: gen}
		}
	}
}

func (v logsView) Update(msg tea.Msg) (logsView, tea.Cmd) {
	switch msg := msg.(type) {
	case logStreamStartedMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		if msg.err != nil {
			v.ended = true
			return v, reportErr(msg.err)
		}
		v.lines, v.errs = msg.lines, msg.errs
		return v, waitForLogLines(v.lines, v.errs, v.gen)

	case logLinesMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		for _, l := range msg.lines {
			if l.Stderr {
				v.buf.push(stderrMark + l.Text)
			} else {
				v.buf.push(l.Text)
			}
		}
		v.render() // once per batch, not per line
		if v.follow {
			v.viewport.GotoBottom()
		}
		return v, waitForLogLines(v.lines, v.errs, v.gen)

	case logStreamEndedMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		v.ended = true
		if msg.err != nil {
			return v, reportErr(fmt.Errorf("log stream: %w", msg.err))
		}
		return v, reportInfo("log stream closed (container stopped?)")

	case tea.KeyMsg:
		if v.search.Focused() {
			return v.handleSearchKey(msg)
		}
		return v.handleKey(msg)
	}
	return v, nil
}

func (v logsView) handleSearchKey(msg tea.KeyMsg) (logsView, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		v.search.Blur()
		v.search.SetValue("")
		v.term, v.current = "", -1
		v.render()
		return v, nil
	case tea.KeyEnter:
		v.search.Blur()
		v.term, v.current = strings.ToLower(v.search.Value()), -1
		v.render()
		return v.jump(true), nil
	}
	var cmd tea.Cmd
	v.search, cmd = v.search.Update(msg)
	return v, cmd
}

func (v logsView) handleKey(msg tea.KeyMsg) (logsView, tea.Cmd) {
	k := logsKeyMap
	switch {
	case key.Matches(msg, globalKeyMap.Back):
		if v.term != "" { // first esc clears the search
			v.term, v.current = "", -1
			v.search.SetValue("")
			v.render()
			return v, nil
		}
		return v, goBack
	case key.Matches(msg, k.Follow):
		v.follow = !v.follow
		if v.follow {
			v.viewport.GotoBottom()
		}
		return v, nil
	case key.Matches(msg, k.Search):
		return v, v.search.Focus()
	case key.Matches(msg, k.Next) && v.term != "":
		return v.jump(true), nil
	case key.Matches(msg, k.Prev) && v.term != "":
		return v.jump(false), nil
	case key.Matches(msg, k.Wrap):
		v.wrap = !v.wrap
		v.viewport.SetXOffset(0)
		v.render()
		if v.follow {
			v.viewport.GotoBottom()
		}
		return v, nil
	case key.Matches(msg, k.Timestamps):
		v.opts.Timestamps = !v.opts.Timestamps
		return v, v.restart()
	}
	var cmd tea.Cmd
	v.viewport, cmd = v.viewport.Update(msg)
	if !v.viewport.AtBottom() {
		v.follow = false // scrolling up pauses follow
	}
	return v, cmd
}

func (v logsView) jump(forward bool) logsView {
	m, ok := nextMatch(v.matches, v.current, v.topLine(), forward)
	if !ok {
		return v
	}
	v.current, v.follow = m, false
	if i := m - v.buf.dropped; i >= 0 && i < len(v.lineOffsets) {
		v.viewport.SetYOffset(v.lineOffsets[i])
	}
	return v
}

func (v logsView) topLine() int {
	i := sort.SearchInts(v.lineOffsets, v.viewport.YOffset+1) - 1
	return v.buf.dropped + max(i, 0)
}

// render rebuilds the viewport content: gutter for stderr, search
// highlights, optional wrapping, and the row each line starts at.
func (v *logsView) render() {
	v.matches = v.matches[:0]
	v.lineOffsets = v.lineOffsets[:0]
	var b strings.Builder
	row := 0
	width := v.viewport.Width - 2 // gutter
	for i := range v.buf.len() {
		line, isErr := strings.CutPrefix(v.buf.at(i), stderrMark)
		line = strings.ReplaceAll(line, "\t", "    ")
		if containsFold(line, v.term) {
			v.matches = append(v.matches, v.buf.dropped+i)
			line = highlightMatches(line, v.term, matchStyle)
		}
		gutter := stdoutGutter
		if isErr {
			gutter = stderrGutter
		}
		if v.wrap && width > 0 {
			line = strings.ReplaceAll(ansi.Hardwrap(line, width, true), "\n", "\n"+gutter)
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		v.lineOffsets = append(v.lineOffsets, row)
		row += strings.Count(line, "\n") + 1
		b.WriteString(gutter + line)
	}
	v.viewport.SetContent(b.String())
}

func (v *logsView) SetSize(w, h int) {
	v.viewport.Width = w
	v.viewport.Height = max(h-1, 1)
	v.search.Width = w - 2
	if v.wrap {
		v.render()
	}
	if v.follow {
		v.viewport.GotoBottom()
	}
}

func (v logsView) View() string {
	title := titleStyle.Render("Logs " + v.container.Name)
	flags := []string{"follow:" + onOff(v.follow), "wrap:" + onOff(v.wrap), "ts:" + onOff(v.opts.Timestamps),
		fmt.Sprintf("lines:%d", v.buf.len())}
	if v.ended {
		flags = append(flags, "(stream closed)")
	}
	info := "  " + strings.Join(flags, "  ")
	switch {
	case v.search.Focused():
		info += "  " + v.search.View()
	case v.term != "":
		info += fmt.Sprintf("  /%s %s", v.term, v.matchPosition())
	}
	return lipgloss.JoinVertical(lipgloss.Left, title+statusStyle.Render(info), v.viewport.View())
}

func (v logsView) matchPosition() string {
	pos := 0
	for i, m := range v.matches {
		if m == v.current {
			pos = i + 1
		}
	}
	return fmt.Sprintf("%d/%d", pos, len(v.matches))
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (v logsView) capturingInput() bool { return v.search.Focused() }

func (v logsView) keys() []key.Binding {
	k := logsKeyMap
	b := []key.Binding{k.Up, k.Down, k.Follow, k.Search}
	if v.term != "" {
		b = append(b, k.Next, k.Prev)
	}
	b = append(b, k.Wrap, k.Timestamps)
	if !v.wrap {
		b = append(b, k.Scroll)
	}
	return append(b, globalKeyMap.Back)
}
