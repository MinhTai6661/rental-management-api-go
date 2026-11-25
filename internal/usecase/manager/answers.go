package manager

import (
	"context"
	"strconv"

	"github.com/google/uuid"

	"rental-management/internal/domain/invitation"
	domain "rental-management/internal/domain/manager"
)

// Answers is the invited person's side of the invitations to manage rooms: see an invitation, accept it (the rooms are
// handed over then, all of them or none) or decline it, and list the invitations addressed to the account. Every link
// or id that cannot be used by the caller (wrong, answered, withdrawn, expired, for somebody else) gives the one error
// domain.ErrInvitationInvalid, so an invitation tells nothing to whoever it is not for.
type Answers struct{ *core }

// NewAnswers builds the invited person's use cases.
func NewAnswers(d Deps, o Options) *Answers { return &Answers{newCore(d, o)} }

// Ref names the invitation to answer: by the secret of its link (which wins when both are given), or by the id of an
// invitation addressed to the caller's account.
type Ref struct {
	Token        *string
	InvitationID *uuid.UUID
}

// find reads the invitation a reference points to, without any lock.
func (s *Answers) find(ctx context.Context, userID uuid.UUID, ref Ref) (domain.Invitation, error) {
	var (
		inv domain.Invitation
		err error
	)
	switch {
	case ref.Token != nil:
		if !domain.ValidLinkToken(*ref.Token) {
			return domain.Invitation{}, domain.ErrInvitationInvalid
		}
		inv, err = s.Invitations.FindByTokenHash(ctx, s.Tokens.Hash(*ref.Token))
	case ref.InvitationID != nil:
		inv, err = s.Invitations.FindForRecipient(ctx, *ref.InvitationID, userID)
	default:
		return domain.Invitation{}, domain.ErrInvitationInvalid
	}
	return inv, invalidIfMissing(err)
}

// open finds the invitation the caller may answer and the account answering. With lock, the rooms and then the
// invitation are locked (the order of every transaction touching both) and the invitation is read again under the lock,
// so what was checked still holds.
func (s *Answers) open(ctx context.Context, userID uuid.UUID, ref Ref, lock bool) (domain.Invitation, invitation.Person, error) {
	inv, err := s.find(ctx, userID, ref)
	if err != nil {
		return domain.Invitation{}, invitation.Person{}, err
	}
	if lock {
		if _, err = s.Rooms.Lock(ctx, inv.LandlordID, inv.RoomIDs); err != nil {
			return domain.Invitation{}, invitation.Person{}, err
		}
		if inv, err = s.Invitations.ForUpdate(ctx, inv.ID); err != nil {
			return domain.Invitation{}, invitation.Person{}, invalidIfMissing(err)
		}
	}
	if !inv.OpenAt(s.Clock.Now()) {
		return domain.Invitation{}, invitation.Person{}, domain.ErrInvitationInvalid
	}
	p, err := s.Invitees.Person(ctx, userID)
	if err != nil {
		return domain.Invitation{}, invitation.Person{}, invalidIfMissing(err)
	}
	if !invitation.Allows(inv.RecipientID, inv.RecipientEmail, userID, p.Email, p.EmailVerified) {
		return domain.Invitation{}, invitation.Person{}, domain.ErrInvitationInvalid
	}
	return inv, p, nil
}

// Preview shows the invitation to the person it is for, before they answer. Nothing changes.
func (s *Answers) Preview(ctx context.Context, userID uuid.UUID, ref Ref) (domain.InvitationOffer, error) {
	inv, _, err := s.open(ctx, userID, ref, false)
	if err != nil {
		return domain.InvitationOffer{}, err
	}
	offer, err := s.Invitations.Offer(ctx, inv.ID)
	return offer, invalidIfMissing(err)
}

// Accepted is what accepting made: the hand-overs, one per room.
type Accepted struct {
	Assignments []domain.Assignment
	Rooms       []string
}

// Accept hands the rooms of the invitation to the caller. The caller must be someone the landlord may ask (see
// domain.CanBeAsked); every room must still be the landlord's and have no manager, otherwise nothing is handed over
// (domain.ErrRoomsChanged) and the invitation stays open. The caller gets the manager role; the landlord is told once.
func (s *Answers) Accept(ctx context.Context, userID uuid.UUID, ref Ref) (Accepted, error) {
	var (
		inv   domain.Invitation
		out   Accepted
		names []string
	)
	err := s.Tx.WithinTx(ctx, func(ctx context.Context) error {
		var (
			p   invitation.Person
			err error
		)
		if inv, p, err = s.open(ctx, userID, ref, true); err != nil {
			return err
		}
		if err := domain.CanBeAsked(inv.LandlordID, domain.Candidate{ID: p.ID, Active: p.Active, Roles: p.Roles}); err != nil {
			return err
		}
		rooms, err := s.Rooms.Lock(ctx, inv.LandlordID, inv.RoomIDs)
		if err != nil {
			return err
		}
		if len(rooms) != len(inv.RoomIDs) {
			return domain.ErrRoomsChanged
		}
		managed, err := s.Assignments.ManagedRooms(ctx, inv.RoomIDs)
		if err != nil {
			return err
		}
		if len(managed) > 0 {
			return domain.ErrRoomsChanged
		}
		now := s.Clock.Now()
		if out.Assignments, err = s.Assignments.Insert(ctx, inv.LandlordID, userID, inv.RoomIDs, inv.NotifyLandlord, inv.Capabilities, now); err != nil {
			return err
		}
		names = roomNames(rooms, inv.RoomIDs)
		for _, a := range out.Assignments {
			if err := s.Audit.Record(ctx, AuditEvent{
				Action: AuditAccepted, ActorID: userID, RoomID: a.RoomID, OwnerID: inv.LandlordID, TargetUserID: &userID,
				InvitationID: inv.ID, Meta: map[string]any{
					"invitation_id": inv.ID.String(), "assignment_id": a.ID.String(),
					"capabilities": domain.Strings(inv.Capabilities), "notify_landlord": inv.NotifyLandlord,
				},
			}); err != nil {
				return err
			}
		}
		if err := s.Roles.Grant(ctx, userID); err != nil {
			return err
		}
		inv.Answer(domain.InvitationAccepted, userID, now)
		return s.Invitations.Save(ctx, inv)
	})
	if err != nil {
		return Accepted{}, err
	}
	out.Rooms = names
	s.tellLandlord(ctx, inv, userID, NotifyAccepted, names)
	return out, nil
}

// Decline turns the invitation down. The landlord is told.
func (s *Answers) Decline(ctx context.Context, userID uuid.UUID, ref Ref) error {
	var (
		inv   domain.Invitation
		names []string
	)
	err := s.Tx.WithinTx(ctx, func(ctx context.Context) error {
		var err error
		if inv, _, err = s.open(ctx, userID, ref, true); err != nil {
			return err
		}
		rooms, err := s.Rooms.Names(ctx, inv.RoomIDs)
		if err != nil {
			return err
		}
		names = roomNames(rooms, inv.RoomIDs)
		inv.Answer(domain.InvitationDeclined, userID, s.Clock.Now())
		if err := s.Invitations.Save(ctx, inv); err != nil {
			return err
		}
		for _, r := range inv.RoomIDs {
			if err := s.Audit.Record(ctx, AuditEvent{
				Action: AuditDeclined, ActorID: userID, RoomID: r, OwnerID: inv.LandlordID, TargetUserID: &userID,
				InvitationID: inv.ID, Meta: map[string]any{"invitation_id": inv.ID.String()},
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.tellLandlord(ctx, inv, userID, NotifyDeclined, names)
	return nil
}

// tellLandlord sends the landlord one message for all the rooms of an answered invitation.
func (s *Answers) tellLandlord(ctx context.Context, inv domain.Invitation, userID uuid.UUID, kind string, names []string) {
	params := map[string]string{"count": strconv.Itoa(len(inv.RoomIDs)), "rooms": domain.NamesLabel(names), "invitation_id": inv.ID.String()}
	if p, err := s.Invitees.Person(ctx, userID); err == nil {
		params["manager"] = p.FullName
	}
	s.notify(ctx, inv.LandlordID, kind, AudienceLandlord, "/managers", params)
}

// Inbox lists the open invitations addressed to the account, newest first, and how many there are in all.
func (s *Answers) Inbox(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.InvitationOffer, int, error) {
	return s.Invitations.Inbox(ctx, userID, s.Clock.Now(), limit, offset)
}
