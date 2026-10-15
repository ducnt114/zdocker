package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/zdocker/internal/docker"
)

// statsHistory is how many samples the sparklines show (~1 per second).
const statsHistory = 60

var statsKey = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "stats"))

type (
	openStatsMsg    struct{ container docker.Container }
	statsStartedMsg struct {
		samples <-chan docker.Stats
		errs    <-chan error
		gen     int
		err     error
	}
	statsSamplesMsg struct {
		samples []docker.Stats
		gen     int
	}
	statsEndedMsg struct {
		gen int
		err error
	}
)

// statsView streams one container's resource usage, like `docker stats`
// for a single container, with a short history drawn as sparklines.
type statsView struct {
	client    docker.Client
	container docker.Container
	history   []docker.Stats // oldest first, at most statsHistory
	ended     bool
	samples   <-chan docker.Stats
	errs      <-chan error
	cancel    context.CancelFunc
	gen       int
	width     int
	now       func() time.Time
}

func newStatsView() statsView { return statsView{now: time.Now} }

func (v *statsView) open(c docker.Client, ctr docker.Container) tea.Cmd {
	v.stop()
	v.gen++
	v.client, v.container = c, ctr
	v.history, v.ended = nil, false
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	id, gen := ctr.ID, v.gen
	return func() tea.Msg {
		samples, errs, err := c.ContainerStats(ctx, id)
		return statsStartedMsg{samples: samples, errs: errs, gen: gen, err: err}
	}
}

func (v *statsView) stop() {
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
}

func waitForStats(samples <-chan docker.Stats, errs <-chan error, gen int) tea.Cmd {
	return func() tea.Msg {
		batch, ok := receiveBatch(samples, 10)
		if ok {
			return statsSamplesMsg{samples: batch, gen: gen}
		}
		select {
		case err := <-errs:
			return statsEndedMsg{gen: gen, err: err}
		default:
			return statsEndedMsg{gen: gen}
		}
	}
}

func (v statsView) Update(msg tea.Msg) (statsView, tea.Cmd) {
	switch msg := msg.(type) {
	case statsStartedMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		if msg.err != nil {
			v.ended = true
			return v, reportErr(msg.err)
		}
		v.samples, v.errs = msg.samples, msg.errs
		return v, waitForStats(v.samples, v.errs, v.gen)
	case statsSamplesMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		v.history = append(v.history, msg.samples...)
		if n := len(v.history); n > statsHistory {
			v.history = v.history[n-statsHistory:]
		}
		return v, waitForStats(v.samples, v.errs, v.gen)
	case statsEndedMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		v.ended = true
		if msg.err != nil {
			return v, reportErr(fmt.Errorf("stats: %w", msg.err))
		}
		return v, reportInfo("stats stream closed (container stopped?)")
	case tea.KeyMsg:
		if key.Matches(msg, globalKeyMap.Back) {
			return v, goBack
		}
	}
	return v, nil
}

func (v *statsView) SetSize(w, _ int) { v.width = w }

var statLabel = lipgloss.NewStyle().Bold(true).Width(7)

func (v statsView) View() string {
	title := titleStyle.Render("Stats " + v.container.Name)
	if v.ended {
		title += statusStyle.Render("  (stream closed)")
	}
	if len(v.history) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, title, statusStyle.Render("waiting for the first sample…"))
	}
	last := v.history[len(v.history)-1]
	cpu := make([]float64, len(v.history))
	mem := make([]float64, len(v.history))
	var cpuPeak float64
	var memPeak uint64
	for i, s := range v.history {
		cpu[i], mem[i] = s.CPUPercent, float64(s.MemUsed)
		cpuPeak = max(cpuPeak, s.CPUPercent)
		memPeak = max(memPeak, s.MemUsed)
	}
	sparkWidth := max(min(v.width-48, statsHistory), 10)
	memLimit := "no limit"
	if last.MemLimit > 0 {
		memLimit = formatBinary(last.MemLimit)
	}
	lines := []string{
		title,
		"",
		statLabel.Render("CPU") + fmt.Sprintf("%-28s", fmt.Sprintf("%6.1f%%", last.CPUPercent)) +
			sparkStyle.Render(sparkline(cpu, sparkWidth, 1)) + statusStyle.Render(fmt.Sprintf("  peak %.1f%%", cpuPeak)),
		statLabel.Render("MEM") + fmt.Sprintf("%-28s", fmt.Sprintf("%s / %s (%.1f%%)", formatBinary(last.MemUsed), memLimit, last.MemPercent)) +
			sparkStyle.Render(sparkline(mem, sparkWidth, 1<<20)) + statusStyle.Render("  peak "+formatBinary(memPeak)),
		statLabel.Render("NET") + fmt.Sprintf("↓ %s  ↑ %s", formatSize(int64(last.NetRx)), formatSize(int64(last.NetTx))),
		statLabel.Render("BLOCK") + fmt.Sprintf("read %s  write %s", formatSize(int64(last.BlockRead)), formatSize(int64(last.BlockWrite))),
		statLabel.Render("PIDS") + fmt.Sprint(last.PIDs),
		"",
		statusStyle.Render(fmt.Sprintf("%d samples, last %s ago", len(v.history), formatAge(v.now().Sub(last.Time)))),
	}
	return strings.Join(lines, "\n")
}

var sparkStyle = lipgloss.NewStyle().Foreground(colorAccent)

// sparkline draws the last width values with block characters, scaled to
// the largest value shown (at least floor, so near-zero noise stays flat).
func sparkline(values []float64, width int, floor float64) string {
	const bars = "▁▂▃▄▅▆▇█"
	levels := []rune(bars)
	if len(values) > width {
		values = values[len(values)-width:]
	}
	peak := floor
	for _, v := range values {
		peak = max(peak, v)
	}
	var b strings.Builder
	for _, v := range values {
		i := int(v / peak * float64(len(levels)-1))
		b.WriteRune(levels[min(max(i, 0), len(levels)-1)])
	}
	return b.String()
}

// formatBinary renders bytes in binary units like `docker stats` does for
// memory: "200MiB", "1GiB".
func formatBinary(n uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if f >= 100 || f == float64(int64(f)) {
		return fmt.Sprintf("%.0f%s", f, units[i])
	}
	return fmt.Sprintf("%.3g%s", f, units[i])
}

func (v statsView) capturingInput() bool { return false }

func (v statsView) keys() []key.Binding { return []key.Binding{globalKeyMap.Back} }
