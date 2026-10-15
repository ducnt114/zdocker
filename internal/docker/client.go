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
