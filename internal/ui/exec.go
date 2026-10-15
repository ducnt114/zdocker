package ui

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ducnt114/zdocker/internal/docker"
)

var execKey = key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "shell"))

// shellScript starts bash if the image has it, else sh (alpine, busybox).
const shellScript = "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"

type (
	openShellMsg   struct{ container docker.Container }
	shellExitedMsg struct {
		name string
		err  error
	}
)

// shellRunner builds and runs `docker exec`. Its functions are fields so
// tests can check the command without a docker CLI.
type shellRunner struct {
	lookPath func(file string) (string, error)
	command  func(name string, args ...string) *exec.Cmd
}

var defaultShellRunner = shellRunner{lookPath: exec.LookPath, command: exec.Command}

// execArgs is the docker CLI command line for a shell in c, talking to the
// same daemon as zdocker: the same context, or the same --host.
func execArgs(contextName, host string, c docker.Container) []string {
	var args []string
	switch {
	case contextName != "" && contextName != docker.DefaultContext:
		args = append(args, "--context", contextName)
	case contextName == "" && host != "":
		args = append(args, "-H", host) // zdocker was started with --host
	}
	return append(args, "exec", "-it", c.ID, "sh", "-c", shellScript)
}

// openShell suspends the TUI and runs an interactive shell in c through
// the docker CLI (tea.ExecProcess hands the terminal over and takes it
// back when the shell exits). Re-implementing `docker exec -it` over the
// API would mean raw terminal mode, resizes and a hijacked connection;
// the CLI already does all that.
func (r shellRunner) openShell(contextName, host string, c docker.Container) tea.Cmd {
	if c.State != "running" {
		return reportErr(fmt.Errorf("%s is %s: a shell needs a running container", c.Name, c.State))
	}
	docker, err := r.lookPath("docker")
	if err != nil {
		return reportErr(errors.New("opening a shell needs the docker CLI in PATH"))
	}
	cmd := r.command(docker, execArgs(contextName, host, c)...)
	name := c.Name
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return shellExitedMsg{name: name, err: err} })
}
