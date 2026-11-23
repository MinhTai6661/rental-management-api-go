package identity

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/pkg/authz"
)

// ChangePassword sets a new password and closes every other session of the account (the session that changed
// it stays logged in).
func (a *Auth) ChangePassword(ctx context.Context, p authz.Principal, current, next string) error {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return domain.ErrUnauthenticated
	}
	keep, err := uuid.Parse(p.SessionID)
	if err != nil {
		return domain.ErrUnauthenticated
	}
	stored, err := a.users.PasswordHash(ctx, userID)
	if err != nil {
		return err
	}
	// An account without a password (made through another service) has nothing to compare with.
	hash := a.dummyHash
	if stored != nil {
		hash = *stored
	}
	matches, verr := a.hasher.Verify(current, hash)
	if verr != nil {
		a.log.Error("stored password hash is unusable", "user_id", userID, "error", verr)
	}
	if stored == nil || !matches {
		return domain.ErrCurrentPasswordWrong
	}
	if current == next {
		return domain.ErrNewPasswordSame
	}
	newHash, err := a.hasher.Hash(next)
	if err != nil {
		return err
	}

	var closed []uuid.UUID
	err = a.tx.WithinTx(ctx, func(ctx context.Context) error {
		now := a.clock.Now()
		if err := a.users.UpdatePassword(ctx, userID, newHash, now); err != nil {
			return err
		}
		closed, err = a.sessions.RevokeAllOfUser(ctx, userID, &keep, now, "password_change")
		return err
	})
	if err != nil {
		return err
	}
	return a.revocations.RevokeSessions(ctx, uuidStrings(closed), a.opts.RevokedFor)
}
