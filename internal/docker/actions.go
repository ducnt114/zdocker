package docker

import (
	"context"

	mobyclient "github.com/moby/moby/client"
)

// stopTimeoutSeconds is how long a container gets to exit after SIGTERM
// before it is killed — `docker stop`'s default.
const stopTimeoutSeconds = 10

func (c *client) StartContainer(ctx context.Context, id string) error {
	_, err := c.api.ContainerStart(ctx, id, mobyclient.ContainerStartOptions{})
	return err
}

func (c *client) StopContainer(ctx context.Context, id string) error {
	t := stopTimeoutSeconds
	_, err := c.api.ContainerStop(ctx, id, mobyclient.ContainerStopOptions{Timeout: &t})
	return err
}

func (c *client) RestartContainer(ctx context.Context, id string) error {
	t := stopTimeoutSeconds
	_, err := c.api.ContainerRestart(ctx, id, mobyclient.ContainerRestartOptions{Timeout: &t})
	return err
}

// RemoveContainer removes a container; force kills it first if it runs
// (`docker rm -f`). Volumes are kept.
func (c *client) RemoveContainer(ctx context.Context, id string, force bool) error {
	_, err := c.api.ContainerRemove(ctx, id, mobyclient.ContainerRemoveOptions{Force: force})
	return err
}

// RemoveImage removes an image reference like `docker rmi`: given a
// "repo:tag" it removes that tag, and the image itself once no tag is
// left; given an ID it removes the image (refused if several tags point
// to it, unless force).
func (c *client) RemoveImage(ctx context.Context, ref string, force bool) error {
	_, err := c.api.ImageRemove(ctx, ref, mobyclient.ImageRemoveOptions{Force: force})
	return err
}
