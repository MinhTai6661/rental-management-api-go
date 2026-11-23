package contract

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
)

// change runs one change to a contract the viewer sees, in a transaction: it locks the room, then the contract, lets
// mutate change it (an error from it undoes everything), stores it and brings the room's status in line. The audit
// record is written in the same transaction. Returns the contract as the viewer then sees it.
func (s *core) change(ctx context.Context, v domain.Viewer, id uuid.UUID, mutate func(c *domain.Contract) (action string, meta map[string]any, err error)) (domain.Detail, error) {
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		d, err := s.contracts.Get(ctx, v, id)
		if err != nil {
			return notFound(err)
		}
		room, err := s.lockRoom(ctx, d.RoomID)
		if err != nil {
			return err
		}
		c, err := s.contracts.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		action, meta, err := mutate(&c)
		if err != nil {
			return err
		}
		if err := s.contracts.Save(ctx, c); err != nil {
			return err
		}
		if err := s.syncRoom(ctx, room); err != nil {
			return err
		}
		return s.record(ctx, action, v.UserID, c, meta)
	})
	if err != nil {
		return domain.Detail{}, err
	}
	return s.detail(ctx, v, id)
}

// detail is the contract as the viewer sees it; domain.ErrNotFound when they do not.
func (c *core) detail(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.Detail, error) {
	d, err := c.contracts.Get(ctx, v, id)
	return d, notFound(err)
}

// Terminate is the landlord calling a contract off: a running contract ends today (or at its own earlier end date), a
// waiting registration is rejected, the room is released and both sides are told. reason is shown to the tenant.
func (s *Contracts) Terminate(ctx context.Context, landlordID, id uuid.UUID, reason *string) (domain.Detail, error) {
	v := domain.Viewer{UserID: landlordID, AsLandlord: true}
	d, err := s.change(ctx, v, id, func(c *domain.Contract) (string, map[string]any, error) {
		if !c.CanTerminate() {
			return "", nil, domain.ErrInvalidTransition
		}
		c.Terminate(s.today())
		return AuditTerminated, map[string]any{"reason": reason, "status": string(c.Status)}, nil
	})
	if err != nil {
		return domain.Detail{}, err
	}
	s.notifyEnded(ctx, d, reasonParam(reason))
	return d, nil
}

// RequestCancellation is the tenant of a running contract asking to end it. The contract keeps running until the
// landlord answers.
func (s *Contracts) RequestCancellation(ctx context.Context, tenantID, id uuid.UUID, reason *string) (domain.Detail, error) {
	v := domain.Viewer{UserID: tenantID}
	d, err := s.change(ctx, v, id, func(c *domain.Contract) (string, map[string]any, error) {
		if !c.CanRequestCancellation(tenantID) {
			return "", nil, domain.ErrCancellationNotAllowed
		}
		c.RequestCancellation(s.clock.Now(), reason)
		return AuditCancellationRequested, map[string]any{"reason": reason}, nil
	})
	if err != nil {
		return domain.Detail{}, err
	}
	s.notify(ctx, d, NotifyCancellationRequested, true, nil)
	return d, nil
}

// WithdrawCancellation is the tenant taking their request back: the contract is as if nothing was asked.
func (s *Contracts) WithdrawCancellation(ctx context.Context, tenantID, id uuid.UUID) (domain.Detail, error) {
	return s.change(ctx, domain.Viewer{UserID: tenantID}, id, func(c *domain.Contract) (string, map[string]any, error) {
		if !c.CanWithdrawCancellation(tenantID) {
			return "", nil, domain.ErrCancellationNotAllowed
		}
		c.WithdrawCancellation()
		return AuditCancellationWithdrawn, nil, nil
	})
}

// ApproveCancellation is the landlord agreeing: the contract ends today (or earlier, if its end date already passed)
// and the room is free again.
func (s *Contracts) ApproveCancellation(ctx context.Context, landlordID, id uuid.UUID) (domain.Detail, error) {
	d, err := s.change(ctx, domain.Viewer{UserID: landlordID, AsLandlord: true}, id, func(c *domain.Contract) (string, map[string]any, error) {
		if !c.CanRespondToCancellation() {
			return "", nil, domain.ErrCancellationNotAllowed
		}
		c.ApproveCancellation(s.today())
		return AuditCancellationApproved, nil, nil
	})
	if err != nil {
		return domain.Detail{}, err
	}
	s.notifyEnded(ctx, d, nil)
	return d, nil
}

// DeclineCancellation is the landlord refusing: the contract keeps running and the tenant sees why.
func (s *Contracts) DeclineCancellation(ctx context.Context, landlordID, id uuid.UUID, note *string) (domain.Detail, error) {
	d, err := s.change(ctx, domain.Viewer{UserID: landlordID, AsLandlord: true}, id, func(c *domain.Contract) (string, map[string]any, error) {
		if !c.CanRespondToCancellation() {
			return "", nil, domain.ErrCancellationNotAllowed
		}
		c.DeclineCancellation(note)
		return AuditCancellationDeclined, map[string]any{"note": note}, nil
	})
	if err != nil {
		return domain.Detail{}, err
	}
	s.notify(ctx, d, NotifyCancellationDeclined, false, nil)
	return d, nil
}

func reasonParam(reason *string) map[string]string {
	if reason == nil || *reason == "" {
		return nil
	}
	return map[string]string{"reason": *reason}
}
