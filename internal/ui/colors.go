package ui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/zdocker/internal/docker"
)

// health is how a row should be coloured.
type health int

const (
	healthy  health = iota // default colour: nothing to see
	pending                // starting, paused, restarting, health check starting
	failing                // exited with an error, dead, unhealthy
	finished               // exited cleanly, or never started
)

var healthStyles = map[health]lipgloss.Style{
	healthy:  lipgloss.NewStyle(),
	pending:  lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#B58900", Dark: "#F5C542"}),
	failing:  lipgloss.NewStyle().Foreground(colorError),
	finished: lipgloss.NewStyle().Foreground(colorMuted),
}

// exitCode matches "Exited (137) 2 minutes ago".
var exitCode = regexp.MustCompile(`^Exited \((\d+)\)`)

// containerHealth classifies a container from its state and the daemon's
// status text (which carries the exit code and health check result).
func containerHealth(c docker.Container) health {
	switch {
	case strings.Contains(c.Status, "(unhealthy)"):
		return failing
	case strings.Contains(c.Status, "(health: starting)"):
		return pending
	}
	switch c.State {
	case "running":
		return healthy
	case "exited":
		if m := exitCode.FindStringSubmatch(c.Status); m != nil && m[1] != "0" {
			return failing
		}
		return finished
	case "dead":
		return failing
	case "restarting", "paused", "removing":
		return pending
	}
	return finished // created
}
