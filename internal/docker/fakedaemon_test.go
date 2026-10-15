package docker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// fakeDaemon is a tiny Docker Engine API over HTTP, enough to exercise the
// real Moby client end to end without a Docker daemon. Handlers are keyed
// by "METHOD /path" with the API version prefix ("/v1.47") stripped.
type fakeDaemon struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	handlers map[string]http.HandlerFunc
	requests []string // "METHOD /path?query", in order
}

var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{t: t, handlers: map[string]http.HandlerFunc{}}
	d.srv = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *fakeDaemon) serve(w http.ResponseWriter, r *http.Request) {
	path := versionPrefix.ReplaceAllString(r.URL.Path, "")
	d.mu.Lock()
	d.requests = append(d.requests, r.Method+" "+path+"?"+r.URL.RawQuery)
	h := d.handlers[r.Method+" "+path]
	d.mu.Unlock()

	// The client negotiates the API version with /_ping first.
	w.Header().Set("Api-Version", "1.47")
	w.Header().Set("Ostype", "linux")
	switch {
	case h != nil:
		h(w, r)
	case path == "/_ping":
		w.Write([]byte("OK"))
	default:
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"fake daemon: no handler for ` + r.Method + ` ` + path + `"}`))
	}
}

func (d *fakeDaemon) handle(methodPath string, h http.HandlerFunc) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[methodPath] = h
}

// json responds with v encoded as JSON.
func (d *fakeDaemon) json(methodPath string, v any) {
	d.handle(methodPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	})
}

// methods returns the HTTP methods of the requests received so far
// (excluding /_ping), e.g. to assert nothing but GET was sent.
func (d *fakeDaemon) calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, r := range d.requests {
		if !strings.Contains(r, "/_ping") {
			out = append(out, r)
		}
	}
	return out
}

// client returns a real zdocker client talking to the fake daemon.
func (d *fakeDaemon) client() Client {
	d.t.Helper()
	d.t.Setenv("DOCKER_HOST", "")
	d.t.Setenv("DOCKER_TLS_VERIFY", "")
	d.t.Setenv("DOCKER_API_VERSION", "")
	c, err := New("tcp://" + d.srv.Listener.Addr().String())
	if err != nil {
		d.t.Fatal(err)
	}
	return c
}
