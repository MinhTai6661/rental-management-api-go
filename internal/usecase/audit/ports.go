package audit

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/audit"
)

// The interfaces below are what the audit use cases need from the outside world. The adapters implement them
// structurally; the rules are tested with fakes.

// Repository stores the trail. Append joins the transaction of ctx, so an entry commits or rolls back with the
// action it describes.
type Repository interface {
	// Append writes the entries (each into the table of its tier) in one statement per table. isolated runs it in a
	// savepoint of the transaction of ctx, so a failure leaves the surrounding transaction usable.
	Append(ctx context.Context, entries []domain.Entry, isolated bool) error
	// Search returns the newest entries first, merged from the tiers the query asks for.
	Search(ctx context.Context, q domain.Query) ([]domain.Record, error)
	// ScrubPersonalData blanks the address, device and session of every entry the person made.
	ScrubPersonalData(ctx context.Context, userID uuid.UUID) error
	// CreatePartition makes the partition of table for the month; a month that exists is left alone.
	CreatePartition(ctx context.Context, table string, month time.Time) error
	// Partitions lists the partitions of both tables (the default partitions have a zero Month).
	Partitions(ctx context.Context) ([]domain.Partition, error)
	// DropPartition removes a partition and the rows in it.
	DropPartition(ctx context.Context, name string) error
}

// Objects tells who owns the thing a history is asked about.
type Objects interface {
	// OwnerOf returns the landlord of the room, contract or invoice, or shared.ErrNotFound.
	OwnerOf(ctx context.Context, subject domain.Subject, id uuid.UUID) (uuid.UUID, error)
}

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// ClockFunc turns a function (time.Now) into a Clock.
type ClockFunc func() time.Time

// Now calls the function.
func (f ClockFunc) Now() time.Time { return f() }
