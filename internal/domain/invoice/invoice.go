// Package invoice is the domain of the bills of a rental contract: how a stay is split into billing periods, what goes
// on a bill (rent, fixed charges, meter consumption, extras), how a bill moves from draft to sent to paid, and how it
// reaches the person it is for. It imports no framework, driver or clock: the use cases load the data, pass "today" in
// and act on the answer. Money is whole VND (int64); dates are calendar dates kept as time.Time at midnight UTC.
package invoice

import (
	"time"

	"github.com/google/uuid"
)

// Status is where a bill stands.
type Status string

// Bill statuses.
const (
	// StatusDraft: written by the landlord, not sent; only the landlord sees it.
	StatusDraft Status = "draft"
	// StatusSent: sent to the person and not paid yet.
	StatusSent Status = "sent"
	StatusPaid Status = "paid"
)

// Delivery is how a bill reaches the person it is for.
type Delivery string

// Deliveries.
const (
	// DeliveryApp: a notice and an email to the tenant's account.
	DeliveryApp Delivery = "app"
	// DeliveryLink: the landlord passes on the link that shows the bill (no account needed to open it).
	DeliveryLink Delivery = "link"
)

// Source says how a bill was sent.
type Source string

// Sources.
const (
	// SourceManual: the landlord pressed send.
	SourceManual Source = "manual"
	// SourceAutomatic: the landlord's plan has automatic invoicing and the daily command sent it.
	SourceAutomatic Source = "automatic"
)

// Charge units: what the amount of a charge is measured against (the units of a room's charges).
const (
	UnitPerPeriod     = "per_period"
	UnitPerPerson     = "per_person"
	UnitPerKWh        = "per_kwh"
	UnitPerCubicMeter = "per_cubic_meter"
	UnitOneTime       = "one_time"
)

// Item kinds beyond the kinds of a room's charges.
const (
	KindRent  = "rent"
	KindOther = "other"
)

// Limits of the data.
const (
	MaxNameLength = 80
	MaxNoteLength = 500
	MaxReadings   = 20
	MaxExtras     = 10
	MaxExtraText  = 150
	MaxMoneyVND   = 1_000_000_000_000
	// ShareTokenLength is the length of the secret of a share link.
	ShareTokenLength = 64
)

// Charge is a cost of a room (a service, electricity, water...) as the billing rules see it.
type Charge struct {
	ID        uuid.UUID
	Name      string
	Kind      string
	Unit      string
	AmountVND int64
}

// IsMetered says whether the charge is by what a meter says (electricity, water) and not by period or person.
func (c Charge) IsMetered() bool { return c.Unit == UnitPerKWh || c.Unit == UnitPerCubicMeter }

// Period is one billing period of a stay. End is inclusive.
type Period struct {
	// Index is 0 for the first bill of the contract (one-time charges go there only).
	Index int
	Start time.Time
	End   time.Time
	// Units is how many rental units (months, or days for a stay billed by the day) the period counts.
	Units int
}

// ReadingInput is what the landlord read from one meter for a bill.
type ReadingInput struct {
	ChargeID uuid.UUID
	// Value is the new index of the meter.
	Value float64
	// Previous is the index to count from; nil takes the last stored one.
	Previous *float64
	// UnitPriceVND overrides the charge's price for this bill only.
	UnitPriceVND *int64
}

// Consumption is what one meter used on a bill.
type Consumption struct {
	ChargeID     uuid.UUID
	Previous     float64
	Value        float64
	Used         float64
	UnitPriceVND int64
}

// Extra is a line the landlord adds by hand.
type Extra struct {
	Description string
	AmountVND   int64
}

// Item is a line of a bill.
type Item struct {
	ID           uuid.UUID
	Kind         string
	Description  string
	Quantity     float64
	UnitPriceVND int64
	AmountVND    int64
	DisplayOrder int
}

// Reading is a meter reading a bill recorded.
type Reading struct {
	ID           uuid.UUID
	ChargeID     uuid.UUID
	ChargeName   string
	ChargeUnit   string
	InvoiceID    uuid.UUID
	ReadOn       time.Time
	Value        float64
	Consumption  float64
	UnitPriceVND int64
}

// Previous is the index the reading counted from.
func (r Reading) Previous() float64 { return round2(r.Value - r.Consumption) }

// Share is the link that shows a bill without signing in. The raw token is never stored: TokenHash finds the bill and
// Sealed (the token encrypted) lets the landlord copy the same link again.
type Share struct {
	TokenHash string
	Sealed    string
	ExpiresAt *time.Time
}

// Live says whether the link works at now.
func (s Share) Live(now time.Time) bool {
	return s.TokenHash != "" && s.Sealed != "" && s.ExpiresAt != nil && s.ExpiresAt.After(now)
}

// Invoice is a bill.
type Invoice struct {
	ID           uuid.UUID
	ContractID   uuid.UUID
	CreatedByID  *uuid.UUID
	Number       string
	Name         string
	PaymentLabel string
	PaymentCode  string
	PeriodStart  time.Time
	PeriodEnd    time.Time
	TotalVND     int64
	DueAt        time.Time
	Status       Status
	SentAt       *time.Time
	SentVia      *Source
	SentByID     *uuid.UUID
	PaidAt       *time.Time
	Note         *string
	Delivery     Delivery
	Share        Share
	Items        []Item
	Readings     []Reading
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Label is what people call the bill (older bills have only their number).
func (i Invoice) Label() string {
	if i.Name != "" {
		return i.Name
	}
	return i.Number
}

// ContractFacts is what billing needs to know about a contract, its room and its people.
type ContractFacts struct {
	ID         uuid.UUID
	RoomID     uuid.UUID
	RoomName   string
	LandlordID uuid.UUID
	// LandlordName is the account name of the landlord.
	LandlordName string
	// TenantID is nil for the blank contract of a room that nobody with an account holds.
	TenantID *uuid.UUID
	// TenantName and TenantPhone are what the landlord wrote on a contract nobody holds.
	TenantName  *string
	TenantPhone *string
	// TenantAccountName and TenantEmail belong to the tenant's account.
	TenantAccountName *string
	TenantEmail       *string
	Status            string
	PriceVND          int64
	OccupantCount     int
	StartsOn          time.Time
	EndsOn            *time.Time
	// BilledByDay: the room is rented by the day (else by the month).
	BilledByDay bool
}

// Running says whether the contract is running (only a running contract can be billed).
func (c ContractFacts) Running() bool { return c.Status == "active" }

// HasAccount says whether somebody with an account holds the contract.
func (c ContractFacts) HasAccount() bool { return c.TenantID != nil }

// Recipient names who the bill is for: the tenant's account name, else the name the landlord wrote.
func (c ContractFacts) Recipient() string {
	switch {
	case c.TenantAccountName != nil && *c.TenantAccountName != "":
		return *c.TenantAccountName
	case c.TenantName != nil:
		return *c.TenantName
	}
	return ""
}

// Person is the author of a bill as it is shown.
type Person struct {
	ID   uuid.UUID
	Name string
}

// Detail is a bill with its contract, as the lists and the detail show it.
type Detail struct {
	Invoice
	Contract  ContractFacts
	CreatedBy *Person
}

// Viewer is who is looking at bills: a landlord (the bills of their contracts), a manager (those of the rooms handed to
// them) or a tenant (their own).
type Viewer struct {
	UserID     uuid.UUID
	AsLandlord bool
	// AsManager: the bills of the rooms handed to the viewer (a manager acting as one), never those of a contract the
	// viewer holds as tenant.
	AsManager bool
}

// Totals is what the payments of a bill add up to.
type Totals struct {
	// PaidVND is the money received (confirmed payments), PendingVND the money waiting for the landlord's check.
	PaidVND    int64
	PendingVND int64
}

// Summary is the payment state of a bill as it is shown.
type Summary struct {
	PaidVND      int64
	PendingVND   int64
	RemainingVND int64
}

// SummaryOf works out what is still to be paid: the total less what was received and what waits for review.
func SummaryOf(total int64, t Totals) Summary {
	return Summary{PaidVND: t.PaidVND, PendingVND: t.PendingVND, RemainingVND: max(0, total-t.PaidVND-t.PendingVND)}
}

// BilledPeriod is a period that has a bill.
type BilledPeriod struct {
	InvoiceID   uuid.UUID
	PeriodStart time.Time
	Number      string
	Name        string
	SentAt      *time.Time
	SentVia     *Source
}

// PeriodRow is a started period of a contract with its bill, if any, and the name and transfer words a new bill of
// it would get.
type PeriodRow struct {
	Period
	Billed              *BilledPeriod
	DefaultName         string
	DefaultPaymentLabel string
}

// ReadingEntry is a line of the meter history of a room.
type ReadingEntry struct {
	Reading
}

// Preview is what a bill would look like, without saving it.
type Preview struct {
	Period   Period
	Items    []Item
	TotalVND int64
}

// PublicView is a bill as the person it is for sees it through its link, without signing in: only what the bill
// itself says, no ids of people, rooms or contracts.
type PublicView struct {
	Number      string
	Name        string
	Status      Status
	PeriodStart time.Time
	PeriodEnd   time.Time
	TotalVND    int64
	DueAt       time.Time
	PaidAt      *time.Time
	Note        *string
	PaymentCode string
	Room        string
	Landlord    string
	Recipient   string
	Summary     Summary
	Items       []PublicItem
}

// PublicItem is a line of a public bill: what it says, without the id the bill's own lines carry.
type PublicItem struct {
	Kind         string
	Description  string
	Quantity     float64
	UnitPriceVND int64
	AmountVND    int64
}

// PublicItems drops the ids of the lines of a bill.
func PublicItems(items []Item) []PublicItem {
	out := make([]PublicItem, len(items))
	for i, it := range items {
		out[i] = PublicItem{Kind: it.Kind, Description: it.Description, Quantity: it.Quantity, UnitPriceVND: it.UnitPriceVND, AmountVND: it.AmountVND}
	}
	return out
}

// Abilities is what a viewer may do with a bill (the web hides the buttons that would be refused).
type Abilities struct {
	Update    bool
	Delete    bool
	SetStatus bool
	Send      bool
}

// DaysOf counts the whole days from a to b (both dates).
func DaysOf(a, b time.Time) int { return int(b.Sub(a).Hours() / 24) }
