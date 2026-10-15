package docker

import (
	"context"
	"fmt"

	mobyclient "github.com/moby/moby/client"
)

// PruneReport says what a prune removed.
type PruneReport struct {
	Containers     int
	Images         int
	SpaceReclaimed uint64
}

// Prune removes stopped containers, then dangling images — what
// `docker container prune` followed by `docker image prune` do by default.
// Containers go first: a dangling image used by a stopped container can
// only be removed once that container is gone. Volumes are never touched.
func (c *client) Prune(ctx context.Context) (PruneReport, error) {
	var r PruneReport
	ctrs, err := c.api.ContainerPrune(ctx, mobyclient.ContainerPruneOptions{})
	if err != nil {
		return r, fmt.Errorf("prune containers: %w", err)
	}
	r.Containers = len(ctrs.Report.ContainersDeleted)
	r.SpaceReclaimed += ctrs.Report.SpaceReclaimed

	imgs, err := c.api.ImagePrune(ctx, mobyclient.ImagePruneOptions{
		Filters: mobyclient.Filters{}.Add("dangling", "true"),
	})
	if err != nil {
		return r, fmt.Errorf("prune images: %w", err)
	}
	for _, d := range imgs.Report.ImagesDeleted {
		if d.Deleted != "" { // entries are also listed for untagged references
			r.Images++
		}
	}
	r.SpaceReclaimed += imgs.Report.SpaceReclaimed
	return r, nil
}
