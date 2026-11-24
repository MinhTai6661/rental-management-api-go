package notification

import (
	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

// Sender is who writes an announcement and the role they act in. A staff role writes for the platform; the landlord
// role writes for the landlord's own people.
type Sender struct {
	UserID uuid.UUID
	Role   string
}

// Kind is the kind of message the sender writes; "" for a role that may not write any.
func (s Sender) Kind() SenderKind {
	switch {
	case authz.IsStaff(s.Role):
		return SenderPlatform
	case s.Role == authz.RoleLandlord:
		return SenderLandlord
	}
	return ""
}

// ReachableRoles are the roles a platform sender may name in the criteria: every non-staff role and the staff roles
// below the sender's own.
func ReachableRoles(senderRole string) []string {
	return append([]string{authz.RoleTenant, authz.RoleLandlord, authz.RoleManager}, StaffRolesBelow(senderRole)...)
}
