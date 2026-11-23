package invoice

import (
	"errors"
	"time"
)

// ParseDelivery reads a delivery code.
func ParseDelivery(s string) (Delivery, bool) {
	switch d := Delivery(s); d {
	case DeliveryApp, DeliveryLink:
		return d, true
	}
	return "", false
}

// ResolveDelivery is what a bill actually uses: a tenant with an account is reached in the app unless the landlord
// chose the link; a contract nobody holds has no account to reach, so only the link is left.
func ResolveDelivery(hasAccount bool, asked *Delivery) Delivery {
	if !hasAccount {
		return DeliveryLink
	}
	if asked != nil {
		return *asked
	}
	return DeliveryApp
}

// ReachesAccount says whether the bill goes to the tenant's account (a notice and an email).
func (d Delivery) ReachesAccount() bool { return d == DeliveryApp }

// PlanStatusChange checks a change of payment status a landlord asks for and says whether it settles a draft
// directly. A bill goes between sent and paid; a draft cannot change its payment status until it is sent, except that
// one whose money is already in hand may go straight to paid (it becomes sent without telling the person).
func PlanStatusChange(from, to Status) (settleDraft bool, err error) {
	if to == StatusDraft || (from == StatusDraft && to != StatusPaid) {
		return false, ErrSendFirst
	}
	return from == StatusDraft, nil
}

// CanSend says whether a bill can be sent (or sent again): any but a paid one.
func CanSend(s Status) bool { return s != StatusPaid }

// Locked says whether a bill can no longer be corrected or removed: it is paid, or somebody paid (or is paying) it, so
// it cannot change under a tenant who already sent money for it.
func Locked(s Status, t Totals) bool { return s == StatusPaid || t.PaidVND > 0 || t.PendingVND > 0 }

// AbilitiesFor tells what the viewer may do with a bill: a landlord corrects and removes an unlocked bill, sends any
// bill that is not paid and settles it; a tenant can do none of that.
func AbilitiesFor(v Viewer, s Status, t Totals) Abilities {
	if !v.AsLandlord {
		return Abilities{}
	}
	open := !Locked(s, t)
	return Abilities{Update: open, Delete: open, SetStatus: true, Send: CanSend(s)}
}

// VisibleToTenant says whether a tenant sees the bill in the app: only a bill that was sent to the account (a draft is
// the landlord's work in progress, and a bill passed on by link is not for the app).
func VisibleToTenant(s Status, d Delivery) bool { return s != StatusDraft && d == DeliveryApp }

// ValidShareToken says whether a string looks like the secret of a share link (so a wrong one is refused without
// touching the database).
func ValidShareToken(s string) bool {
	if len(s) != ShareTokenLength {
		return false
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return true
}

// Reminder is what a bill's due date calls for today.
type Reminder int

// The reminders.
const (
	ReminderNone Reminder = iota
	// ReminderDueSoon: the bill falls due in a few days.
	ReminderDueSoon
	// ReminderOverdue: the bill fell due yesterday.
	ReminderOverdue
)

// ReminderFor tells which reminder a bill with that due date gets today: soonDays before the due date, and the day
// after it. dueDate and today are calendar dates.
func ReminderFor(dueDate, today time.Time, soonDays int) Reminder {
	switch {
	case dueDate.Equal(today.AddDate(0, 0, soonDays)):
		return ReminderDueSoon
	case dueDate.Equal(today.AddDate(0, 0, -1)):
		return ReminderOverdue
	}
	return ReminderNone
}

// IsRefusal says whether err is one of the domain's reasons for refusing a request (the others are failures of the
// system).
func IsRefusal(err error) bool {
	var rule *RuleViolation
	for _, target := range []error{
		ErrNotFound, ErrContractNotRunning, ErrAlreadyPaid, ErrSendFirst, ErrLocked, ErrReviewPendingFirst,
		ErrLinkNeedsSent, ErrLinkInvalid, ErrPeriodBilled,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return errors.As(err, &rule)
}
