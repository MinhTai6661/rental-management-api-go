package contracthttp

import (
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
)

// The bodies of the contract responses. The json tags are the contract with the web app.

// FileURLs turns the storage key of a picture into the public address the client loads it from.
type FileURLs interface {
	URL(key string) string
}

// CoverView holds the URLs of a cover picture.
type CoverView struct {
	URL      string `json:"url"`
	ThumbURL string `json:"thumb_url"`
}

// PersonView is the other party of a contract: what is needed to name and reach them.
type PersonView struct {
	ID          uuid.UUID `json:"id"`
	FullName    string    `json:"full_name"`
	Email       *string   `json:"email"`
	PhoneNumber *string   `json:"phone_number"`
}

// CancellationView is a tenant's request to end a running contract and the answer to it.
type CancellationView struct {
	Status       *string    `json:"status"`
	RequestedAt  *time.Time `json:"requested_at"`
	Reason       *string    `json:"reason"`
	ResponseNote *string    `json:"response_note"`
}

// ContractRoomView is the room of a contract in a landlord's list.
type ContractRoomView struct {
	ID                  uuid.UUID  `json:"id"`
	Name                string     `json:"name"`
	Status              string     `json:"status"`
	PriceVND            int64      `json:"price_vnd"`
	MinimumRentalPeriod int32      `json:"minimum_rental_period"`
	RentalPeriodUnit    string     `json:"rental_period_unit"`
	AddressDetail       string     `json:"address_detail"`
	Cover               *CoverView `json:"cover"`
}

// AbilitiesView is what the viewer may do with a contract.
type AbilitiesView struct {
	Review bool `json:"review"`
	Update bool `json:"update"`
	Delete bool `json:"delete"`
}

// ContractView is a contract in the lists and the detail.
type ContractView struct {
	ID            uuid.UUID        `json:"id"`
	Status        string           `json:"status"`
	PriceVND      int64            `json:"price_vnd"`
	DepositVND    *int64           `json:"deposit_vnd"`
	OccupantCount int              `json:"occupant_count"`
	StartsOn      string           `json:"starts_on"`
	EndsOn        *string          `json:"ends_on"`
	Notes         *string          `json:"notes"`
	Confirmed     bool             `json:"confirmed"`
	Cancellation  CancellationView `json:"cancellation"`
	Tenant        *PersonView      `json:"tenant"`
	// Unassigned: nobody with an account holds the contract (the system opened it for a room marked rented).
	Unassigned  bool             `json:"unassigned"`
	TenantName  *string          `json:"tenant_name"`
	TenantPhone *string          `json:"tenant_phone"`
	Room        ContractRoomView `json:"room"`
	Landlord    PersonView       `json:"landlord"`
	Abilities   AbilitiesView    `json:"abilities"`
	// Billing says how many of the contract's bills are not paid yet (the landlord's quick billing page marks them).
	Billing      LandlordBillingView `json:"billing"`
	DisplayOrder int                 `json:"display_order"`
	CreatedAt    time.Time           `json:"created_at"`
}

// LandlordBillingView is where a contract stands on its bills for its landlord.
type LandlordBillingView struct {
	// UnpaidInvoices counts the bills that are not paid yet: drafts and sent ones.
	UnpaidInvoices int `json:"unpaid_invoices"`
}

// RentalBillingView is where a contract stands on its bills for its renter: what is still to be paid, what waits for the
// landlord's check and whether something is overdue. Only a bill that was sent counts.
type RentalBillingView struct {
	DueCount     int   `json:"due_count"`
	DueAmountVND int64 `json:"due_amount_vnd"`
	// Partial is true when part of a due bill was paid or sent already.
	Partial     bool       `json:"partial"`
	ReviewCount int        `json:"review_count"`
	Overdue     bool       `json:"overdue"`
	NextDueAt   *time.Time `json:"next_due_at"`
}

func rentalBillingViewOf(b domain.Billing) RentalBillingView {
	return RentalBillingView{
		DueCount: b.DueCount, DueAmountVND: b.DueAmountVND, Partial: b.Partial, ReviewCount: b.ReviewCount, Overdue: b.Overdue,
		NextDueAt: b.NextDueAt,
	}
}

// WardView is the ward of a room's address.
type WardView struct {
	Code     int32  `json:"code"`
	Name     string `json:"name"`
	Province struct {
		Code int32  `json:"code"`
		Name string `json:"name"`
	} `json:"province"`
}

// ChargeView is a cost on top of the rent.
type ChargeView struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	AmountVND int64     `json:"amount_vnd"`
	Unit      string    `json:"unit"`
	Note      *string   `json:"note"`
}

// PictureView is a picture of a rented room.
type PictureView struct {
	ID       uuid.UUID `json:"id"`
	Kind     string    `json:"kind"`
	URL      string    `json:"url"`
	ThumbURL string    `json:"thumb_url"`
	Width    int       `json:"width"`
	Height   int       `json:"height"`
}

// RentalRoomView is the room of a contract as its tenant sees it.
type RentalRoomView struct {
	ID                  uuid.UUID  `json:"id"`
	Name                string     `json:"name"`
	Description         *string    `json:"description"`
	PriceVND            int64      `json:"price_vnd"`
	MinimumRentalPeriod int32      `json:"minimum_rental_period"`
	RentalPeriodUnit    string     `json:"rental_period_unit"`
	AreaSquareMeters    *float64   `json:"area_square_meters"`
	AddressDetail       string     `json:"address_detail"`
	Ward                *WardView  `json:"ward"`
	Cover               *CoverView `json:"cover"`
}

// RentalView is a contract as its tenant sees it ("my rooms"); the detail adds the costs and the pictures.
type RentalView struct {
	ID            uuid.UUID         `json:"id"`
	Status        string            `json:"status"`
	PriceVND      int64             `json:"price_vnd"`
	DepositVND    *int64            `json:"deposit_vnd"`
	OccupantCount int               `json:"occupant_count"`
	StartsOn      string            `json:"starts_on"`
	EndsOn        *string           `json:"ends_on"`
	Notes         *string           `json:"notes"`
	Confirmed     bool              `json:"confirmed"`
	Cancellation  CancellationView  `json:"cancellation"`
	Room          RentalRoomView    `json:"room"`
	Landlord      PersonView        `json:"landlord"`
	Billing       RentalBillingView `json:"billing"`
	CreatedAt     time.Time         `json:"created_at"`
}

// FailureView is a contract a bulk answer refused.
type FailureView struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Code    string    `json:"code"`
	Message string    `json:"message"`
}

// BulkView is the result of a bulk answer.
type BulkView struct {
	Done   []uuid.UUID   `json:"done"`
	Failed []FailureView `json:"failed"`
}

func dateOf(t time.Time) string { return t.Format(domain.DateLayout) }

func datePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := dateOf(*t)
	return &s
}

func coverViewOf(c *domain.Cover, urls FileURLs) *CoverView {
	if c == nil {
		return nil
	}
	return &CoverView{URL: urls.URL(c.Key), ThumbURL: urls.URL(c.ThumbKey)}
}

func personViewOf(p domain.Person) PersonView {
	return PersonView{ID: p.ID, FullName: p.FullName, Email: p.Email, PhoneNumber: p.PhoneNumber}
}

func cancellationViewOf(c domain.Cancellation) CancellationView {
	v := CancellationView{RequestedAt: c.RequestedAt, Reason: c.Reason, ResponseNote: c.ResponseNote}
	if c.Status != nil {
		s := string(*c.Status)
		v.Status = &s
	}
	return v
}

func contractViewOf(d domain.Detail, v domain.Viewer, urls FileURLs) ContractView {
	out := ContractView{
		ID: d.ID, Status: string(d.Status), PriceVND: d.PriceVND, DepositVND: d.DepositVND, OccupantCount: d.OccupantCount,
		StartsOn: dateOf(d.StartsOn), EndsOn: datePtr(d.EndsOn), Notes: d.Notes, Confirmed: d.ConfirmedAt != nil,
		Cancellation: cancellationViewOf(d.Cancellation), Unassigned: d.Unassigned(), TenantName: d.TenantName,
		TenantPhone: d.TenantPhone,
		Room: ContractRoomView{
			ID: d.Room.ID, Name: d.Room.Name, Status: d.Room.Status, PriceVND: d.Room.PriceVND,
			MinimumRentalPeriod: d.Room.MinPeriod, RentalPeriodUnit: string(d.Room.PeriodUnit),
			AddressDetail: d.Room.AddressDetail, Cover: coverViewOf(d.Room.Cover, urls),
		},
		Landlord: personViewOf(d.Landlord), DisplayOrder: d.DisplayOrder, CreatedAt: d.CreatedAt,
		Billing: LandlordBillingView{UnpaidInvoices: d.Billing.UnpaidInvoices},
	}
	if d.Tenant != nil {
		p := personViewOf(*d.Tenant)
		out.Tenant = &p
	}
	a := d.AbilitiesFor(v)
	out.Abilities = AbilitiesView{Review: a.Review, Update: a.Update, Delete: a.Delete}
	return out
}

func contractViewsOf(items []domain.Detail, v domain.Viewer, urls FileURLs) []ContractView {
	out := make([]ContractView, len(items))
	for i, d := range items {
		out[i] = contractViewOf(d, v, urls)
	}
	return out
}

func wardViewOf(w *domain.WardRef) *WardView {
	if w == nil {
		return nil
	}
	v := &WardView{Code: w.Code, Name: w.Name}
	v.Province.Code, v.Province.Name = w.Province.Code, w.Province.Name
	return v
}

func rentalViewOf(d domain.Detail, urls FileURLs) RentalView {
	return RentalView{
		ID: d.ID, Status: string(d.Status), PriceVND: d.PriceVND, DepositVND: d.DepositVND, OccupantCount: d.OccupantCount,
		StartsOn: dateOf(d.StartsOn), EndsOn: datePtr(d.EndsOn), Notes: d.Notes, Confirmed: d.ConfirmedAt != nil,
		Cancellation: cancellationViewOf(d.Cancellation),
		Room: RentalRoomView{
			ID: d.Room.ID, Name: d.Room.Name, Description: d.Room.Description, PriceVND: d.Room.PriceVND,
			MinimumRentalPeriod: d.Room.MinPeriod, RentalPeriodUnit: string(d.Room.PeriodUnit), AreaSquareMeters: d.Room.Area,
			AddressDetail: d.Room.AddressDetail, Ward: wardViewOf(d.Room.Ward), Cover: coverViewOf(d.Room.Cover, urls),
		},
		Landlord: personViewOf(d.Landlord), Billing: rentalBillingViewOf(d.Billing), CreatedAt: d.CreatedAt,
	}
}

func rentalViewsOf(items []domain.Detail, urls FileURLs) []RentalView {
	out := make([]RentalView, len(items))
	for i, d := range items {
		out[i] = rentalViewOf(d, urls)
	}
	return out
}

// RentalDetailRoomView is the room of a rental's detail: with its costs and its pictures.
type RentalDetailRoomView struct {
	RentalRoomView
	Charges  []ChargeView  `json:"charges"`
	Pictures []PictureView `json:"pictures"`
}

// RentalDetailView is the detail of a rental; its room carries the costs and the pictures (the field of the
// same name shadows the embedded one).
type RentalDetailView struct {
	RentalView
	Room RentalDetailRoomView `json:"room"`
}

func rentalDetailViewOf(d domain.RentalDetail, urls FileURLs) RentalDetailView {
	base := rentalViewOf(d.Detail, urls)
	v := RentalDetailView{RentalView: base, Room: RentalDetailRoomView{
		RentalRoomView: base.Room, Charges: make([]ChargeView, len(d.Charges)), Pictures: make([]PictureView, len(d.Pictures)),
	}}
	for i, c := range d.Charges {
		v.Room.Charges[i] = ChargeView{ID: c.ID, Name: c.Name, Kind: c.Kind, AmountVND: c.AmountVND, Unit: c.Unit, Note: c.Note}
	}
	for i, p := range d.Pictures {
		v.Room.Pictures[i] = PictureView{ID: p.ID, Kind: p.Kind, URL: urls.URL(p.FileKey), ThumbURL: urls.URL(p.ThumbKey), Width: p.Width, Height: p.Height}
	}
	return v
}
