package docker

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// readMethods and writeMethods classify every Client method. Adding a
// method to Client without listing it here fails the test below — on
// purpose: a write method must also be blocked by ReadOnly.
var (
	readMethods  = []string{"Ping", "ListContainers", "ListImages", "ContainerLogs", "ContainerStats", "Inspect", "Events"}
	writeMethods = []string{"StartContainer", "StopContainer", "RestartContainer", "RemoveContainer", "RemoveImage"}
)

func TestEveryMethodIsClassified(t *testing.T) {
	typ := reflect.TypeOf((*Client)(nil)).Elem()
	for i := range typ.NumMethod() {
		name := typ.Method(i).Name
		if !slices.Contains(readMethods, name) && !slices.Contains(writeMethods, name) {
			t.Errorf("Client.%s is neither in readMethods nor in writeMethods", name)
		}
	}
	if n := len(readMethods) + len(writeMethods); n != typ.NumMethod() {
		t.Errorf("%d methods classified, Client has %d", n, typ.NumMethod())
	}
}

// call invokes a method by name with a background context and zero values
// for the other arguments, returning its error result.
func call(c Client, name string) error {
	m := reflect.ValueOf(c).MethodByName(name)
	args := make([]reflect.Value, m.Type().NumIn())
	for i := range args {
		in := m.Type().In(i)
		if in == reflect.TypeOf((*context.Context)(nil)).Elem() {
			args[i] = reflect.ValueOf(context.Background())
		} else {
			args[i] = reflect.Zero(in)
		}
	}
	out := m.Call(args)
	err, _ := out[len(out)-1].Interface().(error)
	return err
}

func TestReadOnlyBlocksEveryWrite(t *testing.T) {
	d := newFakeDaemon(t)
	ro := ReadOnly(d.client())
	for _, name := range writeMethods {
		if err := call(ro, name); !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s on a read-only client returned %v", name, err)
		}
	}
	if calls := d.calls(); len(calls) != 0 {
		t.Errorf("a read-only client reached the daemon: %v", calls)
	}

	// Reads still work.
	if _, err := ro.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWriteRequests(t *testing.T) {
	d := newFakeDaemon(t)
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	for _, mp := range []string{
		"POST /containers/c1/start", "POST /containers/c1/stop", "POST /containers/c1/restart",
		"DELETE /containers/c1", "DELETE /images/nginx:1.27",
	} {
		d.handle(mp, ok)
	}
	d.handle("DELETE /images/nginx:1.27", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[{"Untagged":"nginx:1.27"}]`))
	})
	c := d.client()
	ctx := context.Background()

	steps := []struct {
		name string
		do   func() error
		want string // method, path and query fragment
	}{
		{"start", func() error { return c.StartContainer(ctx, "c1") }, "POST /containers/c1/start"},
		{"stop", func() error { return c.StopContainer(ctx, "c1") }, "POST /containers/c1/stop?t=10"},
		{"restart", func() error { return c.RestartContainer(ctx, "c1") }, "POST /containers/c1/restart?t=10"},
		{"rm -f", func() error { return c.RemoveContainer(ctx, "c1", true) }, "DELETE /containers/c1?force=1"},
		{"rmi", func() error { return c.RemoveImage(ctx, "nginx:1.27", false) }, "DELETE /images/nginx:1.27?"},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		calls := d.calls()
		if last := calls[len(calls)-1]; !strings.HasPrefix(last, s.want) {
			t.Errorf("%s sent %q, want prefix %q", s.name, last, s.want)
		}
	}
}

func TestWriteErrorsComeFromTheDaemon(t *testing.T) {
	d := newFakeDaemon(t)
	d.handle("DELETE /images/nginx:1.27", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"message":"conflict: unable to remove repository reference \"nginx:1.27\" (must force) - container 1f2e is using its referenced image"}`))
	})
	err := d.client().RemoveImage(context.Background(), "nginx:1.27", false)
	if err == nil || !strings.Contains(err.Error(), "must force") {
		t.Fatalf("err = %v", err)
	}
}
