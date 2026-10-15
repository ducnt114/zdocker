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
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
)

var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

type daemon struct {
	mu          sync.Mutex
	containers  []container.Summary
	subscribers map[chan events.Message]bool // /events streams
}

func newDaemon() *daemon {
	now := time.Now()
	return &daemon{subscribers: map[chan events.Message]bool{}, containers: []container.Summary{
		{ID: "c0ffee000001aaaaaaaaaaaaaaaaaaaa", Names: []string{"/web"}, Image: "nginx:1.27",
			State: container.StateRunning, Status: "Up 2 hours (healthy)", Created: now.Add(-2 * time.Hour).Unix(),
			Ports:  []container.PortSummary{{PrivatePort: 80, PublicPort: 8080, Type: "tcp"}},
			Labels: map[string]string{"com.docker.compose.project": "shop"}},
		{ID: "c0ffee000002aaaaaaaaaaaaaaaaaaaa", Names: []string{"/api"}, Image: "shop/api:dev",
			State: container.StateRunning, Status: "Up 2 hours", Created: now.Add(-2 * time.Hour).Unix(),
			Labels: map[string]string{"com.docker.compose.project": "shop"}},
		{ID: "c0ffee000003aaaaaaaaaaaaaaaaaaaa", Names: []string{"/worker"}, Image: "shop/worker:dev",
			State: container.StateRestarting, Status: "Restarting (1) 4 seconds ago", Created: now.Add(-30 * time.Minute).Unix()},
		{ID: "c0ffee000004aaaaaaaaaaaaaaaaaaaa", Names: []string{"/migrate"}, Image: "shop/api:dev",
			State: container.StateExited, Status: "Exited (0) 25 minutes ago", Created: now.Add(-26 * time.Minute).Unix()},
		{ID: "c0ffee000005aaaaaaaaaaaaaaaaaaaa", Names: []string{"/flaky"}, Image: "busybox:1.36",
			State: container.StateExited, Status: "Exited (137) 3 minutes ago", Created: now.Add(-time.Hour).Unix()},
	}}
}

func (d *daemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := versionPrefix.ReplaceAllString(r.URL.Path, "")
	log.Printf("%s %s?%s", r.Method, path, r.URL.RawQuery)
	w.Header().Set("Api-Version", "1.47")
	w.Header().Set("Ostype", "linux")

	switch r.Method + " " + path {
	case "GET /_ping", "HEAD /_ping":
		w.Write([]byte("OK"))
	case "GET /containers/json":
		d.listContainers(w, r)
	case "GET /events":
		d.streamEvents(w, r)
	default:
		http.Error(w, `{"message":"fakedaemon: not implemented"}`, http.StatusNotFound)
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
