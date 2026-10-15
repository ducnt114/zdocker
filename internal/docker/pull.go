package docker

import (
	"context"
	"errors"
	"strings"

	mobyclient "github.com/moby/moby/client"
)

// LayerProgress is the state of one layer during a pull.
type LayerProgress struct {
	ID      string // short layer ID
	Status  string // Waiting, Downloading, Verifying Checksum, Extracting, Pull complete, Already exists…
	Current int64  // bytes so far in the current phase
	Total   int64  // 0 if unknown
}

// Done reports whether the layer needs no more work.
func (l LayerProgress) Done() bool {
	return l.Status == "Pull complete" || l.Status == "Already exists"
}

// PullProgress is a snapshot of a pull: every layer seen so far, in the
// order they first appeared, and the latest overall status line.
type PullProgress struct {
	Ref    string
	Layers []LayerProgress
	Status string // e.g. "Status: Downloaded newer image for nginx:1.27"
	Done   bool
	Err    error // set on the last snapshot if the pull failed
}

// PullImage pulls ref (`docker pull`), streaming progress snapshots. The
// channel is closed after the final snapshot (Done or Err set).
//
// The daemon sends a stream of JSON messages ("Downloading" with byte
// counts, one stream of updates per layer, interleaved); this folds them
// into one state per layer so the UI can draw a bar each.
func (c *client) PullImage(ctx context.Context, ref string) (<-chan PullProgress, error) {
	resp, err := c.api.ImagePull(ctx, ref, mobyclient.ImagePullOptions{})
	if err != nil {
		return nil, err
	}
	out := make(chan PullProgress, 16)
	go func() {
		defer close(out)
		defer resp.Close()

		state := PullProgress{Ref: ref}
		index := map[string]int{} // layer ID → position in state.Layers
		send := func(p PullProgress) bool {
			p.Layers = append([]LayerProgress(nil), p.Layers...) // a snapshot, not shared
			select {
			case out <- p:
				return true
			case <-ctx.Done():
				return false
			}
		}

		// JSONMessages is an iterator (range-over-func, Go 1.23).
		for msg, err := range resp.JSONMessages(ctx) {
			if err != nil {
				if ctx.Err() == nil {
					state.Err = err
					send(state)
				}
				return
			}
			if msg.Error != nil { // e.g. "manifest unknown", "pull access denied"
				state.Err = errors.New(msg.Error.Message)
				send(state)
				return
			}
			if !isLayerStatus(msg.Status) || msg.ID == "" {
				// Overall lines: "Pulling from library/nginx" (its ID is
				// the tag), "Digest: …", "Status: …".
				state.Status = strings.TrimSpace(msg.Status)
			} else {
				i, ok := index[msg.ID]
				if !ok {
					i = len(state.Layers)
					index[msg.ID] = i
					state.Layers = append(state.Layers, LayerProgress{ID: msg.ID})
				}
				l := &state.Layers[i]
				l.Status = msg.Status
				if msg.Progress != nil && msg.Progress.Total > 0 {
					l.Current, l.Total = msg.Progress.Current, msg.Progress.Total
				}
			}
			if !send(state) {
				return
			}
		}
		state.Done = true
		send(state)
	}()
	return out, nil
}

// isLayerStatus reports whether a status line is about one layer.
func isLayerStatus(s string) bool {
	switch s {
	case "Pulling fs layer", "Waiting", "Downloading", "Verifying Checksum", "Download complete",
		"Extracting", "Pull complete", "Already exists", "Retrying":
		return true
	}
	return strings.HasPrefix(s, "Retrying in")
}
