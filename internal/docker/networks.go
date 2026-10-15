package docker

import (
	"context"
	"sort"
	"time"

	mobyclient "github.com/moby/moby/client"
)

// builtinNetworks exist on every daemon and cannot be removed.
var builtinNetworks = map[string]bool{"bridge": true, "host": true, "none": true}

// Network is one row of the networks table.
type Network struct {
	ID       string
	Name     string
	Driver   string // bridge, host, overlay, macvlan, null…
	Scope    string // local or swarm
	Subnet   string // first IPAM subnet, e.g. 172.18.0.0/16
	Internal bool   // no access to the outside
	Created  time.Time
	Project  string // Docker Compose project, if any
	InUse    int    // containers (in any state) attached to it
	BuiltIn  bool   // bridge, host, none
}

// ShortID is the 12-character ID the docker CLI shows.
func (n Network) ShortID() string { return shortID(n.ID) }

// ListNetworks returns networks sorted by name, with how many containers
// are attached to each one.
func (c *client) ListNetworks(ctx context.Context) ([]Network, error) {
	res, err := c.api.NetworkList(ctx, mobyclient.NetworkListOptions{})
	if err != nil {
		return nil, err
	}
	ctrs, err := c.allContainers(ctx)
	if err != nil {
		return nil, err
	}
	inUse := map[string]int{} // by network ID
	for _, ct := range ctrs {
		if ct.NetworkSettings == nil {
			continue
		}
		for _, ep := range ct.NetworkSettings.Networks {
			if ep != nil {
				inUse[ep.NetworkID]++
			}
		}
	}

	out := make([]Network, 0, len(res.Items))
	for _, n := range res.Items {
		net := Network{
			ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope, Internal: n.Internal,
			Created: n.Created, Project: n.Labels[composeProjectLabel], InUse: inUse[n.ID],
			BuiltIn: builtinNetworks[n.Name],
		}
		if len(n.IPAM.Config) > 0 && n.IPAM.Config[0].Subnet.IsValid() {
			net.Subnet = n.IPAM.Config[0].Subnet.String()
		}
		out = append(out, net)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RemoveNetwork deletes a network (`docker network rm`). The daemon refuses
// networks with containers attached, and the built-in ones.
func (c *client) RemoveNetwork(ctx context.Context, id string) error {
	_, err := c.api.NetworkRemove(ctx, id, mobyclient.NetworkRemoveOptions{})
	return err
}
