package ui

// Adapted from kboba (github.com/ducnt114/kboba, internal/ui/logbuffer.go).

import "strings"

// ringBuffer keeps the last N log lines. Pushing is O(1) and never
// reallocates: once full, the oldest line is overwritten.
type ringBuffer struct {
	lines []string
	start int // index of the oldest line
	size  int // number of lines stored
	// dropped counts lines evicted so far. Line i (0 = oldest kept) has the
	// absolute number dropped+i, which stays stable while the buffer turns.
	dropped int
}

func newRingBuffer(capacity int) ringBuffer {
	return ringBuffer{lines: make([]string, capacity)}
}

func (r *ringBuffer) push(line string) {
	if len(r.lines) == 0 {
		return
	}
	if r.size < len(r.lines) {
		r.lines[(r.start+r.size)%len(r.lines)] = line
		r.size++
		return
	}
	r.lines[r.start] = line
	r.start = (r.start + 1) % len(r.lines)
	r.dropped++
}

func (r *ringBuffer) reset() {
	clear(r.lines)
	r.start, r.size, r.dropped = 0, 0, 0
}

func (r ringBuffer) len() int { return r.size }

// at returns the i-th line, oldest first.
func (r ringBuffer) at(i int) string {
	return r.lines[(r.start+i)%len(r.lines)]
}

// String joins the lines, oldest first, separated by newlines.
func (r ringBuffer) String() string {
	n := 0
	for i := range r.size {
		n += len(r.at(i)) + 1
	}
	var b strings.Builder
	b.Grow(n)
	for i := range r.size {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(r.at(i))
	}
	return b.String()
}
