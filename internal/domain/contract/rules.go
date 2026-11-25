package contract

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DateLayout is how a calendar date is written.
const DateLayout = "2006-01-02"

// Date builds a calendar date.
func Date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// ParseDate reads a calendar date written as 2026-11-23.
func ParseDate(s string) (time.Time, error) { return time.Parse(DateLayout, s) }

// Today is the calendar date of now in the business time zone, as a date.
func Today(now time.Time, loc *time.Location) time.Time {
	y, m, d := now.In(loc).Date()
	return Date(y, m, d)
}

// CanMove says whether a contract may go from one status to another: a registration is answered, a running contract
// ends, and a contract closed by mistake can be reopened.
func CanMove(from, to Status) bool {
	switch from {
	case StatusPending:
		return to == StatusActive || to == StatusRejected
	case StatusActive:
		return to == StatusEnded
	case StatusEnded:
		return to == StatusActive
	case StatusRejected:
		return to == StatusPending || to == StatusActive
	}
	return false
}

// ClosingDate is the end date a running contract gets when it is closed: today, unless its own end date has already
// passed.
func ClosingDate(currentEnd *time.Time, today time.Time) time.Time {
	if currentEnd != nil && currentEnd.Before(today) {
		return *currentEnd
	}
	return today
}

// AddPeriod is the date a period of amount units starting on start runs to. Months follow the calendar: the same day
// of a later month (31 Jan + 1 month = 28 Feb, never spilling into March); days are added as they are.
func AddPeriod(start time.Time, amount int, unit PeriodUnit) time.Time {
	start = Date(start.Year(), start.Month(), start.Day())
	if unit == UnitDay {
		return start.AddDate(0, 0, amount)
	}
	months := start.Year()*12 + int(start.Month()) - 1 + amount
	year, month := months/12, time.Month(months%12+1)
	day := min(start.Day(), daysIn(year, month))
	return Date(year, month, day)
}

func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// EarliestEndIfTooSoon returns the earliest allowed end date when a contract with an end date lasts less than the
// room's minimum rental period, and nil when it is fine. A contract without an end date always qualifies.
func EarliestEndIfTooSoon(start time.Time, end *time.Time, amount int32, unit PeriodUnit) *time.Time {
	if end == nil {
		return nil
	}
	earliest := AddPeriod(start, int(amount), unit)
	if Date(end.Year(), end.Month(), end.Day()).Before(earliest) {
		return &earliest
	}
	return nil
}

// CheckMinimumPeriod returns a *BelowMinimumPeriodError when the contract ends too soon for the room, else nil.
func CheckMinimumPeriod(start time.Time, end *time.Time, amount int32, unit PeriodUnit) error {
	if earliest := EarliestEndIfTooSoon(start, end, amount, unit); earliest != nil {
		return &BelowMinimumPeriodError{Amount: amount, Unit: unit, Earliest: *earliest}
	}
	return nil
}

// CheckDates refuses an end date that does not come after the start date.
func CheckDates(start time.Time, end *time.Time) error {
	if end != nil && !end.After(start) {
		return ErrEndNotAfterStart
	}
	return nil
}

// RoomVacant says whether a room can be registered for: live, available, and no tenant holds a running contract.
func RoomVacant(r RoomFacts, o Occupancy) bool {
	return r.Live && r.Status == RoomAvailable && o.WithTenant == 0
}

// SyncedRoomStatus is the status a room should have after its contracts changed: rented while one runs, free again
// when the last one is gone; a room under repair stays under repair. changed is false when nothing has to be done.
func SyncedRoomStatus(current string, o Occupancy) (status string, changed bool) {
	switch {
	case o.Running > 0 && current != RoomRented:
		return RoomRented, true
	case o.Running == 0 && current == RoomRented:
		return RoomAvailable, true
	}
	return current, false
}

// LimitReached says whether a count has reached a plan limit; nil means unlimited.
func LimitReached(limit *int, count int) bool { return limit != nil && count >= *limit }

// The transitions below change a contract's state in memory; the use case stores the result.

// CanRequestCancellation: the tenant of a running contract may ask to end it, once, until the landlord has answered.
func (c Contract) CanRequestCancellation(userID uuid.UUID) bool {
	return c.HeldBy(userID) && c.Status == StatusActive && !c.cancellationIs(CancellationRequested)
}

// CanWithdrawCancellation: the tenant takes their own open request back.
func (c Contract) CanWithdrawCancellation(userID uuid.UUID) bool {
	return c.HeldBy(userID) && c.cancellationIs(CancellationRequested)
}

// CanRespondToCancellation: the landlord answers an open request.
func (c Contract) CanRespondToCancellation() bool { return c.cancellationIs(CancellationRequested) }

// CanTerminate: the landlord calls off a running contract or a registration still waiting.
func (c Contract) CanTerminate() bool { return c.Status == StatusActive || c.Status == StatusPending }

// CanTenantDelete: a tenant can only withdraw their own registration that still waits.
func (c Contract) CanTenantDelete(userID uuid.UUID) bool {
	return c.HeldBy(userID) && c.Status == StatusPending
}

func (c Contract) cancellationIs(s CancellationStatus) bool {
	return c.Cancellation.Status != nil && *c.Cancellation.Status == s
}

// RequestCancellation records the tenant's request.
func (c *Contract) RequestCancellation(now time.Time, reason *string) {
	requested := CancellationRequested
	c.Cancellation = Cancellation{Status: &requested, RequestedAt: &now, Reason: reason}
}

// WithdrawCancellation clears the request: the contract is as if nothing was asked.
func (c *Contract) WithdrawCancellation() { c.Cancellation = Cancellation{} }

// DeclineCancellation keeps the contract running and tells the tenant why.
func (c *Contract) DeclineCancellation(note *string) {
	declined := CancellationDeclined
	c.Cancellation.Status = &declined
	c.Cancellation.ResponseNote = note
}

// Close ends a running contract today (or at its own earlier end date) and answers an open request to end it as
// approved.
func (c *Contract) Close(today time.Time) {
	closing := ClosingDate(c.EndsOn, today)
	c.Status, c.EndsOn = StatusEnded, &closing
	if c.cancellationIs(CancellationRequested) {
		approved := CancellationApproved
		c.Cancellation.Status = &approved
	}
}

// ApproveCancellation ends the contract the tenant asked to end.
func (c *Contract) ApproveCancellation(today time.Time) {
	c.Close(today)
	approved := CancellationApproved
	c.Cancellation.Status = &approved
}

// Terminate calls the contract off: a running one ends, a waiting registration is turned down.
func (c *Contract) Terminate(today time.Time) {
	if c.Status == StatusActive {
		c.Close(today)
		return
	}
	c.Status = StatusRejected
}

// MoveTo changes the status along the life cycle (the caller checked CanMove). Running again clears the request that
// ended the contract; ending a running contract closes it today unless its own end date came sooner or one was set
// in the same change (endGiven), and answers an open request to end it as approved.
func (c *Contract) MoveTo(to Status, today time.Time, endGiven bool) {
	from := c.Status
	c.Status = to
	if to == StatusActive && from == StatusEnded {
		c.Cancellation = Cancellation{}
	}
	if to == StatusEnded && from == StatusActive {
		if !endGiven {
			closing := ClosingDate(c.EndsOn, today)
			c.EndsOn = &closing
		}
		if c.cancellationIs(CancellationRequested) {
			approved := CancellationApproved
			c.Cancellation.Status = &approved
		}
	}
}

// Reorder applies a drag and drop: the moved ids swap into the slots the moved ids held, in the order the request
// gives them; the other rows stay where they are. Positions count 1..n, and only rows whose stored order differs
// from their new position are returned. current is in the list's stored order.
func Reorder(current []OrderedID, moved []uuid.UUID) []Position {
	inCurrent := make(map[uuid.UUID]bool, len(current))
	for _, row := range current {
		inCurrent[row.ID] = true
	}
	var queue []uuid.UUID
	isMoved := map[uuid.UUID]bool{}
	for _, id := range moved {
		if inCurrent[id] && !isMoved[id] {
			isMoved[id] = true
			queue = append(queue, id)
		}
	}
	var out []Position
	for i, row := range current {
		id := row.ID
		if isMoved[id] {
			id, queue = queue[0], queue[1:]
		}
		if want := i + 1; id != row.ID || row.Order != want {
			out = append(out, Position{ID: id, Order: want})
		}
	}
	return out
}

// AllListed says whether every id is among the current rows.
func AllListed(current []OrderedID, ids []uuid.UUID) bool {
	known := make(map[uuid.UUID]bool, len(current))
	for _, row := range current {
		known[row.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			return false
		}
	}
	return true
}

// EscapeLike escapes the characters that mean something inside a LIKE pattern (backslash is the escape character).
func EscapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// IsRefusal says whether err is one of the domain's reasons for refusing a request (the others are failures of the
// system).
func IsRefusal(err error) bool {
	var (
		below *BelowMinimumPeriodError
		limit *ContractLimitError
		reg   *RegistrationLimitError
	)
	for _, target := range []error{
		ErrNotFound, ErrRoomNotFound, ErrItemsNotFound, ErrInvalidTransition, ErrRoomOccupied, ErrRoomNotVacant,
		ErrAlreadyRegistered, ErrOwnRoom, ErrCancellationNotAllowed, ErrNotDeletable, ErrStartInPast,
		ErrEndNotAfterStart, ErrOccupantsInvalid, ErrRoomNotListed, ErrInvitationInvalid, ErrInvitationNotPending,
		ErrInvitationLinkUnavailable, ErrInvitationOwnOffer,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return errors.As(err, &below) || errors.As(err, &limit) || errors.As(err, &reg)
}

// ClosedBlank is a blank contract that was just ended.
type ClosedBlank struct {
	ContractID uuid.UUID
	RoomID     uuid.UUID
	LandlordID uuid.UUID
}

// Abilities is what a viewer may do with a contract (the web hides the buttons that would be refused).
type Abilities struct {
	// Review: answer a registration that waits (approve or reject it).
	Review bool
	// Update: edit the contract.
	Update bool
	// Delete: remove the contract.
	Delete bool
}

// AbilitiesFor tells what the viewer may do with the contract: a landlord edits and removes any contract of their
// rooms and answers the registrations that wait; a tenant can only withdraw their own registration that waits.
func (c Contract) AbilitiesFor(v Viewer) Abilities {
	if v.AsLandlord {
		return Abilities{Review: c.Status == StatusPending, Update: true, Delete: true}
	}
	if v.AsManager {
		return Abilities{} // a manager only reads contracts
	}
	return Abilities{Delete: c.CanTenantDelete(v.UserID)}
}
