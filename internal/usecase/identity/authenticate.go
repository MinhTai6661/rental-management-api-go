package identity

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

// Authenticate turns a bearer token into the caller: it checks the token, that it was not revoked, that the account
// is still active, and works out the role and permissions of the request. A revocation check that cannot be made
// (Redis down) is an error wrapping shared.ErrUnavailable, never a pass.
func (a *Auth) Authenticate(ctx context.Context, bearer string) (authz.Principal, error) {
	claims, err := a.tokens.Verify(bearer)
	if err != nil {
		return authz.Principal{}, domain.ErrUnauthenticated
	}
	revoked, err := a.revocations.IsRevoked(ctx, claims.TokenID, claims.SessionID)
	if err != nil {
		return authz.Principal{}, err
	}
	if revoked {
		return authz.Principal{}, domain.ErrUnauthenticated
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return authz.Principal{}, domain.ErrUnauthenticated
	}

	user, err := a.users.AuthUser(ctx, userID)
	if errors.Is(err, shared.ErrNotFound) {
		return authz.Principal{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return authz.Principal{}, err
	}
	if user.Status != authz.StatusActive {
		return authz.Principal{}, domain.ErrAccountInactive
	}
	role := authz.ResolveActiveRole(claims.Role, user.Roles, deref(user.LastActiveRole))
	if role == "" {
		return authz.Principal{}, domain.ErrForbidden
	}
	permissions, err := a.perms.ForRole(ctx, role)
	if err != nil {
		return authz.Principal{}, err
	}
	return authz.Principal{
		UserID: claims.UserID, SessionID: claims.SessionID, Role: role, Permissions: permissions,
		TokenID: claims.TokenID, TokenExpiresAt: claims.ExpiresAt,
	}, nil
}
