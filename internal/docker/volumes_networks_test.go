package docker

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"
)

func TestListVolumes(t *testing.T) {
	d := newFakeDaemon(t)
	d.json("GET /volumes", map[string]any{"Volumes": []volume.Volume{
		{Name: "shop_db", Driver: "local", CreatedAt: "2026-10-01T10:00:00Z", Labels: map[string]string{"com.docker.compose.project": "shop"}},
		{Name: "orphan", Driver: "local"},
	}})
	d.json("GET /containers/json", []container.Summary{
		{ID: "c1", Mounts: []container.MountPoint{{Type: mount.TypeVolume, Name: "shop_db"}, {Type: mount.TypeBind, Name: "orphan"}}},
		{ID: "c2", Mounts: []container.MountPoint{{Type: mount.TypeVolume, Name: "shop_db"}}},
	})

	got, err := d.client().ListVolumes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "orphan" || got[1].Name != "shop_db" {
		t.Fatalf("volumes = %+v", got)
	}
	if got[0].InUse != 0 { // a bind mount with the same name doesn't count
		t.Errorf("orphan in use = %d", got[0].InUse)
	}
	db := got[1]
	if db.InUse != 2 || db.Project != "shop" || !db.Created.Equal(time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("shop_db = %+v", db)
	}
}

func TestListNetworks(t *testing.T) {
	d := newFakeDaemon(t)
	d.json("GET /networks", []network.Summary{
		{Network: network.Network{ID: "n-bridge", Name: "bridge", Driver: "bridge", Scope: "local"}},
		{Network: network.Network{ID: "n-shop", Name: "shop_default", Driver: "bridge", Scope: "local",
			IPAM:   network.IPAM{Config: []network.IPAMConfig{{Subnet: netip.MustParsePrefix("172.18.0.0/16")}}},
			Labels: map[string]string{"com.docker.compose.project": "shop"}}},
	})
	d.json("GET /containers/json", []container.Summary{
		{ID: "c1", NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{
			"shop_default": {NetworkID: "n-shop"}, "bridge": {NetworkID: "n-bridge"},
		}}},
		{ID: "c2", NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{
			"shop_default": {NetworkID: "n-shop"},
		}}},
		{ID: "c3"}, // no network settings
	})

	got, err := d.client().ListNetworks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].BuiltIn || got[0].InUse != 1 {
		t.Fatalf("bridge = %+v", got)
	}
	if shop := got[1]; shop.BuiltIn || shop.InUse != 2 || shop.Subnet != "172.18.0.0/16" || shop.Project != "shop" {
		t.Errorf("shop_default = %+v", shop)
	}
}

func TestRemoveVolumeAndNetwork(t *testing.T) {
	d := newFakeDaemon(t)
	noContent := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	d.handle("DELETE /volumes/shop_db", noContent)
	d.handle("DELETE /networks/n-shop", noContent)
	c := d.client()
	if err := c.RemoveVolume(context.Background(), "shop_db", false); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveNetwork(context.Background(), "n-shop"); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(d.calls(), "\n")
	if !strings.Contains(calls, "DELETE /volumes/shop_db") || !strings.Contains(calls, "DELETE /networks/n-shop") {
		t.Fatalf("calls:\n%s", calls)
	}
}

func TestInspectVolumeAndNetwork(t *testing.T) {
	d := newFakeDaemon(t)
	d.handle("GET /volumes/shop_db", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"Name":"shop_db","Driver":"local","Mountpoint":"/var/lib/docker/volumes/shop_db/_data"}`))
	})
	d.handle("GET /networks/n-shop", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"Name":"shop_default","Id":"n-shop"}`))
	})
	c := d.client()
	if out, err := c.Inspect(context.Background(), KindVolume, "shop_db"); err != nil || !strings.Contains(out, "_data") {
		t.Fatalf("volume: %v\n%s", err, out)
	}
	if out, err := c.Inspect(context.Background(), KindNetwork, "n-shop"); err != nil || !strings.Contains(out, "shop_default") {
		t.Fatalf("network: %v\n%s", err, out)
	}
}
