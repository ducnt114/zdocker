package docker

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestPrune(t *testing.T) {
	d := newFakeDaemon(t)
	d.handle("POST /containers/prune", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ContainersDeleted":["c1","c2","c3"],"SpaceReclaimed":4000000}`))
	})
	d.handle("POST /images/prune", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ImagesDeleted":[{"Untagged":"x@sha256:1"},{"Deleted":"sha256:1"},{"Deleted":"sha256:2"}],"SpaceReclaimed":48000000}`))
	})

	got, err := d.client().Prune(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Containers != 3 || got.Images != 2 || got.SpaceReclaimed != 52_000_000 {
		t.Fatalf("report = %+v", got)
	}

	calls := d.calls()
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "POST /containers/prune") || !strings.HasPrefix(calls[1], "POST /images/prune") {
		t.Fatalf("containers must be pruned before images: %v", calls)
	}
	if !strings.Contains(calls[1], "dangling") {
		t.Errorf("only dangling images: %q", calls[1])
	}
}

func TestPruneStopsOnContainerError(t *testing.T) {
	d := newFakeDaemon(t)
	d.handle("POST /containers/prune", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"message":"a prune operation is already running"}`))
	})
	_, err := d.client().Prune(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("err = %v", err)
	}
	for _, c := range d.calls() {
		if strings.Contains(c, "/images/prune") {
			t.Fatal("images must not be pruned after the container prune failed")
		}
	}
}
