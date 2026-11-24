package notification

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	popupNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	s1       = uuid.MustParse("00000000-0000-0000-0000-0000000000a1")
	s2       = uuid.MustParse("00000000-0000-0000-0000-0000000000a2")
)

func at(t time.Time) *time.Time { return &t }
func num(n int) *int            { return &n }

func rules(f Frequency) PopupRules { return PopupRules{Frequency: f, Revision: 1} }

func TestShowsWindow(t *testing.T) {
	r := rules(FrequencyOnce)
	r.StartsAt = at(popupNow.Add(time.Hour))
	if Shows(r, nil, &s1, popupNow) {
		t.Error("not started")
	}
	r.StartsAt, r.EndsAt = nil, at(popupNow)
	if Shows(r, nil, &s1, popupNow) {
		t.Error("ends_at == now is over")
	}
	r.StartsAt, r.EndsAt = at(popupNow.Add(-time.Hour)), at(popupNow.Add(time.Hour))
	if !Shows(r, nil, &s1, popupNow) {
		t.Error("inside the window")
	}
}

func TestShowsFrequencies(t *testing.T) {
	closed := popupNow.Add(-time.Hour)
	seenOpen := &Seen{Revision: 1, SessionID: &s1}
	seenClosed := &Seen{Revision: 1, SessionID: &s1, DismissedAt: &closed}

	once := rules(FrequencyOnce)
	if !Shows(once, seenOpen, &s1, popupNow) || Shows(once, seenClosed, &s1, popupNow) {
		t.Error("once: shows until closed")
	}
	again := once
	again.Revision = 2
	if !Shows(again, seenClosed, &s1, popupNow) {
		t.Error("a newer revision shows a closed popup again")
	}

	session := rules(FrequencyEverySession)
	if Shows(session, seenClosed, &s1, popupNow) || !Shows(session, seenClosed, &s2, popupNow) {
		t.Error("every_session: same session stays closed, a new session shows it")
	}

	d := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	days := rules(FrequencyEveryDays)
	closedOld := &Seen{Revision: 1, SessionID: &s1, DismissedAt: &d}
	days.FrequencyDays = num(3)
	if !Shows(days, closedOld, &s1, popupNow) {
		t.Error("3 days after closing it shows")
	}
	days.FrequencyDays = num(4)
	if Shows(days, closedOld, &s1, popupNow) {
		t.Error("4 days: not yet")
	}
}

func TestShowsLimits(t *testing.T) {
	r := rules(FrequencyOnce)
	r.MaxTotal = num(3)
	if !Shows(r, &Seen{Revision: 1, ViewCount: 2}, &s1, popupNow) || Shows(r, &Seen{Revision: 1, ViewCount: 3}, &s1, popupNow) {
		t.Error("max_total")
	}

	r = rules(FrequencyEverySession)
	r.MaxPerPeriod = num(2)
	if !Shows(r, &Seen{Revision: 1, SessionID: &s1, PeriodViewCount: 1}, &s1, popupNow) {
		t.Error("1 of 2 in this session shows")
	}
	if Shows(r, &Seen{Revision: 1, SessionID: &s1, PeriodViewCount: 2}, &s1, popupNow) {
		t.Error("2 of 2 in this session does not")
	}
	if !Shows(r, &Seen{Revision: 1, SessionID: &s1, PeriodViewCount: 2}, &s2, popupNow) {
		t.Error("another session starts a new period")
	}

	r = rules(FrequencyEveryDays)
	r.FrequencyDays, r.MaxPerPeriod = num(1), num(2)
	running := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	old := time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC)
	if Shows(r, &Seen{Revision: 1, PeriodViewCount: 2, PeriodStartedAt: &running}, &s1, popupNow) {
		t.Error("daily, 2 of 2 in the running period")
	}
	if !Shows(r, &Seen{Revision: 1, PeriodViewCount: 2, PeriodStartedAt: &old}, &s1, popupNow) {
		t.Error("the period of yesterday is over")
	}
}

func TestPeriodAfterView(t *testing.T) {
	started := popupNow.Add(-2 * time.Hour)
	daily := num(1)
	if c, s := PeriodAfterView(FrequencyEveryDays, daily, &Seen{PeriodViewCount: 1, PeriodStartedAt: &started}, &s1, popupNow); c != 2 || !s.Equal(started) {
		t.Errorf("a running period gets one more and keeps its start: %d %v", c, s)
	}
	old := popupNow.Add(-48 * time.Hour)
	if c, s := PeriodAfterView(FrequencyEveryDays, daily, &Seen{PeriodViewCount: 5, PeriodStartedAt: &old}, &s1, popupNow); c != 1 || !s.Equal(popupNow) {
		t.Errorf("a closed period restarts: %d %v", c, s)
	}
	if c, s := PeriodAfterView(FrequencyOnce, nil, nil, &s1, popupNow); c != 1 || !s.Equal(popupNow) {
		t.Errorf("never seen: %d %v", c, s)
	}
}

func TestAfterAction(t *testing.T) {
	r := rules(FrequencyOnce)
	first := AfterAction(nil, ActionSeen, r, &s1, popupNow)
	if first.ViewCount != 1 || first.PeriodViewCount != 1 || first.FirstSeenAt != popupNow || first.DismissedAt != nil {
		t.Errorf("first view: %+v", first)
	}
	later := popupNow.Add(time.Minute)
	clicked := AfterAction(&first, ActionClick, r, &s1, later)
	if clicked.ClickedAt == nil || clicked.DismissedAt == nil || clicked.ViewCount != 1 || !clicked.FirstSeenAt.Equal(popupNow) {
		t.Errorf("a click also closes: %+v", clicked)
	}
	r.Revision = 2
	reset := AfterAction(&clicked, ActionSeen, r, &s1, later)
	if reset.DismissedAt != nil || reset.ClickedAt != nil || reset.ViewCount != 1 || reset.Revision != 2 {
		t.Errorf("a newer revision starts over: %+v", reset)
	}
}

func TestParseImpressionAction(t *testing.T) {
	for _, s := range []string{"seen", "dismiss", "click"} {
		if _, ok := ParseImpressionAction(s); !ok {
			t.Errorf("%s", s)
		}
	}
	if _, ok := ParseImpressionAction("close"); ok {
		t.Error("unknown")
	}
}
