package identity

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
)

// roleGranter is the part of the user stores that adds a role.
type roleGranter interface {
	AddRole(ctx context.Context, userID uuid.UUID, role string, now time.Time) error
}

// assignRoles gives the user the roles after the domain rules agree: this is the only place roles are granted.
func assignRoles(ctx context.Context, users roleGranter, userID uuid.UUID, roles []string, now time.Time) error {
	if err := domain.ValidateRoleGrant(roles); err != nil {
		return err
	}
	for _, role := range roles {
		if err := users.AddRole(ctx, userID, role, now); err != nil {
			return err
		}
	}
	return nil
}
