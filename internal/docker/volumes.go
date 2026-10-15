package docker

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	mobyclient "github.com/moby/moby/client"
)

// Volume is one row of the volumes table.
type Volume struct {
	Name       string
	Driver     string
	Mountpoint string
	Created    time.Time // zero if the daemon didn't say
	Project    string    // Docker Compose project, if any
	InUse      int       // containers (in any state) mounting it
	Labels     map[string]string
}

// allContainers lists every container; several lists join with it to
// count what is in use (images, volumes, networks).
func (c *client) allContainers(ctx context.Context) ([]container.Summary, error) {
	res, err := c.api.ContainerList(ctx, mobyclient.ContainerListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	return res.Items, nil
}

// ListVolumes returns volumes sorted by name, with how many containers
// mount each one.
func (c *client) ListVolumes(ctx context.Context) ([]Volume, error) {
	res, err := c.api.VolumeList(ctx, mobyclient.VolumeListOptions{})
	if err != nil {
		return nil, err
	}
	ctrs, err := c.allContainers(ctx)
	if err != nil {
		return nil, err
	}
	inUse := map[string]int{}
	for _, ct := range ctrs {
		for _, m := range ct.Mounts {
			if m.Type == mount.TypeVolume {
				inUse[m.Name]++
			}
		}
	}

	out := make([]Volume, 0, len(res.Items))
	for _, v := range res.Items {
		created, _ := time.Parse(time.RFC3339, v.CreatedAt)
		out = append(out, Volume{
			Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Created: created,
			Project: v.Labels[composeProjectLabel], InUse: inUse[v.Name], Labels: v.Labels,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RemoveVolume deletes a volume and its data (`docker volume rm`). The
// daemon refuses volumes still used by a container unless force.
func (c *client) RemoveVolume(ctx context.Context, name string, force bool) error {
	_, err := c.api.VolumeRemove(ctx, name, mobyclient.VolumeRemoveOptions{Force: force})
	return err
}
