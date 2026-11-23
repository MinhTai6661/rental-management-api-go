package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"rental-management/internal/modules/identity/sqlcgen"
	"rental-management/pkg/apperr"
	"rental-management/pkg/authz"
	"rental-management/pkg/db"
	"rental-management/pkg/mail"
)

// emailSendTimeout bounds one delivery to the SMTP server; sending happens after the request is answered.
const emailSendTimeout = 15 * time.Second

// Options are the settings of the service.
type Options struct {
	// TermsVersion is the version of the terms of use people must accept.
	TermsVersion string
	// WebURL is the origin of the web app; the link of the verification email points there.
	WebURL string
	// RefreshTTL is the life of a refresh token (a session lasts as long as it keeps being refreshed).
	RefreshTTL time.Duration
	// ReuseGrace is how long after its rotation a refresh token may still be presented (two tabs refreshing at once).
	ReuseGrace time.Duration
	// VerificationTTL is how long an email verification link works; ResendCooldown the wait between two emails.
	VerificationTTL time.Duration
	ResendCooldown  time.Duration
	// RevokedFor is how long a revoked session stays blocked: the life of an access token plus its clock leeway.
	RevokedFor time.Duration
	// PermissionCacheTTL is how long the permissions of a role are kept in memory.
	PermissionCacheTTL time.Duration
}

// Deps are the collaborators of the service.
type Deps struct {
	Store       *Store
	Tokens      *TokenIssuer
	Revocations *Revocations
	Mailer      mail.Mailer
	Renderer    *mail.Renderer
	Log         *slog.Logger
	// Now is the clock; time.Now in production.
	Now func() time.Time
	// Dispatch runs background work (sending an email) so the response does not wait for it, and so the time a
	// request takes does not tell whether an address has an account. Nil runs it in a goroutine; tests run it inline.
	Dispatch func(work func())
}

// Service is the use cases of accounts and login sessions.
type Service struct {
	store       *Store
	tokens      *TokenIssuer
	revocations *Revocations
	mailer      mail.Mailer
	renderer    *mail.Renderer
	log         *slog.Logger
	now         func() time.Time
	dispatch    func(work func())
	perms       *permissionCache
	opts        Options
	// dummyHash is verified when an email has no account, so a wrong email costs as much time as a wrong password.
	dummyHash  string
	background sync.WaitGroup
}

// NewService builds the service.
func NewService(d Deps, o Options) (*Service, error) {
	dummy, err := HashPassword("not-a-real-password")
	if err != nil {
		return nil, err
	}
	s := &Service{
		store: d.Store, tokens: d.Tokens, revocations: d.Revocations, mailer: d.Mailer, renderer: d.Renderer,
		log: d.Log, now: d.Now, dispatch: d.Dispatch, opts: o, dummyHash: dummy,
		perms: newPermissionCache(d.Store.q, o.PermissionCacheTTL, d.Now),
	}
	if s.dispatch == nil {
		s.dispatch = s.runInBackground
	}
	return s, nil
}

// runInBackground runs work in a goroutine that Wait can wait for.
func (s *Service) runInBackground(work func()) {
	s.background.Add(1)
	go func() {
		defer s.background.Done()
		work()
	}()
}

// Wait blocks until the background work (emails being sent) is done; call it when the server shuts down.
func (s *Service) Wait() { s.background.Wait() }

// Client is where a request came from; it is kept with the login session.
type Client struct {
	IP        string
	UserAgent string
}

// maxUserAgentBytes caps the user agent stored with a session.
const maxUserAgentBytes = 255

func (c Client) ipAddr() *netip.Addr {
	addr, err := netip.ParseAddr(c.IP)
	if err != nil {
		return nil
	}
	return &addr
}

func (c Client) userAgent() *string {
	if c.UserAgent == "" {
		return nil
	}
	ua := c.UserAgent
	if len(ua) > maxUserAgentBytes {
		ua = ua[:maxUserAgentBytes]
	}
	return &ua
}

// Tokens are the credentials a client gets after logging in or refreshing.
type Tokens struct {
	TokenType        string `json:"token_type"`
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
}

// LoginResult is the answer of a login or a role switch.
type LoginResult struct {
	User   UserView `json:"user"`
	Tokens Tokens   `json:"tokens"`
}

// RegisterInput is what a person gives to create an account.
type RegisterInput struct {
	FullName      string
	Email         string
	Password      string
	PhoneNumber   *string
	AddressDetail *string
	WardCode      *int32
	// Lang is the language of the verification email ("vi" or "en").
	Lang string
}

// Register creates a tenant account that cannot log in until its email is verified, and sends the verification
// email. Staff and landlord roles are never granted here.
func (s *Service) Register(ctx context.Context, in RegisterInput) (UserView, error) {
	email := normalizeEmail(in.Email)
	hash, err := HashPassword(in.Password)
	if err != nil {
		return UserView{}, err
	}
	verifyToken, verifyHash, err := NewOpaqueToken()
	if err != nil {
		return UserView{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return UserView{}, fmt.Errorf("make user id: %w", err)
	}
	now := s.now()
	role := authz.RoleTenant

	err = s.store.InTx(ctx, func(q *sqlcgen.Queries) error {
		if _, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
			ID: id, Email: &email, PasswordHash: &hash, FullName: in.FullName, PhoneNumber: in.PhoneNumber,
			WardCode: in.WardCode, AddressDetail: in.AddressDetail, LastActiveRole: &role,
			TermsVersion: &s.opts.TermsVersion, TermsAcceptedAt: &now, Now: now,
		}); err != nil {
			return err
		}
		if err := assignRoles(ctx, q, id, []string{role}, now); err != nil {
			return err
		}
		return s.insertVerificationToken(ctx, q, id, verifyHash, now)
	})
	switch {
	case db.IsUniqueViolation(err):
		return UserView{}, apperr.Invalid(apperr.Field{Name: "email", Rule: apperr.RuleUnique})
	case db.IsForeignKeyViolation(err):
		return UserView{}, apperr.Invalid(apperr.Field{Name: "ward_code", Rule: apperr.RuleExists})
	case err != nil:
		return UserView{}, err
	}

	s.sendVerification(ctx, in.FullName, email, verifyToken, in.Lang)
	return s.userView(ctx, id, role)
}

// Login checks the credentials and opens a session. A wrong email and a wrong password look the same, and cost the
// same time. Only after the password is right does the caller learn that the account is disabled or unverified.
func (s *Service) Login(ctx context.Context, email, password string, client Client) (LoginResult, error) {
	user, err := s.store.q.GetUserByEmail(ctx, normalizeEmail(email))
	hash := s.dummyHash
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return LoginResult{}, err
	}
	if found && user.PasswordHash != nil {
		hash = *user.PasswordHash
	}
	matches, verr := VerifyPassword(password, hash)
	if verr != nil {
		s.log.Error("stored password hash is unusable", "user_id", user.ID, "error", verr)
	}
	if !found || user.PasswordHash == nil || !matches {
		return LoginResult{}, apperr.InvalidCredentials()
	}
	if user.Status != authz.StatusActive {
		return LoginResult{}, apperr.AccountInactive()
	}
	if user.EmailVerifiedAt == nil {
		return LoginResult{}, apperr.EmailUnverified()
	}

	auth, err := s.store.q.GetAuthUser(ctx, user.ID)
	if err != nil {
		return LoginResult{}, err
	}
	role := initialRole(auth.Roles, deref(auth.LastActiveRole))
	if role == "" {
		return LoginResult{}, apperr.Forbidden()
	}
	return s.openSession(ctx, user.ID, role, client)
}

// openSession records a new login session acting in role and returns its credentials and the user.
func (s *Service) openSession(ctx context.Context, userID uuid.UUID, role string, client Client) (LoginResult, error) {
	now := s.now()
	sessionID, err := uuid.NewV7()
	if err != nil {
		return LoginResult{}, fmt.Errorf("make session id: %w", err)
	}
	var refresh string
	err = s.store.InTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.SetLastActiveRole(ctx, sqlcgen.SetLastActiveRoleParams{ID: userID, Role: &role, Now: now}); err != nil {
			return err
		}
		refresh, err = s.createSession(ctx, q, userID, sessionID, role, client, now)
		return err
	})
	if err != nil {
		return LoginResult{}, err
	}
	return s.credentials(ctx, userID, sessionID, role, refresh)
}

// createSession inserts the session row and its first refresh token and returns the raw token.
func (s *Service) createSession(ctx context.Context, q *sqlcgen.Queries, userID, sessionID uuid.UUID, role string, client Client, now time.Time) (string, error) {
	if err := q.CreateSession(ctx, sqlcgen.CreateSessionParams{
		ID: sessionID, UserID: userID, ActiveRole: role, UserAgent: client.userAgent(), IpAddress: client.ipAddr(), Now: now,
	}); err != nil {
		return "", err
	}
	return s.insertRefreshToken(ctx, q, sessionID, nil, now)
}

// insertRefreshToken stores a new refresh token of the session (its hash only) and returns the raw token.
func (s *Service) insertRefreshToken(ctx context.Context, q *sqlcgen.Queries, sessionID uuid.UUID, parent *uuid.UUID, now time.Time) (string, error) {
	raw, hash, err := NewOpaqueToken()
	if err != nil {
		return "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("make refresh token id: %w", err)
	}
	err = q.InsertRefreshToken(ctx, sqlcgen.InsertRefreshTokenParams{
		ID: id, SessionID: sessionID, TokenHash: hash, ParentID: parent, ExpiresAt: now.Add(s.opts.RefreshTTL), Now: now,
	})
	return raw, err
}

// credentials signs the access token and describes the user, for the answer of a login or a role switch.
func (s *Service) credentials(ctx context.Context, userID, sessionID uuid.UUID, role, refresh string) (LoginResult, error) {
	tokens, err := s.tokensFor(userID, sessionID, role, refresh)
	if err != nil {
		return LoginResult{}, err
	}
	user, err := s.userView(ctx, userID, role)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{User: user, Tokens: tokens}, nil
}

func (s *Service) tokensFor(userID, sessionID uuid.UUID, role, refresh string) (Tokens, error) {
	access, _, err := s.tokens.Sign(userID, sessionID, role)
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{
		TokenType: "Bearer", AccessToken: access, ExpiresIn: int(s.tokens.TTL().Seconds()),
		RefreshToken: refresh, RefreshExpiresIn: int(s.opts.RefreshTTL.Seconds()),
	}, nil
}

// Refresh exchanges a refresh token for a new pair. Each refresh token works once: using an old one again after
// the grace window means a copy was kept somewhere, and the whole session is closed.
func (s *Service) Refresh(ctx context.Context, raw string, client Client) (Tokens, error) {
	var (
		out       Tokens
		fail      error
		closedSID []string
	)
	err := s.store.InTx(ctx, func(q *sqlcgen.Queries) error {
		now := s.now()
		rt, err := q.GetRefreshTokenForUpdate(ctx, HashToken(raw))
		if errors.Is(err, pgx.ErrNoRows) {
			fail = apperr.RefreshInvalid()
			return nil
		}
		if err != nil {
			return err
		}

		// closeSession ends the session and answers "invalid". It returns nil so the transaction COMMITS: rolling
		// back here would undo the very revocation that protects the account.
		closeSession := func(reason string) error {
			if err := q.RevokeSession(ctx, sqlcgen.RevokeSessionParams{ID: rt.SessionID, Now: now, Reason: &reason}); err != nil {
				return err
			}
			closedSID = append(closedSID, rt.SessionID.String())
			fail = apperr.RefreshInvalid()
			return nil
		}

		switch decideRefresh(refreshState{rt.ExpiresAt, rt.RotatedAt, rt.SessionRevokedAt}, now, s.opts.ReuseGrace) {
		case refreshReject:
			fail = apperr.RefreshInvalid()
			return nil
		case refreshReuse:
			s.log.Warn("refresh token reused: session closed", "user_id", rt.UserID, "session_id", rt.SessionID)
			return closeSession("reuse_detected")
		}

		user, err := q.GetAuthUser(ctx, rt.UserID)
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
			if err := q.MarkRefreshTokenRotated(ctx, sqlcgen.MarkRefreshTokenRotatedParams{ID: rt.ID, Now: now}); err != nil {
				return err
			}
		}
		refresh, err := s.insertRefreshToken(ctx, q, rt.SessionID, &rt.ID, now)
		if err != nil {
			return err
		}
		if err := q.TouchSession(ctx, sqlcgen.TouchSessionParams{
			ID: rt.SessionID, Now: now, ActiveRole: role, UserAgent: client.userAgent(), IpAddress: client.ipAddr(),
		}); err != nil {
			return err
		}
		out, err = s.tokensFor(rt.UserID, rt.SessionID, role, refresh)
		return err
	})
	if err != nil {
		return Tokens{}, err
	}
	if len(closedSID) > 0 {
		// The session is already closed in the database; this also stops the access tokens it still has.
		if err := s.revocations.RevokeSessions(ctx, closedSID, s.opts.RevokedFor); err != nil {
			s.log.Error("could not block the access tokens of a closed session", "error", err)
		}
	}
	if fail != nil {
		return Tokens{}, fail
	}
	return out, nil
}

// Logout ends the caller's session: its access token stops working at once and its refresh token with it. When the
// caller names a refresh token of one of their own sessions, that session is the one closed.
func (s *Service) Logout(ctx context.Context, p authz.Principal, refresh string) error {
	target, err := uuid.Parse(p.SessionID)
	if err != nil {
		return apperr.Unauthenticated()
	}
	if refresh != "" {
		if row, err := s.store.q.GetRefreshTokenSession(ctx, HashToken(refresh)); err == nil && row.UserID.String() == p.UserID {
			target = row.SessionID
		}
	}
	now := s.now()
	reason := "logout"
	if err := s.store.q.RevokeSession(ctx, sqlcgen.RevokeSessionParams{ID: target, Now: now, Reason: &reason}); err != nil {
		return err
	}
	return s.blockTokenAndSessions(ctx, p, []string{target.String()})
}

// LogoutAll ends every session of the caller, on every device.
func (s *Service) LogoutAll(ctx context.Context, p authz.Principal) error {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return apperr.Unauthenticated()
	}
	reason := "logout_all"
	ids, err := s.store.q.RevokeUserSessions(ctx, sqlcgen.RevokeUserSessionsParams{UserID: userID, Now: s.now(), Reason: &reason})
	if err != nil {
		return err
	}
	return s.blockTokenAndSessions(ctx, p, uuidStrings(ids))
}

// blockTokenAndSessions stops the caller's current access token and the access tokens of the closed sessions.
func (s *Service) blockTokenAndSessions(ctx context.Context, p authz.Principal, sessions []string) error {
	if err := s.revocations.RevokeToken(ctx, p.TokenID, p.TokenExpiresAt.Sub(s.now())); err != nil {
		return err
	}
	return s.revocations.RevokeSessions(ctx, sessions, s.opts.RevokedFor)
}

// SwitchRole moves the caller to another role they hold. The old session is closed and a new one opens in the
// new role, so tokens of the old role cannot be used any more. The refresh token proves the caller holds the session.
func (s *Service) SwitchRole(ctx context.Context, p authz.Principal, role, refresh string, client Client) (LoginResult, error) {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return LoginResult{}, apperr.Unauthenticated()
	}
	oldSession, err := uuid.Parse(p.SessionID)
	if err != nil {
		return LoginResult{}, apperr.Unauthenticated()
	}
	row, err := s.store.q.GetRefreshTokenSession(ctx, HashToken(refresh))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (row.UserID != userID || row.SessionID != oldSession || row.RevokedAt != nil)) {
		return LoginResult{}, apperr.RefreshInvalid()
	}
	if err != nil {
		return LoginResult{}, err
	}

	newSession, err := uuid.NewV7()
	if err != nil {
		return LoginResult{}, fmt.Errorf("make session id: %w", err)
	}
	var newRefresh string
	err = s.store.InTx(ctx, func(q *sqlcgen.Queries) error {
		now := s.now()
		held, err := q.GetAuthUser(ctx, userID)
		if err != nil {
			return err
		}
		if !contains(held.Roles, role) {
			return apperr.Invalid(apperr.Field{Name: "role", Rule: apperr.RuleRoleNotHeld})
		}
		reason := "role_switch"
		if err := q.RevokeSession(ctx, sqlcgen.RevokeSessionParams{ID: oldSession, Now: now, Reason: &reason}); err != nil {
			return err
		}
		if err := q.SetLastActiveRole(ctx, sqlcgen.SetLastActiveRoleParams{ID: userID, Role: &role, Now: now}); err != nil {
			return err
		}
		newRefresh, err = s.createSession(ctx, q, userID, newSession, role, client, now)
		return err
	})
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.blockTokenAndSessions(ctx, p, []string{oldSession.String()}); err != nil {
		return LoginResult{}, err
	}
	return s.credentials(ctx, userID, newSession, role, newRefresh)
}

// ChangePassword sets a new password and closes every other session of the account (the session that changed
// it stays logged in).
func (s *Service) ChangePassword(ctx context.Context, p authz.Principal, current, next string) error {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return apperr.Unauthenticated()
	}
	keep, err := uuid.Parse(p.SessionID)
	if err != nil {
		return apperr.Unauthenticated()
	}
	stored, err := s.store.q.GetPasswordHash(ctx, userID)
	if err != nil {
		return err
	}
	// An account without a password (made through another service) has nothing to compare with.
	hash := s.dummyHash
	if stored != nil {
		hash = *stored
	}
	matches, verr := VerifyPassword(current, hash)
	if verr != nil {
		s.log.Error("stored password hash is unusable", "user_id", userID, "error", verr)
	}
	if stored == nil || !matches {
		return apperr.Invalid(apperr.Field{Name: "current_password", Rule: apperr.RulePasswordIncorrect})
	}
	if current == next {
		return apperr.Invalid(apperr.Field{Name: "new_password", Rule: apperr.RulePasswordSame})
	}
	newHash, err := HashPassword(next)
	if err != nil {
		return err
	}

	var closed []uuid.UUID
	err = s.store.InTx(ctx, func(q *sqlcgen.Queries) error {
		now := s.now()
		if err := q.UpdatePassword(ctx, sqlcgen.UpdatePasswordParams{ID: userID, PasswordHash: &newHash, Now: now}); err != nil {
			return err
		}
		reason := "password_change"
		closed, err = q.RevokeUserSessions(ctx, sqlcgen.RevokeUserSessionsParams{
			UserID: userID, ExceptID: &keep, Now: now, Reason: &reason,
		})
		return err
	})
	if err != nil {
		return err
	}
	return s.revocations.RevokeSessions(ctx, uuidStrings(closed), s.opts.RevokedFor)
}

// Me describes the caller, acting in the role of the current request.
func (s *Service) Me(ctx context.Context, p authz.Principal) (UserView, error) {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return UserView{}, apperr.Unauthenticated()
	}
	return s.userView(ctx, userID, p.Role)
}

// Authenticate turns a bearer token into the caller: it checks the token, that it was not revoked, that the account
// is still active, and works out the role and permissions of the request. A revocation check that cannot be made
// (Redis down) is a 503, never a pass.
func (s *Service) Authenticate(ctx context.Context, bearer string) (authz.Principal, error) {
	claims, err := s.tokens.Verify(bearer)
	if err != nil {
		return authz.Principal{}, apperr.Unauthenticated()
	}
	revoked, err := s.revocations.IsRevoked(ctx, claims.TokenID, claims.SessionID)
	if err != nil {
		return authz.Principal{}, err
	}
	if revoked {
		return authz.Principal{}, apperr.Unauthenticated()
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return authz.Principal{}, apperr.Unauthenticated()
	}

	user, err := s.store.q.GetAuthUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return authz.Principal{}, apperr.Unauthenticated()
	}
	if err != nil {
		return authz.Principal{}, err
	}
	if user.Status != authz.StatusActive {
		return authz.Principal{}, apperr.AccountInactive()
	}
	role := authz.ResolveActiveRole(claims.Role, user.Roles, deref(user.LastActiveRole))
	if role == "" {
		return authz.Principal{}, apperr.Forbidden()
	}
	permissions, err := s.perms.ForRole(ctx, role)
	if err != nil {
		return authz.Principal{}, err
	}
	return authz.Principal{
		UserID: claims.UserID, SessionID: claims.SessionID, Role: role, Permissions: permissions,
		TokenID: claims.TokenID, TokenExpiresAt: claims.ExpiresAt,
	}, nil
}

// VerifyEmail marks the address of the token as verified. A token works once and expires; one that is unknown,
// used or expired gets the same answer.
func (s *Service) VerifyEmail(ctx context.Context, raw string) error {
	verified := false
	err := s.store.InTx(ctx, func(q *sqlcgen.Queries) error {
		now := s.now()
		row, err := q.GetVerificationTokenForUpdate(ctx, HashToken(raw))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !verificationUsable(row.ExpiresAt, row.UsedAt, now) {
			return nil
		}
		if err := q.MarkVerificationTokenUsed(ctx, sqlcgen.MarkVerificationTokenUsedParams{ID: row.ID, Now: now}); err != nil {
			return err
		}
		// Any other link sent to this account is now useless.
		if err := q.InvalidateVerificationTokens(ctx, sqlcgen.InvalidateVerificationTokensParams{UserID: row.UserID, Now: now}); err != nil {
			return err
		}
		verified = true
		return q.MarkEmailVerified(ctx, sqlcgen.MarkEmailVerifiedParams{ID: row.UserID, Now: now})
	})
	if err != nil {
		return err
	}
	if !verified {
		return apperr.VerificationInvalid()
	}
	return nil
}

// ResendVerification sends a new verification link. It answers the same whether or not the address has an
// account (nobody can use it to find out who is registered): an unknown, already verified or disabled address, or
// one that got an email less than the cooldown ago, simply gets nothing. Older links stop working.
func (s *Service) ResendVerification(ctx context.Context, email, lang string) error {
	email = normalizeEmail(email)
	user, err := s.store.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if user.EmailVerifiedAt != nil || user.Status != authz.StatusActive {
		return nil
	}
	last, err := s.store.q.LatestVerificationTokenAt(ctx, user.ID)
	var lastSent *time.Time
	switch {
	case err == nil:
		lastSent = &last
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	now := s.now()
	if !resendAllowed(lastSent, now, s.opts.ResendCooldown) {
		return nil
	}

	raw, hash, err := NewOpaqueToken()
	if err != nil {
		return err
	}
	err = s.store.InTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.InvalidateVerificationTokens(ctx, sqlcgen.InvalidateVerificationTokensParams{UserID: user.ID, Now: now}); err != nil {
			return err
		}
		return s.insertVerificationToken(ctx, q, user.ID, hash, now)
	})
	if err != nil {
		return err
	}
	s.sendVerification(ctx, user.FullName, email, raw, lang)
	return nil
}

func (s *Service) insertVerificationToken(ctx context.Context, q *sqlcgen.Queries, userID uuid.UUID, hash []byte, now time.Time) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("make verification token id: %w", err)
	}
	return q.InsertVerificationToken(ctx, sqlcgen.InsertVerificationTokenParams{
		ID: id, UserID: userID, TokenHash: hash, ExpiresAt: now.Add(s.opts.VerificationTTL), Now: now,
	})
}

// sendVerification emails the link in the background. The raw token only goes into the message: it is never logged.
func (s *Service) sendVerification(ctx context.Context, fullName, email, token, lang string) {
	link := strings.TrimRight(s.opts.WebURL, "/") + "/verify-email?token=" + url.QueryEscape(token)
	msg, err := s.renderer.VerifyEmail(lang, mail.VerifyEmail{To: email, FullName: fullName, URL: link, ExpiresIn: s.opts.VerificationTTL})
	if err != nil {
		s.log.Error("render verification email", "error", err)
		return
	}
	// The email outlives the request, so it must not be cancelled with it.
	sendCtx := context.WithoutCancel(ctx)
	s.dispatch(func() {
		ctx, cancel := context.WithTimeout(sendCtx, emailSendTimeout)
		defer cancel()
		if err := s.mailer.Send(ctx, msg); err != nil {
			s.log.Error("send verification email failed", "error", err)
		}
	})
}

// assignRoles gives the user the roles. Unknown roles are refused, and a staff role is never put together with
// another kind of role: this is the only place roles are granted.
func assignRoles(ctx context.Context, q *sqlcgen.Queries, userID uuid.UUID, roles []string, now time.Time) error {
	for _, role := range roles {
		if authz.Seniority(role) == 0 {
			return fmt.Errorf("unknown role %q", role)
		}
	}
	if authz.MixesStaffWithOthers(roles) {
		return errors.New("staff roles cannot be mixed with other roles")
	}
	for _, role := range roles {
		if err := q.AddUserRole(ctx, sqlcgen.AddUserRoleParams{UserID: userID, RoleCode: role, Now: now}); err != nil {
			return err
		}
	}
	return nil
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}
