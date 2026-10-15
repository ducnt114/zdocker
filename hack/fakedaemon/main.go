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
)

var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

type daemon struct {
	mu         sync.Mutex
	containers []container.Summary
}

func newDaemon() *daemon {
	now := time.Now()
	return &daemon{containers: []container.Summary{
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:2375", "address to listen on")
	flag.Parse()
	log.Printf("fake Docker daemon on tcp://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, newDaemon()))
}
