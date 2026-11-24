// Package identity holds the use cases of accounts and login sessions: register, log in, refresh, log out, switch
// role, change password, verify the email, and authenticate a request. It declares the ports it needs (ports.go)
// and imports no framework, driver or HTTP code.
package identity

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// emailSendTimeout bounds one delivery to the mail server; sending happens after the request is answered.
const emailSendTimeout = 15 * time.Second

// dummyPassword is hashed once at start-up: it is verified when an email has no account, so a wrong email costs as
// much time as a wrong password.
const dummyPassword = "not-a-real-password"

// Options are the settings of the auth use cases.
type Options struct {
	// TermsVersion is the version of the terms of use people must accept.
	TermsVersion string
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

// Deps are the collaborators of the auth use cases.
type Deps struct {
	Tx            TxManager
	Users         UserRepository
	Sessions      SessionRepository
	Verifications VerificationRepository
	Permissions   PermissionRepository
	Revocations   RevocationStore
	Hasher        PasswordHasher
	Tokens        TokenIssuer
	Opaque        OpaqueTokens
	Mailer        VerificationMailer
	Clock         Clock
	Log           *slog.Logger
	// Audit records what happens to accounts and sessions (nil: nothing).
	Audit Auditor
	// Terms gives the version of the terms people must accept (nil: Options.TermsVersion).
	Terms TermsVersions
	// Dispatch runs background work (sending an email) so the response does not wait for it, and so the time a
	// request takes does not tell whether an address has an account. Nil runs it in a goroutine; tests run it inline.
	Dispatch func(work func())
}

// Auth is the set of use cases of accounts and login sessions.
type Auth struct {
	tx            TxManager
	users         UserRepository
	sessions      SessionRepository
	verifications VerificationRepository
	revocations   RevocationStore
	hasher        PasswordHasher
	tokens        TokenIssuer
	opaque        OpaqueTokens
	mailer        VerificationMailer
	clock         Clock
	log           *slog.Logger
	audit         Auditor
	terms         TermsVersions
	dispatch      func(work func())
	perms         *permissionCache
	opts          Options
	// dummyHash is verified when an email has no account.
	dummyHash  string
	background sync.WaitGroup
}

// NewAuth builds the use cases.
func NewAuth(d Deps, o Options) (*Auth, error) {
	dummy, err := d.Hasher.Hash(dummyPassword)
	if err != nil {
		return nil, err
	}
	a := &Auth{
		tx: d.Tx, users: d.Users, sessions: d.Sessions, verifications: d.Verifications, hasher: d.Hasher,
		tokens: d.Tokens, opaque: d.Opaque, revocations: d.Revocations, mailer: d.Mailer, clock: d.Clock, log: d.Log,
		dispatch: d.Dispatch, terms: d.Terms, opts: o, dummyHash: dummy,
		perms: newPermissionCache(d.Permissions, o.PermissionCacheTTL, d.Clock),
	}
	a.audit = d.Audit
	if a.audit == nil {
		a.audit = NoAudit{}
	}
	if a.dispatch == nil {
		a.dispatch = a.runInBackground
	}
	return a, nil
}

// runInBackground runs work in a goroutine that Wait can wait for.
func (a *Auth) runInBackground(work func()) {
	a.background.Add(1)
	go func() {
		defer a.background.Done()
		work()
	}()
}

// Wait blocks until the background work (emails being sent) is done; call it when the server shuts down.
func (a *Auth) Wait() { a.background.Wait() }

// sendVerification emails the token in the background. The raw token only goes into the message: it is never logged.
func (a *Auth) sendVerification(ctx context.Context, fullName, email, token, lang string) {
	// The email outlives the request, so it must not be cancelled with it.
	sendCtx := context.WithoutCancel(ctx)
	a.dispatch(func() {
		ctx, cancel := context.WithTimeout(sendCtx, emailSendTimeout)
		defer cancel()
		if err := a.mailer.SendVerification(ctx, email, fullName, token, lang, a.opts.VerificationTTL); err != nil {
			a.log.Error("send verification email failed", "error", err)
		}
	})
}

// termsVersion is the version of the terms people must accept now.
func (a *Auth) termsVersion(ctx context.Context) (string, error) {
	if a.terms == nil {
		return a.opts.TermsVersion, nil
	}
	return a.terms.Current(ctx)
}
