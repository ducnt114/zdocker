package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"
)

// composeProjectLabel is set by Docker Compose on the containers it creates.
const composeProjectLabel = "com.docker.compose.project"

// Container is the UI-friendly summary of a container: one table row.
type Container struct {
	ID      string // full ID
	Name    string // without the leading "/"
	Image   string
	State   string // running, exited, paused, restarting, created, dead…
	Status  string // human text from the daemon: "Up 3 hours", "Exited (1) 2 minutes ago"
	Ports   string // like `docker ps`: "0.0.0.0:8080->80/tcp, 443/tcp"
	Created time.Time
	Project string // Docker Compose project, if any
	Labels  map[string]string
}

// ShortID is the 12-character ID the docker CLI shows.
func (c Container) ShortID() string { return shortID(c.ID) }

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// newContainer converts the API's summary into a Container.
func newContainer(s container.Summary) Container {
	name := shortID(s.ID)
	if len(s.Names) > 0 {
		name = strings.TrimPrefix(s.Names[0], "/")
	}
	return Container{
		ID:      s.ID,
		Name:    name,
		Image:   s.Image,
		State:   string(s.State),
		Status:  s.Status,
		Ports:   formatPorts(s.Ports),
		Created: time.Unix(s.Created, 0),
		Project: s.Labels[composeProjectLabel],
		Labels:  s.Labels,
	}
}

// formatPorts renders ports the way `docker ps` does. A port published on
// both IPv4 and IPv6 is listed once per address family, as by the CLI.
func formatPorts(ports []container.PortSummary) string {
	sorted := append([]container.PortSummary(nil), ports...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].PrivatePort != sorted[j].PrivatePort {
			return sorted[i].PrivatePort < sorted[j].PrivatePort
		}
		return sorted[i].Type < sorted[j].Type
	})
	var out []string
	seen := map[string]bool{}
	for _, p := range sorted {
		var s string
		if p.PublicPort != 0 {
			ip := "0.0.0.0"
			if p.IP.IsValid() {
				ip = p.IP.String()
				if p.IP.Is6() {
					ip = "[" + ip + "]"
				}
			}
			s = fmt.Sprintf("%s:%d->%d/%s", ip, p.PublicPort, p.PrivatePort, p.Type)
		} else {
			s = fmt.Sprintf("%d/%s", p.PrivatePort, p.Type)
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return strings.Join(out, ", ")
}

func (c *client) ListContainers(ctx context.Context, all bool) ([]Container, error) {
	res, err := c.api.ContainerList(ctx, mobyclient.ContainerListOptions{All: all})
	if err != nil {
		return nil, err
	}
	out := make([]Container, len(res.Items))
	for i, s := range res.Items {
		out[i] = newContainer(s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
