package identity

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

// Login checks the credentials and opens a session. A wrong email and a wrong password look the same, and cost the
// same time. Only after the password is right does the caller learn that the account is disabled or unverified.
func (a *Auth) Login(ctx context.Context, email, password string, client Client) (LoginResult, error) {
	user, err := a.users.FindByEmail(ctx, domain.NormalizeEmail(email))
	hash := a.dummyHash
	found := err == nil
	if err != nil && !errors.Is(err, shared.ErrNotFound) {
		return LoginResult{}, err
	}
	if found && user.PasswordHash != nil {
		hash = *user.PasswordHash
	}
	matches, verr := a.hasher.Verify(password, hash)
	if verr != nil {
		a.log.Error("stored password hash is unusable", "user_id", user.ID, "error", verr)
	}
	email = domain.NormalizeEmail(email)
	var target *uuid.UUID
	if found {
		target = &user.ID
	}
	if !found || user.PasswordHash == nil || !matches {
		a.auditFailedLogin(ctx, email, failBadCredentials, target)
		return LoginResult{}, domain.ErrInvalidCredentials
	}
	if user.Status != authz.StatusActive {
		a.auditFailedLogin(ctx, email, failAccountInactive, target)
		return LoginResult{}, domain.ErrAccountInactive
	}
	if user.EmailVerifiedAt == nil {
		a.auditFailedLogin(ctx, email, failEmailUnverified, target)
		return LoginResult{}, domain.ErrEmailUnverified
	}

	auth, err := a.users.AuthUser(ctx, user.ID)
	if err != nil {
		return LoginResult{}, err
	}
	role := domain.InitialRole(auth.Roles, deref(auth.LastActiveRole))
	if role == "" {
		a.auditFailedLogin(ctx, email, failNoRole, target)
		return LoginResult{}, domain.ErrForbidden
	}
	return a.openSession(ctx, user.ID, role, client)
}
