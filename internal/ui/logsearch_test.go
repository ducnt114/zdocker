package ui

// Adapted from kboba (github.com/ducnt114/kboba, internal/ui/logsearch_test.go).

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestHighlightMatches(t *testing.T) {
	// A style that is visible without a terminal: wrap in brackets.
	style := lipgloss.NewStyle().Transform(func(s string) string { return "[" + s + "]" })

	tests := []struct{ line, term, want string }{
		{"no match here", "xyz", "no match here"},
		{"ERROR: disk error", "error", "[ERROR]: disk [error]"},
		{"aaa", "aa", "[aa]a"},
		{"anything", "", "anything"},
	}
	for _, tt := range tests {
		if got := highlightMatches(tt.line, tt.term, style); got != tt.want {
			t.Errorf("highlightMatches(%q, %q) = %q, want %q", tt.line, tt.term, got, tt.want)
		}
	}
}

func TestContainsFold(t *testing.T) {
	if !containsFold("Connection REFUSED", "refused") {
		t.Error("should match case-insensitively")
	}
	if containsFold("anything", "") {
		t.Error("empty term matches nothing")
	}
}

func TestNextMatch(t *testing.T) {
	matches := []int{10, 20, 30}
	tests := []struct {
		name          string
		current, from int
		forward       bool
		want          int
	}{
		{"first from top", -1, 0, true, 10},
		{"first after position", -1, 15, true, 20},
		{"wrap to first when past the end", -1, 99, true, 10},
		{"next", 10, 0, true, 20},
		{"next wraps", 30, 0, true, 10},
		{"previous", 20, 0, false, 10},
		{"previous wraps", 10, 0, false, 30},
		{"current evicted, forward", 5, 0, true, 10},
		{"current between matches, backward", 25, 0, false, 20},
	}
	for _, tt := range tests {
		got, ok := nextMatch(matches, tt.current, tt.from, tt.forward)
		if !ok || got != tt.want {
			t.Errorf("%s: got %d,%v want %d", tt.name, got, ok, tt.want)
		}
	}
	if _, ok := nextMatch(nil, -1, 0, true); ok {
		t.Error("no matches should return ok=false")
	}
}
