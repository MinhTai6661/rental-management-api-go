package notification

import (
	"time"

	"github.com/google/uuid"
)

// The popup limits: how many candidates one request looks at and how many it returns.
const (
	PopupCandidates = 20
	PopupsAtOnce    = 3
)

// ImpressionAction is what a person did with a popup.
type ImpressionAction string

// The impression actions.
const (
	ActionSeen    ImpressionAction = "seen"
	ActionDismiss ImpressionAction = "dismiss"
	ActionClick   ImpressionAction = "click"
)

// ParseImpressionAction returns the action named s.
func ParseImpressionAction(s string) (ImpressionAction, bool) {
	switch a := ImpressionAction(s); a {
	case ActionSeen, ActionDismiss, ActionClick:
		return a, true
	}
	return "", false
}

// PopupRules are the parts of an announcement that decide whether its popup shows again to somebody who saw it.
type PopupRules struct {
	StartsAt, EndsAt *time.Time
	Frequency        Frequency
	FrequencyDays    *int
	MaxPerPeriod     *int
	MaxTotal         *int
	Revision         int
}

// PopupRules returns the visibility rules of the announcement.
func (a Announcement) PopupRules() PopupRules {
	return PopupRules{
		StartsAt: a.StartsAt, EndsAt: a.EndsAt, Frequency: a.Frequency, FrequencyDays: a.FrequencyDays,
		MaxPerPeriod: a.MaxPerPeriod, MaxTotal: a.MaxTotal, Revision: a.Revision,
	}
}

// Seen is what one person did with one popup so far (the row of announcement_impressions).
type Seen struct {
	Revision    int
	SessionID   *uuid.UUID
	FirstSeenAt time.Time
	LastSeenAt  time.Time
	DismissedAt *time.Time
	ClickedAt   *time.Time
	// ViewCount is how many times it was shown in all; PeriodViewCount how many in the period it is in.
	ViewCount       int
	PeriodViewCount int
	PeriodStartedAt *time.Time
}

func sameSession(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func periodDays(days *int) int {
	if days == nil || *days < 1 {
		return 1
	}
	return *days
}

// periodIsOpen reports whether a period of "every N days" that started at startedAt is still running.
func periodIsOpen(startedAt *time.Time, days *int, now time.Time) bool {
	return startedAt != nil && startedAt.AddDate(0, 0, periodDays(days)).After(now)
}

// Shows reports whether the popup is shown to a person now: it is inside its window, the person's earlier views allow
// another one and none of its limits is reached. seen is nil when the person never saw it. Popups can always be
// closed, so there is no "stays until it ends" case.
func Shows(r PopupRules, seen *Seen, session *uuid.UUID, now time.Time) bool {
	if (r.StartsAt != nil && r.StartsAt.After(now)) || (r.EndsAt != nil && !r.EndsAt.After(now)) {
		return false
	}
	// Never seen, or the message was edited with "show again".
	if seen == nil || seen.Revision < r.Revision {
		return true
	}
	if r.MaxTotal != nil && seen.ViewCount >= *r.MaxTotal {
		return false
	}
	if r.MaxPerPeriod != nil && ViewsInPeriod(r.Frequency, r.FrequencyDays, seen, session, now) >= *r.MaxPerPeriod {
		return false
	}
	switch r.Frequency {
	case FrequencyOnce:
		return seen.DismissedAt == nil
	case FrequencyEverySession:
		return seen.DismissedAt == nil || !sameSession(seen.SessionID, session)
	case FrequencyEveryDays:
		return seen.DismissedAt == nil || !seen.DismissedAt.AddDate(0, 0, periodDays(r.FrequencyDays)).After(now)
	}
	return false
}

// ViewsInPeriod is how many times the popup was shown in the period it is in now. A period is a session ("every
// session"), N days from the first view in it ("every N days"), or the whole time ("once", which has no period).
func ViewsInPeriod(f Frequency, days *int, seen *Seen, session *uuid.UUID, now time.Time) int {
	switch f {
	case FrequencyOnce:
		return seen.ViewCount
	case FrequencyEverySession:
		if sameSession(seen.SessionID, session) {
			return seen.PeriodViewCount
		}
	case FrequencyEveryDays:
		if periodIsOpen(seen.PeriodStartedAt, days, now) {
			return seen.PeriodViewCount
		}
	}
	return 0
}

// PeriodAfterView is the period count after one more view: the running period gets one more, a new period starts at one.
func PeriodAfterView(f Frequency, days *int, seen *Seen, session *uuid.UUID, now time.Time) (count int, startedAt time.Time) {
	if seen == nil {
		return 1, now
	}
	running := false
	switch f {
	case FrequencyOnce:
		running = true
	case FrequencyEverySession:
		running = sameSession(seen.SessionID, session)
	case FrequencyEveryDays:
		running = periodIsOpen(seen.PeriodStartedAt, days, now)
	}
	if !running {
		return 1, now
	}
	startedAt = now
	if seen.PeriodStartedAt != nil {
		startedAt = *seen.PeriodStartedAt
	}
	return seen.PeriodViewCount + 1, startedAt
}

// AfterAction is what a person's row says after they did something with the popup. prev is nil for a first contact. A
// popup edited with "show again" (a newer revision) starts over for the person; a click also closes the popup.
func AfterAction(prev *Seen, action ImpressionAction, r PopupRules, session *uuid.UUID, now time.Time) Seen {
	var next Seen
	if prev != nil {
		next = *prev
		if prev.Revision < r.Revision {
			next.DismissedAt, next.ClickedAt, next.ViewCount, next.PeriodViewCount, next.PeriodStartedAt = nil, nil, 0, 0, nil
		}
	}
	// What the person had done before this action, for the period count.
	var before *Seen
	if prev != nil {
		copied := next
		before = &copied
	}
	next.Revision = r.Revision
	next.SessionID = session
	if prev == nil {
		next.FirstSeenAt = now
	}
	next.LastSeenAt = now
	switch action {
	case ActionDismiss:
		next.DismissedAt = &now
	case ActionClick:
		next.ClickedAt = &now
		if next.DismissedAt == nil {
			next.DismissedAt = &now
		}
	default:
		next.ViewCount++
		next.PeriodViewCount, next.PeriodStartedAt = periodFields(PeriodAfterView(r.Frequency, r.FrequencyDays, before, session, now))
	}
	return next
}

// periodFields adapts PeriodAfterView's result to the two fields it fills.
func periodFields(count int, started time.Time) (int, *time.Time) { return count, &started }
