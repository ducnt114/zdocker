package ui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ducnt114/zdocker/internal/docker"
)

const (
	// eventsDebounce groups a burst of events (`docker compose up` starting
	// ten containers) into a single re-list.
	eventsDebounce = 200 * time.Millisecond
	minBackoff     = time.Second
	maxBackoff     = 30 * time.Second
)

type eventsState int

const (
	eventsOff eventsState = iota
	eventsLive
	eventsReconnecting
)

// Every message of the watcher carries the generation of the stream it
// belongs to; a reconnect bumps it, so leftovers of an old stream are
// ignored (the same idea as kboba's watch generations).
type (
	eventsStartedMsg struct {
		events <-chan docker.Event
		errs   <-chan error
		gen    int
	}
	dockerEventsMsg struct {
		events []docker.Event
		gen    int
	}
	eventsEndedMsg struct {
		err error
		gen int
	}
	debounceMsg  struct{ gen int }
	reconnectMsg struct{ gen int }
	// reloadMsg asks the root model to re-list what changed.
	reloadMsg struct{ containers, images bool }
)

// eventsWatcher follows the daemon's /events stream and turns it into
// reloads of the views. It is not a view: it has no View, only state.
type eventsWatcher struct {
	client docker.Client
	cancel context.CancelFunc
	events <-chan docker.Event
	errs   <-chan error
	gen    int

	state   eventsState
	backoff time.Duration

	// Kinds changed since the last reload, and whether a debounce timer
	// is already running.
	dirtyContainers, dirtyImages bool
	debouncing                   bool
	last                         string // last event, for the header
}

// start (re)subscribes to events, cancelling any previous stream.
func (w *eventsWatcher) start() tea.Cmd {
	w.stop()
	w.gen++
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	c, gen := w.client, w.gen
	return func() tea.Msg {
		evs, errs := c.Events(ctx)
		return eventsStartedMsg{events: evs, errs: errs, gen: gen}
	}
}

func (w *eventsWatcher) stop() {
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	w.debouncing = false
}

func waitForEvents(evs <-chan docker.Event, errs <-chan error, gen int) tea.Cmd {
	return func() tea.Msg {
		batch, ok := receiveBatch(evs, 100)
		if ok {
			return dockerEventsMsg{events: batch, gen: gen}
		}
		select { // the producer sends its error before closing evs
		case err := <-errs:
			return eventsEndedMsg{err: err, gen: gen}
		default:
			return eventsEndedMsg{gen: gen}
		}
	}
}

// healthy is called when the daemon answered normally (a list succeeded):
// the next disconnection starts again from the shortest backoff.
func (w *eventsWatcher) healthy() { w.backoff = 0 }

func nextBackoff(d time.Duration) time.Duration {
	if d == 0 {
		return minBackoff
	}
	return min(2*d, maxBackoff)
}

func (w eventsWatcher) Update(msg tea.Msg) (eventsWatcher, tea.Cmd) {
	switch msg := msg.(type) {
	case eventsStartedMsg:
		if msg.gen != w.gen {
			return w, nil
		}
		w.events, w.errs, w.state = msg.events, msg.errs, eventsLive
		return w, waitForEvents(w.events, w.errs, w.gen)

	case dockerEventsMsg:
		if msg.gen != w.gen {
			return w, nil
		}
		for _, ev := range msg.events {
			switch ev.Kind {
			case "container":
				w.dirtyContainers = true
				// Creating or removing a container changes the images'
				// "in use" count.
				if ev.Action == "create" || ev.Action == "destroy" {
					w.dirtyImages = true
				}
			case "image":
				w.dirtyImages = true
			}
			w.last = describeEvent(ev)
		}
		cmds := []tea.Cmd{waitForEvents(w.events, w.errs, w.gen)}
		if !w.debouncing {
			w.debouncing = true
			gen := w.gen
			cmds = append(cmds, tea.Tick(eventsDebounce, func(time.Time) tea.Msg { return debounceMsg{gen: gen} }))
		}
		return w, tea.Batch(cmds...)

	case debounceMsg:
		if msg.gen != w.gen {
			return w, nil
		}
		w.debouncing = false
		reload := reloadMsg{containers: w.dirtyContainers, images: w.dirtyImages}
		w.dirtyContainers, w.dirtyImages = false, false
		return w, func() tea.Msg { return reload }

	case eventsEndedMsg:
		if msg.gen != w.gen {
			return w, nil
		}
		// The daemon restarted, the socket went away… Try again later,
		// waiting longer after each failure.
		w.stop()
		w.state = eventsReconnecting
		w.backoff = nextBackoff(w.backoff)
		reason := "stream closed"
		if msg.err != nil {
			reason = msg.err.Error()
		}
		gen := w.gen
		return w, tea.Batch(
			reportErr(fmt.Errorf("lost Docker events (%s); reconnecting in %s", reason, w.backoff)),
			tea.Tick(w.backoff, func(time.Time) tea.Msg { return reconnectMsg{gen: gen} }),
		)

	case reconnectMsg:
		if msg.gen != w.gen {
			return w, nil
		}
		// Events missed while disconnected are lost: re-list everything.
		return w, tea.Batch(w.start(), func() tea.Msg { return reloadMsg{containers: true, images: true} })
	}
	return w, nil
}

func describeEvent(ev docker.Event) string {
	name := ev.Name
	if name == "" {
		name = shortRef(ev.ID)
	}
	return fmt.Sprintf("%s %s: %s", ev.Kind, name, ev.Action)
}

func shortRef(id string) string {
	if len(id) > 19 && id[:7] == "sha256:" {
		return id[7:19]
	}
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
