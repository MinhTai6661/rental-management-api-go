package notification

import (
	"errors"
	"testing"
	"time"
)

var ict = time.FixedZone("ICT", 7*3600)

func day(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, ict) }

func TestDaysOldestFirst(t *testing.T) {
	days, err := Days(day(10, 30), day(11, 2))
	if err != nil || len(days) != 4 || !days[0].Equal(day(10, 30)) || !days[3].Equal(day(11, 2)) {
		t.Fatalf("%v %v", days, err)
	}
}

func TestDaysRefusals(t *testing.T) {
	if _, err := Days(day(11, 2), day(11, 1)); !errors.Is(err, ErrRangeBackwards) {
		t.Errorf("backwards: %v", err)
	}
	if days, err := Days(day(1, 1), day(1, 1).AddDate(0, 0, MaxStatDays-1)); err != nil || len(days) != MaxStatDays {
		t.Errorf("92 days are fine: %d %v", len(days), err)
	}
	if _, err := Days(day(1, 1), day(1, 1).AddDate(0, 0, MaxStatDays)); !errors.Is(err, ErrRangeTooLong) {
		t.Errorf("93 days: %v", err)
	}
}

func TestResolveDays(t *testing.T) {
	today := day(11, 24)
	first, last := ResolveDays(nil, nil, today)
	if !last.Equal(today) || !first.Equal(today.AddDate(0, 0, -29)) {
		t.Errorf("default 30 days ending today: %v %v", first, last)
	}
	to := day(11, 10)
	first, last = ResolveDays(nil, &to, today)
	if !last.Equal(to) || !first.Equal(to.AddDate(0, 0, -29)) {
		t.Errorf("30 days ending at to: %v %v", first, last)
	}
	from := day(11, 1)
	if first, _ = ResolveDays(&from, nil, today); !first.Equal(from) {
		t.Errorf("from is kept: %v", first)
	}
}

func TestPercent(t *testing.T) {
	if p := Percent(1, 3); p == nil || *p != 33.3 {
		t.Errorf("1/3: %v", p)
	}
	if p := Percent(2, 4); p == nil || *p != 50 {
		t.Errorf("2/4: %v", p)
	}
	if Percent(0, 0) != nil || Percent(3, 0) != nil {
		t.Error("no whole is nil, not 0")
	}
	if v := PerViewer(45, 30); v == nil || *v != 1.5 || PerViewer(1, 0) != nil {
		t.Errorf("per viewer: %v", v)
	}
}
