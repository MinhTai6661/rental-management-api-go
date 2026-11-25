package manager

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/domain/invitation"
	domain "rental-management/internal/domain/manager"
)

// Invitations is the landlord's side of the invitations to manage rooms: make one, list and read them, copy the link
// again, make a new link, withdraw. An invitation is for one account, for an address or for anybody (see
// invitation.Audience); the person it is for accepts it and the rooms are handed over then.
type Invitations struct{ *core }

// NewInvitations builds the landlord's invitation use cases.
func NewInvitations(d Deps, o Options) *Invitations { return &Invitations{newCore(d, o)} }

// CreateInput is a landlord's invitation: the rooms, what the manager may do on them and who it is for.
type CreateInput struct {
	RoomIDs []uuid.UUID
	// ReplaceRoomIDs are rooms (among RoomIDs) that are in an invitation still waiting: they leave that invitation.
	ReplaceRoomIDs []uuid.UUID
	Capabilities   []domain.Capability
	// NotifyLandlord defaults to true at the edge; the use case takes it as given.
	NotifyLandlord bool
	// RecipientID names the account it is for; Email an address (an account with that address is named instead).
	RecipientID *uuid.UUID
	Email       *string
}

// Delivery tells how the invitation reached the person it is for.
type Delivery struct {
	App   bool
	Email bool
}

// Link is the link of an invitation and when it stops working.
type Link struct {
	URL       string
	ExpiresAt time.Time
}

// Issued is an invitation just made, with its link (shown now; later the landlord copies it again).
type Issued struct {
	Invitation domain.InvitationDetail
	Link       Link
	// Delivery is empty for a renewed link, which is not sent anywhere.
	Delivery Delivery
	// Replaced are the waiting invitations that gave up rooms to this one.
	Replaced []uuid.UUID
}

// withdrawn is a recipient of an old invitation that lost rooms to a new one.
type withdrawn struct {
	to    uuid.UUID
	count int
}

// Create makes an invitation and sends it: to the inbox and the email of the account it names, by email to an address,
// and nowhere for an invitation to anybody (the landlord passes the link on). Rooms must be the landlord's and free
// (no manager, not in a waiting invitation unless they are named in ReplaceRoomIDs, which takes them out of that
// invitation). Sending happens after the invitation is stored and never undoes it.
func (s *Invitations) Create(ctx context.Context, landlordID uuid.UUID, in CreateInput) (Issued, error) {
	roomIDs := distinct(in.RoomIDs)
	if len(roomIDs) == 0 {
		return Issued{}, domain.ErrNoRooms
	}
	replace := distinct(in.ReplaceRoomIDs)
	for _, id := range replace {
		if !slices.Contains(roomIDs, id) {
			return Issued{}, domain.ErrReplaceNotListed
		}
	}
	addressee, err := s.Invitees.Resolve(ctx, landlordID, in.RecipientID, in.Email)
	if err != nil {
		return Issued{}, err
	}

	var (
		id       uuid.UUID
		token    string
		replaced []uuid.UUID
		losers   []withdrawn
	)
	err = s.Tx.WithinTx(ctx, func(ctx context.Context) error {
		rooms, err := s.Rooms.Lock(ctx, landlordID, roomIDs)
		if err != nil {
			return err
		}
		if len(rooms) != len(roomIDs) {
			return domain.ErrRoomNotOwned
		}
		managed, err := s.Assignments.ManagedRooms(ctx, roomIDs)
		if err != nil {
			return err
		}
		if len(managed) > 0 {
			return &domain.RoomsError{Err: domain.ErrRoomsManaged, Names: namesOf(managed)}
		}
		if replaced, losers, err = s.takeFromWaiting(ctx, landlordID, rooms, replace); err != nil {
			return err
		}
		hash, sealed, err := s.newSecret(&token)
		if err != nil {
			return err
		}
		caps := domain.Set(in.Capabilities)
		inv := domain.Invitation{
			LandlordID: landlordID, Status: domain.InvitationPending, TokenHash: hash, Sealed: sealed,
			ExpiresAt: s.Clock.Now().Add(s.opts.InvitationTTL), NotifyLandlord: in.NotifyLandlord, Capabilities: caps,
			RoomIDs: roomIDs, RecipientEmail: addressee.Email,
		}
		if addressee.Recipient != nil {
			inv.RecipientID = &addressee.Recipient.ID
		}
		if id, err = s.Invitations.Insert(ctx, inv); err != nil {
			return err
		}
		meta := map[string]any{
			"invitation_id": id.String(), "capabilities": domain.Strings(caps), "notify_landlord": in.NotifyLandlord,
			"replaced_invitation_ids": uuidStrings(replaced),
		}
		for _, r := range roomIDs {
			if err := s.Audit.Record(ctx, AuditEvent{
				Action: AuditInvited, ActorID: landlordID, RoomID: r, OwnerID: landlordID, TargetUserID: inv.RecipientID,
				InvitationID: id, Meta: meta,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Issued{}, err
	}
	d, err := s.Invitations.Get(ctx, landlordID, id)
	if err != nil {
		return Issued{}, notFound(err)
	}
	landlordName := s.landlordName(ctx, landlordID)
	for _, l := range losers {
		s.notify(ctx, l.to, NotifyInvitationWithdrawn, AudienceTenant, "/my-rooms",
			map[string]string{"landlord": landlordName, "count": strconv.Itoa(l.count)})
	}
	return Issued{
		Invitation: d, Link: Link{URL: s.invitationURL("token", token), ExpiresAt: d.ExpiresAt},
		Delivery: s.deliver(ctx, d, landlordName, addressee, token), Replaced: replaced,
	}, nil
}

// takeFromWaiting deals with the rooms that sit in an invitation still waiting: such a room is refused unless it is
// named in replace, in which case it leaves that invitation (which is withdrawn when no room is left in it). It returns
// the invitations that gave up rooms and who was told of it.
func (s *Invitations) takeFromWaiting(ctx context.Context, landlordID uuid.UUID, rooms []RoomInfo, replace []uuid.UUID) ([]uuid.UUID, []withdrawn, error) {
	ids := make([]uuid.UUID, len(rooms))
	for i, r := range rooms {
		ids[i] = r.ID
	}
	now := s.Clock.Now()
	open, err := s.Invitations.LockOpenOfRooms(ctx, ids, now)
	if err != nil {
		return nil, nil, err
	}
	var blocked []RoomInfo
	for _, r := range rooms {
		if slices.Contains(replace, r.ID) {
			continue
		}
		if slices.ContainsFunc(open, func(inv domain.Invitation) bool { return slices.Contains(inv.RoomIDs, r.ID) }) {
			blocked = append(blocked, r)
		}
	}
	if len(blocked) > 0 {
		return nil, nil, &domain.RoomsError{Err: domain.ErrRoomsPending, Names: namesOf(blocked)}
	}
	var (
		gave   []uuid.UUID
		losers []withdrawn
	)
	for _, inv := range open {
		taken := make([]uuid.UUID, 0, len(inv.RoomIDs))
		for _, r := range inv.RoomIDs {
			if slices.Contains(replace, r) {
				taken = append(taken, r)
			}
		}
		if len(taken) == 0 {
			continue
		}
		if err := s.Invitations.RemoveRooms(ctx, inv.ID, taken); err != nil {
			return nil, nil, err
		}
		if len(taken) == len(inv.RoomIDs) {
			if err := inv.Revoke(now); err != nil {
				return nil, nil, err
			}
			if err := s.Invitations.Save(ctx, inv); err != nil {
				return nil, nil, err
			}
		}
		for _, r := range taken {
			if err := s.Audit.Record(ctx, AuditEvent{
				Action: AuditInvitationRevoked, ActorID: landlordID, RoomID: r, OwnerID: landlordID, TargetUserID: inv.RecipientID,
				InvitationID: inv.ID, Meta: map[string]any{"invitation_id": inv.ID.String(), "replaced": true},
			}); err != nil {
				return nil, nil, err
			}
		}
		gave = append(gave, inv.ID)
		if inv.RecipientID != nil {
			losers = append(losers, withdrawn{to: *inv.RecipientID, count: len(taken)})
		}
	}
	return gave, losers, nil
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// landlordName is how the landlord is known to the people they invite.
func (c *core) landlordName(ctx context.Context, landlordID uuid.UUID) string {
	p, err := c.Invitees.Person(ctx, landlordID)
	if err != nil {
		return ""
	}
	return p.FullName
}

// List returns a page of the landlord's invitations, open ones first, and how many match.
func (s *Invitations) List(ctx context.Context, landlordID uuid.UUID, status *domain.InvitationStatus, limit, offset int) ([]domain.InvitationDetail, int, error) {
	return s.Invitations.List(ctx, landlordID, status, limit, offset)
}

// Get returns one of the landlord's invitations; domain.ErrNotFound for anybody else's.
func (s *Invitations) Get(ctx context.Context, landlordID, id uuid.UUID) (domain.InvitationDetail, error) {
	d, err := s.Invitations.Get(ctx, landlordID, id)
	return d, notFound(err)
}

// Link returns the link the landlord made, the same one every time, while the invitation waits. Anything that stops it
// being shown (answered, withdrawn, expired, made before links were kept, a secret that cannot be opened) is
// domain.ErrLinkUnavailable: make a new link then.
func (s *Invitations) Link(ctx context.Context, landlordID, id uuid.UUID) (Link, error) {
	d, err := s.Get(ctx, landlordID, id)
	if err != nil {
		return Link{}, err
	}
	if s.Sealer == nil || !d.LinkAvailable(s.Clock.Now()) {
		return Link{}, domain.ErrLinkUnavailable
	}
	token, err := s.Sealer.Open(*d.Sealed)
	if err != nil {
		return Link{}, domain.ErrLinkUnavailable
	}
	return Link{URL: s.invitationURL("token", token), ExpiresAt: d.ExpiresAt}, nil
}

// Regenerate makes a new link and a new end for a waiting invitation (an expired one too): the old link stops working.
// Nothing is sent again; the landlord passes the new link on.
func (s *Invitations) Regenerate(ctx context.Context, landlordID, id uuid.UUID) (Issued, error) {
	var token string
	err := s.Tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.Invitations.Get(ctx, landlordID, id); err != nil {
			return notFound(err)
		}
		inv, err := s.Invitations.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		hash, sealed, err := s.newSecret(&token)
		if err != nil {
			return err
		}
		if err := inv.Renew(hash, sealed, s.Clock.Now().Add(s.opts.InvitationTTL)); err != nil {
			return err
		}
		return s.Invitations.Save(ctx, inv)
	})
	if err != nil {
		return Issued{}, err
	}
	d, err := s.Invitations.Get(ctx, landlordID, id)
	if err != nil {
		return Issued{}, notFound(err)
	}
	return Issued{Invitation: d, Link: Link{URL: s.invitationURL("token", token), ExpiresAt: d.ExpiresAt}}, nil
}

// Revoke withdraws an invitation that has not been answered: its link stops working.
func (s *Invitations) Revoke(ctx context.Context, landlordID, id uuid.UUID) error {
	return s.Tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.Invitations.Get(ctx, landlordID, id); err != nil {
			return notFound(err)
		}
		inv, err := s.Invitations.ForUpdate(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if err := inv.Revoke(s.Clock.Now()); err != nil {
			return err
		}
		if err := s.Invitations.Save(ctx, inv); err != nil {
			return err
		}
		for _, r := range inv.RoomIDs {
			if err := s.Audit.Record(ctx, AuditEvent{
				Action: AuditInvitationRevoked, ActorID: landlordID, RoomID: r, OwnerID: landlordID, TargetUserID: inv.RecipientID,
				InvitationID: inv.ID, Meta: map[string]any{"invitation_id": inv.ID.String()},
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// deliver sends the invitation to the person it is for and says how. An account gets it in its inbox and by email to the
// address on file, with a link that carries no secret (the account signs in to answer); an address gets an email with the
// link; an invitation to anybody is sent nowhere. A failure is logged and never undoes the invitation.
func (s *Invitations) deliver(ctx context.Context, d domain.InvitationDetail, landlord string, a invitation.Addressee, token string) Delivery {
	var out Delivery
	rooms := make([]string, len(d.Rooms))
	for i, r := range d.Rooms {
		rooms[i] = r.Name
	}
	switch {
	case a.Recipient != nil:
		s.notify(ctx, a.Recipient.ID, NotifyInvitationReceived, AudienceTenant, "/manager-invitation?invitation="+d.ID.String(),
			map[string]string{"landlord": landlord, "count": strconv.Itoa(len(rooms)), "invitation_id": d.ID.String()})
		out.App = true
		if a.Recipient.Email != nil {
			out.Email = s.mail(ctx, d, *a.Recipient.Email, landlord, rooms, s.invitationURL("invitation", d.ID.String()))
		}
	case a.Email != nil:
		out.Email = s.mail(ctx, d, *a.Email, landlord, rooms, s.invitationURL("token", token))
	}
	return out
}

// mail emails the invitation unless the address asked for no more of them; it says whether an email went out.
func (s *Invitations) mail(ctx context.Context, d domain.InvitationDetail, to, landlord string, rooms []string, link string) bool {
	may, err := s.Invitees.MayMail(ctx, to)
	if err != nil {
		s.Log.Warn("check whether an invitation may be mailed", "error", err, "invitation_id", d.ID)
		return false
	}
	if !may {
		return false
	}
	if err := s.Mailer.SendInvitation(ctx, InvitationMail{To: to, Landlord: landlord, Rooms: rooms, URL: link, ExpiresAt: d.ExpiresAt}); err != nil {
		s.Log.Warn("email an invitation", "error", err, "invitation_id", d.ID)
		return false
	}
	return true
}
