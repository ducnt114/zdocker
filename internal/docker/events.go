package docker

import (
	"context"
	"time"

	"github.com/moby/moby/api/types/events"
	mobyclient "github.com/moby/moby/client"
)

// Event is one change reported by the daemon, e.g. container "web" died.
type Event struct {
	Kind   string // "container" or "image"
	Action string // start, die, destroy, pull, delete, …
	ID     string
	Name   string // container name or image reference, when known
	Time   time.Time
}

// Events streams container and image events until ctx is cancelled or the
// connection drops (e.g. the daemon restarts). The first channel is closed
// when the stream ends; if it ended with an error other than cancellation,
// that error is sent on the second channel first.
//
// Docker has no informer-style watch with resource versions: a client
// lists once, then follows /events and re-lists what changed.
func (c *client) Events(ctx context.Context) (<-chan Event, <-chan error) {
	out := make(chan Event, 64)
	errs := make(chan error, 1)

	filters := mobyclient.Filters{}.Add("type", string(events.ContainerEventType), string(events.ImageEventType))
	res := c.api.Events(ctx, mobyclient.EventsListOptions{Filters: filters})

	go func() {
		defer close(out)
		for {
			// Moby never closes Messages; an error (or the end of the
			// stream) arrives on Err instead, so watch both.
			select {
			case m := <-res.Messages:
				select {
				case out <- newEvent(m):
				case <-ctx.Done():
					return
				}
			case err := <-res.Err:
				if err != nil && ctx.Err() == nil {
					errs <- err
				}
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, errs
}

func newEvent(m events.Message) Event {
	name := m.Actor.Attributes["name"]
	return Event{
		Kind:   string(m.Type),
		Action: string(m.Action),
		ID:     m.Actor.ID,
		Name:   name,
		Time:   time.Unix(0, m.TimeNano),
	}
}
