package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"

	"github.com/moby/moby/api/pkg/stdcopy"
	mobyclient "github.com/moby/moby/client"
)

const (
	logTailLines    = 500
	maxLogLineBytes = 1024 * 1024
)

// LogOptions are the user-tunable parts of a log request.
type LogOptions struct {
	Timestamps bool // prefix lines with RFC3339 timestamps
}

// LogLine is one line of a container's output.
type LogLine struct {
	Text   string
	Stderr bool
}

// ContainerLogs follows a container's logs (`docker logs -f --tail 500`).
//
// Lines arrive on the first channel, which is closed when the stream ends
// (ctx cancelled, container removed, daemon gone); an error other than
// cancellation is sent on the second channel first.
//
// Without a TTY the daemon multiplexes stdout and stderr into one stream of
// frames (an 8-byte header saying which stream, then the payload); stdcopy
// splits them. With a TTY there is a single raw stream.
func (c *client) ContainerLogs(ctx context.Context, id string, o LogOptions) (<-chan LogLine, <-chan error, error) {
	info, err := c.api.ContainerInspect(ctx, id, mobyclient.ContainerInspectOptions{})
	if err != nil {
		return nil, nil, err
	}
	tty := info.Container.Config != nil && info.Container.Config.Tty

	body, err := c.api.ContainerLogs(ctx, id, mobyclient.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       strconv.Itoa(logTailLines),
		Timestamps: o.Timestamps,
	})
	if err != nil {
		return nil, nil, err
	}

	out := make(chan LogLine, 256)
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer body.Close()

		stdout := &lineWriter{ctx: ctx, out: out}
		stderr := &lineWriter{ctx: ctx, out: out, stderr: true}
		if tty {
			_, err = io.Copy(stdout, body)
		} else {
			_, err = stdcopy.StdCopy(stdout, stderr, body)
		}
		stdout.flush()
		stderr.flush()
		if err != nil && !errors.Is(err, io.EOF) && ctx.Err() == nil {
			errs <- err
		}
	}()
	return out, errs, nil
}

// lineWriter turns written bytes into LogLines. stdcopy writes frames to
// the stdout and stderr writers one after the other from one goroutine, so
// sending lines as soon as they are complete keeps the original order
// between the two streams.
type lineWriter struct {
	ctx    context.Context
	out    chan<- LogLine
	stderr bool
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		if err := w.send(w.buf[:i]); err != nil {
			return 0, err
		}
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > maxLogLineBytes { // a "line" without end: cut it
		if err := w.send(w.buf); err != nil {
			return 0, err
		}
		w.buf = nil
	}
	return len(p), nil
}

func (w *lineWriter) send(line []byte) error {
	text := string(bytes.TrimSuffix(line, []byte("\r")))
	select {
	case w.out <- LogLine{Text: text, Stderr: w.stderr}:
		return nil
	case <-w.ctx.Done():
		return w.ctx.Err()
	}
}

// flush sends a last line that had no trailing newline.
func (w *lineWriter) flush() {
	if len(w.buf) > 0 && w.ctx.Err() == nil {
		w.send(w.buf)
		w.buf = nil
	}
}
