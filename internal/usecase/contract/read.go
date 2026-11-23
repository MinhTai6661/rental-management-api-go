package contract

import (
	"context"
	"strings"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
)

// Contracts is the use cases of a landlord's contracts (and of a tenant's view of their own).
type Contracts struct{ *core }

// NewContracts builds the contract use cases.
func NewContracts(d Deps, o Options) *Contracts { return &Contracts{newCore(d, o)} }

// List returns a page of the contracts the viewer sees and the total.
func (s *Contracts) List(ctx context.Context, v domain.Viewer, f ListFilter, limit, offset int) ([]domain.Detail, int, error) {
	return s.contracts.Search(ctx, v, queryOf(f, limit, offset))
}

// queryOf turns a request filter into the repository's query.
func queryOf(f ListFilter, limit, offset int) domain.Query {
	q := domain.Query{
		RoomID: f.RoomID, BuildingID: f.BuildingID, NoBuilding: f.NoBuilding, CancellationStatus: f.CancellationStatus,
		HasUnpaidInvoice: f.HasUnpaidInvoice, Sort: f.Sort, Limit: limit, Offset: offset,
	}
	if f.Status != nil {
		q.Statuses = []domain.Status{*f.Status}
	}
	if search := strings.ToLower(strings.TrimSpace(f.Search)); search != "" {
		like := domain.EscapeLike(search)
		q.SearchLike = &like
	}
	return q
}

// Get returns one contract the viewer sees; domain.ErrNotFound for anybody else's.
func (s *Contracts) Get(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.Detail, error) {
	return s.detail(ctx, v, id)
}

// Rentals is the renter's side of contracts ("my rooms"): their own contracts grouped by tab.
type Rentals struct{ *core }

// NewRentals builds the renter's use cases.
func NewRentals(d Deps, o Options) *Rentals { return &Rentals{newCore(d, o)} }

// RentalPage is a page of the renter's contracts with how many they have in each tab, and how many of the running
// ones are in each state of money.
type RentalPage struct {
	Items         []domain.Detail
	Total         int
	Counts        map[string]int
	PaymentCounts domain.PaymentCounts
}

// List returns the tenant's contracts, latest start first, of a group (active, pending, history; "" for all), with
// how many there are per group whichever one is listed.
func (r *Rentals) List(ctx context.Context, tenantID uuid.UUID, group string, limit, offset int) (RentalPage, error) {
	return r.ListByPayment(ctx, tenantID, group, nil, limit, offset)
}

// ListByPayment is List narrowed to the contracts in a state of money: due (a sent bill has money left to pay), review (a
// payment waits for the landlord's check) or settled (neither). Each contract carries its billing summary, and the page
// says how many running contracts are in each state whichever one is listed.
func (r *Rentals) ListByPayment(ctx context.Context, tenantID uuid.UUID, group string, payment *domain.PaymentState, limit, offset int) (RentalPage, error) {
	items, total, err := r.contracts.Search(ctx, domain.Viewer{UserID: tenantID}, domain.Query{
		Statuses: GroupStatuses(group), PaymentState: payment, Sort: "-starts_on", Limit: limit, Offset: offset,
	})
	if err != nil {
		return RentalPage{}, err
	}
	paymentCounts, err := r.contracts.PaymentCounts(ctx, tenantID, GroupStatuses(GroupActive))
	if err != nil {
		return RentalPage{}, err
	}
	byStatus, err := r.contracts.StatusCounts(ctx, tenantID)
	if err != nil {
		return RentalPage{}, err
	}
	counts := map[string]int{}
	for _, g := range []string{GroupActive, GroupPending, GroupHistory} {
		for _, st := range GroupStatuses(g) {
			counts[g] += byStatus[st]
		}
	}
	return RentalPage{Items: items, Total: total, Counts: counts, PaymentCounts: paymentCounts}, nil
}

// Get returns one of the tenant's own contracts with the room's costs and pictures; domain.ErrNotFound for anybody
// else's.
func (r *Rentals) Get(ctx context.Context, tenantID, id uuid.UUID) (domain.RentalDetail, error) {
	d, err := r.contracts.Get(ctx, domain.Viewer{UserID: tenantID}, id)
	if err != nil {
		return domain.RentalDetail{}, notFound(err)
	}
	charges, pictures, err := r.contracts.RentalExtras(ctx, d.RoomID)
	if err != nil {
		return domain.RentalDetail{}, err
	}
	return domain.RentalDetail{Detail: d, Charges: charges, Pictures: pictures}, nil
}
