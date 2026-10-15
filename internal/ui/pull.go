package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/zdocker/internal/docker"
)

var pullKey = key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "pull"))

type (
	// prefillCommandMsg opens the command bar with text already typed.
	prefillCommandMsg struct{ text string }
	pullStartedMsg    struct {
		progress <-chan docker.PullProgress
		gen      int
		err      error
	}
	pullProgressMsg struct {
		p   docker.PullProgress
		gen int
	}
	pullEndedMsg struct{ gen int }
)

// pullView shows `docker pull` progress: an overall bar and one per layer.
type pullView struct {
	client  docker.Client
	ref     string
	state   docker.PullProgress
	running bool
	updates <-chan docker.PullProgress
	cancel  context.CancelFunc
	gen     int
	width   int
}

func newPullView() pullView { return pullView{} }

func (v *pullView) open(c docker.Client, ref string) tea.Cmd {
	v.stop()
	v.gen++
	v.client, v.ref = c, ref
	v.state, v.running = docker.PullProgress{Ref: ref}, true
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	gen := v.gen
	return func() tea.Msg {
		ch, err := c.PullImage(ctx, ref)
		return pullStartedMsg{progress: ch, gen: gen, err: err}
	}
}

// stop cancels a pull still running (leaving the view aborts it, like
// ctrl+c on `docker pull`).
func (v *pullView) stop() {
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
}

func waitForPull(ch <-chan docker.PullProgress, gen int) tea.Cmd {
	return func() tea.Msg {
		batch, ok := receiveBatch(ch, 200)
		if !ok {
			return pullEndedMsg{gen: gen}
		}
		return pullProgressMsg{p: batch[len(batch)-1], gen: gen} // only the latest snapshot matters
	}
}

func (v pullView) Update(msg tea.Msg) (pullView, tea.Cmd) {
	switch msg := msg.(type) {
	case pullStartedMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		if msg.err != nil {
			v.running = false
			v.state.Err = msg.err
			return v, reportErr(msg.err)
		}
		v.updates = msg.progress
		return v, waitForPull(v.updates, v.gen)
	case pullProgressMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		v.state = msg.p
		switch {
		case msg.p.Err != nil:
			v.running = false
			return v, reportErr(fmt.Errorf("pull %s: %w", v.ref, msg.p.Err))
		case msg.p.Done:
			v.running = false
			return v, reportInfo("pulled " + v.ref)
		}
		return v, waitForPull(v.updates, v.gen)
	case pullEndedMsg:
		if msg.gen == v.gen {
			v.running = false
		}
		return v, nil
	case tea.KeyMsg:
		if key.Matches(msg, globalKeyMap.Back) {
			return v, goBack
		}
	}
	return v, nil
}

// layerFraction estimates how far a layer is: downloading counts for 80%,
// extracting for the remaining 20%, like the phases of a real pull.
func layerFraction(l docker.LayerProgress) float64 {
	part := func() float64 {
		if l.Total <= 0 {
			return 0
		}
		return min(float64(l.Current)/float64(l.Total), 1)
	}
	switch {
	case l.Done():
		return 1
	case l.Status == "Downloading":
		return 0.8 * part()
	case l.Status == "Verifying Checksum", l.Status == "Download complete":
		return 0.8
	case l.Status == "Extracting":
		return 0.8 + 0.2*part()
	}
	return 0
}

// pullFraction is the overall progress: the average over layers.
func pullFraction(p docker.PullProgress) float64 {
	if p.Done {
		return 1
	}
	if len(p.Layers) == 0 {
		return 0
	}
	sum := 0.0
	for _, l := range p.Layers {
		sum += layerFraction(l)
	}
	return sum / float64(len(p.Layers))
}

func (v *pullView) SetSize(w, _ int) { v.width = w }

func (v pullView) View() string {
	title := titleStyle.Render("Pull " + v.ref)
	switch {
	case v.state.Err != nil:
		title += statusErrorStyle.Render("  failed")
	case v.state.Done:
		title += liveStyle.Render("  done")
	case v.running:
		title += statusStyle.Render("  pulling…  (esc cancels)")
	}
	barWidth := max(min(v.width-40, 60), 10)
	overall := progress.New(progress.WithDefaultGradient(), progress.WithWidth(barWidth))
	small := progress.New(progress.WithSolidFill(string(colorAccent.Dark)), progress.WithoutPercentage(), progress.WithWidth(barWidth/2))

	lines := []string{title, "", overall.ViewAs(pullFraction(v.state)), ""}
	for _, l := range v.state.Layers {
		size := ""
		if l.Total > 0 && !l.Done() {
			size = fmt.Sprintf("%s/%s", formatSize(l.Current), formatSize(l.Total))
		}
		lines = append(lines, fmt.Sprintf("%-12s %-18s %s %s", l.ID, l.Status, small.ViewAs(layerFraction(l)), size))
	}
	if v.state.Status != "" {
		lines = append(lines, "", statusStyle.Render(v.state.Status))
	}
	if v.state.Err != nil {
		lines = append(lines, "", statusErrorStyle.Render(v.state.Err.Error()))
	}
	return lipgloss.NewStyle().MaxWidth(v.width).Render(strings.Join(lines, "\n"))
}

func (v pullView) capturingInput() bool { return false }

func (v pullView) keys() []key.Binding { return []key.Binding{globalKeyMap.Back} }
