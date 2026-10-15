package docker

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func streamJSON(lines ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for _, l := range lines {
			w.Write([]byte(l + "\n"))
			w.(http.Flusher).Flush()
		}
	}
}

func lastSnapshot(t *testing.T, ch <-chan PullProgress) (PullProgress, int) {
	t.Helper()
	var last PullProgress
	n := 0
	for p := range ch {
		last, n = p, n+1
	}
	return last, n
}

func TestPullImage(t *testing.T) {
	d := newFakeDaemon(t)
	d.handle("POST /images/create", streamJSON(
		`{"status":"Pulling from library/nginx","id":"1.27"}`,
		`{"status":"Pulling fs layer","id":"aaa"}`,
		`{"status":"Already exists","id":"bbb"}`,
		`{"status":"Downloading","progressDetail":{"current":500,"total":1000},"id":"aaa"}`,
		`{"status":"Downloading","progressDetail":{"current":1000,"total":1000},"id":"aaa"}`,
		`{"status":"Extracting","progressDetail":{"current":200,"total":1000},"id":"aaa"}`,
		`{"status":"Pull complete","id":"aaa"}`,
		`{"status":"Digest: sha256:abc"}`,
		`{"status":"Status: Downloaded newer image for nginx:1.27"}`,
	))
	ch, err := d.client().PullImage(context.Background(), "nginx:1.27")
	if err != nil {
		t.Fatal(err)
	}
	last, n := lastSnapshot(t, ch)
	if !last.Done || last.Err != nil || n < 9 {
		t.Fatalf("final = %+v after %d snapshots", last, n)
	}
	if last.Status != "Status: Downloaded newer image for nginx:1.27" {
		t.Errorf("status = %q", last.Status)
	}
	if len(last.Layers) != 2 || last.Layers[0].ID != "aaa" || last.Layers[1].Status != "Already exists" {
		t.Fatalf("layers = %+v", last.Layers)
	}
	if !last.Layers[0].Done() || last.Layers[0].Total != 1000 {
		t.Errorf("layer aaa = %+v", last.Layers[0])
	}
	// Only "Pull complete" and "Already exists" are final.
	if (LayerProgress{Status: "Download complete"}).Done() {
		t.Error("Download complete is not done: extracting follows")
	}

	// fromImage and tag are sent separately.
	var call string
	for _, c := range d.calls() {
		if strings.HasPrefix(c, "POST /images/create") {
			call = c
		}
	}
	if !strings.Contains(call, "fromImage=docker.io%2Flibrary%2Fnginx") && !strings.Contains(call, "fromImage=nginx") || !strings.Contains(call, "tag=1.27") {
		t.Errorf("request = %q", call)
	}
}

func TestPullImageError(t *testing.T) {
	d := newFakeDaemon(t)
	d.handle("POST /images/create", streamJSON(
		`{"status":"Pulling from library/nope","id":"latest"}`,
		`{"errorDetail":{"message":"manifest for nope:latest not found: manifest unknown"},"error":"manifest unknown"}`,
	))
	ch, err := d.client().PullImage(context.Background(), "nope")
	if err != nil {
		t.Fatal(err)
	}
	last, _ := lastSnapshot(t, ch)
	if last.Err == nil || !strings.Contains(last.Err.Error(), "manifest unknown") || last.Done {
		t.Fatalf("final = %+v", last)
	}
}

func TestPullSnapshotsAreIndependent(t *testing.T) {
	d := newFakeDaemon(t)
	d.handle("POST /images/create", streamJSON(
		`{"status":"Downloading","progressDetail":{"current":1,"total":10},"id":"aaa"}`,
		`{"status":"Downloading","progressDetail":{"current":9,"total":10},"id":"aaa"}`,
	))
	ch, _ := d.client().PullImage(context.Background(), "x")
	first := <-ch
	second := <-ch
	if first.Layers[0].Current != 1 || second.Layers[0].Current != 9 {
		t.Fatalf("snapshots share memory: %d, %d", first.Layers[0].Current, second.Layers[0].Current)
	}
	for range ch {
	}
}
