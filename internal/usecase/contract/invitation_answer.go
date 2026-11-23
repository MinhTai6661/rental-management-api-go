package contract

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/invitation"
	"rental-management/internal/domain/shared"
)

// InvitationAnswers is the invited person's side of the offers of rooms by link: see an offer, accept it (the contract
// is made then, under the rules of every contract) or decline it, and list the offers addressed to the account. Every
// link or id that cannot be used by the caller (wrong, answered, withdrawn, expired, for somebody else, room gone)
// gives the one error domain.ErrInvitationInvalid, so an offer tells nothing to whoever it is not for.
type InvitationAnswers struct{ *core }

// NewInvitationAnswers builds the invited person's use cases.
func NewInvitationAnswers(d Deps, o Options) *InvitationAnswers {
	return &InvitationAnswers{newCore(d, o)}
}

// InvitationRef names the invitation to answer: by the secret of its link (which wins when both are given), or by the
// id of an invitation addressed to the caller's account.
type InvitationRef struct {
	Token        *string
	InvitationID *uuid.UUID
}

// open finds the invitation the caller may answer. With lock, the room and then the invitation are locked (the order of
// every transaction touching both) and the invitation is read again under the lock, so what was checked still holds.
// The room is the zero value without lock.
func (s *InvitationAnswers) open(ctx context.Context, userID uuid.UUID, ref InvitationRef, lock bool) (domain.Invitation, domain.RoomFacts, error) {
	inv, err := s.find(ctx, userID, ref)
	if err != nil {
		return domain.Invitation{}, domain.RoomFacts{}, err
	}
	var room domain.RoomFacts
	if lock {
		if room, err = s.rooms.Lock(ctx, inv.RoomID); err != nil {
			return domain.Invitation{}, domain.RoomFacts{}, invalidIfMissing(err)
		}
		if !room.Live {
			return domain.Invitation{}, domain.RoomFacts{}, domain.ErrInvitationInvalid
		}
		if inv, err = s.invitations.ForUpdate(ctx, inv.ID); err != nil {
			return domain.Invitation{}, domain.RoomFacts{}, invalidIfMissing(err)
		}
	}
	if !inv.OpenAt(s.clock.Now()) {
		return domain.Invitation{}, domain.RoomFacts{}, domain.ErrInvitationInvalid
	}
	p, err := s.invitees.Person(ctx, userID)
	if err != nil {
		return domain.Invitation{}, domain.RoomFacts{}, invalidIfMissing(err)
	}
	if !invitation.Allows(inv.RecipientID, inv.RecipientEmail, userID, p.Email, p.EmailVerified) {
		return domain.Invitation{}, domain.RoomFacts{}, domain.ErrInvitationInvalid
	}
	return inv, room, nil
}

// find reads the invitation a reference points to, without any lock.
func (s *InvitationAnswers) find(ctx context.Context, userID uuid.UUID, ref InvitationRef) (domain.Invitation, error) {
	var (
		inv domain.Invitation
		err error
	)
	switch {
	case ref.Token != nil:
		if !domain.ValidLinkToken(*ref.Token) {
			return domain.Invitation{}, domain.ErrInvitationInvalid
		}
		inv, err = s.invitations.FindByTokenHash(ctx, s.tokens.Hash(*ref.Token))
	case ref.InvitationID != nil:
		inv, err = s.invitations.FindForRecipient(ctx, *ref.InvitationID, userID)
	default:
		return domain.Invitation{}, domain.ErrInvitationInvalid
	}
	return inv, invalidIfMissing(err)
}

// invalidIfMissing turns "no such row" into the one answer for every unusable invitation.
func invalidIfMissing(err error) error {
	if errors.Is(err, shared.ErrNotFound) || errors.Is(err, domain.ErrNotFound) {
		return domain.ErrInvitationInvalid
	}
	return err
}

// Preview shows the offer to the person it is for, before they answer. Nothing changes.
func (s *InvitationAnswers) Preview(ctx context.Context, userID uuid.UUID, ref InvitationRef) (domain.InvitationOffer, error) {
	inv, _, err := s.open(ctx, userID, ref, false)
	if err != nil {
		return domain.InvitationOffer{}, err
	}
	offer, err := s.invitations.Offer(ctx, inv.ID)
	return offer, invalidIfMissing(err)
}

// Accept makes the contract the invitation offers, for the caller: the same admission as any contract (the room must be
// free, the landlord's plan limit and the room's minimum period hold...). Anything that refuses it rolls the whole
// transaction back, so the invitation stays open. The landlord is told; the caller sees their new contract.
func (s *InvitationAnswers) Accept(ctx context.Context, userID uuid.UUID, ref InvitationRef) (domain.Detail, error) {
	var (
		contractID uuid.UUID
		status     domain.Status
	)
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		inv, room, err := s.open(ctx, userID, ref, true)
		if err != nil {
			return err
		}
		if inv.AuthoredBy(userID) {
			return domain.ErrInvitationOwnOffer
		}
		t := inv.Terms
		status = t.ContractStatus
		k := domain.Contract{
			TenantID: &userID, Status: t.ContractStatus, PriceVND: t.PriceVND, DepositVND: t.DepositVND,
			OccupantCount: t.OccupantCount, StartsOn: t.StartsOn, EndsOn: t.EndsOn, Notes: t.Note,
		}
		stored, err := s.admit(ctx, room, k, admission{
			actorID: userID, action: AuditCreated,
			meta: map[string]any{
				"origin": "invitation", "invitation_id": inv.ID.String(), "status": string(t.ContractStatus), "price_vnd": t.PriceVND,
			},
		})
		if err != nil {
			return err
		}
		contractID = stored.ID
		now := s.clock.Now()
		inv.Answer(domain.InvitationAccepted, userID, now, &stored.ID)
		if err := s.invitations.Save(ctx, inv); err != nil {
			return err
		}
		return s.audit.Record(ctx, AuditEvent{
			Action: AuditInvitationAccepted, ActorID: userID, ContractID: stored.ID, InvitationID: inv.ID, RoomID: inv.RoomID,
			OwnerID: inv.LandlordID, TargetUserID: &userID,
		})
	})
	if err != nil {
		return domain.Detail{}, admitError(err, status)
	}
	d, err := s.detail(ctx, domain.Viewer{UserID: userID}, contractID)
	if err != nil {
		return domain.Detail{}, err
	}
	// The landlord hears of the contract; the tenant just made it and is not told.
	s.notify(ctx, d, NotifyInvitationAccepted, true, nil)
	return d, nil
}

// Decline turns the offer down. The landlord is told.
func (s *InvitationAnswers) Decline(ctx context.Context, userID uuid.UUID, ref InvitationRef) error {
	var inv domain.Invitation
	var room domain.RoomFacts
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		var err error
		if inv, room, err = s.open(ctx, userID, ref, true); err != nil {
			return err
		}
		inv.Answer(domain.InvitationDeclined, userID, s.clock.Now(), nil)
		if err := s.invitations.Save(ctx, inv); err != nil {
			return err
		}
		return s.audit.Record(ctx, AuditEvent{
			Action: AuditInvitationDeclined, ActorID: userID, InvitationID: inv.ID, RoomID: inv.RoomID, OwnerID: inv.LandlordID,
			TargetUserID: &userID,
		})
	})
	if err != nil {
		return err
	}
	params := map[string]string{"room": room.Name, "invitation_id": inv.ID.String()}
	if p, err := s.invitees.Person(ctx, userID); err == nil {
		params["tenant"] = p.FullName
	}
	if err := s.notifier.Notify(ctx, Notification{
		UserID: inv.LandlordID, Kind: NotifyInvitationDeclined, Params: params, URL: "/contracts", Audience: AudienceLandlord,
	}); err != nil {
		s.log.Warn("notify about a declined invitation", "error", err, "invitation_id", inv.ID)
	}
	return nil
}

// Inbox lists the open offers addressed to the account, newest first, and how many there are in all.
func (s *InvitationAnswers) Inbox(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.InvitationOffer, int, error) {
	return s.invitations.Inbox(ctx, userID, s.clock.Now(), limit, offset)
}
