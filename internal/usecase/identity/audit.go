package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
)

// The audit actions of accounts and sessions (the audit context owns the catalog; these are the names it knows).
const (
	AuditRegister        = "auth.register"
	AuditEmailConfirmed  = "auth.email_confirmed"
	AuditLogin           = "auth.login"
	AuditLoginFailed     = "auth.login_failed"
	AuditLogout          = "auth.logout"
	AuditTokenReuse      = "auth.token_reuse" //nolint:gosec // an action name, not a credential
	AuditRoleSwitched    = "auth.role_switched"
	AuditPasswordChanged = "auth.password_changed"
)

// Why a sign-in failed, as the audit trail says it.
const (
	failBadCredentials  = "bad_credentials"
	failAccountInactive = "account_inactive"
	failEmailUnverified = "email_unverified"
	failNoRole          = "no_role"
)

// loginVia is how people sign in with an email and a password.
const loginVia = "password"

// emailHash is what the trail keeps of an email a failed sign-in typed: a hash, never the address.
func emailHash(normalizedEmail string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(normalizedEmail)))
	return hex.EncodeToString(sum[:])
}

// auditFailedLogin records a failed sign-in. It never fails the request: the entry is routine.
func (a *Auth) auditFailedLogin(ctx context.Context, email, reason string, target *uuid.UUID) {
	if err := a.audit.Record(ctx, AuditEvent{
		Action: AuditLoginFailed, TargetUserID: target, Meta: map[string]any{"reason": reason, "email_hash": emailHash(email)},
	}); err != nil {
		a.log.Warn("could not record a failed sign-in", "error", err)
	}
}
