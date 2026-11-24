package audit

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// The two tables of the trail; the partitions are named after them.
const (
	TableImportant = "audit_logs"
	TableRoutine   = "audit_activity"
)

// TableOf is the table that holds a tier.
func TableOf(t Tier) string {
	if t == Routine {
		return TableRoutine
	}
	return TableImportant
}

// MonthStart is midnight UTC of the first day of t's month: partitions are cut there.
func MonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// PartitionName is the name of the partition of table for the month of t, e.g. audit_logs_2026_11.
func PartitionName(table string, t time.Time) string {
	m := MonthStart(t)
	return fmt.Sprintf("%s_%04d_%02d", table, m.Year(), int(m.Month()))
}

// MonthsAhead lists the first days of the month of now and the next ahead months.
func MonthsAhead(now time.Time, ahead int) []time.Time {
	first := MonthStart(now)
	months := make([]time.Time, 0, ahead+1)
	for i := 0; i <= ahead; i++ {
		months = append(months, first.AddDate(0, i, 0))
	}
	return months
}

// ParsePartition reads the month of a partition name of table; ok is false for any other name (the default partition).
func ParsePartition(table, name string) (month time.Time, ok bool) {
	m := regexp.MustCompile("^" + regexp.QuoteMeta(table) + `_(\d{4})_(\d{2})$`).FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, false
	}
	year, _ := strconv.Atoi(m[1])
	mon, _ := strconv.Atoi(m[2])
	if mon < 1 || mon > 12 {
		return time.Time{}, false
	}
	return time.Date(year, time.Month(mon), 1, 0, 0, 0, 0, time.UTC), true
}

// Expired reports whether every row of the partition of month is older than the cutoff, so the partition may be
// dropped whole.
func Expired(month, cutoff time.Time) bool {
	return !month.AddDate(0, 1, 0).After(cutoff)
}

// Cutoff is the time before which a tier's entries are no longer kept.
func Cutoff(t Tier, now time.Time, importantMonths, routineDays int) time.Time {
	if t == Routine {
		return now.AddDate(0, 0, -routineDays)
	}
	return now.AddDate(0, -importantMonths, 0)
}
