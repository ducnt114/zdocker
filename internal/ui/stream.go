package ui

// Adapted from kboba (github.com/ducnt114/kboba, internal/ui/stream.go).

// receiveBatch blocks until one value arrives on ch, then also takes every
// value that is already waiting (up to limit in total) without blocking.
//
// This is how kboba turns a channel into Bubble Tea messages: one tea.Cmd
// waits for the next batch, the Update that handles it re-subscribes with a
// new tea.Cmd. Batching means a burst (the initial pod list, a chatty log)
// costs one Update/render instead of one per item.
//
// ok is false only when ch is closed and nothing was received.
func receiveBatch[T any](ch <-chan T, limit int) (batch []T, ok bool) {
	first, ok := <-ch
	if !ok {
		return nil, false
	}
	batch = append(batch, first)
	for len(batch) < limit {
		select {
		case v, ok := <-ch:
			if !ok {
				// Deliver what we have; the next call reports the close.
				return batch, true
			}
			batch = append(batch, v)
		default:
			return batch, true
		}
	}
	return batch, true
}
