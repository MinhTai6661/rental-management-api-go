package contract

import (
	"context"
	"strconv"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/invitation"
)

// Approve opens an offer a manager wrote: from now on it waits for an answer like any other, under a new secret and a
// new end, and goes to the person it is for (an offer to anybody is sent nowhere: the manager asks for a new link).
// Only the landlord of the room can; the offer must still be waiting for them, and a room a tenant holds in the
// meantime refuses an offer that would make a running contract.
func (s *Invitations) Approve(ctx context.Context, landlordID, id uuid.UUID, note *string) (IssuedInvitation, error) {
	before, err := s.invitations.Get(ctx, landlordID, id)
	if err != nil {
		return IssuedInvitation{}, notFound(err)
	}
	var token string
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.lockRoom(ctx, before.RoomID); err != nil {
			return err
		}
		inv, err := s.invitations.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if inv.Status != domain.InvitationAwaitingApproval {
			return domain.ErrInvitationNotAwaiting
		}
		if inv.Terms.ContractStatus == domain.StatusActive {
			occ, err := s.contracts.Occupancy(ctx, inv.RoomID)
			if err != nil {
				return err
			}
			if occ.WithTenant > 0 {
				return domain.ErrRoomOccupied
			}
		}
		hash, sealed, err := s.newSecret(&token)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		if err := inv.Approve(landlordID, now, note, hash, sealed, now.Add(s.opts.InvitationTTL)); err != nil {
			return err
		}
		if err := s.invitations.Save(ctx, inv); err != nil {
			return err
		}
		return s.recordInvitation(ctx, AuditInvitationApproved, landlordID, inv, nil)
	})
	if err != nil {
		return IssuedInvitation{}, err
	}
	d, err := s.invitations.Get(ctx, landlordID, id)
	if err != nil {
		return IssuedInvitation{}, notFound(err)
	}
	addressee := invitation.Addressee{Email: d.RecipientEmail}
	if d.RecipientID != nil {
		if p, err := s.invitees.Person(ctx, *d.RecipientID); err == nil {
			addressee = invitation.Addressee{Recipient: &p}
		} else {
			s.log.Warn("find who an approved offer is for", "error", err, "invitation_id", id)
		}
	}
	delivery := s.deliver(ctx, d, addressee, token)
	if d.CreatedByID != nil {
		sent := addressee.Recipient != nil || addressee.Email != nil
		s.tell(ctx, *d.CreatedByID, NotifyInvitationApproved, map[string]string{
			"room": d.RoomName, "invitation_id": d.ID.String(), "sent": strconv.FormatBool(sent),
		}, "/contracts")
	}
	return IssuedInvitation{Invitation: d, Link: InvitationLink{URL: s.invitationURL("token", token), ExpiresAt: d.ExpiresAt}, Delivery: delivery}, nil
}

// Reject turns an offer a manager wrote down with the landlord's reason: it is withdrawn and the manager is told why.
func (s *Invitations) Reject(ctx context.Context, landlordID, id uuid.UUID, reason string) error {
	before, err := s.invitations.Get(ctx, landlordID, id)
	if err != nil {
		return notFound(err)
	}
	var author *uuid.UUID
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.lockRoom(ctx, before.RoomID); err != nil {
			return err
		}
		inv, err := s.invitations.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if err := inv.Reject(landlordID, s.clock.Now(), reason); err != nil {
			return err
		}
		if err := s.invitations.Save(ctx, inv); err != nil {
			return err
		}
		author = inv.CreatedByID
		return s.recordInvitation(ctx, AuditInvitationRejected, landlordID, inv, map[string]any{"reason": reason})
	})
	if err != nil {
		return err
	}
	if author != nil {
		s.tell(ctx, *author, NotifyInvitationRejected, map[string]string{
			"room": before.RoomName, "invitation_id": id.String(), "reason": reason,
		}, "/contracts")
	}
	return nil
}

// tell sends a message to a manager about an offer; a failure is logged.
func (c *core) tell(ctx context.Context, to uuid.UUID, kind string, params map[string]string, url string) {
	if err := c.notifier.Notify(ctx, Notification{UserID: to, Kind: kind, Params: params, URL: url}); err != nil {
		c.log.Warn("notify a manager about an offer", "error", err, "kind", kind)
	}
}

// ManagerLinks is what the manager context asks of offers when a manager loses their rooms.
type ManagerLinks struct{ *core }

// NewManagerLinks builds the use case the manager bridge calls.
func NewManagerLinks(d Deps, o Options) *ManagerLinks { return &ManagerLinks{newCore(d, o)} }

// WithdrawAwaiting withdraws every offer the manager wrote that still waits for the landlord: a manager with no rooms
// left cannot have offers pending. It runs in the transaction of the hand-over being ended.
func (l *ManagerLinks) WithdrawAwaiting(ctx context.Context, managerID uuid.UUID) error {
	withdrawn, err := l.invitations.RevokeAwaitingOfAuthor(ctx, managerID, l.clock.Now())
	if err != nil {
		return err
	}
	for _, inv := range withdrawn {
		if err := l.recordInvitation(ctx, AuditInvitationRevoked, uuid.Nil, inv, map[string]any{"reason": "manager_lost_rooms"}); err != nil {
			return err
		}
	}
	return nil
}
