package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ducnt114/zdocker/internal/docker"
)

// fakeClient is an in-memory docker.Client for UI tests.
type fakeClient struct {
	mu         sync.Mutex
	pingErr    error
	containers []docker.Container
	listCalls  []bool // the `all` argument of each ListContainers call
}

func (f *fakeClient) Ping(context.Context) (docker.ServerInfo, error) {
	return docker.ServerInfo{Host: "unix:///var/run/docker.sock", APIVersion: "1.47", OSType: "linux"}, f.pingErr
}

func (f *fakeClient) ListContainers(_ context.Context, all bool) ([]docker.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls = append(f.listCalls, all)
	var out []docker.Container
	for _, c := range f.containers {
		if all || c.State == "running" {
			out = append(out, c)
		}
	}
	return out, nil
}

func newFake() *fakeClient {
	now := time.Now()
	return &fakeClient{containers: []docker.Container{
		{ID: "a1", Name: "api", Image: "api:dev", State: "exited", Status: "Exited (1) 3 minutes ago", Created: now.Add(-time.Hour)},
		{ID: "w1", Name: "web", Image: "nginx:1.27", State: "running", Status: "Up 2 hours", Ports: "0.0.0.0:8080->80/tcp", Created: now.Add(-2 * time.Hour)},
	}}
}

func startModel(t *testing.T, c docker.Client) Model {
	t.Helper()
	m := New(c)
	m = send(t, m, tea.WindowSizeMsg{Width: 140, Height: 30})
	for _, msg := range runCmd(m.Init()) {
		m = send(t, m, msg)
	}
	return m
}

func TestStartupListsContainers(t *testing.T) {
	m := startModel(t, newFake())
	if m.server.APIVersion != "1.47" || !strings.Contains(m.status, "connected to unix:///var/run/docker.sock") {
		t.Fatalf("server=%+v status=%q", m.server, m.status)
	}
	rows := m.containers.table.rows
	if len(rows) != 2 || rows[0][0] != "api" || rows[1][4] != "0.0.0.0:8080->80/tcp" {
		t.Fatalf("rows = %v", rows)
	}
	if !strings.Contains(m.View(), "Containers(all)[2]") {
		t.Fatalf("view:\n%s", m.View())
	}
}

func TestToggleAll(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	m = send(t, m, press("a"))
	if m.containers.showAll || len(m.containers.items) != 1 || m.containers.items[0].Name != "web" {
		t.Fatalf("showAll=%v items=%+v", m.containers.showAll, m.containers.items)
	}
	if f.listCalls[len(f.listCalls)-1] {
		t.Fatal("running-only view must list with all=false")
	}
	if !strings.Contains(m.containers.View(), "Containers(running)[1]") {
		t.Fatalf("title: %s", m.containers.View())
	}
}

func TestStaleLoadIgnored(t *testing.T) {
	m := startModel(t, newFake())
	old := m.containers.gen
	m = send(t, m, press("r")) // a newer load
	m = send(t, m, containersLoadedMsg{gen: old, items: nil})
	if len(m.containers.items) != 2 {
		t.Fatal("an older load result replaced a newer one")
	}
}

func TestCursorFollowsContainer(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	m = send(t, m, press("j")) // web
	// A new container sorts before web: the cursor must stay on web.
	f.containers = append([]docker.Container{{ID: "0", Name: "aaa", State: "running"}}, f.containers...)
	m = send(t, m, press("r"))
	if c, _ := m.containers.selected(); c.Name != "web" {
		t.Fatalf("selected %q", c.Name)
	}
}

func TestDaemonUnreachable(t *testing.T) {
	f := newFake()
	f.pingErr = errors.New("dial unix /var/run/docker.sock: connect: no such file or directory")
	m := startModel(t, f)
	if !m.statusIsErr || !strings.Contains(m.status, "cannot reach the Docker daemon") {
		t.Fatalf("status = %q", m.status)
	}
	if len(f.listCalls) != 0 {
		t.Fatal("must not list containers before connecting")
	}
}

func TestUnknownCommand(t *testing.T) {
	m := startModel(t, newFake())
	m = typeCommand(t, m, "nope")
	if !m.statusIsErr {
		t.Fatal("expected an error")
	}
}

func TestContainerHealth(t *testing.T) {
	tests := []struct {
		state, status string
		want          health
	}{
		{"running", "Up 2 hours", healthy},
		{"running", "Up 2 hours (healthy)", healthy},
		{"running", "Up 5 seconds (health: starting)", pending},
		{"running", "Up 2 hours (unhealthy)", failing},
		{"exited", "Exited (0) 3 minutes ago", finished},
		{"exited", "Exited (137) 3 minutes ago", failing},
		{"restarting", "Restarting (1) 2 seconds ago", pending},
		{"paused", "Up 1 hour (Paused)", pending},
		{"dead", "Dead", failing},
		{"created", "Created", finished},
	}
	for _, tt := range tests {
		got := containerHealth(docker.Container{State: tt.state, Status: tt.status})
		if got != tt.want {
			t.Errorf("%s / %q: got %d, want %d", tt.state, tt.status, got, tt.want)
		}
	}
}
