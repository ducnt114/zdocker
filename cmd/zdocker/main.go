// Command zdocker is a small k9s-like terminal UI for local Docker
// containers, images, volumes and networks.
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
	host := flag.String("host", "", "Docker daemon address; overrides --context (default: from the docker context)")
	contextName := flag.String("context", "", "docker context to use (default: the CLI's current context)")
	readOnly := flag.Bool("read-only", false, "only look: disable start, stop, restart and remove")
	flag.Parse()

	configDir := docker.ConfigDir()
	listContexts := func() ([]docker.Context, error) { return docker.ListContexts(configDir) }
	connect := func(name string) (docker.Client, error) {
		ctxs, err := listContexts()
		if err != nil {
			return nil, err
		}
		c, ok := docker.FindContext(ctxs, name)
		if !ok {
			return nil, fmt.Errorf("docker context %q not found", name)
		}
		client, err := docker.NewForContext(c)
		if err != nil {
			return nil, err
		}
		if *readOnly {
			client = docker.ReadOnly(client) // enforced in the client, not just hidden in the UI
		}
		return client, nil
	}

	var client docker.Client
	var err error
	name := ""
	if *host != "" {
		client, err = docker.New(*host)
		if err == nil && *readOnly {
			client = docker.ReadOnly(client)
		}
	} else {
		name = *contextName
		if name == "" {
			name = currentContext(listContexts)
		}
		client, err = connect(name)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "zdocker:", err)
		os.Exit(1)
	}

	model := ui.New(client, ui.Options{
		ReadOnly: *readOnly,
		Context:  name,
		Contexts: listContexts,
		Connect:  connect,
	})
	if _, err := tea.NewProgram(model, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "zdocker:", err)
		os.Exit(1)
	}
}

// currentContext is the docker CLI's current context ("default" if the
// contexts can't be read).
func currentContext(list func() ([]docker.Context, error)) string {
	ctxs, err := list()
	if err == nil {
		for _, c := range ctxs {
			if c.Current {
				return c.Name
			}
		}
	}
	return docker.DefaultContext
}
