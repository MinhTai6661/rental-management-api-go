package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/identity"
)

// SessionRepository stores login sessions and their refresh tokens (hashes only).
type SessionRepository struct{ pool *pgxpool.Pool }

// NewSessionRepository returns a repository on the pool.
func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

// Create opens a session.
func (r *SessionRepository) Create(ctx context.Context, s domain.NewSession, now time.Time) error {
	return translate(queries(ctx, r.pool).CreateSession(ctx, sqlcgen.CreateSessionParams{
		ID: s.ID, UserID: s.UserID, ActiveRole: s.Role, UserAgent: s.UserAgent, IpAddress: s.IP, Now: now,
	}))
}

// Revoke closes one session (nothing happens when it is already closed).
func (r *SessionRepository) Revoke(ctx context.Context, id uuid.UUID, now time.Time, reason string) error {
	return translate(queries(ctx, r.pool).RevokeSession(ctx, sqlcgen.RevokeSessionParams{ID: id, Now: now, Reason: &reason}))
}

// RevokeAllOfUser closes every open session of the user, except the one given (nil closes them all), and returns
// the ids it closed.
func (r *SessionRepository) RevokeAllOfUser(ctx context.Context, userID uuid.UUID, except *uuid.UUID, now time.Time, reason string) ([]uuid.UUID, error) {
	ids, err := queries(ctx, r.pool).RevokeUserSessions(ctx, sqlcgen.RevokeUserSessionsParams{
		UserID: userID, ExceptID: except, Now: now, Reason: &reason,
	})
	return ids, translate(err)
}

// Touch records a use of the session: when, in which role and from where.
func (r *SessionRepository) Touch(ctx context.Context, t domain.SessionTouch, now time.Time) error {
	return translate(queries(ctx, r.pool).TouchSession(ctx, sqlcgen.TouchSessionParams{
		ID: t.ID, Now: now, ActiveRole: t.Role, UserAgent: t.UserAgent, IpAddress: t.IP,
	}))
}

// InsertRefreshToken stores a refresh token.
func (r *SessionRepository) InsertRefreshToken(ctx context.Context, t domain.NewRefreshToken, now time.Time) error {
	return translate(queries(ctx, r.pool).InsertRefreshToken(ctx, sqlcgen.InsertRefreshTokenParams{
		ID: t.ID, SessionID: t.SessionID, TokenHash: t.Hash, ParentID: t.ParentID, ExpiresAt: t.ExpiresAt, Now: now,
	}))
}

// RefreshTokenForUpdate returns the token with its session state and locks its row until the transaction ends, so
// two requests with the same token are handled one after the other. shared.ErrNotFound when unknown.
func (r *SessionRepository) RefreshTokenForUpdate(ctx context.Context, hash []byte) (domain.RefreshToken, error) {
	row, err := queries(ctx, r.pool).GetRefreshTokenForUpdate(ctx, hash)
	if err != nil {
		return domain.RefreshToken{}, translate(err)
	}
	return domain.RefreshToken{
		ID: row.ID, SessionID: row.SessionID, ExpiresAt: row.ExpiresAt, RotatedAt: row.RotatedAt, UserID: row.UserID,
		ActiveRole: row.ActiveRole, SessionRevokedAt: row.SessionRevokedAt,
	}, nil
}

// MarkRefreshTokenRotated records that the token was exchanged for a new one.
func (r *SessionRepository) MarkRefreshTokenRotated(ctx context.Context, id uuid.UUID, now time.Time) error {
	return translate(queries(ctx, r.pool).MarkRefreshTokenRotated(ctx, sqlcgen.MarkRefreshTokenRotatedParams{ID: id, Now: now}))
}

// SessionOfRefreshToken returns the session and user a refresh token belongs to, or shared.ErrNotFound.
func (r *SessionRepository) SessionOfRefreshToken(ctx context.Context, hash []byte) (domain.TokenSession, error) {
	row, err := queries(ctx, r.pool).GetRefreshTokenSession(ctx, hash)
	if err != nil {
		return domain.TokenSession{}, translate(err)
	}
	return domain.TokenSession{SessionID: row.SessionID, UserID: row.UserID, RevokedAt: row.RevokedAt}, nil
}
