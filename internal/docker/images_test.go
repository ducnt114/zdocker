package docker

import (
	"context"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
)

func TestListImages(t *testing.T) {
	d := newFakeDaemon(t)
	d.json("GET /images/json", []image.Summary{
		{ID: "sha256:aaaaaaaaaaaaaaaaaaaa", RepoTags: []string{"nginx:1.27", "nginx:latest"}, Size: 187_000_000, Created: 1700000000},
		{ID: "sha256:bbbbbbbbbbbbbbbbbbbb", RepoTags: []string{"localhost:5000/shop/api:dev"}, Size: 42_000_000},
		{ID: "sha256:cccccccccccccccccccc", RepoTags: nil, Size: 5_000}, // dangling
	})
	d.json("GET /containers/json", []container.Summary{
		{ID: "c1", ImageID: "sha256:aaaaaaaaaaaaaaaaaaaa"},
		{ID: "c2", ImageID: "sha256:aaaaaaaaaaaaaaaaaaaa"},
		{ID: "c3", ImageID: "sha256:bbbbbbbbbbbbbbbbbbbb"},
	})

	got, err := d.client().ListImages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		repo, tag string
		inUse     int
	}
	want := []row{
		{"<none>", "<none>", 0},
		{"localhost:5000/shop/api", "dev", 1}, // the registry port is not a tag
		{"nginx", "1.27", 2},
		{"nginx", "latest", 2}, // one row per tag
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows: %+v", len(got), got)
	}
	for i, w := range want {
		if got[i].Repository != w.repo || got[i].Tag != w.tag || got[i].Containers != w.inUse {
			t.Errorf("row %d = %+v, want %+v", i, got[i], w)
		}
	}
	if got[0].Ref() != "cccccccccccc" || got[2].Ref() != "nginx:1.27" || got[2].ShortID() != "aaaaaaaaaaaa" {
		t.Errorf("refs: %q %q %q", got[0].Ref(), got[2].Ref(), got[2].ShortID())
	}

	// The container list used for counting includes stopped containers.
	found := false
	for _, c := range d.calls() {
		if c == "GET /containers/json?all=1" {
			found = true
		}
	}
	if !found {
		t.Errorf("calls = %v", d.calls())
	}
}

func TestSplitRef(t *testing.T) {
	tests := map[string][2]string{
		"nginx:1.27":              {"nginx", "1.27"},
		"localhost:5000/app:dev":  {"localhost:5000/app", "dev"},
		"localhost:5000/app":      {"localhost:5000/app", "<none>"},
		"<none>:<none>":           {"<none>", "<none>"},
		"ghcr.io/org/tool:v1.2.3": {"ghcr.io/org/tool", "v1.2.3"},
	}
	for in, want := range tests {
		if repo, tag := splitRef(in); repo != want[0] || tag != want[1] {
			t.Errorf("splitRef(%q) = %q, %q", in, repo, tag)
		}
	}
}
