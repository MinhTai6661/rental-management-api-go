package identity

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/pkg/authz"
)

// Logout ends the caller's session: its access token stops working at once and its refresh token with it. When the
// caller names a refresh token of one of their own sessions, that session is the one closed.
func (a *Auth) Logout(ctx context.Context, p authz.Principal, refresh string) error {
	target, err := uuid.Parse(p.SessionID)
	if err != nil {
		return domain.ErrUnauthenticated
	}
	if refresh != "" {
		if row, err := a.sessions.SessionOfRefreshToken(ctx, a.opaque.Hash(refresh)); err == nil && row.UserID.String() == p.UserID {
			target = row.SessionID
		}
	}
	if err := a.sessions.Revoke(ctx, target, a.clock.Now(), "logout"); err != nil {
		return err
	}
	a.auditLogout(ctx, p, target, map[string]any{"everywhere": false})
	return a.blockTokenAndSessions(ctx, p, []string{target.String()})
}

// LogoutAll ends every session of the caller, on every device.
func (a *Auth) LogoutAll(ctx context.Context, p authz.Principal) error {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return domain.ErrUnauthenticated
	}
	ids, err := a.sessions.RevokeAllOfUser(ctx, userID, nil, a.clock.Now(), "logout_all")
	if err != nil {
		return err
	}
	a.auditLogout(ctx, p, uuid.Nil, map[string]any{"everywhere": true, "sessions": len(ids)})
	return a.blockTokenAndSessions(ctx, p, uuidStrings(ids))
}

// auditLogout records a sign-out; it never fails the request (the entry is routine).
func (a *Auth) auditLogout(ctx context.Context, p authz.Principal, session uuid.UUID, meta map[string]any) {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return
	}
	if err := a.audit.Record(ctx, AuditEvent{
		Action: AuditLogout, ActorID: userID, ActorRole: p.Role, SessionID: session, TargetUserID: &userID, Meta: meta,
	}); err != nil {
		a.log.Warn("could not record a sign-out", "error", err)
	}
}

// blockTokenAndSessions stops the caller's current access token and the access tokens of the closed sessions.
func (a *Auth) blockTokenAndSessions(ctx context.Context, p authz.Principal, sessions []string) error {
	if err := a.revocations.RevokeToken(ctx, p.TokenID, p.TokenExpiresAt.Sub(a.clock.Now())); err != nil {
		return err
	}
	return a.revocations.RevokeSessions(ctx, sessions, a.opts.RevokedFor)
}
