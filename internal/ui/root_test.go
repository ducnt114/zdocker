package ui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ducnt114/zdocker/internal/docker"
)

// fakeClient is an in-memory docker.Client for UI tests.
type fakeClient struct {
	mu         sync.Mutex
	pingErr    error
	containers []docker.Container
	listCalls  []bool            // the `all` argument of each ListContainers call
	streams    []context.Context // one per Events call
	images     []docker.Image
	imageCalls int
	logCalls   []logCall
	writes     []string
	writeErr   error
	statsCalls []context.Context
	statsIDs   []string

	volumes      []docker.Volume
	networks     []docker.Network
	volumeCalls  int
	networkCalls int
	pulls        []pullCall
	pullBlocks   bool
}

// ContainerLogs emits the container's canned lines and stays open (like
// a follow) until ctx is cancelled.
func (f *fakeClient) ContainerLogs(ctx context.Context, id string, o docker.LogOptions) (<-chan docker.LogLine, <-chan error, error) {
	f.mu.Lock()
	f.logCalls = append(f.logCalls, logCall{id: id, opts: o, ctx: ctx})
	f.mu.Unlock()
	lines := make(chan docker.LogLine, 3)
	lines <- docker.LogLine{Text: "GET /healthz 200"}
	lines <- docker.LogLine{Text: "WARN slow request", Stderr: true}
	lines <- docker.LogLine{Text: "GET / 200"}
	go func() {
		<-ctx.Done()
		close(lines)
	}()
	return lines, make(chan error, 1), nil
}

func (f *fakeClient) Inspect(_ context.Context, kind docker.Kind, id string) (string, error) {
	if id == "gone" {
		return "", errors.New("No such container: gone")
	}
	return "{\n  \"Id\": \"" + id + "\",\n  \"Kind\": \"" + string(kind) + "\"\n}", nil
}

// Writes are recorded as "start w1", "rm w1 force=true", "rmi nginx:1.27".
func (f *fakeClient) record(s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, s)
	return f.writeErr
}
func (f *fakeClient) StartContainer(_ context.Context, id string) error {
	return f.record("start " + id)
}
func (f *fakeClient) StopContainer(_ context.Context, id string) error { return f.record("stop " + id) }
func (f *fakeClient) RestartContainer(_ context.Context, id string) error {
	return f.record("restart " + id)
}
func (f *fakeClient) RemoveContainer(_ context.Context, id string, force bool) error {
	return f.record(fmt.Sprintf("rm %s force=%v", id, force))
}
func (f *fakeClient) RemoveImage(_ context.Context, ref string, force bool) error {
	return f.record(fmt.Sprintf("rmi %s force=%v", ref, force))
}

// ContainerStats sends two samples and stays open until ctx is cancelled.
func (f *fakeClient) ContainerStats(ctx context.Context, id string) (<-chan docker.Stats, <-chan error, error) {
	f.mu.Lock()
	f.statsCalls = append(f.statsCalls, ctx)
	f.statsIDs = append(f.statsIDs, id)
	f.mu.Unlock()
	ch := make(chan docker.Stats, 2)
	now := time.Now()
	ch <- docker.Stats{Time: now, CPUPercent: 10, MemUsed: 100 << 20, MemLimit: 1 << 30, MemPercent: 9.8, PIDs: 3}
	ch <- docker.Stats{Time: now, CPUPercent: 42.5, MemUsed: 200 << 20, MemLimit: 1 << 30, MemPercent: 19.5,
		NetRx: 1_200_000, NetTx: 340_000, BlockRead: 4_100, BlockWrite: 8_200, PIDs: 7}
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, make(chan error, 1), nil
}

func (f *fakeClient) ListVolumes(context.Context) ([]docker.Volume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.volumeCalls++
	return f.volumes, nil
}
func (f *fakeClient) ListNetworks(context.Context) ([]docker.Network, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.networkCalls++
	return f.networks, nil
}
func (f *fakeClient) RemoveVolume(_ context.Context, name string, force bool) error {
	return f.record(fmt.Sprintf("rmvol %s force=%v", name, force))
}
func (f *fakeClient) RemoveNetwork(_ context.Context, id string) error {
	return f.record("rmnet " + id)
}

// PullImage sends a short, scripted pull (or a failure for "nope") and
// records the context so tests can check cancellation.
func (f *fakeClient) PullImage(ctx context.Context, ref string) (<-chan docker.PullProgress, error) {
	f.mu.Lock()
	f.pulls = append(f.pulls, pullCall{ref: ref, ctx: ctx})
	block := f.pullBlocks
	f.mu.Unlock()
	ch := make(chan docker.PullProgress, 4)
	layers := []docker.LayerProgress{{ID: "aaa", Status: "Downloading", Current: 50, Total: 100}, {ID: "bbb", Status: "Already exists"}}
	ch <- docker.PullProgress{Ref: ref, Layers: layers, Status: "Pulling from library/" + ref}
	if block { // a slow pull: stays open until cancelled
		go func() { <-ctx.Done(); close(ch) }()
		return ch, nil
	}
	if ref == "nope" {
		ch <- docker.PullProgress{Ref: ref, Err: errors.New("manifest unknown")}
	} else {
		done := []docker.LayerProgress{{ID: "aaa", Status: "Pull complete"}, {ID: "bbb", Status: "Already exists"}}
		ch <- docker.PullProgress{Ref: ref, Layers: done, Status: "Status: Downloaded newer image for " + ref, Done: true}
	}
	close(ch)
	return ch, nil
}

type pullCall struct {
	ref string
	ctx context.Context
}

func (f *fakeClient) Prune(context.Context) (docker.PruneReport, error) {
	if err := f.record("prune"); err != nil {
		return docker.PruneReport{}, err
	}
	return docker.PruneReport{Containers: 1, Images: 1, SpaceReclaimed: 53_000}, nil
}

type logCall struct {
	id   string
	opts docker.LogOptions
	ctx  context.Context
}

func (f *fakeClient) ListImages(context.Context) ([]docker.Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imageCalls++
	return append([]docker.Image(nil), f.images...), nil
}

// Events returns a stream that stays open (and quiet) until ctx is
// cancelled; tests inject dockerEventsMsg / eventsEndedMsg directly.
func (f *fakeClient) Events(ctx context.Context) (<-chan docker.Event, <-chan error) {
	f.mu.Lock()
	f.streams = append(f.streams, ctx)
	f.mu.Unlock()
	out := make(chan docker.Event)
	go func() {
		<-ctx.Done()
		close(out)
	}()
	return out, make(chan error, 1)
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
	return &fakeClient{volumes: []docker.Volume{
		{Name: "orphan", Driver: "local"},
		{Name: "shop_db", Driver: "local", Project: "shop", InUse: 1, Created: now.Add(-time.Hour)},
	}, networks: []docker.Network{
		{ID: "n-bridge", Name: "bridge", Driver: "bridge", Scope: "local", BuiltIn: true, InUse: 1},
		{ID: "n-old", Name: "old_net", Driver: "bridge", Scope: "local"},
		{ID: "n-shop", Name: "shop_default", Driver: "bridge", Scope: "local", Subnet: "172.18.0.0/16", InUse: 2},
	}, images: []docker.Image{
		{ID: "sha256:dead00000000beef", Repository: "<none>", Tag: "<none>", Size: 5_000, Created: now.Add(-48 * time.Hour)},
		{ID: "sha256:aaaa00000000aaaa", Repository: "nginx", Tag: "1.27", Size: 187_000_000, Created: now.Add(-24 * time.Hour), Containers: 1},
		{ID: "sha256:aaaa00000000aaaa", Repository: "nginx", Tag: "latest", Size: 187_000_000, Created: now.Add(-24 * time.Hour), Containers: 1},
	}, containers: []docker.Container{
		{ID: "a1", Name: "api", Image: "api:dev", State: "exited", Status: "Exited (1) 3 minutes ago", Created: now.Add(-time.Hour)},
		{ID: "w1", Name: "web", Image: "nginx:1.27", State: "running", Status: "Up 2 hours", Ports: "0.0.0.0:8080->80/tcp", Created: now.Add(-2 * time.Hour)},
	}}
}

func startModel(t *testing.T, c docker.Client) Model {
	t.Helper()
	m := New(c, Options{})
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
	rows := m.containers.table.table.rows
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
	if m.containers.showAll || len(m.containers.items) != 1 || m.containers.items["w1"].Name != "web" {
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

func TestEventsTriggerDebouncedReload(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	if len(f.streams) != 1 || m.events.state != eventsLive {
		t.Fatalf("streams=%d state=%v", len(f.streams), m.events.state)
	}
	if !strings.Contains(m.View(), "● live") {
		t.Fatal("header should show the live indicator")
	}
	gen := m.events.gen
	before := len(f.listCalls)

	// A new container appears; two events arrive in a burst.
	f.containers = append(f.containers, docker.Container{ID: "n1", Name: "new", State: "running"})
	m = send(t, m, dockerEventsMsg{gen: gen, events: []docker.Event{{Kind: "container", Action: "create", Name: "new"}}})
	m = send(t, m, dockerEventsMsg{gen: gen, events: []docker.Event{{Kind: "container", Action: "start", Name: "new"}}})
	if len(f.listCalls) != before {
		t.Fatal("must wait for the debounce before re-listing")
	}
	if !m.events.debouncing || !strings.Contains(m.View(), "container new: start") {
		t.Fatalf("debouncing=%v header:\n%s", m.events.debouncing, m.View())
	}

	m = send(t, m, debounceMsg{gen: gen})
	if len(f.listCalls) != before+1 {
		t.Fatalf("one re-list expected for the burst, got %d", len(f.listCalls)-before)
	}
	if len(m.containers.items) != 3 {
		t.Fatalf("items = %d", len(m.containers.items))
	}
	// Image events don't re-list containers.
	m = send(t, m, dockerEventsMsg{gen: gen, events: []docker.Event{{Kind: "image", Action: "pull"}}})
	m = send(t, m, debounceMsg{gen: gen})
	if len(f.listCalls) != before+1 {
		t.Fatal("an image event re-listed containers")
	}
}

func TestEventsReconnectWithBackoff(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	gen := m.events.gen

	m = send(t, m, eventsEndedMsg{gen: gen, err: errors.New("unexpected EOF")})
	if m.events.state != eventsReconnecting || m.events.backoff != time.Second {
		t.Fatalf("state=%v backoff=%v", m.events.state, m.events.backoff)
	}
	if !strings.Contains(m.status, "lost Docker events (unexpected EOF); reconnecting in 1s") {
		t.Fatalf("status = %q", m.status)
	}
	if f.streams[0].Err() == nil {
		t.Fatal("the broken stream's context should be cancelled")
	}
	if !strings.Contains(m.View(), "○ reconnecting") {
		t.Fatal("header should show reconnecting")
	}

	// Reconnect: a new stream, and a full re-list (events were missed).
	before := len(f.listCalls)
	m = send(t, m, reconnectMsg{gen: m.events.gen})
	if len(f.streams) != 2 || m.events.state != eventsLive {
		t.Fatalf("streams=%d state=%v", len(f.streams), m.events.state)
	}
	if len(f.listCalls) != before+1 {
		t.Fatal("reconnecting must re-list containers")
	}
	// The successful list reset the backoff.
	if m.events.backoff != 0 {
		t.Fatalf("backoff = %v after a successful list", m.events.backoff)
	}

	// Leftovers from the old stream are ignored.
	m = send(t, m, eventsEndedMsg{gen: gen})
	if m.events.state != eventsLive {
		t.Fatal("a stale end-of-stream must be ignored")
	}
}

func TestBackoffGrows(t *testing.T) {
	var got []time.Duration
	d := time.Duration(0)
	for range 7 {
		d = nextBackoff(d)
		got = append(got, d)
	}
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30}
	for i := range want {
		if got[i] != want[i]*time.Second {
			t.Fatalf("backoffs = %v", got)
		}
	}
}

func TestImagesView(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	m = typeCommand(t, m, "images")
	if m.active != viewImages {
		t.Fatalf("active = %v", m.active)
	}
	rows := m.images.table.table.rows
	if len(rows) != 3 {
		t.Fatalf("rows = %v", rows)
	}
	// REPOSITORY TAG ID SIZE INUSE AGE
	if rows[1][0] != "nginx" || rows[1][2] != "aaaa00000000" || rows[1][3] != "187MB" || rows[1][4] != "1" {
		t.Errorf("nginx row = %v", rows[1])
	}
	if rows[0][0] != "<none>" || rows[0][4] != "-" {
		t.Errorf("dangling row = %v", rows[0])
	}
	// The total counts the nginx image once, not once per tag.
	if !strings.Contains(m.images.View(), "2 images, 187MB") {
		t.Fatalf("title: %s", m.images.View())
	}
}

func TestContainerCreateReloadsImages(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	gen := m.events.gen

	before := f.imageCalls
	m = send(t, m, dockerEventsMsg{gen: gen, events: []docker.Event{{Kind: "container", Action: "start"}}})
	m = send(t, m, debounceMsg{gen: gen})
	if f.imageCalls != before {
		t.Fatal("a container start doesn't change image usage: no image re-list")
	}
	m = send(t, m, dockerEventsMsg{gen: gen, events: []docker.Event{{Kind: "container", Action: "create"}}})
	m = send(t, m, debounceMsg{gen: gen})
	if f.imageCalls != before+1 {
		t.Fatal("a container create changes the IN USE count: images should be re-listed")
	}
}

func TestFormatSize(t *testing.T) {
	tests := map[int64]string{
		0:             "0B",
		999:           "999B",
		5_000:         "5kB",
		187_000_000:   "187MB",
		1_234_000_000: "1.23GB",
		999_900_000:   "1000MB", // %.3g would print 1e+03: avoided
	}
	for in, want := range tests {
		if got := formatSize(in); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestLogsView(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	m = send(t, m, press("j")) // web
	m = send(t, m, press("enter"))

	if m.active != viewLogs || len(f.logCalls) != 1 || f.logCalls[0].id != "w1" {
		t.Fatalf("active=%v calls=%+v", m.active, f.logCalls)
	}
	if got := m.logs.buf.len(); got != 3 {
		t.Fatalf("buffered %d lines", got)
	}
	view := ansi.Strip(m.logs.View())
	if !strings.Contains(view, "Logs web") || !strings.Contains(view, "▍ WARN slow request") || !strings.Contains(view, "  GET / 200") {
		t.Fatalf("view:\n%s", view)
	}

	// Search ignores the stderr marker; t restarts with timestamps.
	m = send(t, m, press("/"))
	for _, r := range "warn" {
		m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = send(t, m, press("enter"))
	if len(m.logs.matches) != 1 || m.logs.matches[0] != 1 {
		t.Fatalf("matches = %v", m.logs.matches)
	}
	m = send(t, m, press("t"))
	if len(f.logCalls) != 2 || !f.logCalls[1].opts.Timestamps || f.logCalls[0].ctx.Err() == nil {
		t.Fatalf("t should restart the stream with timestamps: %+v", f.logCalls)
	}

	// The search survives the restart: the first esc clears it, the second
	// leaves the view and cancels the stream.
	m = send(t, m, press("esc"))
	if m.active != viewLogs || m.logs.term != "" {
		t.Fatalf("first esc: active=%v term=%q", m.active, m.logs.term)
	}
	m = send(t, m, press("esc"))
	if m.active != viewContainers || f.logCalls[1].ctx.Err() == nil {
		t.Fatalf("active=%v cancelled=%v", m.active, f.logCalls[1].ctx.Err() != nil)
	}
}

func TestLogsStreamEnded(t *testing.T) {
	m := startModel(t, newFake())
	m = send(t, m, press("enter"))
	m = send(t, m, logStreamEndedMsg{gen: m.logs.gen})
	if !m.logs.ended || !strings.Contains(m.status, "log stream closed") {
		t.Fatalf("ended=%v status=%q", m.logs.ended, m.status)
	}
	// A late end from an older stream is ignored after a restart.
	old := m.logs.gen
	m = send(t, m, press("t"))
	m = send(t, m, logStreamEndedMsg{gen: old, err: errors.New("boom")})
	if m.logs.ended {
		t.Fatal("stale end-of-stream applied to the new stream")
	}
}

func typeKeys(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func names(rows [][]string) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r[0])
	}
	return out
}

func TestFilterContainers(t *testing.T) {
	m := startModel(t, newFake())
	m = send(t, m, press("/"))
	if !m.containers.capturingInput() {
		t.Fatal("filter should have focus")
	}
	m = typeKeys(t, m, "nginx") // matches the image, not the name
	m = send(t, m, press("enter"))
	if got := names(m.containers.table.table.rows); len(got) != 1 || got[0] != "web" {
		t.Fatalf("rows = %v", got)
	}
	if !strings.Contains(m.containers.View(), "/nginx") {
		t.Fatal("title should show the active filter")
	}
	// q typed into the filter must not quit.
	m = send(t, m, press("/"))
	next, cmd := m.Update(press("q"))
	m = next.(Model)
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("q quit while typing a filter")
		}
	}
	m = send(t, m, press("esc")) // cancel the input…
	m = send(t, m, press("esc")) // …nothing left to clear
	if got := names(m.containers.table.table.rows); len(got) != 2 {
		t.Fatalf("filter not cleared: %v", got)
	}
}

func TestSortImagesBySizeAndAge(t *testing.T) {
	f := newFake()
	f.images = append(f.images, docker.Image{ID: "sha256:small", Repository: "busybox", Tag: "1.36", Size: 4_260_000, Created: time.Now().Add(-time.Minute)})
	m := startModel(t, f)
	m = typeCommand(t, m, "images")

	// s cycles REPOSITORY → TAG → IMAGE ID → SIZE.
	for range 4 {
		m = send(t, m, press("s"))
	}
	if m.images.table.sortCol != "SIZE" {
		t.Fatalf("sortCol = %q", m.images.table.sortCol)
	}
	// By bytes: 5kB < 4.26MB < 187MB (as text "187MB" < "4.26MB" < "5kB").
	if got := names(m.images.table.table.rows); got[0] != "<none>" || got[1] != "busybox" || got[3] != "nginx" {
		t.Fatalf("by size = %v", got)
	}
	if !strings.Contains(m.images.table.table.cols[3].Title, "SIZE↑") {
		t.Fatalf("header = %q", m.images.table.table.cols[3].Title)
	}
	// AGE: youngest first.
	m = send(t, m, press("s"))
	m = send(t, m, press("s"))
	if m.images.table.sortCol != "AGE" || names(m.images.table.table.rows)[0] != "busybox" {
		t.Fatalf("by age = %v (%q)", names(m.images.table.table.rows), m.images.table.sortCol)
	}
	m = send(t, m, press("S"))
	if names(m.images.table.table.rows)[0] != "<none>" {
		t.Fatalf("reversed age = %v", names(m.images.table.table.rows))
	}
}

func TestSelectionSurvivesSortAndFilter(t *testing.T) {
	m := startModel(t, newFake())
	m = send(t, m, press("j")) // web
	m = send(t, m, press("S")) // reverse: web is now first
	if c, _ := m.containers.selected(); c.Name != "web" {
		t.Fatalf("selected %q after sort", c.Name)
	}
}

func TestInspect(t *testing.T) {
	m := startModel(t, newFake())
	m = typeCommand(t, m, "images")
	m = send(t, m, press("j")) // nginx:1.27
	m = send(t, m, press("i"))
	if m.active != viewInspect {
		t.Fatalf("active = %v", m.active)
	}
	view := ansi.Strip(m.inspect.View())
	if !strings.Contains(view, "Inspect image nginx:1.27") || !strings.Contains(view, `"Kind": "image"`) {
		t.Fatalf("view:\n%s", view)
	}
	// esc returns to the images table it came from.
	m = send(t, m, press("esc"))
	if m.active != viewImages {
		t.Fatalf("active = %v", m.active)
	}

	// A late answer for another object is ignored.
	m = typeCommand(t, m, "ps")
	m = send(t, m, press("i"))
	m = send(t, m, inspectLoadedMsg{id: "image:sha256:other", text: "stale"})
	if strings.Contains(ansi.Strip(m.inspect.View()), "stale") {
		t.Fatal("stale inspect result shown")
	}
}

func startModelWith(t *testing.T, c docker.Client, opts Options) Model {
	t.Helper()
	m := New(c, opts)
	m = send(t, m, tea.WindowSizeMsg{Width: 140, Height: 30})
	for _, msg := range runCmd(m.Init()) {
		m = send(t, m, msg)
	}
	return m
}

func TestStopNeedsConfirmation(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	m = send(t, m, press("j")) // web

	m = send(t, m, press("x"))
	if m.confirm == nil || !strings.Contains(ansi.Strip(m.View()), "Stop container web?") {
		t.Fatalf("expected a confirmation:\n%s", ansi.Strip(m.View()))
	}
	m = send(t, m, press("n"))
	if len(f.writes) != 0 || m.confirm != nil || m.status != "cancelled" {
		t.Fatalf("n should cancel: writes=%v status=%q", f.writes, m.status)
	}
	// Any key other than y cancels — even the action key again.
	m = send(t, m, press("x"))
	m = send(t, m, press("x"))
	if len(f.writes) != 0 {
		t.Fatal("a stray key ran the action")
	}

	m = send(t, m, press("x"))
	m = send(t, m, press("y"))
	if len(f.writes) != 1 || f.writes[0] != "stop w1" || m.status != "web stopped" {
		t.Fatalf("writes=%v status=%q", f.writes, m.status)
	}
}

func TestStartNeedsNoConfirmation(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	m = send(t, m, press("u")) // api (exited)
	if m.confirm != nil || len(f.writes) != 1 || f.writes[0] != "start a1" {
		t.Fatalf("confirm=%v writes=%v", m.confirm != nil, f.writes)
	}
}

func TestRemoveContainerForceOnlyWhenRunning(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	m = send(t, m, press("D")) // api: exited
	if !strings.Contains(ansi.Strip(m.View()), "Remove container api?") {
		t.Fatal("plain remove prompt expected")
	}
	m = send(t, m, press("y"))
	m = send(t, m, press("j")) // web: running
	m = send(t, m, press("D"))
	if !strings.Contains(ansi.Strip(m.View()), "Container web is running: kill and remove it?") {
		t.Fatalf("running remove prompt:\n%s", ansi.Strip(m.View()))
	}
	m = send(t, m, press("y"))
	want := []string{"rm a1 force=false", "rm w1 force=true"}
	if strings.Join(f.writes, "|") != strings.Join(want, "|") {
		t.Fatalf("writes = %v", f.writes)
	}
}

func TestRemoveImageByTagOrID(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	m = typeCommand(t, m, "images") // rows: <none>, nginx:1.27, nginx:latest
	m = send(t, m, press("D"))
	m = send(t, m, press("y"))
	m = send(t, m, press("j"))
	m = send(t, m, press("D"))
	if !strings.Contains(ansi.Strip(m.View()), "used by 1 container(s)") {
		t.Fatalf("in-use warning missing:\n%s", ansi.Strip(m.View()))
	}
	m = send(t, m, press("y"))
	want := []string{"rmi sha256:dead00000000beef force=false", "rmi nginx:1.27 force=false"}
	if strings.Join(f.writes, "|") != strings.Join(want, "|") {
		t.Fatalf("writes = %v", f.writes)
	}
}

func TestActionErrorShown(t *testing.T) {
	f := newFake()
	f.writeErr = errors.New("conflict: unable to remove repository reference (must force)")
	m := startModel(t, f)
	m = typeCommand(t, m, "img")
	m = send(t, m, press("j"))
	m = send(t, m, press("D"))
	m = send(t, m, press("y"))
	if !m.statusIsErr || !strings.Contains(m.status, "must force") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestReadOnlyMode(t *testing.T) {
	f := newFake()
	m := startModelWith(t, docker.ReadOnly(f), Options{ReadOnly: true})
	if !strings.Contains(ansi.Strip(m.View()), "read-only") {
		t.Fatal("header should show the read-only badge")
	}
	if strings.Contains(ansi.Strip(m.View()), "x stop") {
		t.Fatal("write keys should be hidden in read-only mode")
	}
	m = send(t, m, press("x"))
	if m.confirm != nil || !m.statusIsErr || !strings.Contains(m.status, "--read-only") || len(f.writes) != 0 {
		t.Fatalf("confirm=%v status=%q writes=%v", m.confirm != nil, m.status, f.writes)
	}
}

func TestStatsView(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	m = send(t, m, press("j"))  // web
	before := len(f.statsCalls) // the table's own CPU/MEM streams
	m = send(t, m, press("t"))
	if m.active != viewStats || len(f.statsCalls) != before+1 {
		t.Fatalf("active=%v calls=%d→%d", m.active, before, len(f.statsCalls))
	}
	viewCall := f.statsCalls[before]
	view := ansi.Strip(m.stats.View())
	for _, want := range []string{"Stats web", "42.5%", "200MiB / 1GiB (19.5%)", "↓ 1.2MB  ↑ 340kB", "read 4.1kB  write 8.2kB", "peak 42.5%"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}

	// History is capped; samples of an old stream are ignored.
	many := make([]docker.Stats, 70)
	m = send(t, m, statsSamplesMsg{gen: m.stats.gen, samples: many})
	if len(m.stats.history) != statsHistory {
		t.Fatalf("history = %d", len(m.stats.history))
	}
	m = send(t, m, statsSamplesMsg{gen: m.stats.gen - 1, samples: []docker.Stats{{CPUPercent: 999}}})
	if m.stats.history[len(m.stats.history)-1].CPUPercent == 999 {
		t.Fatal("stale sample applied")
	}

	m = send(t, m, press("esc"))
	if m.active != viewContainers || viewCall.Err() == nil {
		t.Fatal("leaving the stats view must cancel the stream")
	}
}

func TestSparkline(t *testing.T) {
	if got := sparkline([]float64{0, 25, 50, 75, 100}, 10, 1); got != "▁▂▄▆█" {
		t.Errorf("got %q", got)
	}
	// Only the last width values are drawn.
	if got := sparkline([]float64{100, 0, 0}, 2, 1); got != "▁▁" {
		t.Errorf("got %q", got)
	}
	// Values are scaled against max(peak, floor): tiny values stay low
	// instead of filling the whole height.
	if got := sparkline([]float64{0.1, 0.2}, 5, 1); got != "▁▂" {
		t.Errorf("got %q", got)
	}
	if got := sparkline([]float64{0.1, 0.2}, 5, 0.01); got != "▄█" {
		t.Errorf("without a meaningful floor, got %q", got)
	}
}

func TestFormatBinary(t *testing.T) {
	tests := map[uint64]string{0: "0B", 512: "512B", 1536: "1.5KiB", 200 << 20: "200MiB", 1 << 30: "1GiB", 3 * (1 << 29): "1.5GiB"}
	for in, want := range tests {
		if got := formatBinary(in); got != want {
			t.Errorf("formatBinary(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestVolumesView(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	m = typeCommand(t, m, "vol")
	if m.active != viewVolumes {
		t.Fatalf("active = %v", m.active)
	}
	rows := m.volumes.table.table.rows
	if len(rows) != 2 || rows[0][0] != "orphan" || rows[0][3] != "-" || rows[1][2] != "shop" || rows[1][3] != "1" {
		t.Fatalf("rows = %v", rows)
	}
	if !strings.Contains(m.volumes.View(), "Volumes[2]  1 unused") {
		t.Fatalf("title: %s", m.volumes.View())
	}

	m = send(t, m, press("D"))
	if !strings.Contains(ansi.Strip(m.View()), "Remove volume orphan and ALL its data?") {
		t.Fatalf("prompt:\n%s", ansi.Strip(m.View()))
	}
	m = send(t, m, press("y"))
	m = send(t, m, press("j"))
	m = send(t, m, press("D"))
	if !strings.Contains(ansi.Strip(m.View()), "used by 1 container(s)") {
		t.Fatal("in-use warning missing")
	}
	m = send(t, m, press("esc"))
	if strings.Join(f.writes, "|") != "rmvol orphan force=false" {
		t.Fatalf("writes = %v", f.writes)
	}

	m = send(t, m, press("i"))
	if m.active != viewInspect || !strings.Contains(ansi.Strip(m.inspect.View()), "Inspect volume shop_db") {
		t.Fatalf("inspect: active=%v\n%s", m.active, ansi.Strip(m.inspect.View()))
	}
	m = send(t, m, press("esc"))
	if m.active != viewVolumes {
		t.Fatalf("esc should return to volumes, got %v", m.active)
	}
}

func TestNetworksView(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	m = typeCommand(t, m, "net")
	rows := m.networks.table.table.rows
	if len(rows) != 3 || rows[2][0] != "shop_default" || rows[2][4] != "172.18.0.0/16" || rows[2][5] != "2" {
		t.Fatalf("rows = %v", rows)
	}
	// Built-in networks can't be removed: refused before asking.
	m = send(t, m, press("D"))
	if m.confirm != nil || !strings.Contains(m.status, "built into Docker") {
		t.Fatalf("confirm=%v status=%q", m.confirm != nil, m.status)
	}
	m = send(t, m, press("j")) // old_net (the client sorts by name)
	m = send(t, m, press("D"))
	m = send(t, m, press("y"))
	if strings.Join(f.writes, "|") != "rmnet n-old" {
		t.Fatalf("writes = %v", f.writes)
	}
}

func TestVolumeAndNetworkEventsReload(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	gen := m.events.gen
	v, n := f.volumeCalls, f.networkCalls

	m = send(t, m, dockerEventsMsg{gen: gen, events: []docker.Event{{Kind: "volume", Action: "create"}}})
	m = send(t, m, debounceMsg{gen: gen})
	if f.volumeCalls != v+1 || f.networkCalls != n {
		t.Fatalf("volume event: volumes %d→%d, networks %d→%d", v, f.volumeCalls, n, f.networkCalls)
	}
	// A new container changes everyone's "in use" count.
	m = send(t, m, dockerEventsMsg{gen: gen, events: []docker.Event{{Kind: "container", Action: "create"}}})
	m = send(t, m, debounceMsg{gen: gen})
	if f.volumeCalls != v+2 || f.networkCalls != n+1 {
		t.Fatalf("container create: volumes %d, networks %d", f.volumeCalls-v, f.networkCalls-n)
	}
}

func composeFake() *fakeClient {
	f := newFake()
	now := time.Now()
	f.containers = []docker.Container{
		{ID: "s1", Name: "shop-api-1", Image: "shop/api", State: "exited", Status: "Exited (1) 1 minute ago", Project: "shop", Created: now},
		{ID: "s2", Name: "shop-web-1", Image: "nginx", State: "running", Status: "Up 1 hour", Project: "shop", Created: now},
		{ID: "b1", Name: "blog-db-1", Image: "postgres", State: "running", Status: "Up 1 hour", Project: "blog", Created: now},
		{ID: "x1", Name: "solo", Image: "busybox", State: "running", Status: "Up 1 hour", Created: now},
	}
	return f
}

func TestGroupByComposeProject(t *testing.T) {
	m := startModel(t, composeFake())
	m = send(t, m, press("g"))
	// Grouping keeps the selection on the same container.
	if k, _ := m.containers.table.Selected(); k != "b1" {
		t.Fatalf("selected %q after grouping", k)
	}

	want := []string{"▾ blog", "  blog-db-1", "▾ shop", "  shop-api-1", "  shop-web-1", "▾ (no project)", "  solo"}
	if got := names(m.containers.table.table.rows); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("rows = %q", got)
	}
	if hdr := m.containers.table.table.rows[2]; hdr[3] != "1/2 running" {
		t.Fatalf("shop header = %q", hdr)
	}
	if !strings.Contains(m.containers.View(), "Containers(all)[4]") {
		t.Fatal("the count must not include group headers")
	}

	// Sorting (reversed) stays within groups; headers keep leading.
	m = send(t, m, press("S"))
	want = []string{"▾ blog", "  blog-db-1", "▾ shop", "  shop-web-1", "  shop-api-1", "▾ (no project)", "  solo"}
	if got := names(m.containers.table.table.rows); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("reversed = %q", got)
	}
	m = send(t, m, press("S"))

	// enter on the shop header folds it; the cursor stays on it. (Home
	// goes to the top: "g" means "group" in this view.)
	m = send(t, m, tea.KeyMsg{Type: tea.KeyHome})
	m = send(t, m, press("j"))
	m = send(t, m, press("j"))
	m = send(t, m, press("enter"))
	want = []string{"▾ blog", "  blog-db-1", "▸ shop", "▾ (no project)", "  solo"}
	if got := names(m.containers.table.table.rows); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("folded = %q", got)
	}
	if k, _ := m.containers.table.Selected(); k != "group:shop" || m.active != viewContainers {
		t.Fatalf("selected %q, active %v", k, m.active)
	}

	// Filtering for a member of the folded group keeps its header.
	m = send(t, m, press("/"))
	m = typeKeys(t, m, "web")
	m = send(t, m, press("enter"))
	if got := names(m.containers.table.table.rows); strings.Join(got, "|") != "▸ shop" {
		t.Fatalf("filtered = %q", got)
	}
	m = send(t, m, press("esc"))

	// enter on a container (not a header) still opens its logs.
	m = send(t, m, press("k"))
	m = send(t, m, press("enter"))
	if m.active != viewLogs || m.logs.container.Name != "blog-db-1" {
		t.Fatalf("active=%v container=%q", m.active, m.logs.container.Name)
	}
}

func TestGroupHeaderIgnoresContainerActions(t *testing.T) {
	f := composeFake()
	m := startModel(t, f)
	m = send(t, m, press("g"))
	m = send(t, m, tea.KeyMsg{Type: tea.KeyHome}) // the "blog" header
	for _, k := range []string{"x", "D", "u", "i", "t"} {
		m = send(t, m, press(k))
	}
	if m.confirm != nil || len(f.writes) != 0 || m.active != viewContainers {
		t.Fatalf("actions on a header: confirm=%v writes=%v active=%v", m.confirm != nil, f.writes, m.active)
	}
}

// contextWorld is a set of docker contexts, each with its own fake daemon.
type contextWorld struct {
	daemons  map[string]*fakeClient
	connects []string
}

func (w *contextWorld) options(current string) Options {
	return Options{
		Context: current,
		Contexts: func() ([]docker.Context, error) {
			return []docker.Context{
				{Name: "default", Host: "unix:///var/run/docker.sock", Current: true},
				{Name: "colima", Host: "unix:///home/me/.colima/docker.sock"},
				{Name: "far", Host: "ssh://me@far"},
			}, nil
		},
		Connect: func(name string) (docker.Client, error) {
			w.connects = append(w.connects, name)
			if name == "far" {
				return nil, errors.New(`context "far" uses ssh: not supported yet`)
			}
			return w.daemons[name], nil
		},
	}
}

func TestSwitchContext(t *testing.T) {
	colima := newFake()
	colima.containers = []docker.Container{{ID: "z1", Name: "on-colima", State: "running"}}
	w := &contextWorld{daemons: map[string]*fakeClient{"default": newFake(), "colima": colima}}
	m := startModelWith(t, w.daemons["default"], w.options("default"))
	oldStream := w.daemons["default"].streams[0]

	m = typeCommand(t, m, "ctx")
	if m.active != viewContexts {
		t.Fatalf("active = %v", m.active)
	}
	rows := m.contexts.table.table.rows
	if len(rows) != 3 || rows[0][0] != "* default" || rows[0][3] != "current" || rows[1][0] != "  colima" {
		t.Fatalf("rows = %v", rows)
	}

	if k, _ := m.contexts.table.Selected(); k != "default" {
		t.Fatalf("the cursor should start on the context in use, got %q", k)
	}
	m = send(t, m, press("j"))
	m = send(t, m, press("enter"))
	if m.active != viewContainers || m.client != docker.Client(colima) {
		t.Fatalf("active=%v client switched=%v", m.active, m.client == docker.Client(colima))
	}
	if oldStream.Err() == nil {
		t.Fatal("the old daemon's event stream must be stopped")
	}
	if got := names(m.containers.table.table.rows); len(got) != 1 || got[0] != "on-colima" {
		t.Fatalf("containers = %v", got)
	}
	if !strings.Contains(ansi.Strip(m.View()), "ctx: colima") {
		t.Fatal("header should show the new context")
	}

	// The contexts view marks the one in use (not the CLI's current one).
	m = typeCommand(t, m, "ctx")
	if rows := m.contexts.table.table.rows; rows[1][0] != "* colima" || rows[0][0] != "  default" {
		t.Fatalf("rows = %v", rows)
	}
	if k, _ := m.contexts.table.Selected(); k != "colima" {
		t.Fatalf("cursor on %q, want the active colima", k)
	}

	// A late ping answer from the old client is ignored.
	m = send(t, m, connectedMsg{client: w.daemons["default"], err: errors.New("old")})
	if m.statusIsErr {
		t.Fatalf("stale ping applied: %q", m.status)
	}
}

func TestSwitchContextFailureKeepsCurrent(t *testing.T) {
	w := &contextWorld{daemons: map[string]*fakeClient{"default": newFake()}}
	m := startModelWith(t, w.daemons["default"], w.options("default"))
	m = typeCommand(t, m, "ctx far")
	if !m.statusIsErr || !strings.Contains(m.status, "ssh") || m.client != docker.Client(w.daemons["default"]) {
		t.Fatalf("status=%q", m.status)
	}
	if len(w.daemons["default"].streams) != 1 || w.daemons["default"].streams[0].Err() != nil {
		t.Fatal("a failed switch must not touch the current connection")
	}
}

func TestSwitchContextStopsLogs(t *testing.T) {
	w := &contextWorld{daemons: map[string]*fakeClient{"default": newFake(), "colima": newFake()}}
	m := startModelWith(t, w.daemons["default"], w.options("default"))
	m = send(t, m, press("enter")) // logs of the first container
	logCtx := w.daemons["default"].logCalls[0].ctx
	m = typeCommand(t, m, "ctx colima")
	if logCtx.Err() == nil || m.active != viewContainers {
		t.Fatal("switching context must stop the log stream")
	}
}

func TestPullImage(t *testing.T) {
	f := newFake()
	m := startModel(t, f)
	m = typeCommand(t, m, "pull redis:7")
	if len(f.pulls) != 1 || f.pulls[0].ref != "redis:7" {
		t.Fatalf("pulls = %+v", f.pulls)
	}
	if m.active != viewPull || !m.pull.state.Done || m.status != "pulled redis:7" {
		t.Fatalf("active=%v done=%v status=%q", m.active, m.pull.state.Done, m.status)
	}
	view := ansi.Strip(m.pull.View())
	for _, want := range []string{"Pull redis:7", "done", "100%", "aaa", "Pull complete", "Downloaded newer image for redis:7"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}
	m = send(t, m, press("esc"))
	if m.active != viewImages {
		t.Fatalf("esc should show the images, got %v", m.active)
	}
}

func TestPullFailure(t *testing.T) {
	m := startModel(t, newFake())
	m = typeCommand(t, m, "pull nope")
	if !m.statusIsErr || !strings.Contains(m.status, "manifest unknown") || !strings.Contains(ansi.Strip(m.pull.View()), "failed") {
		t.Fatalf("status=%q view:\n%s", m.status, ansi.Strip(m.pull.View()))
	}
}

func TestPullCancelledOnLeave(t *testing.T) {
	f := newFake()
	f.pullBlocks = true
	m := startModel(t, f)
	m = typeCommand(t, m, "pull big/image:1")
	if !m.pull.running {
		t.Fatal("pull should be running")
	}
	m = send(t, m, press("esc"))
	if f.pulls[0].ctx.Err() == nil {
		t.Fatal("leaving the pull view must cancel the pull")
	}
}

func TestPullKeyPrefillsCommand(t *testing.T) {
	m := startModel(t, newFake())
	m = typeCommand(t, m, "img")
	m = send(t, m, press("j")) // nginx:1.27
	m = send(t, m, press("p"))
	if !m.commandMode || m.command.Value() != "pull nginx:1.27" {
		t.Fatalf("command mode=%v value=%q", m.commandMode, m.command.Value())
	}
}

func TestPullReadOnlyAndUsage(t *testing.T) {
	f := newFake()
	m := startModelWith(t, docker.ReadOnly(f), Options{ReadOnly: true})
	m = typeCommand(t, m, "pull redis")
	if !strings.Contains(m.status, "--read-only") || len(f.pulls) != 0 {
		t.Fatalf("status=%q pulls=%d", m.status, len(f.pulls))
	}
	m = typeCommand(t, m, "pull")
	if !strings.Contains(m.status, "usage") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestPullFraction(t *testing.T) {
	l := func(status string, cur, total int64) docker.LayerProgress {
		return docker.LayerProgress{Status: status, Current: cur, Total: total}
	}
	tests := []struct {
		layer docker.LayerProgress
		want  float64
	}{
		{l("Waiting", 0, 0), 0},
		{l("Downloading", 50, 100), 0.4},
		{l("Download complete", 0, 0), 0.8},
		{l("Extracting", 50, 100), 0.9},
		{l("Pull complete", 0, 0), 1},
		{l("Already exists", 0, 0), 1},
		{l("Downloading", 0, 0), 0}, // unknown size
	}
	for _, tt := range tests {
		if got := layerFraction(tt.layer); math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("%s %d/%d: %.2f, want %.2f", tt.layer.Status, tt.layer.Current, tt.layer.Total, got, tt.want)
		}
	}
	p := docker.PullProgress{Layers: []docker.LayerProgress{l("Pull complete", 0, 0), l("Waiting", 0, 0)}}
	if got := pullFraction(p); got != 0.5 {
		t.Errorf("overall = %.2f", got)
	}
	p.Done = true
	if pullFraction(p) != 1 {
		t.Error("a finished pull is 100%")
	}
}

func TestPrune(t *testing.T) {
	f := newFake() // api: exited; <none> image of 5kB
	m := startModel(t, f)
	m = typeCommand(t, m, "prune")
	if m.confirm == nil {
		t.Fatalf("expected a confirmation, status=%q", m.status)
	}
	prompt := ansi.Strip(m.View())
	if !strings.Contains(prompt, "Prune: remove 1 stopped container(s) (api) and 1 dangling image(s) (5kB)?") {
		t.Fatalf("prompt:\n%s", prompt)
	}
	m = send(t, m, press("y"))
	if strings.Join(f.writes, "|") != "prune" || m.status != "removed 1 container(s), 1 image(s), reclaimed 53kB" {
		t.Fatalf("writes=%v status=%q", f.writes, m.status)
	}
}

func TestPruneNothingToDo(t *testing.T) {
	f := newFake()
	f.containers = f.containers[1:] // only the running web
	f.images = f.images[1:]         // no dangling image
	m := startModel(t, f)
	m = typeCommand(t, m, "prune")
	if m.confirm != nil || m.status != "nothing to prune" {
		t.Fatalf("confirm=%v status=%q", m.confirm != nil, m.status)
	}
}

func TestPrunePromptListsAFewNames(t *testing.T) {
	p := prunePlan{containers: []string{"a", "b", "c", "d", "e"}}
	a, _ := pruneAction(p)
	if a.prompt != "Prune: remove 5 stopped container(s) (a, b, c, +2)?" {
		t.Fatalf("prompt = %q", a.prompt)
	}
}

func TestExecArgs(t *testing.T) {
	c := docker.Container{ID: "abc123", Name: "web", State: "running"}
	tests := []struct {
		ctx, host  string
		wantPrefix string
	}{
		{"default", "unix:///var/run/docker.sock", "exec -it abc123"},   // same env as zdocker
		{"lab", "tcp://10.0.0.5:2376", "--context lab exec -it abc123"}, // same context
		{"", "tcp://127.0.0.1:2375", "-H tcp://127.0.0.1:2375 exec -it abc123"},
	}
	for _, tt := range tests {
		got := strings.Join(execArgs(tt.ctx, tt.host, c), " ")
		if !strings.HasPrefix(got, tt.wantPrefix) || !strings.HasSuffix(got, "sh -c "+shellScript) {
			t.Errorf("ctx=%q host=%q: %q", tt.ctx, tt.host, got)
		}
	}
}

func TestOpenShell(t *testing.T) {
	var ran []string
	r := shellRunner{
		lookPath: func(string) (string, error) { return "/usr/bin/docker", nil },
		command: func(name string, args ...string) *exec.Cmd {
			ran = append([]string{name}, args...)
			return exec.Command("true")
		},
	}
	running := docker.Container{ID: "abc", Name: "web", State: "running"}

	if cmd := r.openShell("lab", "", running); cmd == nil {
		t.Fatal("expected an exec command")
	}
	if strings.Join(ran, " ") != "/usr/bin/docker --context lab exec -it abc sh -c "+shellScript {
		t.Fatalf("command = %q", ran)
	}

	stopped := docker.Container{Name: "api", State: "exited"}
	if msg := r.openShell("lab", "", stopped)(); !strings.Contains(fmt.Sprint(msg), "needs a running container") {
		t.Fatalf("stopped container: %v", msg)
	}

	noCLI := shellRunner{lookPath: func(string) (string, error) { return "", errors.New("not found") }, command: r.command}
	if msg := noCLI.openShell("lab", "", running)(); !strings.Contains(fmt.Sprint(msg), "needs the docker CLI") {
		t.Fatalf("no docker CLI: %v", msg)
	}
}

func TestShellReadOnlyAndExit(t *testing.T) {
	f := newFake()
	m := startModelWith(t, docker.ReadOnly(f), Options{ReadOnly: true})
	m = send(t, m, press("j"))
	m = send(t, m, press("e"))
	if !strings.Contains(m.status, "--read-only") {
		t.Fatalf("status = %q", m.status)
	}

	m = startModel(t, newFake())
	m = send(t, m, shellExitedMsg{name: "web"})
	if m.status != "left the shell in web" {
		t.Fatalf("status = %q", m.status)
	}
	m = send(t, m, shellExitedMsg{name: "web", err: errors.New("exit status 127")})
	if !m.statusIsErr || !strings.Contains(m.status, "shell in web: exit status 127") {
		t.Fatalf("status = %q", m.status)
	}
}

// statsStream returns the context of the table's stream for container id.
func statsStream(f *fakeClient, id string) context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.statsIDs) - 1; i >= 0; i-- {
		if f.statsIDs[i] == id {
			return f.statsCalls[i]
		}
	}
	return nil
}

func TestTableMetrics(t *testing.T) {
	f := newFake() // api exited, web running
	m := startModel(t, f)
	waitFor(t, func() bool { return statsStream(f, "w1") != nil })
	if statsStream(f, "a1") != nil {
		t.Fatal("no stats stream for a stopped container")
	}
	cols := m.containers.table.table.cols
	if cols[5].Title != "CPU" || cols[6].Title != "MEM" {
		t.Fatalf("columns = %+v", cols)
	}

	// Samples arrive (injected: the stream goroutines are async).
	gen := m.containers.metrics.gen
	m = send(t, m, tableStatsMsg{gen: gen, samples: []statsSample{{id: "w1", s: docker.Stats{CPUPercent: 12.34, MemUsed: 64 << 20}}}})
	rows := m.containers.table.table.rows
	if rows[1][5] != "12.3%" || rows[1][6] != "64MiB" || rows[0][5] != "-" {
		t.Fatalf("rows = %v", rows)
	}

	// web stops: its stream is cancelled and its numbers dropped.
	f.containers[1].State, f.containers[1].Status = "exited", "Exited (0) 1 second ago"
	webStream := statsStream(f, "w1")
	m = send(t, m, press("r"))
	if webStream.Err() == nil {
		t.Fatal("the stream of a stopped container must be cancelled")
	}
	if _, ok := m.containers.metrics.latest["w1"]; ok || m.containers.table.table.rows[1][5] != "-" {
		t.Fatal("stale numbers kept for a stopped container")
	}
}

func TestTableMetricsToggleAndSort(t *testing.T) {
	f := newFake()
	f.containers = append(f.containers, docker.Container{ID: "b1", Name: "busy", State: "running"})
	m := startModel(t, f)
	waitFor(t, func() bool { return statsStream(f, "b1") != nil && statsStream(f, "w1") != nil })
	gen := m.containers.metrics.gen
	m = send(t, m, tableStatsMsg{gen: gen, samples: []statsSample{
		{id: "w1", s: docker.Stats{CPUPercent: 9}},
		{id: "b1", s: docker.Stats{CPUPercent: 120}}, // more than one CPU
	}})

	for range 6 { // NAME IMAGE STATE STATUS PORTS CPU
		m = send(t, m, press("s"))
	}
	m = send(t, m, press("S")) // busiest first
	if got := names(m.containers.table.table.rows); got[0] != "busy" || got[1] != "web" {
		t.Fatalf("by CPU desc = %v (sort %q)", got, m.containers.table.sortCol)
	}

	// m turns the columns and every stream off.
	streams := []context.Context{statsStream(f, "w1"), statsStream(f, "b1")}
	m = send(t, m, press("m"))
	if len(m.containers.table.table.cols) != 6 || streams[0].Err() == nil || streams[1].Err() == nil {
		t.Fatalf("cols=%d cancelled=%v,%v", len(m.containers.table.table.cols), streams[0].Err() != nil, streams[1].Err() != nil)
	}
	f.mu.Lock()
	calls := len(f.statsCalls)
	f.mu.Unlock()
	m = send(t, m, press("m")) // and back on: streams restart
	waitFor(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.statsCalls) >= calls+2 })
	if len(m.containers.table.table.cols) != 8 {
		t.Fatalf("cols = %d", len(m.containers.table.table.cols))
	}
}

func TestContextSwitchStopsTableMetrics(t *testing.T) {
	w := &contextWorld{daemons: map[string]*fakeClient{"default": newFake(), "colima": newFake()}}
	m := startModelWith(t, w.daemons["default"], w.options("default"))
	waitFor(t, func() bool { return statsStream(w.daemons["default"], "w1") != nil })
	old := statsStream(w.daemons["default"], "w1")
	m = typeCommand(t, m, "ctx colima")
	if old.Err() == nil {
		t.Fatal("switching context must stop the old daemon's stats streams")
	}
}

// waitFor polls cond briefly: the stats pool starts streams in goroutines.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for range 100 {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
