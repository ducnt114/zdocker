package ui

// Adapted from kboba (github.com/ducnt114/kboba, internal/ui/logbuffer_test.go).

import (
	"strconv"
	"testing"
)

func TestRingBufferKeepsLastLines(t *testing.T) {
	r := newRingBuffer(3)
	if r.String() != "" || r.len() != 0 {
		t.Fatal("new buffer should be empty")
	}

	r.push("a")
	r.push("b")
	if got := r.String(); got != "a\nb" {
		t.Fatalf("got %q", got)
	}

	r.push("c")
	r.push("d")
	r.push("e")
	if got := r.String(); got != "c\nd\ne" {
		t.Fatalf("got %q", got)
	}
	if r.len() != 3 || r.at(0) != "c" || r.at(2) != "e" {
		t.Fatalf("len=%d at0=%q at2=%q", r.len(), r.at(0), r.at(2))
	}

	r.reset()
	if r.len() != 0 || r.String() != "" {
		t.Fatal("reset should empty the buffer")
	}
	r.push("x")
	if got := r.String(); got != "x" {
		t.Fatalf("after reset got %q", got)
	}
}

func TestRingBufferLogCapacity(t *testing.T) {
	r := newRingBuffer(maxLogLines)
	for i := range maxLogLines + 123 {
		r.push(strconv.Itoa(i))
	}
	if r.len() != maxLogLines {
		t.Fatalf("len = %d", r.len())
	}
	if r.at(0) != "123" || r.at(maxLogLines-1) != strconv.Itoa(maxLogLines+122) {
		t.Fatalf("oldest=%q newest=%q", r.at(0), r.at(maxLogLines-1))
	}
	// Absolute line numbers survive eviction: line 123 was the 124th pushed.
	if r.dropped != 123 {
		t.Fatalf("dropped = %d", r.dropped)
	}
}
