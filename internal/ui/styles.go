package ui

// Adapted from kboba (github.com/ducnt114/kboba, internal/ui/styles.go).

import "github.com/charmbracelet/lipgloss"

var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#1D63ED", Dark: "#2496ED"} // Docker blue
	colorMuted  = lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#777777"}
	colorError  = lipgloss.AdaptiveColor{Light: "#D7263D", Dark: "#FF5F87"}

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(colorAccent).
			Padding(0, 1)
	headerInfoStyle = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 1)

	statusStyle      = lipgloss.NewStyle().Foreground(colorMuted)
	statusErrorStyle = lipgloss.NewStyle().Foreground(colorError).Bold(true)

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)

	readOnlyStyle = lipgloss.NewStyle().Bold(true).Padding(0, 1).
			Foreground(lipgloss.Color("#000000")).Background(lipgloss.AdaptiveColor{Light: "#B58900", Dark: "#F5C542"})

	liveStyle         = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#2E7D32", Dark: "#5FD068"})
	reconnectingStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#B58900", Dark: "#F5C542"})
)
