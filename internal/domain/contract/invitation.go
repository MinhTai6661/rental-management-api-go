package contract

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/domain/invitation"
)

// InvitationStatus is where an offer of a room by link stands.
type InvitationStatus string

// Invitation statuses.
const (
	// InvitationAwaitingApproval: a manager wrote it and the landlord has not approved it yet (nothing creates it
	// until the manager phase).
	InvitationAwaitingApproval InvitationStatus = "awaiting_approval"
	// InvitationPending: sent, waiting for an answer until it expires.
	InvitationPending  InvitationStatus = "pending"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationDeclined InvitationStatus = "declined"
	// InvitationRevoked: the landlord withdrew it, or its room was deleted.
	InvitationRevoked InvitationStatus = "revoked"
)

// InvitationStatuses lists every status an invitation can have (the CHECK of the table is the last defence).
func InvitationStatuses() []InvitationStatus {
	return []InvitationStatus{InvitationAwaitingApproval, InvitationPending, InvitationAccepted, InvitationDeclined, InvitationRevoked}
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

// ValidOfferStatus says whether an invitation may offer the contract that status on acceptance: waiting for the
// landlord's answer, or running at once.
func ValidOfferStatus(s Status) bool { return s == StatusPending || s == StatusActive }

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

// InvitationTerms are what the contract is made of when the offer is accepted.
type InvitationTerms struct {
	PriceVND       int64
	DepositVND     *int64
	OccupantCount  int
	StartsOn       time.Time
	EndsOn         *time.Time
	ContractStatus Status
	Note           *string
}

// Invitation is a landlord's offer of a room on set terms. Whoever it is for and accepts becomes the tenant.
type Invitation struct {
	ID         uuid.UUID
	RoomID     uuid.UUID
	LandlordID uuid.UUID
	// CreatedByID is the manager who wrote it for the landlord; nil when the landlord did.
	CreatedByID *uuid.UUID
	Status      InvitationStatus
	// TokenHash is how the link is looked up; Sealed is the secret encrypted, kept only while the offer waits so the
	// landlord can copy the same link again.
	TokenHash string
	Sealed    *string
	ExpiresAt time.Time
	Terms     InvitationTerms
	// RecipientID and RecipientEmail say who may answer (see Audience); both nil means anybody.
	RecipientID    *uuid.UUID
	RecipientEmail *string
	AnsweredByID   *uuid.UUID
	AnsweredAt     *time.Time
	ContractID     *uuid.UUID
	// ApprovedByID, ApprovedAt and ApprovalNote are the landlord's answer to an offer a manager wrote.
	ApprovedByID *uuid.UUID
	ApprovedAt   *time.Time
	ApprovalNote *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
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

// LinkAvailable says whether the landlord can still copy the link: the offer is open and its secret was kept.
func (i Invitation) LinkAvailable(now time.Time) bool { return i.OpenAt(now) && i.Sealed != nil }

// AuthoredBy says whether the user wrote the offer: the landlord, or the manager who drafted it.
func (i Invitation) AuthoredBy(userID uuid.UUID) bool {
	return i.LandlordID == userID || (i.CreatedByID != nil && *i.CreatedByID == userID)
}

// Answer records the answer of the person it was for and wipes the secret: accepted needs the contract it made.
func (i *Invitation) Answer(to InvitationStatus, by uuid.UUID, at time.Time, contractID *uuid.UUID) {
	i.Status, i.AnsweredByID, i.AnsweredAt, i.ContractID, i.Sealed = to, &by, &at, contractID, nil
}

// Revoke withdraws an open (waiting or awaiting approval) invitation and wipes the secret.
func (i *Invitation) Revoke(at time.Time) error {
	if i.Status != InvitationPending && i.Status != InvitationAwaitingApproval {
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

// Approve opens an offer a manager wrote: it waits for an answer from now on, under a new secret and a new end.
func (i *Invitation) Approve(by uuid.UUID, at time.Time, note *string, hash string, sealed *string, expires time.Time) error {
	if i.Status != InvitationAwaitingApproval {
		return ErrInvitationNotAwaiting
	}
	i.Status, i.TokenHash, i.Sealed, i.ExpiresAt = InvitationPending, hash, sealed, expires
	i.ApprovedByID, i.ApprovedAt, i.ApprovalNote = &by, &at, note
	return nil
}

// Reject turns an offer a manager wrote down: it is withdrawn with the landlord's reason.
func (i *Invitation) Reject(by uuid.UUID, at time.Time, note string) error {
	if i.Status != InvitationAwaitingApproval {
		return ErrInvitationNotAwaiting
	}
	i.Status, i.AnsweredAt, i.Sealed = InvitationRevoked, &at, nil
	i.ApprovedByID, i.ApprovedAt, i.ApprovalNote = &by, &at, &note
	return nil
}

// MaxApprovalNote is the longest reason or note of an approval.
const MaxApprovalNote = 1000

// InvitationDetail is an invitation as its landlord sees it.
type InvitationDetail struct {
	Invitation
	RoomName     string
	BuildingName *string
	LandlordName string
	// Recipient is the account the offer names (nil for an address or for anybody).
	Recipient *Person
	// AnsweredBy is who answered it.
	AnsweredBy *Person
}

// InvitationOffer is an invitation as the person it is for sees it: the terms, the room and the landlord. It carries no
// word about who else was invited.
type InvitationOffer struct {
	Invitation
	Room RoomRef
	// HouseName is the name of the building the room is in.
	HouseName *string
	// Landlord is the owner of the room; only the id and the name are shown.
	Landlord Person
}

// The errors of the invitation use cases. A link that is wrong, expired, answered, not for the caller or whose room is
// gone all read the same (ErrInvitationInvalid), so a link tells nothing to whoever should not have it.
var (
	// ErrInvitationInvalid: the link or invitation cannot be used by this caller.
	ErrInvitationInvalid = errors.New("invitation is not valid")
	// ErrInvitationNotPending: the invitation was already answered or withdrawn.
	ErrInvitationNotPending = errors.New("invitation is not waiting for an answer")
	// ErrInvitationNotAwaiting: the offer is not waiting for the landlord's approval.
	ErrInvitationNotAwaiting = errors.New("invitation is not waiting for approval")
	// ErrInvitationLinkUnavailable: the link cannot be shown again (make a new one).
	ErrInvitationLinkUnavailable = errors.New("invitation link cannot be shown again")
	// ErrInvitationOwnOffer: nobody accepts their own offer.
	ErrInvitationOwnOffer = errors.New("cannot accept one's own offer")
)
