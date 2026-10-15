// Command fakedaemon serves a tiny, in-memory imitation of the Docker
// Engine API over TCP, so zdocker can be tried (and demoed) without Docker:
//
//	go run ./hack/fakedaemon -addr 127.0.0.1:2375 &
//	zdocker --host tcp://127.0.0.1:2375
//
// It implements only what zdocker uses, with canned data. It is a
// development tool, not a test of Docker's behaviour.
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"
)

var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

// Image IDs shared by the sample images and containers.
const (
	bridgeNetID = "bridge00000000000000aaaa"
	shopNetID   = "a7f3c2e1d0b9a8f7e6d5c4b3"

	nginxID   = "sha256:6e5f0b6bd4f2f1d7a3b0e3c09c8f8a5d0b3c6d9e1f2a3b4c5d6e7f8091a2b3c4"
	apiID     = "sha256:a1b2c3d4e5f60718293a4b5c6d7e8f901a2b3c4d5e6f708192a3b4c5d6e7f809"
	workerID  = "sha256:b2c3d4e5f60718293a4b5c6d7e8f901a2b3c4d5e6f708192a3b4c5d6e7f8091a"
	busyboxID = "sha256:c3d4e5f60718293a4b5c6d7e8f901a2b3c4d5e6f708192a3b4c5d6e7f8091a2b"
	oldID     = "sha256:d4e5f60718293a4b5c6d7e8f901a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c"
)

type daemon struct {
	mu          sync.Mutex
	containers  []container.Summary
	images      []image.Summary
	volumes     []volume.Volume
	networks    []network.Summary
	subscribers map[chan events.Message]bool // /events streams
}

func newDaemon() *daemon {
	now := time.Now()
	shopNet := map[string]*network.EndpointSettings{"shop_default": {NetworkID: shopNetID}}
	d := &daemon{subscribers: map[chan events.Message]bool{}, volumes: []volume.Volume{
		{Name: "shop_db", Driver: "local", CreatedAt: now.Add(-3 * time.Hour).UTC().Format(time.RFC3339),
			Mountpoint: "/var/lib/docker/volumes/shop_db/_data", Labels: map[string]string{"com.docker.compose.project": "shop"}},
		{Name: "shop_cache", Driver: "local", CreatedAt: now.Add(-3 * time.Hour).UTC().Format(time.RFC3339),
			Mountpoint: "/var/lib/docker/volumes/shop_cache/_data", Labels: map[string]string{"com.docker.compose.project": "shop"}},
		{Name: "3f1c9a7e2b4d", Driver: "local", CreatedAt: now.Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339),
			Mountpoint: "/var/lib/docker/volumes/3f1c9a7e2b4d/_data"}, // anonymous, unused
	}, networks: []network.Summary{
		{Network: network.Network{ID: bridgeNetID, Name: "bridge", Driver: "bridge", Scope: "local", Created: now.Add(-60 * 24 * time.Hour),
			IPAM: network.IPAM{Config: []network.IPAMConfig{{Subnet: netip.MustParsePrefix("172.17.0.0/16")}}}}},
		{Network: network.Network{ID: "host0000000000000000", Name: "host", Driver: "host", Scope: "local", Created: now.Add(-60 * 24 * time.Hour)}},
		{Network: network.Network{ID: "none0000000000000000", Name: "none", Driver: "null", Scope: "local", Created: now.Add(-60 * 24 * time.Hour)}},
		{Network: network.Network{ID: shopNetID, Name: "shop_default", Driver: "bridge", Scope: "local", Created: now.Add(-3 * time.Hour),
			Labels: map[string]string{"com.docker.compose.project": "shop"},
			IPAM:   network.IPAM{Config: []network.IPAMConfig{{Subnet: netip.MustParsePrefix("172.18.0.0/16")}}}}},
		{Network: network.Network{ID: "oldnet00000000000000", Name: "old_test_net", Driver: "bridge", Scope: "local", Created: now.Add(-20 * 24 * time.Hour)}},
	}, images: []image.Summary{
		{ID: nginxID, RepoTags: []string{"nginx:1.27", "nginx:latest"}, Size: 192_400_000, Created: now.Add(-21 * 24 * time.Hour).Unix()},
		{ID: apiID, RepoTags: []string{"shop/api:dev"}, Size: 48_200_000, Created: now.Add(-3 * time.Hour).Unix()},
		{ID: workerID, RepoTags: []string{"shop/worker:dev"}, Size: 51_700_000, Created: now.Add(-3 * time.Hour).Unix()},
		{ID: busyboxID, RepoTags: []string{"busybox:1.36"}, Size: 4_260_000, Created: now.Add(-400 * 24 * time.Hour).Unix()},
		{ID: oldID, RepoTags: nil, Size: 48_100_000, Created: now.Add(-9 * 24 * time.Hour).Unix()}, // dangling
	}, containers: []container.Summary{
		{ID: "c0ffee000001aaaaaaaaaaaaaaaaaaaa", Names: []string{"/web"}, ImageID: nginxID, Image: "nginx:1.27",
			State: container.StateRunning, Status: "Up 2 hours (healthy)", Created: now.Add(-2 * time.Hour).Unix(),
			Ports:  []container.PortSummary{{PrivatePort: 80, PublicPort: 8080, Type: "tcp"}},
			Labels: map[string]string{"com.docker.compose.project": "shop"}},
		{ID: "c0ffee000002aaaaaaaaaaaaaaaaaaaa", Names: []string{"/api"}, ImageID: apiID, Image: "shop/api:dev",
			State: container.StateRunning, Status: "Up 2 hours", Created: now.Add(-2 * time.Hour).Unix(),
			Labels: map[string]string{"com.docker.compose.project": "shop"}},
		{ID: "c0ffee000003aaaaaaaaaaaaaaaaaaaa", Names: []string{"/worker"}, ImageID: workerID, Image: "shop/worker:dev",
			State: container.StateRestarting, Status: "Restarting (1) 4 seconds ago", Created: now.Add(-30 * time.Minute).Unix()},
		{ID: "c0ffee000004aaaaaaaaaaaaaaaaaaaa", Names: []string{"/migrate"}, ImageID: apiID, Image: "shop/api:dev",
			State: container.StateExited, Status: "Exited (0) 25 minutes ago", Created: now.Add(-26 * time.Minute).Unix()},
		{ID: "c0ffee000005aaaaaaaaaaaaaaaaaaaa", Names: []string{"/flaky"}, ImageID: busyboxID, Image: "busybox:1.36",
			State: container.StateExited, Status: "Exited (137) 3 minutes ago", Created: now.Add(-time.Hour).Unix()},
	}}
	// Wire the sample containers to volumes and networks.
	for i := range d.containers {
		c := &d.containers[i]
		c.NetworkSettings = &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{"bridge": {NetworkID: bridgeNetID}}}
		switch c.Names[0] {
		case "/api", "/web", "/worker", "/migrate":
			c.NetworkSettings.Networks = shopNet
		}
		switch c.Names[0] {
		case "/api", "/migrate":
			c.Mounts = []container.MountPoint{{Type: mount.TypeVolume, Name: "shop_db"}}
		case "/worker":
			c.Mounts = []container.MountPoint{{Type: mount.TypeVolume, Name: "shop_cache"}}
		}
	}
	return d
}

func (d *daemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := versionPrefix.ReplaceAllString(r.URL.Path, "")
	log.Printf("%s %s?%s", r.Method, path, r.URL.RawQuery)
	w.Header().Set("Api-Version", "1.47")
	w.Header().Set("Ostype", "linux")

	// Image references can contain "/" ("shop/api:dev"): cut prefix and
	// suffix instead of splitting the path.
	if r.Method == "GET" && strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json") && path != "/images/json" {
		d.inspectImage(w, strings.TrimSuffix(strings.TrimPrefix(path, "/images/"), "/json"))
		return
	}
	if r.Method == "DELETE" && strings.HasPrefix(path, "/images/") {
		d.removeImage(w, r, strings.TrimPrefix(path, "/images/"))
		return
	}
	if name, ok := strings.CutPrefix(path, "/volumes/"); ok {
		d.volumeEndpoint(w, r, name)
		return
	}
	if id, ok := strings.CutPrefix(path, "/networks/"); ok {
		d.networkEndpoint(w, r, id)
		return
	}
	if r.Method == "DELETE" && strings.HasPrefix(path, "/containers/") {
		d.removeContainer(w, r, strings.TrimPrefix(path, "/containers/"))
		return
	}
	if id, rest, ok := strings.Cut(strings.TrimPrefix(path, "/containers/"), "/"); ok && strings.HasPrefix(path, "/containers/") && id != "json" {
		d.containerEndpoint(w, r, id, rest)
		return
	}

	switch r.Method + " " + path {
	case "GET /_ping", "HEAD /_ping":
		w.Write([]byte("OK"))
	case "GET /containers/json":
		d.listContainers(w, r)
	case "GET /events":
		d.streamEvents(w, r)
	case "GET /images/json":
		d.mu.Lock()
		writeJSON(w, d.images)
		d.mu.Unlock()
	case "POST /images/create":
		d.pull(w, r)
	case "POST /containers/prune":
		d.pruneContainers(w)
	case "POST /images/prune":
		d.pruneImages(w)
	case "GET /volumes":
		d.mu.Lock()
		writeJSON(w, map[string]any{"Volumes": d.volumes})
		d.mu.Unlock()
	case "GET /networks":
		d.mu.Lock()
		writeJSON(w, d.networks)
		d.mu.Unlock()
	default:
		jsonError(w, `{"message":"fakedaemon: not implemented"}`, http.StatusNotFound)
	}
}

func (d *daemon) listContainers(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	all := r.URL.Query().Get("all") == "1"
	out := []container.Summary{}
	for _, c := range d.containers {
		if all || c.State == container.StateRunning {
			out = append(out, c)
		}
	}
	writeJSON(w, out)
}

// find looks a container up by ID, ID prefix or name, like the daemon.
func (d *daemon) find(ref string) (container.Summary, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.containers {
		if c.ID == ref || strings.HasPrefix(c.ID, ref) || c.Names[0] == "/"+ref {
			return c, true
		}
	}
	return container.Summary{}, false
}

func (d *daemon) containerEndpoint(w http.ResponseWriter, r *http.Request, ref, rest string) {
	c, ok := d.find(ref)
	if !ok {
		jsonError(w, `{"message":"No such container: `+ref+`"}`, http.StatusNotFound)
		return
	}
	switch r.Method + " " + rest {
	case "GET json":
		writeJSON(w, container.InspectResponse{
			ID: c.ID, Name: c.Names[0], Image: c.ImageID, Created: time.Unix(c.Created, 0).Format(time.RFC3339Nano),
			State:  &container.State{Status: c.State, Running: c.State == container.StateRunning},
			Config: &container.Config{Image: c.Image, Labels: c.Labels, Tty: false},
		})
	case "GET logs":
		streamLogs(w, r, c)
	case "GET stats":
		streamStats(w, r, c)
	case "POST start":
		d.setState(c.ID, container.StateRunning, "Up Less than a second", events.ActionStart)
		w.WriteHeader(http.StatusNoContent)
	case "POST stop":
		d.setState(c.ID, container.StateExited, "Exited (0) Less than a second ago", events.ActionDie)
		w.WriteHeader(http.StatusNoContent)
	case "POST restart":
		d.setState(c.ID, container.StateRunning, "Up Less than a second", events.ActionRestart)
		w.WriteHeader(http.StatusNoContent)
	default:
		jsonError(w, `{"message":"fakedaemon: not implemented"}`, http.StatusNotFound)
	}
}

func (d *daemon) inspectImage(w http.ResponseWriter, ref string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, img := range d.images {
		match := img.ID == ref || strings.HasPrefix(strings.TrimPrefix(img.ID, "sha256:"), ref)
		for _, tag := range img.RepoTags {
			match = match || tag == ref
		}
		if match {
			writeJSON(w, map[string]any{
				"Id": img.ID, "RepoTags": img.RepoTags, "Size": img.Size,
				"Created":      time.Unix(img.Created, 0).Format(time.RFC3339Nano),
				"Architecture": "amd64", "Os": "linux",
				"Config": map[string]any{"Cmd": []string{"/docker-entrypoint.sh"}, "ExposedPorts": map[string]any{"80/tcp": map[string]any{}}},
			})
			return
		}
	}
	jsonError(w, `{"message":"No such image: `+ref+`"}`, http.StatusNotFound)
}

// setState changes a container and emits the event a daemon would.
func (d *daemon) setState(id string, state container.ContainerState, status string, action events.Action) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.containers {
		if c := &d.containers[i]; c.ID == id {
			c.State, c.Status = state, status
			d.publish(c.ID, strings.TrimPrefix(c.Names[0], "/"), action)
		}
	}
}

func (d *daemon) removeContainer(w http.ResponseWriter, r *http.Request, ref string) {
	c, ok := d.find(ref)
	if !ok {
		jsonError(w, `{"message":"No such container: `+ref+`"}`, http.StatusNotFound)
		return
	}
	if c.State == container.StateRunning && r.URL.Query().Get("force") != "1" {
		jsonError(w, `{"message":"cannot remove container: container is running: stop the container before removing or force remove"}`, http.StatusConflict)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.containers = slices.DeleteFunc(d.containers, func(x container.Summary) bool { return x.ID == c.ID })
	d.publish(c.ID, strings.TrimPrefix(c.Names[0], "/"), events.ActionDestroy)
	w.WriteHeader(http.StatusNoContent)
}

// removeImage untags "repo:tag" (removing the image with its last tag) or
// removes an image by ID, refusing images in use like the real daemon.
func (d *daemon) removeImage(w http.ResponseWriter, r *http.Request, ref string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.images {
		img := &d.images[i]
		byID := img.ID == ref
		if !byID && !slices.Contains(img.RepoTags, ref) {
			continue
		}
		for _, c := range d.containers {
			if c.ImageID == img.ID && r.URL.Query().Get("force") != "1" {
				jsonError(w, `{"message":"conflict: unable to remove repository reference \"`+ref+`\" (must force) - container `+c.ID[:12]+` is using its referenced image"}`, http.StatusConflict)
				return
			}
		}
		if !byID && len(img.RepoTags) > 1 {
			img.RepoTags = slices.DeleteFunc(img.RepoTags, func(t string) bool { return t == ref })
			d.publishImage(img.ID, ref, events.ActionUnTag)
		} else {
			d.publishImage(img.ID, ref, events.ActionDelete)
			d.images = slices.Delete(d.images, i, i+1)
		}
		writeJSON(w, []map[string]string{{"Untagged": ref}})
		return
	}
	jsonError(w, `{"message":"No such image: `+ref+`"}`, http.StatusNotFound)
}

func (d *daemon) pruneContainers(w http.ResponseWriter) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var deleted []string
	d.containers = slices.DeleteFunc(d.containers, func(c container.Summary) bool {
		switch c.State {
		case container.StateExited, container.StateCreated, container.StateDead:
			deleted = append(deleted, c.ID)
			d.publish(c.ID, strings.TrimPrefix(c.Names[0], "/"), events.ActionDestroy)
			return true
		}
		return false
	})
	writeJSON(w, map[string]any{"ContainersDeleted": deleted, "SpaceReclaimed": uint64(len(deleted)) * 1_200_000})
}

// pruneImages removes dangling images no container uses.
func (d *daemon) pruneImages(w http.ResponseWriter) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var deleted []map[string]string
	var space int64
	d.images = slices.DeleteFunc(d.images, func(img image.Summary) bool {
		if len(img.RepoTags) > 0 || slices.ContainsFunc(d.containers, func(c container.Summary) bool { return c.ImageID == img.ID }) {
			return false
		}
		deleted = append(deleted, map[string]string{"Deleted": img.ID})
		space += img.Size
		d.publishImage(img.ID, img.ID, events.ActionDelete)
		return true
	})
	writeJSON(w, map[string]any{"ImagesDeleted": deleted, "SpaceReclaimed": space})
}

// pull streams `docker pull` progress for three layers over ~3 seconds,
// then adds the image and emits a "pull" event. Names starting with
// "nope" fail like an unknown repository.
func (d *daemon) pull(w http.ResponseWriter, r *http.Request) {
	from, tag := r.URL.Query().Get("fromImage"), r.URL.Query().Get("tag")
	from = strings.TrimPrefix(strings.TrimPrefix(from, "docker.io/"), "library/")
	ref := from + ":" + tag
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	flusher := w.(http.Flusher)
	msg := func(m map[string]any) bool {
		enc.Encode(m)
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return false
		case <-time.After(60 * time.Millisecond):
			return true
		}
	}
	msg(map[string]any{"status": "Pulling from library/" + from, "id": tag})
	if strings.HasPrefix(from, "nope") {
		msg(map[string]any{"errorDetail": map[string]any{"message": "manifest for " + ref + " not found: manifest unknown"}, "error": "manifest unknown"})
		return
	}
	layers := map[string]int64{"9a1b2c3d4e5f": 3_400_000, "8f7e6d5c4b3a": 28_100_000, "7d6c5b4a3f2e": 640}
	order := []string{"9a1b2c3d4e5f", "8f7e6d5c4b3a", "7d6c5b4a3f2e"}
	for _, id := range order {
		msg(map[string]any{"status": "Pulling fs layer", "id": id})
	}
	for step := int64(1); step <= 10; step++ {
		for _, id := range order {
			total := layers[id]
			if !msg(map[string]any{"status": "Downloading", "id": id, "progressDetail": map[string]any{"current": total * step / 10, "total": total}}) {
				return
			}
		}
	}
	for _, id := range order {
		msg(map[string]any{"status": "Extracting", "id": id, "progressDetail": map[string]any{"current": layers[id], "total": layers[id]}})
		msg(map[string]any{"status": "Pull complete", "id": id})
	}
	msg(map[string]any{"status": "Digest: sha256:0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"})
	msg(map[string]any{"status": "Status: Downloaded newer image for " + ref})

	d.mu.Lock()
	defer d.mu.Unlock()
	id := fmt.Sprintf("sha256:%064x", time.Now().UnixNano())
	d.images = append(d.images, image.Summary{ID: id, RepoTags: []string{ref}, Size: 31_500_640, Created: time.Now().Unix()})
	d.publishImage(id, ref, events.ActionPull)
}

func (d *daemon) volumeEndpoint(w http.ResponseWriter, r *http.Request, name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	i := slices.IndexFunc(d.volumes, func(v volume.Volume) bool { return v.Name == name })
	if i < 0 {
		jsonError(w, `{"message":"get `+name+`: no such volume"}`, http.StatusNotFound)
		return
	}
	switch r.Method {
	case "GET":
		writeJSON(w, d.volumes[i])
	case "DELETE":
		for _, c := range d.containers {
			for _, m := range c.Mounts {
				if m.Name == name && r.URL.Query().Get("force") != "true" {
					jsonError(w, `{"message":"remove `+name+`: volume is in use - [`+c.ID[:12]+`]"}`, http.StatusConflict)
					return
				}
			}
		}
		d.volumes = slices.Delete(d.volumes, i, i+1)
		d.publishOther(events.VolumeEventType, name, name, events.ActionDestroy)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (d *daemon) networkEndpoint(w http.ResponseWriter, r *http.Request, id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	i := slices.IndexFunc(d.networks, func(n network.Summary) bool { return n.ID == id || n.Name == id })
	if i < 0 {
		jsonError(w, `{"message":"network `+id+` not found"}`, http.StatusNotFound)
		return
	}
	n := d.networks[i]
	switch r.Method {
	case "GET":
		writeJSON(w, n)
	case "DELETE":
		if n.Name == "bridge" || n.Name == "host" || n.Name == "none" {
			jsonError(w, `{"message":"`+n.Name+` is a pre-defined network and cannot be removed"}`, http.StatusForbidden)
			return
		}
		for _, c := range d.containers {
			if c.NetworkSettings != nil {
				for _, ep := range c.NetworkSettings.Networks {
					if ep.NetworkID == n.ID {
						jsonError(w, `{"message":"error while removing network: network `+n.Name+` id `+n.ID+` has active endpoints"}`, http.StatusForbidden)
						return
					}
				}
			}
		}
		d.networks = slices.Delete(d.networks, i, i+1)
		d.publishOther(events.NetworkEventType, n.ID, n.Name, events.ActionDestroy)
		w.WriteHeader(http.StatusNoContent)
	}
}

// publishOther emits a volume or network event; d.mu must be held.
func (d *daemon) publishOther(typ events.Type, id, name string, action events.Action) {
	m := events.Message{Type: typ, Action: action, TimeNano: time.Now().UnixNano(),
		Actor: events.Actor{ID: id, Attributes: map[string]string{"name": name}}}
	for ch := range d.subscribers {
		select {
		case ch <- m:
		default:
		}
	}
}

func (d *daemon) publishImage(id, name string, action events.Action) {
	m := events.Message{
		Type: events.ImageEventType, Action: action, TimeNano: time.Now().UnixNano(),
		Actor: events.Actor{ID: id, Attributes: map[string]string{"name": name}},
	}
	for ch := range d.subscribers {
		select {
		case ch <- m:
		default:
		}
	}
}

// streamStats writes one stats sample per second, with precpu_stats from
// the previous sample, like the daemon. CPU and memory wander so the
// sparklines move.
func streamStats(w http.ResponseWriter, r *http.Request, c container.Summary) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	flusher := w.(http.Flusher)
	var prev container.CPUStats
	var total, system uint64 = 0, 1_000_000_000
	for i := 0; ; i++ {
		running := c.State == container.StateRunning
		if running {
			total += uint64(100_000_000 + 80_000_000*math.Sin(float64(i)/4)) // ~10–18% of 4 CPUs
		}
		system += 4_000_000_000
		cur := container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: total}, SystemUsage: system, OnlineCPUs: 4}
		mem := uint64(120<<20 + int(30<<20*math.Sin(float64(i)/6)))
		enc.Encode(container.StatsResponse{
			Read: time.Now(), CPUStats: cur, PreCPUStats: prev,
			MemoryStats: container.MemoryStats{Usage: mem + 40<<20, Limit: 1 << 30, Stats: map[string]uint64{"inactive_file": 40 << 20}},
			Networks:    map[string]container.NetworkStats{"eth0": {RxBytes: uint64(i) * 15_000, TxBytes: uint64(i) * 4_000}},
			PidsStats:   container.PidsStats{Current: 5},
		})
		flusher.Flush()
		prev = cur
		if r.URL.Query().Get("stream") != "true" {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// streamLogs writes canned logs as a multiplexed stream (8-byte frame
// header + payload, stdout or stderr), the format of containers without a
// TTY; with follow=1 it keeps writing for running containers.
func streamLogs(w http.ResponseWriter, r *http.Request, c container.Summary) {
	w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
	w.WriteHeader(http.StatusOK)
	flusher := w.(http.Flusher)
	timestamps := r.URL.Query().Get("timestamps") == "1"
	write := func(stream byte, text string) {
		if timestamps {
			text = time.Now().UTC().Format(time.RFC3339Nano) + " " + text
		}
		header := []byte{stream, 0, 0, 0, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(header[4:], uint32(len(text)+1))
		w.Write(header)
		w.Write([]byte(text + "\n"))
		flusher.Flush()
	}
	name := strings.TrimPrefix(c.Names[0], "/")
	for i := range 5 {
		write(1, fmt.Sprintf("%s: earlier line %d", name, i))
	}
	if r.URL.Query().Get("follow") != "1" || c.State != container.StateRunning {
		return
	}
	for i := 0; ; i++ {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(700 * time.Millisecond):
		}
		if i%4 == 3 {
			write(2, fmt.Sprintf("WARN %s: request %d took 1.2s", name, i))
		} else {
			write(1, fmt.Sprintf("%s: GET /items/%d 200 12ms", name, i))
		}
	}
}

// streamEvents follows the event stream like `docker events`, as JSON
// lines, until the client goes away.
func (d *daemon) streamEvents(w http.ResponseWriter, r *http.Request) {
	ch := make(chan events.Message, 16)
	d.mu.Lock()
	d.subscribers[ch] = true
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.subscribers, ch)
		d.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	flusher := w.(http.Flusher)
	flusher.Flush()
	enc := json.NewEncoder(w)
	for {
		select {
		case m := <-ch:
			enc.Encode(m)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// publish sends an event to every subscriber; d.mu must be held.
func (d *daemon) publish(id, name string, action events.Action) {
	m := events.Message{
		Type: events.ContainerEventType, Action: action, TimeNano: time.Now().UnixNano(),
		Actor: events.Actor{ID: id, Attributes: map[string]string{"name": name}},
	}
	for ch := range d.subscribers {
		select {
		case ch <- m:
		default: // a slow client misses events, like with the real daemon
		}
	}
}

// simulate makes "worker" crash-loop: it restarts every few seconds, with
// the events a real daemon would emit, so the TUI updates live.
func (d *daemon) simulate(every time.Duration) {
	for range time.Tick(every) {
		d.mu.Lock()
		for i := range d.containers {
			c := &d.containers[i]
			if c.Names[0] != "/worker" {
				continue
			}
			if c.State == container.StateRunning {
				c.State, c.Status = container.StateRestarting, "Restarting (1) Less than a second ago"
				d.publish(c.ID, "worker", events.ActionDie)
			} else {
				c.State, c.Status = container.StateRunning, "Up Less than a second"
				d.publish(c.ID, "worker", events.ActionStart)
			}
		}
		d.mu.Unlock()
	}
}

// jsonError answers like the daemon: a JSON body {"message": …} with a
// JSON content type, which the client turns into a clean error message.
func jsonError(w http.ResponseWriter, body string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write([]byte(body))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:2375", "address to listen on")
	restartEvery := flag.Duration("restart-every", 5*time.Second, "how often the fake worker container restarts (0: never)")
	flag.Parse()

	d := newDaemon()
	if *restartEvery > 0 {
		go d.simulate(*restartEvery)
	}
	log.Printf("fake Docker daemon on tcp://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, d))
}
