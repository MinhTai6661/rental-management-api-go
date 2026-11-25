package manager

import (
	"slices"

	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

// Candidate is an account as the rules for choosing a manager see it.
type Candidate struct {
	ID     uuid.UUID
	Active bool
	// Roles is every role the account holds.
	Roles []string
}

// CanBeAsked says whether the account may take rooms from the landlord: it exists and is active, is not the landlord,
// holds the tenant role, and is not staff (admin and super admin stay apart from the renting side).
func CanBeAsked(landlordID uuid.UUID, c Candidate) error {
	if !c.Active || c.ID == landlordID || !slices.Contains(c.Roles, authz.RoleTenant) {
		return ErrCannotManage
	}
	if slices.ContainsFunc(c.Roles, authz.IsStaff) {
		return ErrCannotManage
	}
	return nil
}
