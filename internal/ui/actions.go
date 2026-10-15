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

// actionTimeout is longer than requestTimeout: stopping waits up to 10s for
// the container to exit, removing a big image can take a while.
const actionTimeout = 60 * time.Second

var actionKeys = struct {
	Start, Stop, Restart, Remove key.Binding
}{
	Start:   key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "start")),
	Stop:    key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "stop")),
	Restart: key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "restart")),
	Remove:  key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "remove")),
}

// actionMsg asks the root model to perform a change, after confirmation if
// prompt is set. Views describe the action; only the root runs it.
type actionMsg struct {
	prompt   string // "" = no confirmation needed
	progress string // shown while running, e.g. "stopping web…"
	done     string // shown on success, e.g. "web stopped"
	run      func(ctx context.Context, c docker.Client) error
}

type actionDoneMsg struct {
	done string
	err  error
}

// pendingAction is an action waiting for y/n.
type pendingAction struct{ action actionMsg }

func request(a actionMsg) tea.Cmd { return func() tea.Msg { return a } }

// containerActions turns an action key into an actionMsg for c.
func containerAction(k tea.KeyMsg, c docker.Container) (actionMsg, bool) {
	switch {
	case key.Matches(k, actionKeys.Start):
		return actionMsg{
			progress: "starting " + c.Name + "…", done: c.Name + " started",
			run: func(ctx context.Context, cl docker.Client) error { return cl.StartContainer(ctx, c.ID) },
		}, true
	case key.Matches(k, actionKeys.Stop):
		return actionMsg{
			prompt:   fmt.Sprintf("Stop container %s?", c.Name),
			progress: "stopping " + c.Name + "… (up to 10s)", done: c.Name + " stopped",
			run: func(ctx context.Context, cl docker.Client) error { return cl.StopContainer(ctx, c.ID) },
		}, true
	case key.Matches(k, actionKeys.Restart):
		return actionMsg{
			prompt:   fmt.Sprintf("Restart container %s?", c.Name),
			progress: "restarting " + c.Name + "…", done: c.Name + " restarted",
			run: func(ctx context.Context, cl docker.Client) error { return cl.RestartContainer(ctx, c.ID) },
		}, true
	case key.Matches(k, actionKeys.Remove):
		running := c.State == "running" || c.State == "restarting" || c.State == "paused"
		prompt := fmt.Sprintf("Remove container %s?", c.Name)
		if running {
			// Like `docker rm -f`: say so, it kills the process.
			prompt = fmt.Sprintf("Container %s is %s: kill and remove it?", c.Name, c.State)
		}
		return actionMsg{
			prompt:   prompt,
			progress: "removing " + c.Name + "…", done: c.Name + " removed",
			run: func(ctx context.Context, cl docker.Client) error { return cl.RemoveContainer(ctx, c.ID, running) },
		}, true
	}
	return actionMsg{}, false
}

// imageAction handles D on an image row: untag "repo:tag" (the image goes
// once its last tag does) or remove an untagged image by ID.
func imageAction(k tea.KeyMsg, img docker.Image) (actionMsg, bool) {
	if !key.Matches(k, actionKeys.Remove) {
		return actionMsg{}, false
	}
	ref := img.Ref()
	if img.Repository == "<none>" {
		ref = img.ID
	}
	prompt := fmt.Sprintf("Remove image %s?", img.Ref())
	if img.Containers > 0 {
		prompt = fmt.Sprintf("Remove image %s? (used by %d container(s): the daemon will refuse)", img.Ref(), img.Containers)
	}
	return actionMsg{
		prompt:   prompt,
		progress: "removing " + img.Ref() + "…", done: img.Ref() + " removed",
		run: func(ctx context.Context, cl docker.Client) error { return cl.RemoveImage(ctx, ref, false) },
	}, true
}

// runAction performs a in a Cmd. The table updates by itself: the daemon
// emits events for the change.
func runAction(c docker.Client, a actionMsg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDoneMsg{done: a.done, err: a.run(ctx, c)}
	}
}

var confirmStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#B58900", Dark: "#F5C542"})

func (p pendingAction) View() string {
	return confirmStyle.Render(p.action.prompt) + statusStyle.Render("  y: yes  ·  any other key: cancel")
}
