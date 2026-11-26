package identity

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Social login: signing in with another service (Google, Zalo) and linking such a service to an account. The rules
// below are pure; the services themselves sit behind use case ports.

// The services people can sign in with. The value is stored and sent over the API.
const (
	ProviderGoogle = "google"
	ProviderZalo   = "zalo"
)

// KnownProviders lists the services the application has code for.
func KnownProviders() []string { return []string{ProviderGoogle, ProviderZalo} }

// KnownProvider reports whether code names a service the application has code for (whether it is configured is
// another question, answered by the registry of the use cases).
func KnownProvider(code string) bool { return code == ProviderGoogle || code == ProviderZalo }

// SocialIntent is what a round trip to a provider is for.
type SocialIntent string

const (
	// IntentLogin signs a person in (or up).
	IntentLogin SocialIntent = "login"
	// IntentLink adds the provider to the account of a signed-in person.
	IntentLink SocialIntent = "link"
)

// SocialProfile is who signed in at a provider, in the same shape whatever the provider.
type SocialProfile struct {
	Provider string
	// ID is the person's id at the provider (Zalo: scoped to this app).
	ID   string
	Name string
	// Email is nil when the provider gives none (Zalo).
	Email *string
	// EmailVerified is true when the provider vouches that the email belongs to the person.
	EmailVerified bool
}

// SocialState is what the server remembers about a round trip while the person is at the provider. It travels as
// plain data: the key it is stored under is the OAuth `state`.
type SocialState struct {
	Provider string
	Intent   SocialIntent
	// UserID and ActorRole are set for IntentLink: who is linking, and the role they acted in.
	UserID    *uuid.UUID
	ActorRole string
	// ReturnPath is the relative path of the web app to land on after a login.
	ReturnPath string
	// CodeVerifier is the PKCE secret sent when the code is traded.
	CodeVerifier string
}

// SocialLoginGrant is what a one-time exchange code stands for: the account a provider opened, and which provider.
type SocialLoginGrant struct {
	UserID uuid.UUID
	Via    string
}

// OAuthAccount is the account of a provider that opens (or opened) an account here.
type OAuthAccount struct {
	UserID         uuid.UUID
	Provider       string
	ProviderUserID string
	LinkedAt       time.Time
	// UnlinkedAt is set once the person unlinked it; the row stays so the provider cannot sign in again by itself.
	UnlinkedAt *time.Time
}

// Linked reports whether the provider account opens the account now.
func (a OAuthAccount) Linked() bool { return a.UnlinkedAt == nil }

// The refusals of social login. The HTTP adapter turns each into the `error` / `link_error` value the web reads.
var (
	// ErrSocialFailed: the round trip failed (unknown or reused state, provider error, cancelled by the person).
	ErrSocialFailed = errors.New("social sign-in failed")
	// ErrProviderUnavailable: the provider is unknown or has no credentials configured.
	ErrProviderUnavailable = errors.New("provider is not available")
	// ErrSocialCodeInvalid: the exchange code is unknown, used or expired.
	ErrSocialCodeInvalid = errors.New("social login code is not valid")
	// ErrLinkElsewhere: that account of the provider already opens another account here.
	ErrLinkElsewhere = errors.New("provider account is linked to another account")
	// ErrLinkProviderTaken: the account already has another account of this provider linked.
	ErrLinkProviderTaken = errors.New("account has another account of this provider linked")
	// ErrLinkStaff: staff accounts sign in with their password only.
	ErrLinkStaff = errors.New("staff accounts cannot use social login")
	// ErrLinkEmailMismatch: the provider's email is not the account's email.
	ErrLinkEmailMismatch = errors.New("provider email differs from the account email")
	// ErrLinkNoEmail: the account has no email, so a provider that gives one cannot be linked.
	ErrLinkNoEmail = errors.New("account has no email to match")
	// ErrLinkUnlinked: the person unlinked this provider, so it cannot sign in until linked again while signed in.
	ErrLinkUnlinked = errors.New("provider was unlinked")
	// ErrLastSignInMethod: removing the provider would leave no way to sign in.
	ErrLastSignInMethod = errors.New("cannot remove the last way to sign in")
	// ErrNotLinked: the provider is not linked to the account.
	ErrNotLinked = errors.New("provider is not linked")
)

// LinkDecision is what to do when a provider account is linked to an account.
type LinkDecision int

const (
	// LinkCreate adds the provider account.
	LinkCreate LinkDecision = iota
	// LinkAlready: it is linked already, nothing to do.
	LinkAlready
	// LinkRelink switches the provider account that was unlinked back on.
	LinkRelink
	// LinkReplace: the account unlinked another account of this provider and links this one now.
	LinkReplace
	// LinkTaken: the account has another account of this provider linked.
	LinkTaken
)

// DecideLink applies the rule "one account of each provider per account, and it can be linked again after unlinking".
// existing is the provider account the account has on record (linked or not), nil for none.
func DecideLink(existing *OAuthAccount, providerUserID string) LinkDecision {
	switch {
	case existing == nil:
		return LinkCreate
	case existing.ProviderUserID == providerUserID && existing.Linked():
		return LinkAlready
	case existing.ProviderUserID == providerUserID:
		return LinkRelink
	case existing.Linked():
		return LinkTaken
	default:
		return LinkReplace
	}
}

// LinkEmailRefusal says why a provider account may not be linked because of its email, or nil when the email is fine.
// Linking never changes the email of the account: a provider that vouches for an email must give the account's own
// (compared without case), and an account without an email cannot link such a provider. A provider that gives no
// verified email (Zalo) is never refused here.
func LinkEmailRefusal(vouches bool, accountEmail, providerEmail *string) error {
	if !vouches {
		return nil
	}
	if accountEmail == nil || strings.TrimSpace(*accountEmail) == "" {
		return ErrLinkNoEmail
	}
	if providerEmail == nil || !strings.EqualFold(strings.TrimSpace(*accountEmail), strings.TrimSpace(*providerEmail)) {
		return ErrLinkEmailMismatch
	}
	return nil
}

// SignInMethods are the ways an account can sign in with: its password and each linked provider.
type SignInMethods struct {
	HasPassword     bool
	LinkedProviders []string
}

// CanUnlink reports whether the provider can be removed: it must be linked, and another way in must remain.
func (m SignInMethods) CanUnlink(provider string) bool {
	linked := false
	for _, p := range m.LinkedProviders {
		if p == provider {
			linked = true
		}
	}
	return linked && (m.HasPassword || len(m.LinkedProviders) > 1)
}

// PKCEChallenge is the code challenge (method S256, RFC 7636) of a verifier: the base64url SHA-256 of it.
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

var pkceVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9\-._~]{43,128}$`)

// ValidPKCEVerifier reports whether the verifier is 43 to 128 characters of the unreserved set.
func ValidPKCEVerifier(verifier string) bool { return pkceVerifierPattern.MatchString(verifier) }

var returnPathPattern = regexp.MustCompile(`^[A-Za-z0-9/_-]+$`)

// SafeReturnPath keeps only a plain relative path (letters, digits, "/", "_", "-"), so the callback can never send
// the browser to another site; anything else becomes fallback.
func SafeReturnPath(path, fallback string) string {
	trimmed := strings.TrimLeft(path, "/")
	if returnPathPattern.MatchString(trimmed) && !strings.Contains(trimmed, "//") {
		return trimmed
	}
	return fallback
}
