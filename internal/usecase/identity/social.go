package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

// The audit actions of linking providers.
const (
	AuditOAuthLinked   = "auth.oauth_linked"
	AuditOAuthUnlinked = "auth.oauth_unlinked"
)

// maxFullNameRunes is the longest name the users table accepts.
const maxFullNameRunes = 255

// fallbackFullName names an account whose provider gave no name.
const fallbackFullName = "User"

// SocialOptions are the settings of social login.
type SocialOptions struct {
	// StateTTL is how long a round trip to a provider may take; CodeTTL the life of the one-time exchange code.
	StateTTL time.Duration
	CodeTTL  time.Duration
	// DefaultReturnPath is where the web app lands after a login that asked for nothing else.
	DefaultReturnPath string
}

// SocialDeps are the collaborators of social login. Transactions, clock, audit, tokens and sessions are the ones of Auth.
type SocialDeps struct {
	Providers *SocialRegistry
	States    SocialStateStore
	Codes     LoginCodeStore
	Accounts  OAuthAccountRepository
	Users     SocialUserStore
}

// Social is the set of use cases of signing in with Google, Zalo..., and of linking them to an account. The
// login itself (session, refresh token, role) is Auth's: a social sign-in ends in Auth.openSessionVia.
type Social struct {
	auth *Auth
	d    SocialDeps
	opts SocialOptions
}

// NewSocial builds the use cases on top of auth.
func NewSocial(auth *Auth, d SocialDeps, o SocialOptions) *Social {
	return &Social{auth: auth, d: d, opts: o}
}

// ProviderView is a provider people can use.
type ProviderView struct {
	Code string
	// RequiresMatchingEmail: it only links to an account whose email it gives too.
	RequiresMatchingEmail bool
}

// Providers lists the providers that are switched on.
func (s *Social) Providers() []ProviderView {
	var out []ProviderView
	for _, p := range s.d.Providers.Enabled() {
		out = append(out, ProviderView{Code: p.Code(), RequiresMatchingEmail: p.VouchesForEmail()})
	}
	return out
}

// BeginLogin returns where to send the browser to sign in with the provider; returnPath is where the web app lands
// afterwards (anything but a plain relative path is replaced by the default).
func (s *Social) BeginLogin(ctx context.Context, provider, returnPath string) (string, error) {
	return s.begin(ctx, domain.SocialState{
		Provider: provider, Intent: domain.IntentLogin, ReturnPath: domain.SafeReturnPath(returnPath, s.opts.DefaultReturnPath),
	})
}

// BeginLink returns where to send the browser to link the provider to the caller's account. A plain navigation cannot
// carry the access token, so the caller is remembered in the state instead.
func (s *Social) BeginLink(ctx context.Context, p authz.Principal, provider string) (string, error) {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return "", domain.ErrUnauthenticated
	}
	return s.begin(ctx, domain.SocialState{Provider: provider, Intent: domain.IntentLink, UserID: &userID, ActorRole: p.Role})
}

func (s *Social) begin(ctx context.Context, state domain.SocialState) (string, error) {
	provider, ok := s.d.Providers.Find(state.Provider)
	if !ok {
		return "", domain.ErrProviderUnavailable
	}
	verifier, _, err := s.auth.opaque.New()
	if err != nil {
		return "", err
	}
	state.CodeVerifier = verifier
	key, err := s.d.States.Put(ctx, state, s.opts.StateTTL)
	if err != nil {
		return "", err
	}
	return provider.AuthorizationURL(key, verifier), nil
}

// Callback is what the provider sent the browser back with.
type Callback struct {
	Provider string
	// State is the key of the round trip; Code what the provider sent (empty when the person cancelled).
	State string
	Code  string
}

// Outcome is what the web app must be told after a callback.
type Outcome struct {
	Intent     domain.SocialIntent
	Provider   string
	ReturnPath string
	// LoginCode is the one-time code to trade for tokens; set only for a login that succeeded.
	LoginCode string
	// Err is nil on success, else the refusal in domain words (domain.ErrSocialFailed for anything not the
	// person's doing).
	Err error
}

// Complete handles the callback of a provider: it signs the person in (and up), or links the provider to the
// account that asked. It never returns an error: everything that goes wrong is part of the Outcome, because the
// answer is a redirect. A login hands back a one-time code, never tokens.
func (s *Social) Complete(ctx context.Context, cb Callback) Outcome {
	out := Outcome{Intent: domain.IntentLogin, Provider: cb.Provider, ReturnPath: s.opts.DefaultReturnPath}
	state, err := s.d.States.Take(ctx, cb.State)
	if err != nil {
		if !errors.Is(err, shared.ErrNotFound) {
			s.auth.log.Error("read social state", "error", err)
		}
		out.Err = domain.ErrSocialFailed
		return out
	}
	out.Intent, out.ReturnPath = state.Intent, state.ReturnPath

	provider, ok := s.d.Providers.Find(cb.Provider)
	if !ok || state.Provider != cb.Provider || cb.Code == "" {
		out.Err = domain.ErrSocialFailed
		return out
	}
	profile, err := provider.Profile(ctx, cb.Code, state.CodeVerifier)
	if err != nil {
		s.auth.log.Warn("social profile failed", "provider", cb.Provider, "error", err)
		out.Err = domain.ErrSocialFailed
		return out
	}
	profile.Provider = cb.Provider

	if state.Intent == domain.IntentLink {
		out.Err = s.refusalOf(s.link(ctx, state, profile))
		return out
	}
	userID, err := s.login(ctx, profile)
	if err != nil {
		out.Err = s.refusalOf(err)
		return out
	}
	code, err := s.d.Codes.Put(ctx, domain.SocialLoginGrant{UserID: userID, Via: cb.Provider}, s.opts.CodeTTL)
	if err != nil {
		s.auth.log.Error("store social login code", "error", err)
		out.Err = domain.ErrSocialFailed
		return out
	}
	out.LoginCode = code
	return out
}

// refusalOf keeps the refusals the person should hear about and turns anything else (a database error...) into
// domain.ErrSocialFailed after logging it.
func (s *Social) refusalOf(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{
		domain.ErrAccountInactive, domain.ErrLinkElsewhere, domain.ErrLinkProviderTaken, domain.ErrLinkStaff,
		domain.ErrLinkEmailMismatch, domain.ErrLinkNoEmail, domain.ErrLinkUnlinked, domain.ErrSocialFailed,
	} {
		if errors.Is(err, known) {
			return known
		}
	}
	s.auth.log.Error("social sign-in failed", "error", err)
	return domain.ErrSocialFailed
}

// login finds or makes the account a provider account opens. Order: the account linked to it; else (when the
// provider vouches for the email) the account with that email, which then gets the provider linked; else a new
// tenant account. A provider the person unlinked does not sign in any more, not even through the email.
func (s *Social) login(ctx context.Context, profile domain.SocialProfile) (uuid.UUID, error) {
	known, err := s.d.Accounts.FindByProvider(ctx, profile.Provider, profile.ID)
	switch {
	case err == nil:
		if !known.Linked() {
			return uuid.Nil, domain.ErrLinkUnlinked
		}
		return s.ensureUsable(ctx, known.UserID, profile)
	case !errors.Is(err, shared.ErrNotFound):
		return uuid.Nil, err
	}

	if email := vouchedEmail(profile); email != nil {
		user, err := s.d.Users.FindByEmail(ctx, *email)
		switch {
		case err == nil:
			return s.ensureUsable(ctx, user.ID, profile)
		case !errors.Is(err, shared.ErrNotFound):
			return uuid.Nil, err
		}
	}
	return s.signUp(ctx, profile)
}

// ensureUsable checks the account may sign in with social login (active, not staff), makes sure the provider is
// linked to it, and returns its id.
func (s *Social) ensureUsable(ctx context.Context, userID uuid.UUID, profile domain.SocialProfile) (uuid.UUID, error) {
	user, err := s.d.Users.AuthUser(ctx, userID)
	if errors.Is(err, shared.ErrNotFound) {
		return uuid.Nil, domain.ErrAccountInactive // a deleted account is neither brought back nor recreated
	}
	if err != nil {
		return uuid.Nil, err
	}
	if user.Status != authz.StatusActive {
		return uuid.Nil, domain.ErrAccountInactive
	}
	if domain.HasStaffRole(user.Roles) {
		return uuid.Nil, domain.ErrLinkStaff
	}

	// An account has one account of each provider: another one, linked or not, does not open it.
	rows, err := s.d.Accounts.ListOfUser(ctx, userID)
	if err != nil {
		return uuid.Nil, err
	}
	existing := findProvider(rows, profile.Provider)
	if existing != nil && existing.ProviderUserID != profile.ID {
		if existing.Linked() {
			return uuid.Nil, domain.ErrLinkProviderTaken
		}
		return uuid.Nil, domain.ErrLinkUnlinked
	}
	now := s.auth.clock.Now()
	err = s.auth.tx.WithinTx(ctx, func(ctx context.Context) error {
		if existing == nil {
			if err := s.d.Accounts.Create(ctx, newLinkedRow(userID, profile, now), now); err != nil {
				return err
			}
		}
		return s.confirmEmailIfProven(ctx, userID, profile, now)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return userID, nil
}

// confirmEmailIfProven handles the provider proving an address the account holds but never verified.
func (s *Social) confirmEmailIfProven(ctx context.Context, userID uuid.UUID, profile domain.SocialProfile, now time.Time) error {
	email := vouchedEmail(profile)
	if email == nil {
		return nil
	}
	user, err := s.d.Users.FindByEmail(ctx, *email)
	if errors.Is(err, shared.ErrNotFound) || (err == nil && user.ID != userID) {
		return nil // the email is not this account's: nothing was proven for it
	}
	if err != nil {
		return err
	}
	if user.EmailVerifiedAt != nil {
		return nil
	}
	return s.d.Users.ConfirmEmailFromProvider(ctx, userID, now)
}

// signUp creates a tenant account for a person the application does not know yet.
func (s *Social) signUp(ctx context.Context, profile domain.SocialProfile) (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("make user id: %w", err)
	}
	now := s.auth.clock.Now()
	role := authz.RoleTenant
	err = s.auth.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.d.Users.CreateSocial(ctx, domain.NewSocialUser{
			ID: id, Email: vouchedEmail(profile), FullName: displayName(profile.Name), LastActiveRole: role, CreatedAt: now,
		}); err != nil {
			return err
		}
		if err := assignRoles(ctx, s.d.Users, id, []string{role}, now); err != nil {
			return err
		}
		if err := s.d.Accounts.Create(ctx, newLinkedRow(id, profile, now), now); err != nil {
			return err
		}
		return s.auth.audit.Record(ctx, AuditEvent{
			Action: AuditRegister, ActorID: id, ActorRole: role, TargetUserID: &id, Meta: map[string]any{"via": profile.Provider},
		})
	})
	switch {
	case shared.IsUnique(err):
		// The email belongs to an account that is deleted (or one made a moment ago): never take it over.
		return uuid.Nil, domain.ErrAccountInactive
	case err != nil:
		return uuid.Nil, err
	}
	return id, nil
}

// link adds the provider account of profile to the account that started the round trip.
func (s *Social) link(ctx context.Context, state domain.SocialState, profile domain.SocialProfile) error {
	if state.UserID == nil {
		return domain.ErrSocialFailed
	}
	userID := *state.UserID
	user, err := s.d.Users.AuthUser(ctx, userID)
	if errors.Is(err, shared.ErrNotFound) {
		return domain.ErrSocialFailed
	}
	if err != nil {
		return err
	}
	if domain.HasStaffRole(user.Roles) {
		return domain.ErrLinkStaff
	}
	account, err := s.d.Users.Profile(ctx, userID)
	if err != nil {
		return err
	}
	if err := domain.LinkEmailRefusal(profile.EmailVerified, account.Email, profile.Email); err != nil {
		return err
	}
	// That provider account may already open another account here.
	owner, err := s.d.Accounts.FindByProvider(ctx, profile.Provider, profile.ID)
	switch {
	case err == nil && owner.UserID != userID:
		return domain.ErrLinkElsewhere
	case err != nil && !errors.Is(err, shared.ErrNotFound):
		return err
	}

	now := s.auth.clock.Now()
	return s.auth.tx.WithinTx(ctx, func(ctx context.Context) error {
		rows, err := s.d.Accounts.ListOfUserForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		decision := domain.DecideLink(findProvider(rows, profile.Provider), profile.ID)
		switch decision {
		case domain.LinkAlready:
			return nil
		case domain.LinkTaken:
			return domain.ErrLinkProviderTaken
		case domain.LinkRelink:
			err = s.d.Accounts.Relink(ctx, userID, profile.Provider, now)
		case domain.LinkReplace:
			err = s.d.Accounts.Replace(ctx, userID, profile.Provider, profile.ID, now)
		default:
			err = s.d.Accounts.Create(ctx, newLinkedRow(userID, profile, now), now)
		}
		if shared.IsUnique(err) {
			return domain.ErrLinkElsewhere // linked to another account in the meantime
		}
		if err != nil {
			return err
		}
		return s.auth.audit.Record(ctx, AuditEvent{
			Action: AuditOAuthLinked, ActorID: userID, ActorRole: state.ActorRole, TargetUserID: &userID,
			Meta: map[string]any{"provider": profile.Provider, "again": decision != domain.LinkCreate},
		})
	})
}

// Exchange trades a one-time code for the credentials of a normal login: a session, a refresh token and the access
// token, acting in the role used last. The code works once.
func (s *Social) Exchange(ctx context.Context, code string, client Client) (LoginResult, error) {
	grant, err := s.d.Codes.Take(ctx, code)
	if errors.Is(err, shared.ErrNotFound) {
		return LoginResult{}, domain.ErrSocialCodeInvalid
	}
	if err != nil {
		return LoginResult{}, err
	}
	user, err := s.d.Users.AuthUser(ctx, grant.UserID)
	if errors.Is(err, shared.ErrNotFound) {
		return LoginResult{}, domain.ErrAccountInactive
	}
	if err != nil {
		return LoginResult{}, err
	}
	if user.Status != authz.StatusActive {
		return LoginResult{}, domain.ErrAccountInactive
	}
	role := domain.InitialRole(user.Roles, deref(user.LastActiveRole))
	if role == "" {
		return LoginResult{}, domain.ErrForbidden
	}
	return s.auth.openSessionVia(ctx, grant.UserID, role, grant.Via, client)
}

// LinkedProvider is the state of one enabled provider for an account.
type LinkedProvider struct {
	Provider string
	Linked   bool
	LinkedAt *time.Time
	// CanUnlink: removing it would still leave a way to sign in.
	CanUnlink bool
	// Unlinked: the person unlinked it, so it cannot sign in until linked again.
	Unlinked              bool
	RequiresMatchingEmail bool
}

// LinkedProviders is what the account page shows.
type LinkedProviders struct {
	HasPassword bool
	Providers   []LinkedProvider
}

// Accounts describes every enabled provider for the caller.
func (s *Social) Accounts(ctx context.Context, p authz.Principal) (LinkedProviders, error) {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return LinkedProviders{}, domain.ErrUnauthenticated
	}
	rows, err := s.d.Accounts.ListOfUser(ctx, userID)
	if err != nil {
		return LinkedProviders{}, err
	}
	methods, err := s.methodsOf(ctx, userID, rows)
	if err != nil {
		return LinkedProviders{}, err
	}
	out := LinkedProviders{HasPassword: methods.HasPassword}
	for _, provider := range s.d.Providers.Enabled() {
		row := findProvider(rows, provider.Code())
		view := LinkedProvider{
			Provider: provider.Code(), CanUnlink: methods.CanUnlink(provider.Code()), RequiresMatchingEmail: provider.VouchesForEmail(),
		}
		if row != nil {
			view.Linked, view.Unlinked = row.Linked(), !row.Linked()
			view.LinkedAt = &row.LinkedAt
		}
		out.Providers = append(out.Providers, view)
	}
	return out, nil
}

// Unlink removes a provider from the caller's account (it stops signing in, also through its email). The last way to
// sign in cannot be removed.
func (s *Social) Unlink(ctx context.Context, p authz.Principal, provider string) error {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return domain.ErrUnauthenticated
	}
	if !domain.KnownProvider(provider) {
		return domain.ErrProviderUnavailable
	}
	now := s.auth.clock.Now()
	return s.auth.tx.WithinTx(ctx, func(ctx context.Context) error {
		rows, err := s.d.Accounts.ListOfUserForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		methods, err := s.methodsOf(ctx, userID, rows)
		if err != nil {
			return err
		}
		if row := findProvider(rows, provider); row == nil || !row.Linked() {
			return domain.ErrNotLinked
		}
		if !methods.CanUnlink(provider) {
			return domain.ErrLastSignInMethod
		}
		if err := s.d.Accounts.Unlink(ctx, userID, provider, now); err != nil {
			return err
		}
		return s.auth.audit.Record(ctx, AuditEvent{
			Action: AuditOAuthUnlinked, ActorID: userID, ActorRole: p.Role, SessionID: sessionOf(p), TargetUserID: &userID,
			Meta: map[string]any{"provider": provider},
		})
	})
}

func (s *Social) methodsOf(ctx context.Context, userID uuid.UUID, rows []domain.OAuthAccount) (domain.SignInMethods, error) {
	hash, err := s.d.Users.PasswordHash(ctx, userID)
	if err != nil {
		return domain.SignInMethods{}, err
	}
	methods := domain.SignInMethods{HasPassword: hash != nil && *hash != ""}
	for _, row := range rows {
		if row.Linked() {
			methods.LinkedProviders = append(methods.LinkedProviders, row.Provider)
		}
	}
	return methods, nil
}

// sessionOf is the login session of the caller, uuid.Nil when unknown.
func sessionOf(p authz.Principal) uuid.UUID {
	id, err := uuid.Parse(p.SessionID)
	if err != nil {
		return uuid.Nil
	}
	return id
}

func findProvider(rows []domain.OAuthAccount, provider string) *domain.OAuthAccount {
	for i := range rows {
		if rows[i].Provider == provider {
			return &rows[i]
		}
	}
	return nil
}

func newLinkedRow(userID uuid.UUID, profile domain.SocialProfile, now time.Time) domain.OAuthAccount {
	return domain.OAuthAccount{UserID: userID, Provider: profile.Provider, ProviderUserID: profile.ID, LinkedAt: now}
}

// vouchedEmail is the profile's email in stored form when the provider vouches for it, else nil.
func vouchedEmail(profile domain.SocialProfile) *string {
	if profile.Email == nil || !profile.EmailVerified {
		return nil
	}
	email := domain.NormalizeEmail(*profile.Email)
	if email == "" {
		return nil
	}
	return &email
}

// displayName is the provider's name cut to what the database accepts.
func displayName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return fallbackFullName
	}
	if utf8.RuneCountInString(name) > maxFullNameRunes {
		name = string([]rune(name)[:maxFullNameRunes])
	}
	return name
}
