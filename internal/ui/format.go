package ui

// Adapted from kboba (github.com/ducnt114/kboba, internal/ui/format.go).

import (
	"fmt"
	"time"
)

// formatAge renders a duration the way kubectl/k9s show ages: the two most
// significant units, e.g. "42s", "5m10s", "3h4m", "12d", "2y40d".
func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Seconds())
	const (
		minute = 60
		hour   = 60 * minute
		day    = 24 * hour
		year   = 365 * day
	)
	switch {
	case s < minute:
		return fmt.Sprintf("%ds", s)
	case s < 10*minute:
		return trimZero(s/minute, "m", s%minute, "s")
	case s < hour:
		return fmt.Sprintf("%dm", s/minute)
	case s < 10*hour:
		return trimZero(s/hour, "h", s%hour/minute, "m")
	case s < day:
		return fmt.Sprintf("%dh", s/hour)
	case s < 10*day:
		return trimZero(s/day, "d", s%day/hour, "h")
	case s < year:
		return fmt.Sprintf("%dd", s/day)
	default:
		return trimZero(s/year, "y", s%year/day, "d")
	}
}

// trimZero formats "<a><unitA><b><unitB>", dropping the second part if zero.
func trimZero(a int, unitA string, b int, unitB string) string {
	if b == 0 {
		return fmt.Sprintf("%d%s", a, unitA)
	}
	return fmt.Sprintf("%d%s%d%s", a, unitA, b, unitB)
}
