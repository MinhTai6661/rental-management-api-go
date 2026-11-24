package audit

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrRangeBackwards is returned when a filter's end date is before its start date.
var ErrRangeBackwards = errors.New("the end of the range is before its start")

// ErrUnknownAction is returned for an action name that is not in the catalog.
var ErrUnknownAction = errors.New("unknown audit action")

// ErrUnknownTier is returned for a tier name that is neither important nor routine.
var ErrUnknownTier = errors.New("unknown audit tier")

// Zone is the time zone the people of this system read dates in (Vietnam has no daylight saving time).
var Zone = time.FixedZone("ICT", 7*60*60)

// Filter narrows the trail.
type Filter struct {
	ActorID      *uuid.UUID
	ActorRole    string
	Action       *Action
	TargetUserID *uuid.UUID
	RoomID       *uuid.UUID
	ContractID   *uuid.UUID
	InvoiceID    *uuid.UUID
	// From and To are the first and the last day of the range (inclusive), as dates in Zone.
	From *time.Time
	To   *time.Time
	Tier Tier
}

// Bounds turns the days of the range into instants: from the start of the first day to the start of the day after the
// last. A missing end is nil.
func (f Filter) Bounds() (from, before *time.Time, err error) {
	if f.From != nil && f.To != nil && f.To.Before(*f.From) {
		return nil, nil, ErrRangeBackwards
	}
	if f.From != nil {
		d := startOfDay(*f.From)
		from = &d
	}
	if f.To != nil {
		d := startOfDay(*f.To).AddDate(0, 0, 1)
		before = &d
	}
	return from, before, nil
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, Zone)
}

// Query asks the trail for a page.
type Query struct {
	Filter Filter
	// From and Before are the bounds of the filter's days as instants (Before is exclusive).
	From, Before *time.Time
	// Involving keeps the entries the person made or that are about them.
	Involving *uuid.UUID
	// ExcludeStaff drops the entries made by or about admins and super admins; ExcludeSettings the setting changes.
	ExcludeStaff, ExcludeSettings bool
	// Tiers that are read.
	Important, Routine bool
	// BranchLimit is how many rows each tier may contribute (page*per_page+1); Limit and Offset cut the merged list.
	BranchLimit, Limit, Offset int
}

// Partition is one slice of a table of the trail.
type Partition struct {
	Table string
	Name  string
	// Month is the first day of the month the partition holds; zero for the default partition.
	Month time.Time
}

// Subject is the kind of object a history is about.
type Subject string

// The subjects of a history.
const (
	SubjectRoom     Subject = "room"
	SubjectContract Subject = "contract"
	SubjectInvoice  Subject = "invoice"
)
