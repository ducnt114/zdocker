package ui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ducnt114/zdocker/internal/docker"
)

// prunePlan is what a prune would remove, computed before asking.
type prunePlan struct {
	containers []string // names of stopped containers
	images     int      // dangling images
	imageBytes int64
}

type prunePlanMsg struct {
	plan prunePlan
	err  error
}

// stoppedStates are the states `docker container prune` removes.
var stoppedStates = map[string]bool{"exited": true, "created": true, "dead": true}

// planPrune lists what would go, so the confirmation can be specific.
func planPrune(c docker.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		ctrs, err := c.ListContainers(ctx, true)
		if err != nil {
			return prunePlanMsg{err: err}
		}
		imgs, err := c.ListImages(ctx)
		if err != nil {
			return prunePlanMsg{err: err}
		}
		var p prunePlan
		for _, ct := range ctrs {
			if stoppedStates[ct.State] {
				p.containers = append(p.containers, ct.Name)
			}
		}
		seen := map[string]bool{}
		for _, img := range imgs {
			if img.Repository == "<none>" && !seen[img.ID] {
				seen[img.ID] = true
				p.images++
				p.imageBytes += img.Size
			}
		}
		return prunePlanMsg{plan: p}
	}
}

// pruneAction turns a plan into a confirmed action ("" plan → nothing).
func pruneAction(p prunePlan) (actionMsg, bool) {
	if len(p.containers) == 0 && p.images == 0 {
		return actionMsg{}, false
	}
	var parts []string
	if n := len(p.containers); n > 0 {
		names := p.containers
		more := ""
		if n > 3 {
			names, more = names[:3], fmt.Sprintf(", +%d", n-3)
		}
		parts = append(parts, fmt.Sprintf("%d stopped container(s) (%s%s)", n, strings.Join(names, ", "), more))
	}
	if p.images > 0 {
		parts = append(parts, fmt.Sprintf("%d dangling image(s) (%s)", p.images, formatSize(p.imageBytes)))
	}
	var report docker.PruneReport
	return actionMsg{
		prompt:   "Prune: remove " + strings.Join(parts, " and ") + "?",
		progress: "pruning…",
		run: func(ctx context.Context, c docker.Client) error {
			r, err := c.Prune(ctx)
			report = r
			return err
		},
		report: func() string {
			return fmt.Sprintf("removed %d container(s), %d image(s), reclaimed %s",
				report.Containers, report.Images, formatSize(int64(report.SpaceReclaimed)))
		},
	}, true
}
