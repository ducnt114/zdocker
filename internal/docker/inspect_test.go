package docker

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestInspect(t *testing.T) {
	d := newFakeDaemon(t)
	// Raw JSON with a field the client library may not know: it must be
	// shown anyway, since we display the daemon's own response.
	d.handle("GET /containers/c1/json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Id":"c1","Name":"/web","Config":{"Tty":false},"FutureField":{"x":1}}`))
	})
	d.handle("GET /images/sha256:abc/json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Id":"sha256:abc","RepoTags":["nginx:1.27"]}`))
	})
	c := d.client()
	ctx := context.Background()

	out, err := c.Inspect(ctx, KindContainer, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\n  \"Name\": \"/web\"") || !strings.Contains(out, "FutureField") {
		t.Fatalf("container inspect:\n%s", out)
	}

	out, err = c.Inspect(ctx, KindImage, "sha256:abc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\"nginx:1.27\"") {
		t.Fatalf("image inspect:\n%s", out)
	}

	if _, err := c.Inspect(ctx, KindContainer, "gone"); err == nil {
		t.Fatal("expected not found")
	}
}
