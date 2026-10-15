package docker

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
)

func TestPing(t *testing.T) {
	d := newFakeDaemon(t)
	info, err := d.client().Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.APIVersion != "1.47" || info.OSType != "linux" || !strings.HasPrefix(info.Host, "tcp://") {
		t.Fatalf("info = %+v", info)
	}
}

func TestListContainers(t *testing.T) {
	d := newFakeDaemon(t)
	d.json("GET /containers/json", []container.Summary{
		{ID: "bbbbbbbbbbbbbbbbbbbb", Names: []string{"/web"}, Image: "nginx:1.27", State: container.StateRunning,
			Status: "Up 2 hours", Created: 1700000000,
			Labels: map[string]string{"com.docker.compose.project": "shop"},
			Ports: []container.PortSummary{
				{PrivatePort: 443, Type: "tcp"},
				{IP: netip.MustParseAddr("0.0.0.0"), PrivatePort: 80, PublicPort: 8080, Type: "tcp"},
				{IP: netip.MustParseAddr("::"), PrivatePort: 80, PublicPort: 8080, Type: "tcp"},
			}},
		{ID: "aaaaaaaaaaaaaaaaaaaa", Names: []string{"/api"}, Image: "api:dev", State: container.StateExited,
			Status: "Exited (1) 3 minutes ago"},
	})
	c := d.client()

	got, err := c.ListContainers(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "api" || got[1].Name != "web" {
		t.Fatalf("containers = %+v", got)
	}
	web := got[1]
	if web.ShortID() != "bbbbbbbbbbbb" || web.State != "running" || web.Project != "shop" ||
		!web.Created.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("web = %+v", web)
	}
	if want := "0.0.0.0:8080->80/tcp, [::]:8080->80/tcp, 443/tcp"; web.Ports != want {
		t.Errorf("ports = %q, want %q", web.Ports, want)
	}

	// all=true is passed as a query parameter.
	calls := d.calls()
	if len(calls) != 1 || !strings.Contains(calls[0], "all=1") {
		t.Errorf("calls = %v", calls)
	}
}

func TestListContainersRunningOnly(t *testing.T) {
	d := newFakeDaemon(t)
	d.json("GET /containers/json", []container.Summary{})
	if _, err := d.client().ListContainers(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if calls := d.calls(); strings.Contains(calls[0], "all=1") {
		t.Errorf("running-only list must not ask for all: %v", calls)
	}
}

func TestDaemonUnreachable(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	c, err := New("tcp://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Ping(ctx); err == nil {
		t.Fatal("expected an error for an unreachable daemon")
	}
}
