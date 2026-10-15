package ui

// Adapted from kboba (github.com/ducnt114/kboba, internal/ui/sort.go).

import (
	"strings"
	"unicode"
)

// naturalLess compares strings the way people expect: runs of digits are
// compared as numbers, so "pod-9" < "pod-10", "9" < "10" (RESTARTS) and
// "1/3" < "2/3" (READY). Everything else compares case-insensitively.
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		da, db := isDigit(a[0]), isDigit(b[0])
		switch {
		case da && db:
			na, ra := splitDigits(a)
			nb, rb := splitDigits(b)
			if c := compareNumbers(na, nb); c != 0 {
				return c < 0
			}
			a, b = ra, rb
		default:
			// Compare one rune, ignoring case.
			ra, rb := []rune(a)[0], []rune(b)[0]
			la, lb := unicode.ToLower(ra), unicode.ToLower(rb)
			if la != lb {
				return la < lb
			}
			a, b = a[len(string(ra)):], b[len(string(rb)):]
		}
	}
	return len(a) < len(b)
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

// splitDigits splits s into its leading run of digits and the rest.
func splitDigits(s string) (digits, rest string) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return s[:i], s[i:]
}

// compareNumbers compares two digit strings of any length without
// converting them (so huge numbers can't overflow).
func compareNumbers(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	return strings.Compare(a, b)
}
