package report

import (
	"errors"

	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

// The errors of the report rules and use cases.
var (
	// ErrNotFound: no such report, or one the caller may not see (the two read the same).
	ErrNotFound = errors.New("report not found")
	// ErrForbidden: the caller may see the report but not do this to it.
	ErrForbidden = errors.New("not allowed on this report")
	// ErrReadOnly: the author deleted the report, so nothing can be added to it or changed any more.
	ErrReadOnly = errors.New("report is read only")
	// ErrRoomRequired: a room report names no room.
	ErrRoomRequired = errors.New("a room report needs a room")
	// ErrRoomNotYours: the room is not one the tenant rents or rented.
	ErrRoomNotYours = errors.New("room is not one the tenant rented")
)

// Viewer is the caller: who they are and the role the request acts in (one role per request).
type Viewer struct {
	UserID uuid.UUID
	Role   string
}

// CanCreate says whether somebody acting in the role may write a report of the kind: tenants report on their rooms (to
// the landlord) and on the app (to the admins); landlords only on the app.
func CanCreate(role string, kind Kind) bool {
	switch kind {
	case KindRoom:
		return role == authz.RoleTenant
	case KindFeature:
		return role == authz.RoleTenant || role == authz.RoleLandlord
	}
	return false
}

// IsAuthor says whether the viewer wrote the report.
func IsAuthor(v Viewer, r Report) bool { return r.ReporterID == v.UserID }

// IsRecipient says whether the report goes to the viewer: the landlord of the room for a room report, an admin for a
// feature report. A super admin is the recipient of everything.
func IsRecipient(v Viewer, r Report) bool {
	if v.Role == authz.RoleSuperAdmin {
		return true
	}
	switch r.Kind {
	case KindFeature:
		return v.Role == authz.RoleAdmin
	case KindRoom:
		return v.Role == authz.RoleLandlord && r.RoomLandlordID != nil && *r.RoomLandlordID == v.UserID
	}
	return false
}

// CanView: whoever wrote it, or whoever it goes to. Once the author deleted it, it is gone for them; the recipient
// still sees it.
func CanView(v Viewer, r Report) bool {
	if r.Deleted() && IsAuthor(v, r) {
		return false
	}
	return IsAuthor(v, r) || IsRecipient(v, r)
}

// CanReply: the author and the recipient may write in the conversation; a deleted report is read only.
func CanReply(v Viewer, r Report) bool { return !r.Deleted() && CanView(v, r) }

// CanSetStatus: only the recipient says where the report stands, and not once it is deleted.
func CanSetStatus(v Viewer, r Report) bool { return !r.Deleted() && IsRecipient(v, r) }

// CanDelete: a tenant deletes the reports they wrote.
func CanDelete(v Viewer, r Report) bool {
	return !r.Deleted() && IsAuthor(v, r) && v.Role == authz.RoleTenant
}

// Authorize returns nil when the action the viewer asked for is allowed on the report, otherwise the
// error to answer with: not found when they may not even see it, read only when the report is deleted, else forbidden.
func Authorize(v Viewer, r Report, allowed func(Viewer, Report) bool) error {
	switch {
	case !CanView(v, r):
		return ErrNotFound
	case allowed(v, r):
		return nil
	case r.Deleted():
		return ErrReadOnly
	}
	return ErrForbidden
}

// InboxScope says which reports a person has to answer. The zero value is none.
type InboxScope struct {
	// Everything is every report (a super admin).
	Everything bool
	// Features is every feature report (an admin).
	Features bool
	// LandlordID, when set, is the room reports of the rooms of that landlord.
	LandlordID *uuid.UUID
}

// InboxOf is the scope of the viewer's inbox, from the role they act in.
func InboxOf(v Viewer) InboxScope {
	switch v.Role {
	case authz.RoleSuperAdmin:
		return InboxScope{Everything: true}
	case authz.RoleAdmin:
		return InboxScope{Features: true}
	case authz.RoleLandlord:
		id := v.UserID
		return InboxScope{LandlordID: &id}
	}
	return InboxScope{}
}

// Recipient is who a report goes to: one person (the landlord), or every admin.
type Recipient struct {
	UserID uuid.UUID
	Admins bool
}

// RecipientOf is who is told about what happens to the author's side of the report. A room report whose room has no
// known owner goes to nobody.
func RecipientOf(r Report) (Recipient, bool) {
	if r.Kind == KindFeature {
		return Recipient{Admins: true}, true
	}
	if r.RoomLandlordID == nil {
		return Recipient{}, false
	}
	return Recipient{UserID: *r.RoomLandlordID}, true
}
