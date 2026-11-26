package identity

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
)

// The ports of social login. Like the others they are implemented structurally by adapters; the providers speak
// OAuth over HTTP in adapter/oauth, the state and the exchange codes live in Redis, the links in Postgres.

// SocialProvider is one service people can sign in with (Google, Zalo). Adding one means writing an adapter and
// registering it in cmd; the use cases only know this interface.
type SocialProvider interface {
	// Code is the name of the provider in the API and the database (domain.ProviderGoogle...).
	Code() string
	// VouchesForEmail tells whether the provider gives an email it has verified (Google does, Zalo gives none).
	VouchesForEmail() bool
	// AuthorizationURL is where to send the person. state is our one-time key (it must come back unchanged) and
	// codeVerifier the PKCE secret (providers that do not use PKCE ignore it).
	AuthorizationURL(state, codeVerifier string) string
	// Profile trades the code the provider sent back for the profile of the person. Any failure is an error.
	Profile(ctx context.Context, code, codeVerifier string) (domain.SocialProfile, error)
}

// SocialStateStore keeps the state of a round trip while the person is at the provider.
type SocialStateStore interface {
	// Put stores the state for ttl and returns the key to use as the OAuth `state`.
	Put(ctx context.Context, state domain.SocialState, ttl time.Duration) (key string, err error)
	// Take returns the state and deletes it, so a key works once; shared.ErrNotFound when it is unknown, used or expired.
	Take(ctx context.Context, key string) (domain.SocialState, error)
}

// LoginCodeStore keeps the one-time codes that replace tokens in the redirect URL.
type LoginCodeStore interface {
	// Put stores the grant for ttl and returns the code.
	Put(ctx context.Context, grant domain.SocialLoginGrant, ttl time.Duration) (code string, err error)
	// Take returns the grant and deletes it; shared.ErrNotFound when the code is unknown, used or expired.
	Take(ctx context.Context, code string) (domain.SocialLoginGrant, error)
}

// OAuthAccountRepository stores which provider accounts open which accounts here.
type OAuthAccountRepository interface {
	// FindByProvider returns the row of the provider account (linked or not), or shared.ErrNotFound.
	FindByProvider(ctx context.Context, provider, providerUserID string) (domain.OAuthAccount, error)
	// ListOfUser returns the rows of the account, linked or not.
	ListOfUser(ctx context.Context, userID uuid.UUID) ([]domain.OAuthAccount, error)
	// ListOfUserForUpdate is ListOfUser that locks the rows until the transaction ends.
	ListOfUserForUpdate(ctx context.Context, userID uuid.UUID) ([]domain.OAuthAccount, error)
	// Create adds a linked row. A provider account already on record is a unique *shared.ConstraintError.
	Create(ctx context.Context, a domain.OAuthAccount, now time.Time) error
	// Relink switches an unlinked row back on.
	Relink(ctx context.Context, userID uuid.UUID, provider string, now time.Time) error
	// Replace points an unlinked row at another provider account and links it.
	Replace(ctx context.Context, userID uuid.UUID, provider, providerUserID string, now time.Time) error
	// Unlink marks the row unlinked; the row stays.
	Unlink(ctx context.Context, userID uuid.UUID, provider string, now time.Time) error
}

// SocialUserStore is what social login needs from the accounts. UserRepository implements it.
type SocialUserStore interface {
	FindByEmail(ctx context.Context, email string) (domain.User, error)
	AuthUser(ctx context.Context, id uuid.UUID) (domain.AuthUser, error)
	Profile(ctx context.Context, id uuid.UUID) (domain.UserProfile, error)
	PasswordHash(ctx context.Context, id uuid.UUID) (*string, error)
	AddRole(ctx context.Context, userID uuid.UUID, role string, now time.Time) error
	// CreateSocial inserts an account that has no password (and maybe no email); a taken email is a unique
	// *shared.ConstraintError.
	CreateSocial(ctx context.Context, u domain.NewSocialUser) error
	// ConfirmEmailFromProvider marks the email verified and removes the password: the provider proved the address,
	// so a password somebody else set on it while it was unverified no longer counts.
	ConfirmEmailFromProvider(ctx context.Context, id uuid.UUID, now time.Time) error
}
