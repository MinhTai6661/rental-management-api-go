package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

// SwitchRole moves the caller to another role they hold. The old session is closed and a new one opens in the
// new role, so tokens of the old role cannot be used any more. The refresh token proves the caller holds the session.
func (a *Auth) SwitchRole(ctx context.Context, p authz.Principal, role, refresh string, client Client) (LoginResult, error) {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return LoginResult{}, domain.ErrUnauthenticated
	}
	oldSession, err := uuid.Parse(p.SessionID)
	if err != nil {
		return LoginResult{}, domain.ErrUnauthenticated
	}
	row, err := a.sessions.SessionOfRefreshToken(ctx, a.opaque.Hash(refresh))
	if errors.Is(err, shared.ErrNotFound) || (err == nil && (row.UserID != userID || row.SessionID != oldSession || row.RevokedAt != nil)) {
		return LoginResult{}, domain.ErrRefreshInvalid
	}
	if err != nil {
		return LoginResult{}, err
	}

	newSession, err := uuid.NewV7()
	if err != nil {
		return LoginResult{}, fmt.Errorf("make session id: %w", err)
	}
	var newRefresh string
	err = a.tx.WithinTx(ctx, func(ctx context.Context) error {
		now := a.clock.Now()
		held, err := a.users.AuthUser(ctx, userID)
		if err != nil {
			return err
		}
		if !slices.Contains(held.Roles, role) {
			return domain.ErrRoleNotHeld
		}
		if err := a.sessions.Revoke(ctx, oldSession, now, "role_switch"); err != nil {
			return err
		}
		if err := a.users.SetLastActiveRole(ctx, userID, role, now); err != nil {
			return err
		}
		newRefresh, err = a.createSession(ctx, userID, newSession, role, client, now)
		if err != nil {
			return err
		}
		return a.audit.Record(ctx, AuditEvent{
			Action: AuditRoleSwitched, ActorID: userID, ActorRole: role, SessionID: newSession, TargetUserID: &userID,
			Meta: map[string]any{"from": p.Role, "to": role},
		})
	})
	if err != nil {
		return LoginResult{}, err
	}
	if err := a.blockTokenAndSessions(ctx, p, []string{oldSession.String()}); err != nil {
		return LoginResult{}, err
	}
	return a.credentials(ctx, userID, newSession, role, newRefresh)
}
