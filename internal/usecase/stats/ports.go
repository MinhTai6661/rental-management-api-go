// Package stats serves the statistics: a landlord's figures about their rooms, the platform's figures for the staff and
// the system's figures for a super admin. It only reads; every figure is counted and summed by the database.
package stats

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/stats"
)

// PropertyRepository reads the figures of one landlord's rooms. Months are the YYYY-MM of the range, cut in
// domain.Zone; a month with nothing in it is simply absent from the map.
type PropertyRepository interface {
	// Rooms lists the rooms of the scope that were still there on from (the deleted ones included).
	Rooms(ctx context.Context, scope domain.Scope, from time.Time) ([]domain.RoomFact, error)
	// RentedContracts lists the contracts that began and lasted into [from, to].
	RentedContracts(ctx context.Context, scope domain.Scope, from, to time.Time) ([]domain.RentedContract, error)
	// ContractCounts counts the contracts of the standing rooms; expiring ones end between today and soon.
	ContractCounts(ctx context.Context, scope domain.Scope, today, soon time.Time) (domain.ContractCounts, error)
	// BilledByMonth sums the invoices sent or paid by the month their period starts.
	BilledByMonth(ctx context.Context, scope domain.Scope, from, to time.Time) (map[string]int64, error)
	// CollectedByMonth sums the confirmed payments by the month they were paid (from and to are days).
	CollectedByMonth(ctx context.Context, scope domain.Scope, from, to time.Time) (map[string]int64, error)
	// OutstandingByMonth sums what the sent invoices of each month still lack.
	OutstandingByMonth(ctx context.Context, scope domain.Scope, from, to time.Time) (map[string]int64, error)
	// Owed sums what is outstanding now and the part past its due date at now.
	Owed(ctx context.Context, scope domain.Scope, now time.Time) (domain.Owed, error)
	// BilledByRoom and CollectedByRoom sum the same two figures over [from, to] for every room.
	BilledByRoom(ctx context.Context, scope domain.Scope, from, to time.Time) (map[uuid.UUID]int64, error)
	CollectedByRoom(ctx context.Context, scope domain.Scope, from, to time.Time) (map[uuid.UUID]int64, error)
	// Utilities sums the meter readings of electricity and water by month and kind.
	Utilities(ctx context.Context, scope domain.Scope, from, to time.Time) ([]domain.UtilityUse, error)
}

// PlatformRepository reads the figures of the whole platform.
type PlatformRepository interface {
	// Facts counts the platform; monthStart and previousStart are the first instants of this and the last month, now is
	// the time of the request and reviewedSince how far back the review times are averaged.
	Facts(ctx context.Context, monthStart, previousStart, reviewedSince, now time.Time) (domain.PlatformFacts, error)
	// SignUps counts new accounts and new landlord applications by month between the instants from and before.
	SignUps(ctx context.Context, from, before time.Time) (users, applications map[string]int, err error)
	// System reads the figures of the system, counting the audit trail since the instant.
	System(ctx context.Context, auditSince time.Time) (domain.SystemFacts, error)
}

// Cache keeps the answers of a landlord's figures for a while. A cache that fails is only a slower answer, so the
// use cases never fail on it.
type Cache interface {
	// Get decodes the answer of key into dst and says whether there was one.
	Get(ctx context.Context, key string, dst any) (bool, error)
	// Set keeps v under key for ttl.
	Set(ctx context.Context, key string, v any, ttl time.Duration) error
}

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// ClockFunc turns a function (time.Now) into a Clock.
type ClockFunc func() time.Time

// Now calls the function.
func (f ClockFunc) Now() time.Time { return f() }
