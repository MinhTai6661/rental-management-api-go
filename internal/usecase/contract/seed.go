package contract

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/shared"
)

// RoomFinder finds a landlord's room by name (the demo seed uses the rooms the property seed made).
type RoomFinder interface {
	FindByName(ctx context.Context, landlordID uuid.UUID, name string) (domain.RoomFacts, error)
}

// Seeder gives the demo tenant some contracts on the demo landlord's rooms.
type Seeder struct {
	*core
	uc     *Contracts
	inv    *Invitations
	finder RoomFinder
}

// NewSeeder returns a seeder on the contract use cases (so the rules apply to the demo data too).
func NewSeeder(d Deps, o Options, finder RoomFinder) *Seeder {
	return &Seeder{core: newCore(d, o), uc: NewContracts(d, o), inv: NewInvitations(d, o), finder: finder}
}

// SeedDemo gives the demo tenant a running contract on P101, a registration waiting for an answer on P102 and an ended
// contract on P103. It is safe to repeat: it does nothing once the tenant has contracts with the landlord, and nothing
// when the rooms are missing (the property demo data was not loaded).
func (s *Seeder) SeedDemo(ctx context.Context, landlordID, tenantID uuid.UUID) error {
	have, err := s.contracts.CountOfTenant(ctx, landlordID, tenantID)
	if err != nil || have > 0 {
		return err
	}
	rooms := make([]domain.RoomFacts, 3)
	for i, name := range []string{"P101", "P102", "P103"} {
		if rooms[i], err = s.finder.FindByName(ctx, landlordID, name); err != nil {
			if errors.Is(err, shared.ErrNotFound) {
				return nil
			}
			return err
		}
	}
	today := s.today()
	active, err := s.uc.Register(ctx, tenantID, RegisterInput{RoomID: rooms[0].ID, StartsOn: today, OccupantCount: 2})
	if err != nil {
		return err
	}
	approved := domain.StatusActive
	if _, err := s.uc.Update(ctx, landlordID, active.ID, UpdateInput{Status: &approved}); err != nil {
		return err
	}
	if _, err := s.uc.Register(ctx, tenantID, RegisterInput{RoomID: rooms[1].ID, StartsOn: today.AddDate(0, 0, 7)}); err != nil {
		return err
	}
	return s.seedEnded(ctx, landlordID, tenantID, rooms[2], today)
}

// seedEnded stores a contract of the past (a registration cannot start before today, so it is written directly).
func (s *Seeder) seedEnded(ctx context.Context, landlordID, tenantID uuid.UUID, room domain.RoomFacts, today time.Time) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		order, err := s.contracts.TopOrder(ctx, landlordID)
		if err != nil {
			return err
		}
		starts, ends, now := today.AddDate(0, -6, 0), today.AddDate(0, -1, 0), s.clock.Now()
		_, err = s.contracts.Insert(ctx, domain.Contract{
			RoomID: room.ID, LandlordID: landlordID, TenantID: &tenantID, Status: domain.StatusEnded, PriceVND: room.PriceVND,
			OccupantCount: 1, StartsOn: starts, EndsOn: &ends, ConfirmedAt: &now, DisplayOrder: order,
		})
		return err
	})
}

// SeedDemoInvitation offers the demo room P105 to the demo tenant by id, starting in two weeks, so the tenant's inbox
// shows one offer. It goes through the use case (the rules apply and, with no mailer wired, nobody is emailed). It does
// nothing when the landlord already has an invitation, or when the room is missing; it returns whether it made one.
func (s *Seeder) SeedDemoInvitation(ctx context.Context, landlordID, tenantID uuid.UUID) (int, error) {
	_, total, err := s.invitations.List(ctx, landlordID, nil, 1, 0)
	if err != nil || total > 0 {
		return 0, err
	}
	room, err := s.finder.FindByName(ctx, landlordID, "P105")
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	if _, err := s.inv.Create(ctx, landlordID, CreateInvitationInput{
		RoomID: room.ID, StartsOn: s.today().AddDate(0, 0, 14), ContractStatus: domain.StatusActive, RecipientID: &tenantID,
	}); err != nil {
		return 0, err
	}
	return 1, nil
}
