package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/model"
)

// The interfaces below are what the auth service needs from the outside world. The repository package implements
// them; the service never imports it, so the rules can be tested with fakes and no database.

// TxManager runs fn in one transaction. Repository calls made with the ctx given to fn join it. A repository
// error (shared.ErrNotFound, *shared.ConstraintError) comes back through fn unchanged.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// UserRepository stores accounts and their roles. Lookups return shared.ErrNotFound when there is no such row.
type UserRepository interface {
	Create(ctx context.Context, u model.NewUser) error
	FindByEmail(ctx context.Context, email string) (model.User, error)
	Profile(ctx context.Context, id uuid.UUID) (model.UserProfile, error)
	AuthUser(ctx context.Context, id uuid.UUID) (model.AuthUser, error)
	PasswordHash(ctx context.Context, id uuid.UUID) (*string, error)
	UpdatePassword(ctx context.Context, id uuid.UUID, hash string, now time.Time) error
	SetLastActiveRole(ctx context.Context, id uuid.UUID, role string, now time.Time) error
	MarkEmailVerified(ctx context.Context, id uuid.UUID, now time.Time) error
	AddRole(ctx context.Context, userID uuid.UUID, role string, now time.Time) error
}

// SessionRepository stores login sessions and refresh tokens.
type SessionRepository interface {
	Create(ctx context.Context, s model.NewSession, now time.Time) error
	Revoke(ctx context.Context, id uuid.UUID, now time.Time, reason string) error
	RevokeAllOfUser(ctx context.Context, userID uuid.UUID, except *uuid.UUID, now time.Time, reason string) ([]uuid.UUID, error)
	Touch(ctx context.Context, t model.SessionTouch, now time.Time) error
	InsertRefreshToken(ctx context.Context, t model.NewRefreshToken, now time.Time) error
	RefreshTokenForUpdate(ctx context.Context, hash []byte) (model.RefreshToken, error)
	MarkRefreshTokenRotated(ctx context.Context, id uuid.UUID, now time.Time) error
	SessionOfRefreshToken(ctx context.Context, hash []byte) (model.TokenSession, error)
}

// VerificationRepository stores email verification tokens.
type VerificationRepository interface {
	Insert(ctx context.Context, t model.NewVerificationToken, now time.Time) error
	InvalidateForUser(ctx context.Context, userID uuid.UUID, now time.Time) error
	ForUpdate(ctx context.Context, hash []byte) (model.VerificationToken, error)
	MarkUsed(ctx context.Context, id uuid.UUID, now time.Time) error
	LatestCreatedAt(ctx context.Context, userID uuid.UUID) (time.Time, error)
}

// PermissionRepository reads the permissions granted to a role.
type PermissionRepository interface {
	CodesOfRole(ctx context.Context, role string) ([]string, error)
}

// RevocationRepository remembers which access tokens and sessions were closed before their expiry. Every
// method fails closed: an unreachable store is an error, never "not revoked".
type RevocationRepository interface {
	RevokeToken(ctx context.Context, jti string, ttl time.Duration) error
	RevokeSessions(ctx context.Context, sids []string, ttl time.Duration) error
	IsRevoked(ctx context.Context, jti, sid string) (bool, error)
}
