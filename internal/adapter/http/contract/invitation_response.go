package contracthttp

import (
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/invitation"
	usecase "rental-management/internal/usecase/contract"
)

// InviteePersonView is an account an invitation names: enough to recognize it, with only a masked email.
type InviteePersonView struct {
	ID          uuid.UUID `json:"id"`
	FullName    string    `json:"full_name"`
	EmailMasked *string   `json:"email_masked"`
}

// InvitationRoomView is the room of an invitation.
type InvitationRoomView struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Building *string   `json:"building"`
}

// InvitationView is an invitation as its landlord sees it. It never carries the link: that is shown when the invitation
// is made or a new link is asked for, and copied again with GET .../link while link_available.
type InvitationView struct {
	ID             uuid.UUID          `json:"id"`
	Status         string             `json:"status"`
	Expired        bool               `json:"expired"`
	Audience       string             `json:"audience"`
	Room           InvitationRoomView `json:"room"`
	Recipient      *InviteePersonView `json:"recipient"`
	RecipientEmail *string            `json:"recipient_email"`
	PriceVND       int64              `json:"price_vnd"`
	DepositVND     *int64             `json:"deposit_vnd"`
	OccupantCount  int                `json:"occupant_count"`
	StartsOn       string             `json:"starts_on"`
	EndsOn         *string            `json:"ends_on"`
	ContractStatus string             `json:"contract_status"`
	Note           *string            `json:"note"`
	ExpiresAt      time.Time          `json:"expires_at"`
	LinkAvailable  bool               `json:"link_available"`
	AnsweredBy     *InviteePersonView `json:"answered_by"`
	AnsweredAt     *time.Time         `json:"answered_at"`
	ContractID     *uuid.UUID         `json:"contract_id"`
	CreatedAt      time.Time          `json:"created_at"`
}

// DeliveryView tells how the invitation reached the person it is for.
type DeliveryView struct {
	App   bool `json:"app"`
	Email bool `json:"email"`
}

// IssuedInvitationView is a new invitation with its link, shown now.
type IssuedInvitationView struct {
	Invitation InvitationView `json:"invitation"`
	Link       string         `json:"link"`
	Delivery   DeliveryView   `json:"delivery"`
}

// RenewedInvitationView is an invitation with its new link.
type RenewedInvitationView struct {
	Invitation InvitationView `json:"invitation"`
	Link       string         `json:"link"`
}

// InvitationLinkView is the link of an invitation, copied again.
type InvitationLinkView struct {
	Link      string    `json:"link"`
	ExpiresAt time.Time `json:"expires_at"`
}

func inviteePersonOf(p *domain.Person) *InviteePersonView {
	if p == nil {
		return nil
	}
	return &InviteePersonView{ID: p.ID, FullName: p.FullName, EmailMasked: invitation.MaskEmail(p.Email)}
}

func invitationViewOf(d domain.InvitationDetail, now time.Time) InvitationView {
	v := InvitationView{
		ID: d.ID, Status: string(d.Status), Expired: d.Expired(now), Audience: string(d.Audience()),
		Room:      InvitationRoomView{ID: d.RoomID, Name: d.RoomName, Building: d.BuildingName},
		Recipient: inviteePersonOf(d.Recipient), RecipientEmail: d.RecipientEmail, PriceVND: d.Terms.PriceVND,
		DepositVND: d.Terms.DepositVND, OccupantCount: d.Terms.OccupantCount, StartsOn: d.Terms.StartsOn.Format(domain.DateLayout),
		ContractStatus: string(d.Terms.ContractStatus), Note: d.Terms.Note, ExpiresAt: d.ExpiresAt,
		LinkAvailable: d.LinkAvailable(now), AnsweredBy: inviteePersonOf(d.AnsweredBy), AnsweredAt: d.AnsweredAt,
		ContractID: d.ContractID, CreatedAt: d.CreatedAt,
	}
	if d.Terms.EndsOn != nil {
		s := d.Terms.EndsOn.Format(domain.DateLayout)
		v.EndsOn = &s
	}
	return v
}

func invitationViewsOf(items []domain.InvitationDetail, now time.Time) []InvitationView {
	out := make([]InvitationView, len(items))
	for i, d := range items {
		out[i] = invitationViewOf(d, now)
	}
	return out
}

func issuedInvitationViewOf(r usecase.IssuedInvitation, now time.Time) IssuedInvitationView {
	return IssuedInvitationView{
		Invitation: invitationViewOf(r.Invitation, now), Link: r.Link.URL,
		Delivery: DeliveryView{App: r.Delivery.App, Email: r.Delivery.Email},
	}
}

// OfferRoomView is the room of an offer, with its address.
type OfferRoomView struct {
	ID                  uuid.UUID  `json:"id"`
	Name                string     `json:"name"`
	HouseName           *string    `json:"house_name"`
	AddressDetail       string     `json:"address_detail"`
	AreaSquareMeters    *float64   `json:"area_square_meters"`
	MinimumRentalPeriod int32      `json:"minimum_rental_period"`
	RentalPeriodUnit    string     `json:"rental_period_unit"`
	Ward                *WardView  `json:"ward"`
	Cover               *CoverView `json:"cover"`
}

// OfferLandlordView is who made the offer: nothing but the id and the name.
type OfferLandlordView struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"full_name"`
}

// InvitationOfferView is an invitation as the invited person sees it. It says nothing about who else was invited.
type InvitationOfferView struct {
	ID             uuid.UUID         `json:"id"`
	Expired        bool              `json:"expired"`
	ExpiresAt      time.Time         `json:"expires_at"`
	Landlord       OfferLandlordView `json:"landlord"`
	Room           OfferRoomView     `json:"room"`
	PriceVND       int64             `json:"price_vnd"`
	DepositVND     *int64            `json:"deposit_vnd"`
	OccupantCount  int               `json:"occupant_count"`
	StartsOn       string            `json:"starts_on"`
	EndsOn         *string           `json:"ends_on"`
	ContractStatus string            `json:"contract_status"`
	Note           *string           `json:"note"`
}

func invitationOfferViewOf(o domain.InvitationOffer, now time.Time, urls FileURLs) InvitationOfferView {
	return InvitationOfferView{
		ID: o.ID, Expired: o.Expired(now), ExpiresAt: o.ExpiresAt,
		Landlord: OfferLandlordView{ID: o.Landlord.ID, FullName: o.Landlord.FullName},
		Room: OfferRoomView{
			ID: o.Room.ID, Name: o.Room.Name, HouseName: o.HouseName, AddressDetail: o.Room.AddressDetail,
			AreaSquareMeters: o.Room.Area, MinimumRentalPeriod: o.Room.MinPeriod, RentalPeriodUnit: string(o.Room.PeriodUnit),
			Ward: wardViewOf(o.Room.Ward), Cover: coverViewOf(o.Room.Cover, urls),
		},
		PriceVND: o.Terms.PriceVND, DepositVND: o.Terms.DepositVND, OccupantCount: o.Terms.OccupantCount,
		StartsOn: dateOf(o.Terms.StartsOn), EndsOn: datePtr(o.Terms.EndsOn), ContractStatus: string(o.Terms.ContractStatus),
		Note: o.Terms.Note,
	}
}

func invitationOfferViewsOf(items []domain.InvitationOffer, now time.Time, urls FileURLs) []InvitationOfferView {
	out := make([]InvitationOfferView, len(items))
	for i, o := range items {
		out[i] = invitationOfferViewOf(o, now, urls)
	}
	return out
}
