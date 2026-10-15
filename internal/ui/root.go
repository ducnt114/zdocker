// Package ui implements zdocker's terminal UI with Bubble Tea.
//
// The root Model only coordinates: it owns the shared chrome (header,
// command bar, status bar, help bar), routes messages and switches views.
// Each screen is its own sub-model with its own Update and View.
package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/zdocker/internal/docker"
)

type viewID int

const (
	viewContainers viewID = iota
	viewImages
	viewLogs
	viewInspect
	viewStats
	viewVolumes
	viewNetworks
)

// connectedMsg reports the result of the initial Ping.
type connectedMsg struct {
	info docker.ServerInfo
	err  error
}

// Options are startup settings, usually from flags.
type Options struct {
	// ReadOnly hides the write actions; the client given to New should
	// then be docker.ReadOnly(…) so they can't run anyway.
	ReadOnly bool
}

// Model is the root Bubble Tea model.
type Model struct {
	client docker.Client
	server docker.ServerInfo
	opts   Options

	// confirm is an action waiting for y/n, shown in the status line.
	confirm *pendingAction

	events eventsWatcher

	active     viewID
	containers containersView
	images     imagesView
	logs       logsView
	inspect    inspectView
	stats      statsView
	volumes    listView[docker.Volume]
	networks   listView[docker.Network]
	// returnTo is the table logs/inspect go back to.
	returnTo viewID

	commandMode bool
	command     textinput.Model
	help        help.Model

	status      string
	statusIsErr bool

	width, height int
	bodyHeight    int
}

// New returns the root model for a client.
func New(c docker.Client, opts Options) Model {
	ti := textinput.New()
	ti.Prompt = ":"
	ti.Placeholder = "containers | images | volumes | networks | quit"

	return Model{
		client:     c,
		opts:       opts,
		events:     eventsWatcher{client: c},
		active:     viewContainers,
		containers: newContainersView(c),
		images:     newImagesView(c),
		logs:       newLogsView(),
		inspect:    newInspectView(),
		stats:      newStatsView(),
		volumes:    newListView(c, volumesSpec),
		networks:   newListView(c, networksSpec),
		command:    ti,
		help:       help.New(),
		status:     "connecting…",
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(ping(m.client), ageTick())
}

func ping(c docker.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		info, err := c.Ping(ctx)
		return connectedMsg{info: info, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.Width = msg.Width
		m.command.Width = msg.Width - 2
		m.layout()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case statusMsg:
		m.status, m.statusIsErr = msg.text, msg.isErr
		return m, nil

	case connectedMsg:
		if msg.err != nil {
			// e.g. the daemon isn't running or the socket isn't readable.
			m.status, m.statusIsErr = "cannot reach the Docker daemon: "+msg.err.Error(), true
			return m, nil
		}
		m.server = msg.info
		m.status, m.statusIsErr = fmt.Sprintf("connected to %s (API %s)", msg.info.Host, msg.info.APIVersion), false
		// List once, then follow events and re-list what changes.
		return m, tea.Batch(m.containers.load(), m.images.load(), m.volumes.load(), m.networks.load(), m.events.start())

	case eventsStartedMsg, dockerEventsMsg, eventsEndedMsg, debounceMsg, reconnectMsg:
		var cmd tea.Cmd
		m.events, cmd = m.events.Update(msg)
		return m, cmd

	case actionMsg:
		if m.opts.ReadOnly {
			return m, reportErr(docker.ErrReadOnly) // no point asking
		}
		if msg.prompt != "" {
			m.confirm = &pendingAction{action: msg}
			return m, nil
		}
		return m.startAction(msg)

	case actionDoneMsg:
		if msg.err != nil {
			return m, reportErr(msg.err)
		}
		return m, reportInfo(msg.done)

	case openLogsMsg:
		m.returnTo = m.active
		cmd := m.logs.open(m.client, msg.container)
		m.setActive(viewLogs)
		return m, cmd

	case openInspectMsg:
		m.returnTo = m.active
		cmd := m.inspect.open(m.client, msg.kind, msg.id, msg.name)
		m.setActive(viewInspect)
		return m, cmd

	case openStatsMsg:
		m.returnTo = m.active
		cmd := m.stats.open(m.client, msg.container)
		m.setActive(viewStats)
		return m, cmd

	case statsStartedMsg, statsSamplesMsg, statsEndedMsg:
		var cmd tea.Cmd
		m.stats, cmd = m.stats.Update(msg)
		return m, cmd

	case inspectLoadedMsg:
		var cmd tea.Cmd
		m.inspect, cmd = m.inspect.Update(msg)
		return m, cmd

	case backMsg:
		m.setActive(m.returnTo) // the table logs/inspect were opened from
		return m, nil

	case logStreamStartedMsg, logLinesMsg, logStreamEndedMsg:
		var cmd tea.Cmd
		m.logs, cmd = m.logs.Update(msg)
		return m, cmd

	case reloadMsg:
		var cmds []tea.Cmd
		if msg.containers {
			cmds = append(cmds, m.containers.load())
		}
		if msg.images {
			cmds = append(cmds, m.images.load())
		}
		if msg.volumes {
			cmds = append(cmds, m.volumes.load())
		}
		if msg.networks {
			cmds = append(cmds, m.networks.load())
		}
		return m, tea.Batch(cmds...)

	// Messages of the containers view, whatever view is active.
	case containersLoadedMsg:
		if msg.err == nil {
			m.events.healthy() // the daemon answers: reset the reconnect backoff
		}
		var cmd tea.Cmd
		m.containers, cmd = m.containers.Update(msg)
		return m, cmd

	case imagesLoadedMsg:
		if msg.err == nil {
			m.events.healthy()
		}
		var cmd tea.Cmd
		m.images, cmd = m.images.Update(msg)
		return m, cmd

	case listLoadedMsg[docker.Volume]:
		var cmd tea.Cmd
		m.volumes, cmd = m.volumes.Update(msg)
		return m, cmd

	case listLoadedMsg[docker.Network]:
		var cmd tea.Cmd
		m.networks, cmd = m.networks.Update(msg)
		return m, cmd

	case ageTickMsg:
		var cmd tea.Cmd
		m.containers, cmd = m.containers.Update(msg)
		m.images, _ = m.images.Update(msg)
		m.volumes, _ = m.volumes.Update(msg)
		m.networks, _ = m.networks.Update(msg)
		return m, cmd
	}
	return m.updateActive(msg)
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	// A pending confirmation takes the next key: y runs it, anything else
	// cancels. Destructive actions never run on a stray key.
	if m.confirm != nil {
		a := m.confirm.action
		m.confirm = nil
		if msg.String() == "y" || msg.String() == "Y" {
			return m.startAction(a)
		}
		m.status, m.statusIsErr = "cancelled", false
		return m, nil
	}
	if m.commandMode {
		switch msg.Type {
		case tea.KeyEsc:
			m.exitCommandMode()
			return m, nil
		case tea.KeyEnter:
			input := m.command.Value()
			m.exitCommandMode()
			return m.runCommand(input)
		}
		var cmd tea.Cmd
		m.command, cmd = m.command.Update(msg)
		return m, cmd
	}
	// While a view's text input has focus, every key is typed into it.
	if !m.activeCapturingInput() {
		switch {
		case key.Matches(msg, globalKeyMap.Quit):
			return m, tea.Quit
		case key.Matches(msg, globalKeyMap.Command):
			m.commandMode = true
			m.command.SetValue("")
			return m, m.command.Focus()
		case key.Matches(msg, globalKeyMap.Help):
			m.help.ShowAll = !m.help.ShowAll
			m.layout()
			return m, nil
		}
	}
	return m.updateActive(msg)
}

func (m Model) startAction(a actionMsg) (tea.Model, tea.Cmd) {
	m.status, m.statusIsErr = a.progress, false
	return m, runAction(m.client, a)
}

func (m *Model) exitCommandMode() {
	m.commandMode = false
	m.command.Blur()
}

// runCommand executes a ":" command.
func (m Model) runCommand(input string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return m, nil
	}
	switch fields[0] {
	case "containers", "container", "ps", "co":
		m.setActive(viewContainers)
		return m, nil
	case "images", "image", "img", "i":
		m.setActive(viewImages)
		return m, nil
	case "volumes", "volume", "vol", "v":
		m.setActive(viewVolumes)
		return m, nil
	case "networks", "network", "net", "n":
		m.setActive(viewNetworks)
		return m, nil
	case "q", "quit":
		return m, tea.Quit
	}
	return m, reportErr(fmt.Errorf("unknown command %q", fields[0]))
}

func (m *Model) setActive(v viewID) {
	// Leaving the logs view cancels the stream: no goroutine or HTTP
	// connection is leaked (the single place where this happens).
	if m.active == viewLogs && v != viewLogs {
		m.logs.stop()
	}
	if m.active == viewStats && v != viewStats {
		m.stats.stop()
	}
	m.active = v
	m.layout()
}

func (m Model) updateActive(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.active {
	case viewContainers:
		m.containers, cmd = m.containers.Update(msg)
	case viewImages:
		m.images, cmd = m.images.Update(msg)
	case viewLogs:
		m.logs, cmd = m.logs.Update(msg)
	case viewInspect:
		m.inspect, cmd = m.inspect.Update(msg)
	case viewStats:
		m.stats, cmd = m.stats.Update(msg)
	case viewVolumes:
		m.volumes, cmd = m.volumes.Update(msg)
	case viewNetworks:
		m.networks, cmd = m.networks.Update(msg)
	}
	return m, cmd
}

func (m Model) activeCapturingInput() bool {
	switch m.active {
	case viewContainers:
		return m.containers.capturingInput()
	case viewImages:
		return m.images.capturingInput()
	case viewLogs:
		return m.logs.capturingInput()
	case viewInspect:
		return m.inspect.capturingInput()
	case viewStats:
		return m.stats.capturingInput()
	case viewVolumes:
		return m.volumes.capturingInput()
	case viewNetworks:
		return m.networks.capturingInput()
	}
	return false
}

func (m Model) activeKeys() helpKeys {
	switch m.active {
	case viewContainers:
		return helpKeys{view: m.containers.keys(m.opts.ReadOnly)}
	case viewImages:
		return helpKeys{view: m.images.keys(m.opts.ReadOnly)}
	case viewLogs:
		return helpKeys{view: m.logs.keys()}
	case viewInspect:
		return helpKeys{view: m.inspect.keys()}
	case viewStats:
		return helpKeys{view: m.stats.keys()}
	case viewVolumes:
		return helpKeys{view: m.volumes.keys(m.opts.ReadOnly)}
	case viewNetworks:
		return helpKeys{view: m.networks.keys(m.opts.ReadOnly)}
	}
	return helpKeys{}
}

// layout recomputes the body size and gives it to every view.
func (m *Model) layout() {
	if m.width == 0 {
		return
	}
	chrome := 2 + lipgloss.Height(m.help.View(m.activeKeys())) // header + status + help
	m.bodyHeight = max(m.height-chrome, 1)
	m.containers.SetSize(m.width, m.bodyHeight)
	m.images.SetSize(m.width, m.bodyHeight)
	m.logs.SetSize(m.width, m.bodyHeight)
	m.inspect.SetSize(m.width, m.bodyHeight)
	m.stats.SetSize(m.width, m.bodyHeight)
	m.volumes.SetSize(m.width, m.bodyHeight)
	m.networks.SetSize(m.width, m.bodyHeight)
}

func (m Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	var body string
	switch m.active {
	case viewContainers:
		body = m.containers.View()
	case viewImages:
		body = m.images.View()
	case viewLogs:
		body = m.logs.View()
	case viewInspect:
		body = m.inspect.View()
	case viewStats:
		body = m.stats.View()
	case viewVolumes:
		body = m.volumes.View()
	case viewNetworks:
		body = m.networks.View()
	}
	// Pin the body height so the status and help bars stay at the bottom.
	body = lipgloss.NewStyle().Height(m.bodyHeight).MaxHeight(m.bodyHeight).Render(body)
	return lipgloss.JoinVertical(lipgloss.Left, m.headerView(), body, m.statusView(), m.help.View(m.activeKeys()))
}

func (m Model) headerView() string {
	host := m.server.Host
	if host == "" {
		host = "-"
	}
	info := "host: " + host
	if m.server.APIVersion != "" {
		info += "  api: " + m.server.APIVersion
	}
	var live string
	switch m.events.state {
	case eventsLive:
		live = liveStyle.Render("● live")
		if m.events.last != "" {
			live += headerInfoStyle.Render(m.events.last)
		}
	case eventsReconnecting:
		live = reconnectingStyle.Render("○ reconnecting")
	}
	badge := ""
	if m.opts.ReadOnly {
		badge = readOnlyStyle.Render("read-only")
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(headerStyle.Render("zdocker") + badge + headerInfoStyle.Render(info) + live)
}

func (m Model) statusView() string {
	if m.commandMode {
		return m.command.View()
	}
	if m.confirm != nil {
		return lipgloss.NewStyle().MaxWidth(m.width).Render(m.confirm.View())
	}
	style := statusStyle
	if m.statusIsErr {
		style = statusErrorStyle
	}
	return style.MaxWidth(m.width).Render(m.status)
}
