package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
)

// multiplexed encodes chunks the way the daemon does for a container
// without a TTY: each chunk becomes a frame for stdout or stderr.
func multiplexed(chunks ...struct {
	stderr bool
	data   string
}) []byte {
	var buf bytes.Buffer
	for _, c := range chunks {
		stream := stdcopy.Stdout
		if c.stderr {
			stream = stdcopy.Stderr
		}
		// Frame header: [stream, 0, 0, 0, payload size as big-endian uint32].
		header := []byte{byte(stream), 0, 0, 0, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(header[4:], uint32(len(c.data)))
		buf.Write(header)
		buf.WriteString(c.data)
	}
	return buf.Bytes()
}

type chunk = struct {
	stderr bool
	data   string
}

func collectLines(t *testing.T, ch <-chan LogLine) []LogLine {
	t.Helper()
	var out []LogLine
	timeout := time.After(5 * time.Second)
	for {
		select {
		case l, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, l)
		case <-timeout:
			t.Fatal("timed out reading log lines")
		}
	}
}

func inspect(d *fakeDaemon, id string, tty bool) {
	d.json("GET /containers/"+id+"/json", container.InspectResponse{ID: id, Config: &container.Config{Tty: tty}})
}

func TestContainerLogsDemultiplexes(t *testing.T) {
	d := newFakeDaemon(t)
	inspect(d, "c1", false)
	body := multiplexed(
		chunk{false, "starting\nlistening on :80\n"},
		chunk{true, "warn: slow"}, // a line split across frames…
		chunk{true, " disk\n"},    // …is joined again
		chunk{false, "ok\r\nlast without newline"},
	)
	d.handle("GET /containers/c1/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
		w.Write(body)
	})

	lines, errs, err := d.client().ContainerLogs(context.Background(), "c1", LogOptions{Timestamps: true})
	if err != nil {
		t.Fatal(err)
	}
	got := collectLines(t, lines)
	want := []LogLine{
		{"starting", false}, {"listening on :80", false},
		{"warn: slow disk", true},
		{"ok", false}, {"last without newline", false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	select {
	case err := <-errs:
		t.Fatalf("unexpected error: %v", err)
	default:
	}

	// follow, both streams, tail and timestamps are requested.
	var logsCall string
	for _, c := range d.calls() {
		if strings.Contains(c, "/logs") {
			logsCall = c
		}
	}
	for _, q := range []string{"follow=1", "stdout=1", "stderr=1", "tail=500", "timestamps=1"} {
		if !strings.Contains(logsCall, q) {
			t.Errorf("logs request %q lacks %s", logsCall, q)
		}
	}
}

func TestContainerLogsTTY(t *testing.T) {
	d := newFakeDaemon(t)
	inspect(d, "c2", true)
	d.handle("GET /containers/c2/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("raw line 1\r\nraw line 2\r\n")) // no frames with a TTY
	})
	lines, _, err := d.client().ContainerLogs(context.Background(), "c2", LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := collectLines(t, lines)
	if len(got) != 2 || got[0].Text != "raw line 1" || got[1].Stderr {
		t.Fatalf("got %+v", got)
	}
}

func TestContainerLogsCancel(t *testing.T) {
	d := newFakeDaemon(t)
	inspect(d, "c3", true)
	d.handle("GET /containers/c3/logs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done() // a follow that never ends
	})
	ctx, cancel := context.WithCancel(context.Background())
	lines, errs, err := d.client().ContainerLogs(ctx, "c3", LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	collectLines(t, lines) // must close
	select {
	case err := <-errs:
		t.Fatalf("cancellation reported as error: %v", err)
	default:
	}
}

func TestContainerLogsUnknownContainer(t *testing.T) {
	d := newFakeDaemon(t)
	if _, _, err := d.client().ContainerLogs(context.Background(), "gone", LogOptions{}); err == nil {
		t.Fatal("expected an error for a missing container")
	}
}
