package manager

import (
	"time"

	"github.com/google/uuid"

	"rental-management/internal/domain/invitation"
)

// InvitationStatus is where an invitation to manage rooms stands. There is no "awaiting approval" here: that belongs
// to the offers of rooms.
type InvitationStatus string

// The statuses of an invitation.
const (
	InvitationPending  InvitationStatus = "pending"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationDeclined InvitationStatus = "declined"
	InvitationRevoked  InvitationStatus = "revoked"
)

// InvitationStatuses lists every status.
func InvitationStatuses() []InvitationStatus {
	return []InvitationStatus{InvitationPending, InvitationAccepted, InvitationDeclined, InvitationRevoked}
}

// ParseInvitationStatus turns the text into an InvitationStatus.
func ParseInvitationStatus(s string) (InvitationStatus, bool) {
	for _, st := range InvitationStatuses() {
		if string(st) == s {
			return st, true
		}
	}
	return "", false
}

// LinkTokenLength is the length of the secret in an invitation link: 48 random bytes in unpadded base64url.
const LinkTokenLength = 64

// ValidLinkToken says whether a string looks like the secret of an invitation link, so a wrong one is refused without
// touching the database.
func ValidLinkToken(s string) bool {
	if len(s) != LinkTokenLength {
		return false
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return true
}

// MaxRoomsPerInvitation is how many rooms one invitation can hand over.
const MaxRoomsPerInvitation = 100

// Invitation is a landlord's invitation to manage some rooms, sent as a link. Whoever it is for and accepts becomes
// the manager of every room in it, or of none.
type Invitation struct {
	ID         uuid.UUID
	LandlordID uuid.UUID
	Status     InvitationStatus
	// TokenHash is how the link is looked up; Sealed is the secret encrypted, kept only while the invitation waits so
	// the landlord can copy the same link again.
	TokenHash string
	Sealed    *string
	ExpiresAt time.Time
	// NotifyLandlord and Capabilities are copied to every hand-over made on acceptance.
	NotifyLandlord bool
	Capabilities   []Capability
	RoomIDs        []uuid.UUID
	// RecipientID and RecipientEmail say who may answer (see Audience); both nil means anybody.
	RecipientID    *uuid.UUID
	RecipientEmail *string
	AnsweredByID   *uuid.UUID
	AnsweredAt     *time.Time
	CreatedAt      time.Time
}

// Audience says who may answer the invitation.
func (i Invitation) Audience() invitation.Audience {
	return invitation.AudienceOf(i.RecipientID, i.RecipientEmail)
}

// OpenAt says whether the invitation can still be answered at the time.
func (i Invitation) OpenAt(now time.Time) bool {
	return i.Status == InvitationPending && i.ExpiresAt.After(now)
}

// Expired says whether the invitation waited for an answer past its time.
func (i Invitation) Expired(now time.Time) bool {
	return i.Status == InvitationPending && !i.ExpiresAt.After(now)
}

// LinkAvailable says whether the landlord can still copy the link.
func (i Invitation) LinkAvailable(now time.Time) bool { return i.OpenAt(now) && i.Sealed != nil }

// Answer records the answer of the person it was for and wipes the secret.
func (i *Invitation) Answer(to InvitationStatus, by uuid.UUID, at time.Time) {
	i.Status, i.AnsweredByID, i.AnsweredAt, i.Sealed = to, &by, &at, nil
}

// Revoke withdraws a waiting invitation and wipes the secret.
func (i *Invitation) Revoke(at time.Time) error {
	if i.Status != InvitationPending {
		return ErrInvitationNotPending
	}
	i.Status, i.AnsweredAt, i.Sealed = InvitationRevoked, &at, nil
	return nil
}

// Renew gives a waiting invitation, expired or not, a new secret and a new end: the old link stops working.
func (i *Invitation) Renew(hash string, sealed *string, expires time.Time) error {
	if i.Status != InvitationPending {
		return ErrInvitationNotPending
	}
	i.TokenHash, i.Sealed, i.ExpiresAt = hash, sealed, expires
	return nil
}

// Room is a room of an invitation or a hand-over as it is shown.
type Room struct {
	ID           uuid.UUID
	Name         string
	BuildingName *string
}

// Person is an account named in a record.
type Person struct {
	ID        uuid.UUID
	FullName  string
	Email     *string
	AvatarKey *string
}

// InvitationDetail is an invitation as its landlord sees it.
type InvitationDetail struct {
	Invitation
	Rooms     []Room
	Recipient *Person
}

// InvitationOffer is an invitation as the person it is for sees it: the rooms, the permissions and the landlord.
type InvitationOffer struct {
	Invitation
	Rooms        []Room
	LandlordName string
}
