package contract

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
)

// Delete removes a contract. A landlord removes any contract on their rooms; a tenant can only withdraw their own
// registration that still waits (domain.ErrNotDeletable otherwise). A room whose last running contract goes is free
// again.
func (s *Contracts) Delete(ctx context.Context, v domain.Viewer, id uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		d, err := s.contracts.Get(ctx, v, id)
		if err != nil {
			return notFound(err)
		}
		if !v.AsLandlord && !d.CanTenantDelete(v.UserID) {
			return domain.ErrNotDeletable
		}
		room, err := s.lockRoom(ctx, d.RoomID)
		if err != nil {
			return err
		}
		c, err := s.contracts.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if err := s.contracts.SoftDelete(ctx, id); err != nil {
			return err
		}
		if err := s.syncRoom(ctx, room); err != nil {
			return err
		}
		return s.record(ctx, AuditDeleted, v.UserID, c, map[string]any{"status": string(c.Status)})
	})
}

// Reorder saves the landlord's manual order of their contracts. The given contracts take each other's positions in
// the full list, so contracts that are not part of the request (other pages, other tabs) keep their place. An id that
// is not one of the landlord's contracts is domain.ErrItemsNotFound.
func (s *Contracts) Reorder(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		current, err := s.contracts.Order(ctx, landlordID)
		if err != nil {
			return err
		}
		if !domain.AllListed(current, ids) {
			return domain.ErrItemsNotFound
		}
		moves := domain.Reorder(current, ids)
		if len(moves) == 0 {
			return nil
		}
		return s.contracts.SetOrder(ctx, landlordID, moves)
	})
}
