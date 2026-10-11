package eval

import "slices"

const (
	statusRegressed     = "regressed"
	statusImproved      = "improved"
	statusUnchanged     = "unchanged"
	statusNotComparable = "not_comparable"
)

// regression compares pass fractions; pending attempts remain in the denominator.
func regression(bp, br, cp, cr int) string {
	if br == 0 || cr == 0 {
		return statusNotComparable
	}
	switch {
	case int64(cp)*int64(br) < int64(bp)*int64(cr):
		return statusRegressed
	case int64(cp)*int64(br) > int64(bp)*int64(cr):
		return statusImproved
	default:
		return statusUnchanged
	}
}

// medianInteger returns the floored midpoint without overflowing the sum.
func medianInteger[T ~int | ~int64](values []T) T {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Sorted(slices.Values(values))
	middle := len(sorted) / 2
	if len(sorted)%2 != 0 {
		return sorted[middle]
	}
	a, b := sorted[middle-1], sorted[middle]
	return (a & b) + ((a ^ b) >> 1)
}

func medianScore(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Sorted(slices.Values(values))
	middle := len(sorted) / 2
	if len(sorted)%2 != 0 {
		return sorted[middle]
	}
	return sorted[middle-1]/2 + sorted[middle]/2
}
