package ui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/zdocker/internal/docker"
)

func inUseText(n int) string {
	if n == 0 {
		return "-"
	}
	return strconv.Itoa(n)
}

// usageStyle mutes what nothing uses (prune candidates).
func usageStyle(inUse int) lipgloss.Style {
	if inUse == 0 {
		return healthStyles[finished]
	}
	return healthStyles[healthy]
}

func ageCell(created, now time.Time) string {
	if created.IsZero() {
		return "-"
	}
	return formatAge(now.Sub(created))
}

var volumesSpec = listSpec[docker.Volume]{
	title: "Volumes",
	load:  func(ctx context.Context, c docker.Client) ([]docker.Volume, error) { return c.ListVolumes(ctx) },
	key:   func(v docker.Volume) string { return v.Name },
	row: func(v docker.Volume, now time.Time) tableRow {
		return tableRow{
			cells:  []string{v.Name, v.Driver, v.Project, inUseText(v.InUse), ageCell(v.Created, now)},
			sortBy: []string{3: strconv.Itoa(v.InUse), 4: ageSortKey(v.Created)},
			filter: v.Name + " " + v.Project,
			style:  usageStyle(v.InUse),
		}
	},
	columns: func(w int) []tableColumn {
		return flexColumns(w, "NAME",
			tableColumn{Title: "DRIVER", Width: 10}, tableColumn{Title: "PROJECT", Width: 16},
			tableColumn{Title: "IN USE", Width: 6}, tableColumn{Title: "AGE", Width: 7})
	},
	note: func(vs []docker.Volume) string {
		unused := 0
		for _, v := range vs {
			if v.InUse == 0 {
				unused++
			}
		}
		return fmt.Sprintf("%d unused", unused)
	},
	inspect: func(v docker.Volume) openInspectMsg {
		return openInspectMsg{kind: docker.KindVolume, id: v.Name, name: v.Name}
	},
	remove: func(v docker.Volume) (actionMsg, error) {
		prompt := fmt.Sprintf("Remove volume %s and ALL its data?", v.Name)
		if v.InUse > 0 {
			prompt = fmt.Sprintf("Remove volume %s? (used by %d container(s): the daemon will refuse)", v.Name, v.InUse)
		}
		return actionMsg{
			prompt: prompt, progress: "removing volume " + v.Name + "…", done: "volume " + v.Name + " removed",
			run: func(ctx context.Context, c docker.Client) error { return c.RemoveVolume(ctx, v.Name, false) },
		}, nil
	},
}

var networksSpec = listSpec[docker.Network]{
	title: "Networks",
	load:  func(ctx context.Context, c docker.Client) ([]docker.Network, error) { return c.ListNetworks(ctx) },
	key:   func(n docker.Network) string { return n.ID },
	row: func(n docker.Network, now time.Time) tableRow {
		driver := n.Driver
		if n.Internal {
			driver += " (internal)"
		}
		style := usageStyle(n.InUse)
		if n.BuiltIn {
			style = healthStyles[healthy] // never a prune candidate
		}
		return tableRow{
			cells:  []string{n.Name, n.ShortID(), driver, n.Scope, n.Subnet, inUseText(n.InUse), ageCell(n.Created, now)},
			sortBy: []string{5: strconv.Itoa(n.InUse), 6: ageSortKey(n.Created)},
			filter: n.Name + " " + n.ShortID() + " " + n.Project,
			style:  style,
		}
	},
	columns: func(w int) []tableColumn {
		return flexColumns(w, "NAME",
			tableColumn{Title: "NETWORK ID", Width: 12}, tableColumn{Title: "DRIVER", Width: 16},
			tableColumn{Title: "SCOPE", Width: 6}, tableColumn{Title: "SUBNET", Width: 18},
			tableColumn{Title: "IN USE", Width: 6}, tableColumn{Title: "AGE", Width: 7})
	},
	inspect: func(n docker.Network) openInspectMsg {
		return openInspectMsg{kind: docker.KindNetwork, id: n.ID, name: n.Name}
	},
	remove: func(n docker.Network) (actionMsg, error) {
		if n.BuiltIn {
			return actionMsg{}, errors.New("the " + n.Name + " network is built into Docker and cannot be removed")
		}
		prompt := fmt.Sprintf("Remove network %s?", n.Name)
		if n.InUse > 0 {
			prompt = fmt.Sprintf("Remove network %s? (%d container(s) attached: the daemon will refuse)", n.Name, n.InUse)
		}
		return actionMsg{
			prompt: prompt, progress: "removing network " + n.Name + "…", done: "network " + n.Name + " removed",
			run: func(ctx context.Context, c docker.Client) error { return c.RemoveNetwork(ctx, n.ID) },
		}, nil
	},
}
