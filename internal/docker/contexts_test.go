package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeContext creates a context the way the docker CLI stores it.
func writeContext(t *testing.T, dir, name, host, desc string, tls bool) {
	t.Helper()
	meta := filepath.Join(dir, "contexts", "meta", contextID(name))
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"Name":"` + name + `","Metadata":{"Description":"` + desc + `"},"Endpoints":{"docker":{"Host":"` + host + `","SkipTLSVerify":false}}}`
	if err := os.WriteFile(filepath.Join(meta, "meta.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if tls {
		tlsDir := filepath.Join(dir, "contexts", "tls", contextID(name), "docker")
		os.MkdirAll(tlsDir, 0o755)
		os.WriteFile(filepath.Join(tlsDir, "ca.pem"), []byte("x"), 0o644)
	}
}

func clearDockerEnv(t *testing.T) {
	for _, k := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_API_VERSION"} {
		t.Setenv(k, "")
	}
}

func names(cs []Context) string {
	var out []string
	for _, c := range cs {
		mark := ""
		if c.Current {
			mark = "*"
		}
		out = append(out, c.Name+mark)
	}
	return strings.Join(out, ",")
}

func TestListContexts(t *testing.T) {
	clearDockerEnv(t)
	dir := t.TempDir()
	writeContext(t, dir, "remote-tls", "tcp://10.0.0.5:2376", "lab box", true)
	writeContext(t, dir, "colima", "unix:///home/me/.colima/default/docker.sock", "", false)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"currentContext":"colima"}`), 0o644)

	got, err := ListContexts(dir)
	if err != nil {
		t.Fatal(err)
	}
	if names(got) != "default,colima*,remote-tls" {
		t.Fatalf("contexts = %s", names(got))
	}
	if got[0].Host != "unix:///var/run/docker.sock" {
		t.Errorf("default host = %q", got[0].Host)
	}
	if got[2].TLSDir == "" || got[2].Description != "lab box" || got[1].TLSDir != "" {
		t.Errorf("tls/description: %+v", got[1:])
	}

	// DOCKER_CONTEXT beats config.json; DOCKER_HOST beats both.
	t.Setenv("DOCKER_CONTEXT", "remote-tls")
	got, _ = ListContexts(dir)
	if names(got) != "default,colima,remote-tls*" {
		t.Errorf("with DOCKER_CONTEXT: %s", names(got))
	}
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:2375")
	got, _ = ListContexts(dir)
	if names(got) != "default*,colima,remote-tls" || got[0].Host != "tcp://127.0.0.1:2375" {
		t.Errorf("with DOCKER_HOST: %s %q", names(got), got[0].Host)
	}
}

func TestListContextsWithoutConfig(t *testing.T) {
	clearDockerEnv(t)
	got, err := ListContexts(filepath.Join(t.TempDir(), "nothing-here"))
	if err != nil || names(got) != "default*" {
		t.Fatalf("got %s, %v", names(got), err)
	}
}

func TestNewForContext(t *testing.T) {
	clearDockerEnv(t)
	d := newFakeDaemon(t)
	c, err := NewForContext(Context{Name: "lab", Host: "tcp://" + d.srv.Listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	// A non-default context ignores DOCKER_HOST.
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	if _, err := c.Ping(context.Background()); err != nil {
		t.Fatalf("ping through the context's host: %v", err)
	}

	if _, err := NewForContext(Context{Name: "far", Host: "ssh://me@far"}); err == nil || !strings.Contains(err.Error(), "ssh") {
		t.Fatalf("ssh context: %v", err)
	}
}
