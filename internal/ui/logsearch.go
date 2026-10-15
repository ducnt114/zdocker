package ui

// Adapted from kboba (github.com/ducnt114/kboba, internal/ui/logsearch.go).

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var matchStyle = lipgloss.NewStyle().Background(lipgloss.Color("#F5C542")).Foreground(lipgloss.Color("#000000"))

// containsFold reports whether line contains term, ignoring case. term
// must already be lower-case.
func containsFold(line, term string) bool {
	return term != "" && strings.Contains(strings.ToLower(line), term)
}

// highlightMatches renders every case-insensitive occurrence of term in
// line with style. term must already be lower-case.
func highlightMatches(line, term string, style lipgloss.Style) string {
	if term == "" {
		return line
	}
	lower := strings.ToLower(line)
	if len(lower) != len(line) {
		// Lower-casing changed byte lengths (rare Unicode): offsets in lower
		// no longer map onto line, so don't try to highlight.
		return line
	}
	var b strings.Builder
	for {
		i := strings.Index(lower, term)
		if i < 0 {
			b.WriteString(line)
			return b.String()
		}
		end := i + len(term)
		b.WriteString(line[:i])
		b.WriteString(style.Render(line[i:end]))
		line, lower = line[end:], lower[end:]
	}
}

// nextMatch returns the match after (forward) or before current, wrapping
// around. matches are sorted absolute line numbers. With no current match
// (current < 0) it returns the first match at or after from (forward) or
// the last one before it.
func nextMatch(matches []int, current, from int, forward bool) (int, bool) {
	if len(matches) == 0 {
		return 0, false
	}
	if current < 0 {
		for _, m := range matches {
			if m >= from {
				return m, true
			}
		}
		return matches[0], true
	}
	for i, m := range matches {
		if m == current {
			if forward {
				return matches[(i+1)%len(matches)], true
			}
			return matches[(i-1+len(matches))%len(matches)], true
		}
		if m > current {
			// current was evicted or never matched: pick its neighbour.
			if forward {
				return m, true
			}
			return matches[(i-1+len(matches))%len(matches)], true
		}
	}
	if forward {
		return matches[0], true
	}
	return matches[len(matches)-1], true
}
