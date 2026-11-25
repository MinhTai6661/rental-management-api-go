// Package stats holds the pure rules of the statistics: ratios, the range of months a figure is asked for, and the
// occupancy of rooms worked out day by day. It knows no table and no clock; the use cases hand it plain values.
package stats

import "math"

// round1 rounds to one decimal.
func round1(f float64) float64 { return math.Round(f*10) / 10 }

// Percent is part as a percentage of whole, with one decimal. It is nil (not 0) when there is no whole, so a figure
// that cannot be told is never shown as "0 %".
func Percent(part, whole int64) *float64 {
	if whole <= 0 {
		return nil
	}
	v := round1(float64(part) / float64(whole) * 100)
	return &v
}

// Change is how much current moved from previous, as a percentage with one decimal; nil when previous is not positive.
func Change(current, previous int64) *float64 {
	if previous <= 0 {
		return nil
	}
	v := round1(float64(current-previous) / float64(previous) * 100)
	return &v
}
