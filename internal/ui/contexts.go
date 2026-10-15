package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ducnt114/zdocker/internal/docker"
)

type (
	// switchContextMsg asks the root model to connect to another context.
	switchContextMsg struct{ name string }
	// contextSwitchedMsg carries the new client (or why there is none).
	contextSwitchedMsg struct {
		name   string
		client docker.Client
		err    error
	}
)

// contextsSpec lists docker CLI contexts; enter switches to one. Like
// kboba's contexts, switching is in-memory only: the CLI's current context
// is not changed.
func contextsSpec(list func() ([]docker.Context, error), active func() string) listSpec[docker.Context] {
	return listSpec[docker.Context]{
		title: "Contexts",
		load:  func(context.Context, docker.Client) ([]docker.Context, error) { return list() },
		key:   func(c docker.Context) string { return c.Name },
		row: func(c docker.Context, _ time.Time) tableRow {
			name := "  " + c.Name
			style := healthStyles[healthy]
			if c.Name == active() {
				name = "* " + c.Name // the one zdocker is using
				style = headerRowStyle
			}
			tls := ""
			if c.TLSDir != "" {
				tls = "tls"
			}
			cli := ""
			if c.Current {
				cli = "current" // the docker CLI's current context
			}
			return tableRow{
				cells:  []string{name, c.Host, tls, cli, c.Description},
				filter: c.Name + " " + c.Host + " " + c.Description,
				style:  style,
			}
		},
		columns: func(w int) []tableColumn {
			return []tableColumn{
				{Title: "NAME", Width: 20}, {Title: "DOCKER ENDPOINT", Width: 44}, {Title: "TLS", Width: 4},
				{Title: "CLI", Width: 8}, {Title: "DESCRIPTION", Width: max(w-20-44-4-8-5*2-2, 10)},
			}
		},
		enter:     func(c docker.Context) tea.Msg { return switchContextMsg{name: c.Name} },
		enterHelp: "use context",
	}
}

// connectTo builds a client for a context off the UI goroutine (reading
// TLS files, parsing hosts).
func connectTo(connect func(string) (docker.Client, error), name string) tea.Cmd {
	return func() tea.Msg {
		c, err := connect(name)
		return contextSwitchedMsg{name: name, client: c, err: err}
	}
}
