package identity

import (
	"context"
	"errors"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

// Refresh exchanges a refresh token for a new pair. Each refresh token works once: using an old one again after
// the grace window means a copy was kept somewhere, and the whole session is closed.
func (a *Auth) Refresh(ctx context.Context, raw string, client Client) (Tokens, error) {
	var (
		out       Tokens
		fail      error
		closedSID []string
	)
	err := a.tx.WithinTx(ctx, func(ctx context.Context) error {
		now := a.clock.Now()
		rt, err := a.sessions.RefreshTokenForUpdate(ctx, a.opaque.Hash(raw))
		if errors.Is(err, shared.ErrNotFound) {
			fail = domain.ErrRefreshInvalid
			return nil
		}
		if err != nil {
			return err
		}

		// closeSession ends the session and answers "invalid". It returns nil so the transaction COMMITS: rolling
		// back here would undo the very revocation that protects the account.
		closeSession := func(reason string) error {
			if err := a.sessions.Revoke(ctx, rt.SessionID, now, reason); err != nil {
				return err
			}
			closedSID = append(closedSID, rt.SessionID.String())
			fail = domain.ErrRefreshInvalid
			return nil
		}

		state := domain.RefreshState{ExpiresAt: rt.ExpiresAt, RotatedAt: rt.RotatedAt, SessionRevokedAt: rt.SessionRevokedAt}
		switch domain.DecideRefresh(state, now, a.opts.ReuseGrace) {
		case domain.RefreshReject:
			fail = domain.ErrRefreshInvalid
			return nil
		case domain.RefreshReuse:
			a.log.Warn("refresh token reused: session closed", "user_id", rt.UserID, "session_id", rt.SessionID)
			return closeSession("reuse_detected")
		}

		user, err := a.users.AuthUser(ctx, rt.UserID)
		if err != nil {
			return err
		}
		if user.Status != authz.StatusActive {
			return closeSession("account_inactive")
		}
		role := authz.ResolveActiveRole(rt.ActiveRole, user.Roles, deref(user.LastActiveRole))
		if role == "" {
			return closeSession("account_inactive")
		}

		if rt.RotatedAt == nil {
			if err := a.sessions.MarkRefreshTokenRotated(ctx, rt.ID, now); err != nil {
				return err
			}
		}
		refresh, err := a.insertRefreshToken(ctx, rt.SessionID, &rt.ID, now)
		if err != nil {
			return err
		}
		if err := a.sessions.Touch(ctx, domain.SessionTouch{
			ID: rt.SessionID, Role: role, UserAgent: client.userAgent(), IP: client.ipAddr(),
		}, now); err != nil {
			return err
		}
		out, err = a.tokensFor(rt.UserID, rt.SessionID, role, refresh)
		return err
	})
	if err != nil {
		return Tokens{}, err
	}
	if len(closedSID) > 0 {
		// The session is already closed in the database; this also stops the access tokens it still has.
		if err := a.revocations.RevokeSessions(ctx, closedSID, a.opts.RevokedFor); err != nil {
			a.log.Error("could not block the access tokens of a closed session", "error", err)
		}
	}
	if fail != nil {
		return Tokens{}, fail
	}
	return out, nil
}
