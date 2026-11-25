package managerhttp

import (
	"time"

	"github.com/google/uuid"

	"rental-management/internal/domain/invitation"
	domain "rental-management/internal/domain/manager"
	usecase "rental-management/internal/usecase/manager"
)

// PersonView is an account a record names: enough to recognize it, with only a masked email.
type PersonView struct {
	ID          uuid.UUID `json:"id"`
	FullName    string    `json:"full_name"`
	EmailMasked *string   `json:"email_masked"`
}

// RoomView is a room as the landlord and the manager know it.
type RoomView struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Building *string   `json:"building"`
}

// InvitationView is an invitation as its landlord sees it. It never carries the link: that is shown when the
// invitation is made or a new link is asked for, and copied again with GET .../link while link_available.
type InvitationView struct {
	ID             uuid.UUID   `json:"id"`
	Status         string      `json:"status"`
	Expired        bool        `json:"expired"`
	Audience       string      `json:"audience"`
	Rooms          []RoomView  `json:"rooms"`
	Capabilities   []string    `json:"capabilities"`
	NotifyLandlord bool        `json:"notify_landlord"`
	Recipient      *PersonView `json:"recipient"`
	RecipientEmail *string     `json:"recipient_email"`
	ExpiresAt      time.Time   `json:"expires_at"`
	LinkAvailable  bool        `json:"link_available"`
	AnsweredAt     *time.Time  `json:"answered_at"`
	CreatedAt      time.Time   `json:"created_at"`
}

// DeliveryView tells how the invitation reached the person it is for.
type DeliveryView struct {
	App   bool `json:"app"`
	Email bool `json:"email"`
}

// IssuedView is a new invitation with its link, shown now.
type IssuedView struct {
	Invitation            InvitationView `json:"invitation"`
	Link                  string         `json:"link"`
	Delivery              DeliveryView   `json:"delivery"`
	ReplacedInvitationIDs []uuid.UUID    `json:"replaced_invitation_ids"`
}

// LinkView is the link of an invitation, copied again.
type LinkView struct {
	Link      string    `json:"link"`
	ExpiresAt time.Time `json:"expires_at"`
}

func personOf(p *domain.Person) *PersonView {
	if p == nil {
		return nil
	}
	return &PersonView{ID: p.ID, FullName: p.FullName, EmailMasked: invitation.MaskEmail(p.Email)}
}

func roomsOf(rooms []domain.Room) []RoomView {
	out := make([]RoomView, len(rooms))
	for i, r := range rooms {
		out[i] = RoomView{ID: r.ID, Name: r.Name, Building: r.BuildingName}
	}
	return out
}

func invitationViewOf(d domain.InvitationDetail, now time.Time) InvitationView {
	return InvitationView{
		ID: d.ID, Status: string(d.Status), Expired: d.Expired(now), Audience: string(d.Audience()),
		Rooms: roomsOf(d.Rooms), Capabilities: domain.Strings(d.Capabilities), NotifyLandlord: d.NotifyLandlord,
		Recipient: personOf(d.Recipient), RecipientEmail: d.RecipientEmail, ExpiresAt: d.ExpiresAt, LinkAvailable: d.LinkAvailable(now),
		AnsweredAt: d.AnsweredAt, CreatedAt: d.CreatedAt,
	}
}

func invitationViewsOf(items []domain.InvitationDetail, now time.Time) []InvitationView {
	out := make([]InvitationView, len(items))
	for i, d := range items {
		out[i] = invitationViewOf(d, now)
	}
	return out
}

func issuedViewOf(r usecase.Issued, now time.Time) IssuedView {
	replaced := r.Replaced
	if replaced == nil {
		replaced = []uuid.UUID{}
	}
	return IssuedView{
		Invitation: invitationViewOf(r.Invitation, now), Link: r.Link.URL,
		Delivery: DeliveryView{App: r.Delivery.App, Email: r.Delivery.Email}, ReplacedInvitationIDs: replaced,
	}
}

// OfferView is an invitation as the invited person sees it. It says nothing about who else was invited.
type OfferView struct {
	ID             uuid.UUID  `json:"id"`
	Expired        bool       `json:"expired"`
	ExpiresAt      time.Time  `json:"expires_at"`
	Landlord       PersonView `json:"landlord"`
	Rooms          []RoomView `json:"rooms"`
	Capabilities   []string   `json:"capabilities"`
	NotifyLandlord bool       `json:"notify_landlord"`
}

func offerViewOf(o domain.InvitationOffer, now time.Time) OfferView {
	return OfferView{
		ID: o.ID, Expired: o.Expired(now), ExpiresAt: o.ExpiresAt, Landlord: PersonView{ID: o.LandlordID, FullName: o.LandlordName},
		Rooms: roomsOf(o.Rooms), Capabilities: domain.Strings(o.Capabilities), NotifyLandlord: o.NotifyLandlord,
	}
}

func offerViewsOf(items []domain.InvitationOffer, now time.Time) []OfferView {
	out := make([]OfferView, len(items))
	for i, o := range items {
		out[i] = offerViewOf(o, now)
	}
	return out
}

// AssignmentView is one room handed to a manager.
type AssignmentView struct {
	ID             uuid.UUID   `json:"id"`
	Room           RoomView    `json:"room"`
	Manager        *PersonView `json:"manager,omitempty"`
	LandlordName   *string     `json:"landlord_name,omitempty"`
	NotifyLandlord bool        `json:"notify_landlord"`
	Capabilities   []string    `json:"capabilities"`
	AcceptedAt     time.Time   `json:"accepted_at"`
}

func assignmentViewOf(d usecase.AssignmentDetail, forLandlord bool) AssignmentView {
	v := AssignmentView{
		ID: d.ID, Room: RoomView{ID: d.Room.ID, Name: d.Room.Name, Building: d.Room.BuildingName},
		NotifyLandlord: d.NotifyLandlord, Capabilities: domain.Strings(d.Capabilities), AcceptedAt: d.AcceptedAt,
	}
	if forLandlord {
		v.Manager = personOf(&d.Manager)
	} else {
		name := d.LandlordName
		v.LandlordName = &name
	}
	return v
}

func assignmentViewsOf(items []usecase.AssignmentDetail, forLandlord bool) []AssignmentView {
	out := make([]AssignmentView, len(items))
	for i, d := range items {
		out[i] = assignmentViewOf(d, forLandlord)
	}
	return out
}

// GroupFlagsView says whether all, some or none of a manager's rooms notify the landlord.
type GroupFlagsView struct {
	NotifyLandlord string `json:"notify_landlord"`
}

// GroupView is one manager with the sum of what was handed to them. Their rooms are read with ?manager_id=.
type GroupView struct {
	Person       PersonView        `json:"person"`
	Count        int               `json:"count"`
	IDs          []uuid.UUID       `json:"ids"`
	RoomIDs      []uuid.UUID       `json:"room_ids"`
	Flags        GroupFlagsView    `json:"flags"`
	Capabilities map[string]string `json:"capabilities"`
}

func groupViewsOf(items []usecase.GroupView) []GroupView {
	out := make([]GroupView, len(items))
	for i, g := range items {
		caps := make(map[string]string, len(g.Capabilities))
		for code, flag := range g.Capabilities {
			caps[string(code)] = string(flag)
		}
		out[i] = GroupView{
			Person: *personOf(&g.Manager), Count: g.RoomCount, IDs: g.AssignmentIDs, RoomIDs: g.RoomIDs,
			Flags: GroupFlagsView{NotifyLandlord: string(g.Notify)}, Capabilities: caps,
		}
	}
	return out
}

// ManagedGroupView is the rooms one landlord handed to the manager.
type ManagedGroupView struct {
	LandlordID   uuid.UUID   `json:"landlord_id"`
	LandlordName string      `json:"landlord_name"`
	Count        int         `json:"count"`
	RoomIDs      []uuid.UUID `json:"room_ids"`
}

// CapabilityView is a capability a landlord can hand over.
type CapabilityView struct {
	Code     string   `json:"code"`
	Group    string   `json:"group"`
	Risk     string   `json:"risk"`
	Requires []string `json:"requires"`
}

func catalogueView() []CapabilityView {
	cat := domain.Catalogue()
	out := make([]CapabilityView, len(cat))
	for i, c := range cat {
		out[i] = CapabilityView{Code: string(c.Code), Group: c.Group, Risk: string(c.Risk), Requires: domain.Strings(c.Requires)}
	}
	return out
}

// AcceptedView is what accepting handed over.
type AcceptedView struct {
	Assignments []AcceptedAssignmentView `json:"assignments"`
	Rooms       []string                 `json:"rooms"`
}

// AcceptedAssignmentView is one room handed over.
type AcceptedAssignmentView struct {
	ID     uuid.UUID `json:"id"`
	RoomID uuid.UUID `json:"room_id"`
}

// BulkView is the answer of a bulk action: the hand-overs it did.
type BulkView struct {
	Done   []uuid.UUID `json:"done"`
	Failed []any       `json:"failed"`
}
