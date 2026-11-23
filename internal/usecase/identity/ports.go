package identity

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
)

// The interfaces below are what the identity use cases need from the outside world. The adapters implement them
// structurally (they never import this package), so the rules are tested with fakes and no database, Redis or SMTP.
// Repository lookups return shared.ErrNotFound when there is no such row.

// TxManager runs fn in one transaction. Repository calls made with the ctx given to fn join it. A repository
// error (shared.ErrNotFound, *shared.ConstraintError) comes back through fn unchanged.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// UserRepository stores accounts and their roles.
type UserRepository interface {
	Create(ctx context.Context, u domain.NewUser) error
	FindByEmail(ctx context.Context, email string) (domain.User, error)
	Profile(ctx context.Context, id uuid.UUID) (domain.UserProfile, error)
	AuthUser(ctx context.Context, id uuid.UUID) (domain.AuthUser, error)
	PasswordHash(ctx context.Context, id uuid.UUID) (*string, error)
	UpdatePassword(ctx context.Context, id uuid.UUID, hash string, now time.Time) error
	SetLastActiveRole(ctx context.Context, id uuid.UUID, role string, now time.Time) error
	MarkEmailVerified(ctx context.Context, id uuid.UUID, now time.Time) error
	AddRole(ctx context.Context, userID uuid.UUID, role string, now time.Time) error
	SetAcceptsInvitations(ctx context.Context, id uuid.UUID, accepts bool) error
}

// DemoUserRepository is what the demo accounts need from the user store.
type DemoUserRepository interface {
	AddRole(ctx context.Context, userID uuid.UUID, role string, now time.Time) error
	UpsertDemo(ctx context.Context, u domain.DemoUser) (uuid.UUID, error)
	// FirstWardCode returns the first seeded ward, or shared.ErrNotFound when there is none.
	FirstWardCode(ctx context.Context) (int32, error)
}

// SessionRepository stores login sessions and refresh tokens.
type SessionRepository interface {
	Create(ctx context.Context, s domain.NewSession, now time.Time) error
	Revoke(ctx context.Context, id uuid.UUID, now time.Time, reason string) error
	RevokeAllOfUser(ctx context.Context, userID uuid.UUID, except *uuid.UUID, now time.Time, reason string) ([]uuid.UUID, error)
	Touch(ctx context.Context, t domain.SessionTouch, now time.Time) error
	InsertRefreshToken(ctx context.Context, t domain.NewRefreshToken, now time.Time) error
	// RefreshTokenForUpdate locks the token row until the transaction ends.
	RefreshTokenForUpdate(ctx context.Context, hash []byte) (domain.RefreshToken, error)
	MarkRefreshTokenRotated(ctx context.Context, id uuid.UUID, now time.Time) error
	SessionOfRefreshToken(ctx context.Context, hash []byte) (domain.TokenSession, error)
}

// VerificationRepository stores email verification tokens.
type VerificationRepository interface {
	Insert(ctx context.Context, t domain.NewVerificationToken, now time.Time) error
	InvalidateForUser(ctx context.Context, userID uuid.UUID, now time.Time) error
	// ForUpdate locks the token row until the transaction ends.
	ForUpdate(ctx context.Context, hash []byte) (domain.VerificationToken, error)
	MarkUsed(ctx context.Context, id uuid.UUID, now time.Time) error
	LatestCreatedAt(ctx context.Context, userID uuid.UUID) (time.Time, error)
}

// PermissionRepository reads the permissions granted to a role.
type PermissionRepository interface {
	CodesOfRole(ctx context.Context, role string) ([]string, error)
}

// CatalogRepository writes the role and permission catalog.
type CatalogRepository interface {
	SeedCatalog(ctx context.Context, roles []domain.Role, perms []domain.Permission, grants map[string][]string) error
}

// RevocationStore remembers which access tokens and sessions were closed before their expiry. Every method fails
// closed: an unreachable store is an error wrapping shared.ErrUnavailable, never "not revoked".
type RevocationStore interface {
	RevokeToken(ctx context.Context, jti string, ttl time.Duration) error
	RevokeSessions(ctx context.Context, sids []string, ttl time.Duration) error
	IsRevoked(ctx context.Context, jti, sid string) (bool, error)
}

// PasswordHasher hashes passwords for storage and checks them. Verify returns an error only for a stored hash
// that is unusable, never for a wrong password.
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encoded string) (bool, error)
}

// TokenIssuer signs and verifies the access tokens.
type TokenIssuer interface {
	// Sign returns a new access token for the user's session acting in role.
	Sign(userID, sessionID uuid.UUID, role string) (string, domain.AccessClaims, error)
	// Verify returns the claims of a trustworthy token, or an error for any other.
	Verify(raw string) (domain.AccessClaims, error)
	// TTL is the life of an access token.
	TTL() time.Duration
}

// OpaqueTokens makes the random tokens handed to a client (refresh and email verification tokens) and hashes them:
// only the hash is stored, and it is how a token presented later is looked up.
type OpaqueTokens interface {
	New() (raw string, hash []byte, err error)
	Hash(raw string) []byte
}

// VerificationMailer sends the "verify your email address" message. The token only goes into the message: the
// adapter builds the link, and nobody logs it.
type VerificationMailer interface {
	SendVerification(ctx context.Context, to, fullName, token, lang string, expiresIn time.Duration) error
}

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// ClockFunc turns a function (time.Now) into a Clock.
type ClockFunc func() time.Time

// Now calls the function.
func (f ClockFunc) Now() time.Time { return f() }
