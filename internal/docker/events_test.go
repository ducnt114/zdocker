package docker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/events"
)

func recvEvent(t *testing.T, ch <-chan Event) (Event, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		return ev, ok
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an event")
		return Event{}, false
	}
}

func TestEventsStream(t *testing.T) {
	d := newFakeDaemon(t)
	src := make(chan any, 4)
	d.streamEvents(src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	evs, errs := d.client().Events(ctx)
	src <- events.Message{Type: events.ContainerEventType, Action: events.ActionDie,
		Actor: events.Actor{ID: "c1", Attributes: map[string]string{"name": "web", "exitCode": "1"}}, TimeNano: 1700000000000000000}
	src <- events.Message{Type: events.ImageEventType, Action: events.ActionDelete, Actor: events.Actor{ID: "sha256:abc"}}

	ev, _ := recvEvent(t, evs)
	if ev.Kind != "container" || ev.Action != "die" || ev.ID != "c1" || ev.Name != "web" || ev.Time.Unix() != 1700000000 {
		t.Fatalf("first event = %+v", ev)
	}
	if ev, _ := recvEvent(t, evs); ev.Kind != "image" || ev.Action != "delete" {
		t.Fatalf("second event = %+v", ev)
	}

	// Only container and image events are asked for.
	if calls := d.calls(); !strings.Contains(calls[0], "filters=") || !strings.Contains(calls[0], "container") || !strings.Contains(calls[0], "image") {
		t.Fatalf("calls = %v", calls)
	}

	// The daemon going away ends the stream with an error.
	close(src)
	for {
		if _, ok := recvEvent(t, evs); !ok {
			break
		}
	}
	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("expected an error when the stream breaks")
		}
	default:
		t.Fatal("no error reported for a broken stream")
	}
}

func TestEventsCancelIsNotAnError(t *testing.T) {
	d := newFakeDaemon(t)
	d.streamEvents(make(chan any))
	ctx, cancel := context.WithCancel(context.Background())

	evs, errs := d.client().Events(ctx)
	cancel()
	for {
		if _, ok := recvEvent(t, evs); !ok {
			break
		}
	}
	select {
	case err := <-errs:
		t.Fatalf("cancellation reported as error: %v", err)
	default:
	}
}
