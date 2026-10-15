// Command zdocker is a small k9s-like terminal UI for local Docker
// containers and images.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ducnt114/zdocker/internal/docker"
	"github.com/ducnt114/zdocker/internal/ui"
)

func main() {
	host := flag.String("host", "", "Docker daemon address (default: $DOCKER_HOST, else the local socket)")
	readOnly := flag.Bool("read-only", false, "only look: disable start, stop, restart and remove")
	flag.Parse()

	client, err := docker.New(*host)
	if err != nil {
		fmt.Fprintln(os.Stderr, "zdocker:", err)
		os.Exit(1)
	}
	if *readOnly {
		// Enforced in the client, not just hidden in the UI.
		client = docker.ReadOnly(client)
	}
	model := ui.New(client, ui.Options{ReadOnly: *readOnly})
	if _, err := tea.NewProgram(model, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "zdocker:", err)
		os.Exit(1)
	}
}
