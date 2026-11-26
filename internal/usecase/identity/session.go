package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
)

// openSession records a new password login session acting in role and returns its credentials and the user.
func (a *Auth) openSession(ctx context.Context, userID uuid.UUID, role string, client Client) (LoginResult, error) {
	return a.openSessionVia(ctx, userID, role, loginVia, client)
}

// openSessionVia is openSession for a sign-in made another way: via ("password", "google"...) goes into the audit trail.
func (a *Auth) openSessionVia(ctx context.Context, userID uuid.UUID, role, via string, client Client) (LoginResult, error) {
	now := a.clock.Now()
	sessionID, err := uuid.NewV7()
	if err != nil {
		return LoginResult{}, fmt.Errorf("make session id: %w", err)
	}
	var refresh string
	err = a.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := a.users.SetLastActiveRole(ctx, userID, role, now); err != nil {
			return err
		}
		refresh, err = a.createSession(ctx, userID, sessionID, role, client, now)
		if err != nil {
			return err
		}
		return a.audit.Record(ctx, AuditEvent{
			Action: AuditLogin, ActorID: userID, ActorRole: role, SessionID: sessionID, TargetUserID: &userID,
			Meta: map[string]any{"via": via},
		})
	})
	if err != nil {
		return LoginResult{}, err
	}
	return a.credentials(ctx, userID, sessionID, role, refresh)
}

// createSession inserts the session row and its first refresh token and returns the raw token.
func (a *Auth) createSession(ctx context.Context, userID, sessionID uuid.UUID, role string, client Client, now time.Time) (string, error) {
	if err := a.sessions.Create(ctx, domain.NewSession{
		ID: sessionID, UserID: userID, Role: role, UserAgent: client.userAgent(), IP: client.ipAddr(),
	}, now); err != nil {
		return "", err
	}
	return a.insertRefreshToken(ctx, sessionID, nil, now)
}

// insertRefreshToken stores a new refresh token of the session (its hash only) and returns the raw token.
func (a *Auth) insertRefreshToken(ctx context.Context, sessionID uuid.UUID, parent *uuid.UUID, now time.Time) (string, error) {
	raw, hash, err := a.opaque.New()
	if err != nil {
		return "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("make refresh token id: %w", err)
	}
	err = a.sessions.InsertRefreshToken(ctx, domain.NewRefreshToken{
		ID: id, SessionID: sessionID, Hash: hash, ParentID: parent, ExpiresAt: now.Add(a.opts.RefreshTTL),
	}, now)
	return raw, err
}

// credentials signs the access token and describes the user, for the answer of a login or a role switch.
func (a *Auth) credentials(ctx context.Context, userID, sessionID uuid.UUID, role, refresh string) (LoginResult, error) {
	tokens, err := a.tokensFor(userID, sessionID, role, refresh)
	if err != nil {
		return LoginResult{}, err
	}
	user, err := a.userView(ctx, userID, role)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{User: user, Tokens: tokens}, nil
}

func (a *Auth) tokensFor(userID, sessionID uuid.UUID, role, refresh string) (Tokens, error) {
	access, _, err := a.tokens.Sign(userID, sessionID, role)
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{
		TokenType: "Bearer", AccessToken: access, ExpiresIn: int(a.tokens.TTL().Seconds()),
		RefreshToken: refresh, RefreshExpiresIn: int(a.opts.RefreshTTL.Seconds()),
	}, nil
}
