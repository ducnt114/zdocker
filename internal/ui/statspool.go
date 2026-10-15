package ui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ducnt114/zdocker/internal/docker"
)

// statsSample is one stats sample of one container.
type statsSample struct {
	id string
	s  docker.Stats
}

// tableStatsMsg is a batch of samples for the containers table.
type tableStatsMsg struct {
	samples []statsSample
	gen     int
}

// statsPool keeps one stats stream per running container — what
// `docker stats` does — and merges them into one channel (fan-in) that the
// containers table reads in batches.
//
// It is used through a pointer: the containers view is copied on every
// Update, the pool (channels, goroutines) must not be.
type statsPool struct {
	client  docker.Client
	ctx     context.Context // cancelled by stopAll: ends every stream
	cancel  context.CancelFunc
	out     chan statsSample
	streams map[string]context.CancelFunc // container ID → its stream
	latest  map[string]docker.Stats
	waiting bool // a waitForTableStats command is outstanding
	gen     int
}

func newStatsPool(c docker.Client) *statsPool {
	return &statsPool{client: c, streams: map[string]context.CancelFunc{}, latest: map[string]docker.Stats{}}
}

// sync makes the set of streams match the running containers: new ones
// start, gone ones stop. It returns the command reading the merged
// channel, if one isn't already waiting.
func (p *statsPool) sync(running []string) tea.Cmd {
	if p.ctx == nil {
		p.gen++
		p.ctx, p.cancel = context.WithCancel(context.Background())
		p.out = make(chan statsSample, 64)
	}
	want := make(map[string]bool, len(running))
	for _, id := range running {
		want[id] = true
		if _, ok := p.streams[id]; !ok {
			p.start(id)
		}
	}
	for id, cancel := range p.streams {
		if !want[id] {
			cancel()
			delete(p.streams, id)
			delete(p.latest, id)
		}
	}
	if p.waiting || len(p.streams) == 0 {
		return nil
	}
	p.waiting = true
	return waitForTableStats(p.ctx, p.out, p.gen)
}

// start runs one container's stream in a goroutine, forwarding samples to
// the shared channel until its context (or the pool's) is cancelled.
func (p *statsPool) start(id string) {
	ctx, cancel := context.WithCancel(p.ctx)
	p.streams[id] = cancel
	c, out := p.client, p.out
	go func() {
		samples, _, err := c.ContainerStats(ctx, id)
		if err != nil {
			return // the container stopped meanwhile: the next sync retries or drops it
		}
		for s := range samples {
			select {
			case out <- statsSample{id: id, s: s}:
			case <-ctx.Done():
				return
			}
		}
	}()
}

// stopAll ends every stream (metrics turned off, context switched).
func (p *statsPool) stopAll() {
	if p.cancel != nil {
		p.cancel()
	}
	p.ctx, p.cancel, p.out = nil, nil, nil
	p.streams = map[string]context.CancelFunc{}
	p.latest = map[string]docker.Stats{}
	p.waiting = false
}

// waitForTableStats waits for samples (or the pool stopping), then takes
// whatever else is ready: one table refresh per batch, not per sample.
// The channel is never closed (streams come and go), so the pool's
// context is what releases a waiter when the pool stops.
func waitForTableStats(ctx context.Context, out <-chan statsSample, gen int) tea.Cmd {
	return func() tea.Msg {
		var batch []statsSample
		select {
		case s := <-out:
			batch = append(batch, s)
		case <-ctx.Done():
			return nil
		}
		for len(batch) < 500 {
			select {
			case s := <-out:
				batch = append(batch, s)
			default:
				return tableStatsMsg{samples: batch, gen: gen}
			}
		}
		return tableStatsMsg{samples: batch, gen: gen}
	}
}

// receive stores a batch and re-subscribes.
func (p *statsPool) receive(msg tableStatsMsg) tea.Cmd {
	if msg.gen != p.gen || p.ctx == nil {
		return nil // from a pool generation that was stopped
	}
	for _, s := range msg.samples {
		if _, ok := p.streams[s.id]; ok { // not one we stopped meanwhile
			p.latest[s.id] = s.s
		}
	}
	return waitForTableStats(p.ctx, p.out, p.gen)
}
