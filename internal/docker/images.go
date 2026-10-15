package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	mobyclient "github.com/moby/moby/client"
)

// Image is one row of the images table. An image with several tags gives
// several rows (one per tag), like `docker images`.
type Image struct {
	ID         string // sha256:…
	Repository string // "<none>" for dangling images
	Tag        string // "<none>" for dangling images
	Size       int64
	Created    time.Time
	Containers int // containers (in any state) created from this image
}

// Ref is "repository:tag", or the short ID for an untagged image.
func (i Image) Ref() string {
	if i.Repository == "<none>" {
		return shortID(i.ID)
	}
	return i.Repository + ":" + i.Tag
}

// ShortID is the 12-character ID the docker CLI shows.
func (i Image) ShortID() string { return shortID(i.ID) }

// splitRef splits "repo:tag" at the last colon that is not part of a
// registry port ("localhost:5000/app:1.0" → "localhost:5000/app", "1.0").
func splitRef(ref string) (repo, tag string) {
	i := strings.LastIndex(ref, ":")
	if i < 0 || strings.Contains(ref[i:], "/") {
		return ref, "<none>"
	}
	return ref[:i], ref[i+1:]
}

// ListImages returns one row per image tag, sorted by repository and tag.
//
// The API can count the containers using each image, but only when asked
// for expensive disk-usage data; by default the count is -1. A container
// list is cheap, so zdocker joins the two lists itself.
func (c *client) ListImages(ctx context.Context) ([]Image, error) {
	imgs, err := c.api.ImageList(ctx, mobyclient.ImageListOptions{})
	if err != nil {
		return nil, err
	}
	ctrs, err := c.api.ContainerList(ctx, mobyclient.ContainerListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("count containers per image: %w", err)
	}
	inUse := map[string]int{}
	for _, ct := range ctrs.Items {
		inUse[ct.ImageID]++
	}

	var out []Image
	for _, s := range imgs.Items {
		base := Image{ID: s.ID, Size: s.Size, Created: time.Unix(s.Created, 0), Containers: inUse[s.ID]}
		tags := s.RepoTags
		if len(tags) == 0 {
			tags = []string{"<none>:<none>"}
		}
		for _, ref := range tags {
			img := base
			img.Repository, img.Tag = splitRef(ref)
			out = append(out, img)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Repository != out[j].Repository {
			return out[i].Repository < out[j].Repository
		}
		return out[i].Tag < out[j].Tag
	})
	return out, nil
}
