package ui

// Adapted from kboba (github.com/ducnt114/kboba, internal/ui/sort_test.go).

import (
	"slices"
	"testing"
)

func TestNaturalLess(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"9", "10", true},
		{"10", "9", false},
		{"pod-9", "pod-10", true},
		{"1/3", "2/3", true},
		{"0/1", "1/1", true},
		{"Apple", "banana", true}, // case-insensitive
		{"abc", "abcd", true},     // prefix first
		{"007", "7", false},       // equal numbers: not less
		{"7", "007", false},
		{"Running", "Pending", false},
		{"99999999999999999999", "100000000000000000000", true}, // no overflow
	}
	for _, tt := range tests {
		if got := naturalLess(tt.a, tt.b); got != tt.want {
			t.Errorf("naturalLess(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}

	names := []string{"web-10", "web-2", "Web-1", "api"}
	slices.SortFunc(names, func(a, b string) int {
		switch {
		case naturalLess(a, b):
			return -1
		case naturalLess(b, a):
			return 1
		}
		return 0
	})
	if want := []string{"api", "Web-1", "web-2", "web-10"}; !slices.Equal(names, want) {
		t.Fatalf("sorted = %v, want %v", names, want)
	}
}
