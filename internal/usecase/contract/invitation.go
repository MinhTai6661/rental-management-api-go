package contract

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/invitation"
	"rental-management/internal/domain/shared"
)

// Invitations is the landlord's side of the offers of rooms by link: make one, list and read them, copy the link again,
// make a new link, withdraw. An offer is for one account, for an address or for anybody (see invitation.Audience); the
// person it is for accepts it and the contract is made then.
type Invitations struct{ *core }

// NewInvitations builds the landlord's invitation use cases.
func NewInvitations(d Deps, o Options) *Invitations { return &Invitations{newCore(d, o)} }

// CreateInvitationInput is a landlord's offer of a room. A nil PriceVND is the room's price, a zero OccupantCount is
// one, an empty ContractStatus is active (the contract runs as soon as it is accepted).
type CreateInvitationInput struct {
	RoomID         uuid.UUID
	PriceVND       *int64
	DepositVND     *int64
	OccupantCount  int
	StartsOn       time.Time
	EndsOn         *time.Time
	ContractStatus domain.Status
	Note           *string
	// RecipientID names the account it is for; Email an address (an account with that address is named instead).
	RecipientID *uuid.UUID
	Email       *string
}

// Delivery tells how the invitation reached the person it is for: App is true when it sits in the account's inbox,
// Email when an email went out.
type Delivery struct {
	App   bool
	Email bool
}

// InvitationLink is the link of an invitation and when it stops working.
type InvitationLink struct {
	URL       string
	ExpiresAt time.Time
}

// IssuedInvitation is an invitation just made, with its link (shown now; later the landlord copies it again).
type IssuedInvitation struct {
	Invitation domain.InvitationDetail
	Link       InvitationLink
	// Delivery is empty for a renewed link, which is not sent anywhere.
	Delivery Delivery
}

// Create makes an offer and sends it: to the inbox and the email of the account it names, by email to an address, and
// nowhere for an offer to anybody (the landlord passes the link on). Sending happens after the offer is stored and
// never undoes it.
func (s *Invitations) Create(ctx context.Context, actorID uuid.UUID, in CreateInvitationInput) (IssuedInvitation, error) {
	if in.OccupantCount == 0 {
		in.OccupantCount = 1
	}
	if in.OccupantCount < 1 || in.OccupantCount > domain.MaxOccupants {
		return IssuedInvitation{}, domain.ErrOccupantsInvalid
	}
	if err := domain.CheckDates(in.StartsOn, in.EndsOn); err != nil {
		return IssuedInvitation{}, err
	}
	if in.ContractStatus == "" {
		in.ContractStatus = domain.StatusActive
	}
	if !domain.ValidOfferStatus(in.ContractStatus) {
		return IssuedInvitation{}, domain.ErrInvalidTransition
	}
	addressee, err := s.invitees.Resolve(ctx, actorID, in.RecipientID, in.Email)
	if err != nil {
		return IssuedInvitation{}, err
	}

	var (
		id            uuid.UUID
		token         string
		needsApproval bool
		createdBy     *uuid.UUID
		landlordID    uuid.UUID
	)
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		room, err := s.rooms.Lock(ctx, in.RoomID)
		if err != nil {
			if errors.Is(err, shared.ErrNotFound) {
				return domain.ErrRoomNotFound
			}
			return err
		}
		if !room.Live {
			return domain.ErrRoomNotFound
		}
		if needsApproval, createdBy, err = s.author(ctx, actorID, room); err != nil {
			return err
		}
		if addressee.Recipient != nil && addressee.Recipient.ID == room.LandlordID && actorID != room.LandlordID {
			return invitation.ErrRecipientInvalid // a manager does not offer the landlord's own room to the landlord
		}
		if in.ContractStatus == domain.StatusActive {
			occ, err := s.contracts.Occupancy(ctx, room.ID)
			if err != nil {
				return err
			}
			if occ.WithTenant > 0 {
				return domain.ErrRoomOccupied
			}
		}
		if err := domain.CheckMinimumPeriod(in.StartsOn, in.EndsOn, room.MinPeriod, room.PeriodUnit); err != nil {
			return err
		}
		hash, sealed, err := s.newSecret(&token)
		if err != nil {
			return err
		}
		status := domain.InvitationPending
		if needsApproval {
			// Nothing is sent and no link exists until the landlord approves: the secret is not kept.
			status, sealed = domain.InvitationAwaitingApproval, nil
		}
		landlordID = room.LandlordID
		price := room.PriceVND
		if in.PriceVND != nil {
			price = *in.PriceVND
		}
		inv := domain.Invitation{
			RoomID: room.ID, LandlordID: room.LandlordID, CreatedByID: createdBy, Status: status, TokenHash: hash, Sealed: sealed,
			ExpiresAt: s.clock.Now().Add(s.opts.InvitationTTL),
			Terms: domain.InvitationTerms{
				PriceVND: price, DepositVND: in.DepositVND, OccupantCount: in.OccupantCount, StartsOn: in.StartsOn,
				EndsOn: in.EndsOn, ContractStatus: in.ContractStatus, Note: in.Note,
			},
			RecipientEmail: addressee.Email,
		}
		if addressee.Recipient != nil {
			inv.RecipientID = &addressee.Recipient.ID
		}
		if id, err = s.invitations.Insert(ctx, inv); err != nil {
			return err
		}
		inv.ID = id
		meta := map[string]any{
			"audience": string(inv.Audience()), "price_vnd": price, "starts_on": in.StartsOn.Format(domain.DateLayout),
			"contract_status": string(in.ContractStatus),
		}
		if needsApproval {
			meta["awaiting_approval"] = true
		}
		return s.recordInvitation(ctx, AuditInvitationCreated, actorID, inv, meta)
	})
	if err != nil {
		return IssuedInvitation{}, err
	}
	d, err := s.visible(ctx, actorID, id)
	if err != nil {
		return IssuedInvitation{}, err
	}
	if needsApproval {
		s.askApproval(ctx, actorID, landlordID, d)
		return IssuedInvitation{Invitation: d}, nil
	}
	link := InvitationLink{URL: s.invitationURL("token", token), ExpiresAt: d.ExpiresAt}
	return IssuedInvitation{Invitation: d, Link: link, Delivery: s.deliver(ctx, d, addressee, token)}, nil
}

// author decides who may write an offer on the room and whether it needs the landlord's approval before it goes out.
// The landlord writes freely. A manager needs contracts.propose on that very room (a room that is not theirs to
// manage is answered like a room that does not exist), and their offer waits for the landlord unless they also hold
// contracts.create.
func (s *Invitations) author(ctx context.Context, actorID uuid.UUID, room domain.RoomFacts) (needsApproval bool, createdBy *uuid.UUID, err error) {
	if room.LandlordID == actorID {
		return false, nil, nil
	}
	g, err := s.managers.Grant(ctx, actorID, room.ID)
	if err != nil {
		return false, nil, err
	}
	if !g.Propose {
		return false, nil, domain.ErrRoomNotFound
	}
	return !g.Create, &actorID, nil
}

// visible returns an invitation the caller may see: one of the landlord's own, or one a manager wrote.
func (s *Invitations) visible(ctx context.Context, actorID, id uuid.UUID) (domain.InvitationDetail, error) {
	d, err := s.invitations.Get(ctx, actorID, id)
	if errors.Is(err, shared.ErrNotFound) {
		d, err = s.invitations.GetAuthored(ctx, actorID, id)
	}
	return d, notFound(err)
}

// askApproval tells the landlord that a manager's offer waits for them. A failure is logged and never undoes the offer.
func (s *Invitations) askApproval(ctx context.Context, managerID, landlordID uuid.UUID, d domain.InvitationDetail) {
	params := map[string]string{"room": d.RoomName, "invitation_id": d.ID.String()}
	if p, err := s.invitees.Person(ctx, managerID); err == nil {
		params["manager"] = p.FullName
	}
	if err := s.notifier.Notify(ctx, Notification{
		UserID: landlordID, Kind: NotifyInvitationNeedsApproval, Params: params, URL: "/contracts?invitation=" + d.ID.String(),
		Audience: AudienceLandlord,
	}); err != nil {
		s.log.Warn("notify about an offer that needs approval", "error", err, "invitation_id", d.ID)
	}
}

// newSecret makes the secret of a link: its hash, and the encrypted copy when the links are kept. The token is stored
// in *token for the caller to put into the link.
func (s *Invitations) newSecret(token *string) (hash string, sealed *string, err error) {
	*token, hash, err = s.tokens.New()
	if err != nil {
		return "", nil, err
	}
	if s.sealer == nil {
		return hash, nil, nil
	}
	box, err := s.sealer.Seal(*token)
	if err != nil {
		return "", nil, err
	}
	return hash, &box, nil
}

// List returns a page of the landlord's invitations, open ones first, and how many match.
func (s *Invitations) List(ctx context.Context, landlordID uuid.UUID, status *domain.InvitationStatus, limit, offset int) ([]domain.InvitationDetail, int, error) {
	return s.invitations.List(ctx, landlordID, status, limit, offset)
}

// Get returns one of the landlord's invitations; domain.ErrNotFound for anybody else's.
func (s *Invitations) Get(ctx context.Context, actorID, id uuid.UUID) (domain.InvitationDetail, error) {
	return s.visible(ctx, actorID, id)
}

// ListAuthored returns a page of the offers a manager wrote (while they still manage the rooms), open ones first.
func (s *Invitations) ListAuthored(ctx context.Context, authorID uuid.UUID, status *domain.InvitationStatus, limit, offset int) ([]domain.InvitationDetail, int, error) {
	return s.invitations.ListAuthored(ctx, authorID, status, limit, offset)
}

// Link returns the link the landlord made, the same one every time, while the offer waits. Anything that stops it
// being shown (answered, withdrawn, expired, made before links were kept, a secret that cannot be opened) is
// domain.ErrInvitationLinkUnavailable: make a new link then.
func (s *Invitations) Link(ctx context.Context, landlordID, id uuid.UUID) (InvitationLink, error) {
	d, err := s.Get(ctx, landlordID, id)
	if err != nil {
		return InvitationLink{}, err
	}
	if s.sealer == nil || !d.LinkAvailable(s.clock.Now()) {
		return InvitationLink{}, domain.ErrInvitationLinkUnavailable
	}
	token, err := s.sealer.Open(*d.Sealed)
	if err != nil {
		return InvitationLink{}, domain.ErrInvitationLinkUnavailable
	}
	return InvitationLink{URL: s.invitationURL("token", token), ExpiresAt: d.ExpiresAt}, nil
}

// Regenerate makes a new link and a new end for a waiting invitation (an expired one too): the old link stops working.
// Nothing is sent again; the landlord passes the new link on.
func (s *Invitations) Regenerate(ctx context.Context, landlordID, id uuid.UUID) (IssuedInvitation, error) {
	var token string
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.visible(ctx, landlordID, id); err != nil {
			return err
		}
		inv, err := s.invitations.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		hash, sealed, err := s.newSecret(&token)
		if err != nil {
			return err
		}
		if err := inv.Renew(hash, sealed, s.clock.Now().Add(s.opts.InvitationTTL)); err != nil {
			return err
		}
		if err := s.invitations.Save(ctx, inv); err != nil {
			return err
		}
		return s.recordInvitation(ctx, AuditInvitationRegenerated, landlordID, inv, nil)
	})
	if err != nil {
		return IssuedInvitation{}, err
	}
	d, err := s.visible(ctx, landlordID, id)
	if err != nil {
		return IssuedInvitation{}, err
	}
	return IssuedInvitation{Invitation: d, Link: InvitationLink{URL: s.invitationURL("token", token), ExpiresAt: d.ExpiresAt}}, nil
}

// Revoke withdraws an invitation that has not been answered: its link stops working.
func (s *Invitations) Revoke(ctx context.Context, landlordID, id uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.visible(ctx, landlordID, id); err != nil {
			return err
		}
		inv, err := s.invitations.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if err := inv.Revoke(s.clock.Now()); err != nil {
			return err
		}
		if err := s.invitations.Save(ctx, inv); err != nil {
			return err
		}
		return s.recordInvitation(ctx, AuditInvitationRevoked, landlordID, inv, nil)
	})
}

// recordInvitation writes an audit event about an invitation.
func (c *core) recordInvitation(ctx context.Context, action string, actor uuid.UUID, inv domain.Invitation, meta map[string]any) error {
	return c.audit.Record(ctx, AuditEvent{
		Action: action, ActorID: actor, InvitationID: inv.ID, RoomID: inv.RoomID, OwnerID: inv.LandlordID,
		TargetUserID: inv.RecipientID, Meta: meta,
	})
}

// invitationURL is the page of the web app that shows an invitation; key says how the page finds it: "token" carries
// the secret, "invitation" the id of an invitation addressed to the signed-in account.
func (c *core) invitationURL(key, value string) string {
	return strings.TrimRight(c.opts.WebURL, "/") + "/contract-invitation?" + key + "=" + url.QueryEscape(value)
}

// deliver sends the invitation to the person it is for and says how. An account gets it in its inbox and by email to the
// address on file, with a link that carries no secret (the account signs in to answer); an address gets an email with the
// link; an offer to anybody is sent nowhere. A failure is logged and never undoes the offer.
func (s *Invitations) deliver(ctx context.Context, d domain.InvitationDetail, a invitation.Addressee, token string) Delivery {
	var out Delivery
	switch {
	case a.Recipient != nil:
		n := Notification{
			UserID: a.Recipient.ID, Kind: NotifyInvitationReceived, Audience: AudienceTenant,
			Params: map[string]string{"landlord": d.LandlordName, "room": d.RoomName, "invitation_id": d.ID.String()},
			URL:    "/contract-invitation?invitation=" + d.ID.String(),
		}
		if err := s.notifier.Notify(ctx, n); err != nil {
			s.log.Warn("notify about an invitation", "error", err, "invitation_id", d.ID)
		}
		out.App = true
		if a.Recipient.Email != nil {
			out.Email = s.mail(ctx, d, *a.Recipient.Email, s.invitationURL("invitation", d.ID.String()))
		}
	case a.Email != nil:
		out.Email = s.mail(ctx, d, *a.Email, s.invitationURL("token", token))
	}
	return out
}

// mail emails the invitation unless the address asked for no more of them; it says whether an email went out.
func (s *Invitations) mail(ctx context.Context, d domain.InvitationDetail, to, link string) bool {
	may, err := s.invitees.MayMail(ctx, to)
	if err != nil {
		s.log.Warn("check whether an invitation may be mailed", "error", err, "invitation_id", d.ID)
		return false
	}
	if !may {
		return false
	}
	if err := s.mailer.SendInvitation(ctx, InvitationMail{
		To: to, Landlord: d.LandlordName, Room: d.RoomName, URL: link, ExpiresAt: d.ExpiresAt,
	}); err != nil {
		s.log.Warn("email an invitation", "error", err, "invitation_id", d.ID)
		return false
	}
	return true
}
