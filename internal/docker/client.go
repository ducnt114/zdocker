// Package docker is zdocker's gateway to the Docker Engine API. It wraps the
// official Moby client and returns UI-friendly types. It deliberately knows
// nothing about the terminal UI so it can be tested on its own.
package docker

import (
	"context"

	mobyclient "github.com/moby/moby/client"
)

// Client is everything the UI may ask the Docker daemon. The UI depends on
// this interface only, so tests can replace it with a fake.
type Client interface {
	// Ping checks the daemon is reachable and returns basic facts about it.
	Ping(ctx context.Context) (ServerInfo, error)

	// ListContainers returns containers sorted by name; all=false returns
	// only running ones (like `docker ps` without -a).
	ListContainers(ctx context.Context, all bool) ([]Container, error)

	// ListImages returns one row per image tag, with the number of
	// containers using each image.
	ListImages(ctx context.Context) ([]Image, error)

	// ContainerLogs follows a container's logs until ctx is cancelled,
	// with stdout and stderr told apart.
	ContainerLogs(ctx context.Context, id string, o LogOptions) (<-chan LogLine, <-chan error, error)

	// ContainerStats streams resource usage samples (about one per second)
	// until ctx is cancelled.
	ContainerStats(ctx context.Context, id string) (<-chan Stats, <-chan error, error)

	// Inspect returns `docker inspect` output (indented JSON) for a
	// container or an image.
	Inspect(ctx context.Context, kind Kind, id string) (string, error)

	// Events streams container and image events until ctx is cancelled or
	// the stream breaks. Callers re-list on events (there is no watch with
	// resource versions as in Kubernetes).
	Events(ctx context.Context) (<-chan Event, <-chan error)

	// Writes. ReadOnly(c) turns all of these into errors.

	StartContainer(ctx context.Context, id string) error
	// StopContainer sends SIGTERM, then SIGKILL after 10 seconds.
	StopContainer(ctx context.Context, id string) error
	RestartContainer(ctx context.Context, id string) error
	RemoveContainer(ctx context.Context, id string, force bool) error
	// RemoveImage removes a "repo:tag" (untag) or an image ID.
	RemoveImage(ctx context.Context, ref string, force bool) error
}

// ServerInfo describes the daemon zdocker talks to.
type ServerInfo struct {
	Host       string // e.g. unix:///var/run/docker.sock
	APIVersion string // negotiated API version
	OSType     string // linux, windows
}

type client struct {
	api *mobyclient.Client
}

// New connects to the daemon at host, or, if host is empty, to the one the
// environment points to ($DOCKER_HOST, $DOCKER_TLS_VERIFY, …), like the
// docker CLI. Nothing is sent until the first call.
func New(host string) (Client, error) {
	opts := []mobyclient.Opt{mobyclient.FromEnv}
	if host != "" {
		opts = append(opts, mobyclient.WithHost(host))
	}
	api, err := mobyclient.New(opts...)
	if err != nil {
		return nil, err
	}
	return &client{api: api}, nil
}

func (c *client) Ping(ctx context.Context) (ServerInfo, error) {
	res, err := c.api.Ping(ctx, mobyclient.PingOptions{})
	if err != nil {
		return ServerInfo{}, err
	}
	return ServerInfo{Host: c.api.DaemonHost(), APIVersion: res.APIVersion, OSType: res.OSType}, nil
}
