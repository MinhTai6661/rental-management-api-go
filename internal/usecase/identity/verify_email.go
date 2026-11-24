package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
)

// VerifyEmail marks the address of the token as verified. A token works once and expires; one that is unknown,
// used or expired gets the same answer.
func (a *Auth) VerifyEmail(ctx context.Context, raw string) error {
	verified := false
	err := a.tx.WithinTx(ctx, func(ctx context.Context) error {
		now := a.clock.Now()
		row, err := a.verifications.ForUpdate(ctx, a.opaque.Hash(raw))
		if errors.Is(err, shared.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !domain.VerificationUsable(row.ExpiresAt, row.UsedAt, now) {
			return nil
		}
		if err := a.verifications.MarkUsed(ctx, row.ID, now); err != nil {
			return err
		}
		// Any other link sent to this account is now useless.
		if err := a.verifications.InvalidateForUser(ctx, row.UserID, now); err != nil {
			return err
		}
		verified = true
		if err := a.users.MarkEmailVerified(ctx, row.UserID, now); err != nil {
			return err
		}
		return a.audit.Record(ctx, AuditEvent{Action: AuditEmailConfirmed, ActorID: row.UserID, TargetUserID: &row.UserID})
	})
	if err != nil {
		return err
	}
	if !verified {
		return domain.ErrVerificationInvalid
	}
	return nil
}

// insertVerificationToken stores a verification token (its hash only) valid for the configured time.
func (a *Auth) insertVerificationToken(ctx context.Context, userID uuid.UUID, hash []byte, now time.Time) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("make verification token id: %w", err)
	}
	return a.verifications.Insert(ctx, domain.NewVerificationToken{
		ID: id, UserID: userID, Hash: hash, ExpiresAt: now.Add(a.opts.VerificationTTL),
	}, now)
}
