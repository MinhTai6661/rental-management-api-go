package identity

import (
	"context"
	"errors"
	"time"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

// ResendVerification sends a new verification link. It answers the same whether or not the address has an
// account (nobody can use it to find out who is registered): an unknown, already verified or disabled address, or
// one that got an email less than the cooldown ago, simply gets nothing. Older links stop working.
func (a *Auth) ResendVerification(ctx context.Context, email, lang string) error {
	email = domain.NormalizeEmail(email)
	user, err := a.users.FindByEmail(ctx, email)
	if errors.Is(err, shared.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if user.EmailVerifiedAt != nil || user.Status != authz.StatusActive {
		return nil
	}
	last, err := a.verifications.LatestCreatedAt(ctx, user.ID)
	var lastSent *time.Time
	switch {
	case err == nil:
		lastSent = &last
	case !errors.Is(err, shared.ErrNotFound):
		return err
	}
	now := a.clock.Now()
	if !domain.ResendAllowed(lastSent, now, a.opts.ResendCooldown) {
		return nil
	}

	raw, hash, err := a.opaque.New()
	if err != nil {
		return err
	}
	err = a.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := a.verifications.InvalidateForUser(ctx, user.ID, now); err != nil {
			return err
		}
		return a.insertVerificationToken(ctx, user.ID, hash, now)
	})
	if err != nil {
		return err
	}
	a.sendVerification(ctx, user.FullName, email, raw, lang)
	return nil
}
