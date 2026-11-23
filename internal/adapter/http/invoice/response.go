package invoicehttp

import (
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invoice"
	usecase "rental-management/internal/usecase/invoice"
)

// The bodies of the invoice responses. The json tags are the contract with the web app. Money is whole VND.

// ItemView is a line of a bill.
type ItemView struct {
	ID           uuid.UUID `json:"id"`
	Kind         string    `json:"kind"`
	Description  string    `json:"description"`
	Quantity     float64   `json:"quantity"`
	UnitPriceVND int64     `json:"unit_price_vnd"`
	AmountVND    int64     `json:"amount_vnd"`
}

// ReadingView is what was read from a meter for a bill, so it can be corrected the way it was made.
type ReadingView struct {
	ChargeID      uuid.UUID `json:"charge_id"`
	ChargeName    string    `json:"charge_name"`
	PreviousValue float64   `json:"previous_value"`
	ReadingValue  float64   `json:"reading_value"`
	Consumption   float64   `json:"consumption"`
	UnitPriceVND  int64     `json:"unit_price_vnd"`
}

// MeterReadingView is a line of the meter history of a room.
type MeterReadingView struct {
	ID           uuid.UUID `json:"id"`
	ChargeID     uuid.UUID `json:"charge_id"`
	ChargeName   string    `json:"charge_name"`
	ChargeUnit   string    `json:"charge_unit"`
	InvoiceID    uuid.UUID `json:"invoice_id"`
	ReadOn       string    `json:"read_on"`
	ReadingValue float64   `json:"reading_value"`
	Consumption  float64   `json:"consumption"`
	UnitPriceVND int64     `json:"unit_price_vnd"`
}

// PersonView is the author of a bill.
type PersonView struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// AbilitiesView is what the viewer may do with a bill (the web hides the buttons that would be refused).
type AbilitiesView struct {
	Update    bool `json:"update"`
	Delete    bool `json:"delete"`
	SetStatus bool `json:"set_status"`
	Send      bool `json:"send"`
}

// ShareView says whether the link of a bill works (the link itself comes from its own routes).
type ShareView struct {
	Live      bool       `json:"live"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// SummaryView is how much of a bill is paid, waiting for the landlord's check, and still open.
type SummaryView struct {
	PaidVND      int64 `json:"paid_vnd"`
	PendingVND   int64 `json:"pending_vnd"`
	RemainingVND int64 `json:"remaining_vnd"`
}

func summaryOf(s domain.Summary) SummaryView {
	return SummaryView{PaidVND: s.PaidVND, PendingVND: s.PendingVND, RemainingVND: s.RemainingVND}
}

// InvoiceView is a bill in the lists and the detail.
type InvoiceView struct {
	ID            uuid.UUID  `json:"id"`
	ContractID    uuid.UUID  `json:"contract_id"`
	InvoiceNumber string     `json:"invoice_number"`
	Name          string     `json:"name"`
	PeriodStart   string     `json:"period_start"`
	PeriodEnd     string     `json:"period_end"`
	TotalVND      int64      `json:"total_amount_vnd"`
	DueAt         time.Time  `json:"due_at"`
	Status        string     `json:"status"`
	SentAt        *time.Time `json:"sent_at"`
	SentVia       *string    `json:"sent_via"`
	PaidAt        *time.Time `json:"paid_at"`
	Note          *string    `json:"note"`
	PaymentCode   string     `json:"payment_code"`
	PaymentLabel  string     `json:"payment_label"`
	Delivery      string     `json:"delivery"`
	// HasAccount says whether somebody with an account holds the contract; RecipientName is the name the landlord wrote
	// on a contract nobody holds.
	HasAccount     bool          `json:"has_account"`
	RecipientName  *string       `json:"recipient_name"`
	Room           string        `json:"room"`
	CreatedBy      *PersonView   `json:"created_by"`
	Abilities      AbilitiesView `json:"abilities"`
	Share          *ShareView    `json:"share,omitempty"`
	PaymentSummary SummaryView   `json:"payment_summary"`
	Readings       []ReadingView `json:"readings"`
	Items          []ItemView    `json:"items"`
	CreatedAt      time.Time     `json:"created_at"`
}

func itemsOf(items []domain.Item) []ItemView {
	out := make([]ItemView, len(items))
	for i, it := range items {
		out[i] = ItemView{ID: it.ID, Kind: it.Kind, Description: it.Description, Quantity: it.Quantity, UnitPriceVND: it.UnitPriceVND, AmountVND: it.AmountVND}
	}
	return out
}

// previewItemsOf shows the lines of a bill that does not exist yet: they have no id, so they carry none.
func previewItemsOf(items []domain.Item) []publicItemView {
	out := make([]publicItemView, len(items))
	for i, it := range items {
		out[i] = publicItemView{Kind: it.Kind, Description: it.Description, Quantity: it.Quantity, UnitPriceVND: it.UnitPriceVND, AmountVND: it.AmountVND}
	}
	return out
}

func sourceName(s *domain.Source) *string {
	if s == nil {
		return nil
	}
	v := string(*s)
	return &v
}

// invoiceViewOf builds the view of a bill for the viewer: the link state only goes to a landlord.
func invoiceViewOf(v usecase.View, viewer domain.Viewer, now time.Time) InvoiceView {
	view := InvoiceView{
		ID: v.ID, ContractID: v.ContractID, InvoiceNumber: v.Number, Name: v.Label(),
		PeriodStart: v.PeriodStart.Format(domain.DateLayout), PeriodEnd: v.PeriodEnd.Format(domain.DateLayout),
		TotalVND: v.TotalVND, DueAt: v.DueAt, Status: string(v.Status), SentAt: v.SentAt, SentVia: sourceName(v.SentVia),
		PaidAt: v.PaidAt, Note: v.Note, PaymentCode: v.PaymentCode, PaymentLabel: v.PaymentLabel, Delivery: string(v.Delivery),
		HasAccount: v.Contract.HasAccount(), RecipientName: v.Contract.TenantName, Room: v.Contract.RoomName,
		Abilities:      AbilitiesView{Update: v.Abilities.Update, Delete: v.Abilities.Delete, SetStatus: v.Abilities.SetStatus, Send: v.Abilities.Send},
		PaymentSummary: summaryOf(v.Summary), Items: itemsOf(v.Items), Readings: make([]ReadingView, len(v.Readings)), CreatedAt: v.CreatedAt,
	}
	if v.CreatedBy != nil {
		view.CreatedBy = &PersonView{ID: v.CreatedBy.ID, Name: v.CreatedBy.Name}
	}
	if viewer.AsLandlord {
		view.Share = &ShareView{Live: v.Share.Live(now), ExpiresAt: v.Share.ExpiresAt}
	}
	for i, r := range v.Readings {
		view.Readings[i] = ReadingView{
			ChargeID: r.ChargeID, ChargeName: r.ChargeName, PreviousValue: r.Previous(), ReadingValue: r.Value,
			Consumption: r.Consumption, UnitPriceVND: r.UnitPriceVND,
		}
	}
	return view
}

func invoiceViewsOf(items []usecase.View, viewer domain.Viewer, now time.Time) []InvoiceView {
	out := make([]InvoiceView, len(items))
	for i, it := range items {
		out[i] = invoiceViewOf(it, viewer, now)
	}
	return out
}

// PreviewView is what a bill would look like.
type PreviewView struct {
	PeriodStart string           `json:"period_start"`
	PeriodEnd   string           `json:"period_end"`
	Units       int              `json:"units"`
	Items       []publicItemView `json:"items"`
	TotalVND    int64            `json:"total_amount_vnd"`
}

func previewViewOf(p domain.Preview) PreviewView {
	return PreviewView{
		PeriodStart: p.Period.Start.Format(domain.DateLayout), PeriodEnd: p.Period.End.Format(domain.DateLayout),
		Units: p.Period.Units, Items: previewItemsOf(p.Items), TotalVND: p.TotalVND,
	}
}

// BilledView is the bill a period already has.
type BilledView struct {
	ID            uuid.UUID  `json:"id"`
	InvoiceNumber string     `json:"invoice_number"`
	Name          string     `json:"name"`
	SentAt        *time.Time `json:"sent_at"`
	SentVia       *string    `json:"sent_via"`
}

// PeriodView is a started billing period of a contract with its bill, if any.
type PeriodView struct {
	PeriodStart         string      `json:"period_start"`
	PeriodEnd           string      `json:"period_end"`
	Units               int         `json:"units"`
	DefaultName         string      `json:"default_name"`
	DefaultPaymentLabel string      `json:"default_payment_label"`
	Invoice             *BilledView `json:"invoice"`
}

func periodViewsOf(rows []domain.PeriodRow) []PeriodView {
	out := make([]PeriodView, len(rows))
	for i, r := range rows {
		out[i] = PeriodView{
			PeriodStart: r.Start.Format(domain.DateLayout), PeriodEnd: r.End.Format(domain.DateLayout), Units: r.Units,
			DefaultName: r.DefaultName, DefaultPaymentLabel: r.DefaultPaymentLabel,
		}
		if r.Billed != nil {
			out[i].Invoice = &BilledView{ID: r.Billed.InvoiceID, InvoiceNumber: r.Billed.Number, Name: r.Billed.Name, SentAt: r.Billed.SentAt, SentVia: sourceName(r.Billed.SentVia)}
		}
	}
	return out
}

// LinkView is the address that shows a bill without signing in.
type LinkView struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PublicInvoiceView is a bill as the person it is for sees it through its link: only what the bill itself says, no ids of
// people, rooms or contracts.
type PublicInvoiceView struct {
	InvoiceNumber  string           `json:"invoice_number"`
	Name           string           `json:"name"`
	Status         string           `json:"status"`
	PeriodStart    string           `json:"period_start"`
	PeriodEnd      string           `json:"period_end"`
	TotalVND       int64            `json:"total_amount_vnd"`
	DueAt          time.Time        `json:"due_at"`
	PaidAt         *time.Time       `json:"paid_at"`
	Note           *string          `json:"note"`
	PaymentCode    string           `json:"payment_code"`
	Room           string           `json:"room"`
	Landlord       string           `json:"landlord"`
	Recipient      string           `json:"recipient"`
	PaymentSummary SummaryView      `json:"payment_summary"`
	Items          []publicItemView `json:"items"`
}

// publicItemView is a line of a bill without the id, for the public page and for the preview.
type publicItemView struct {
	Kind         string  `json:"kind"`
	Description  string  `json:"description"`
	Quantity     float64 `json:"quantity"`
	UnitPriceVND int64   `json:"unit_price_vnd"`
	AmountVND    int64   `json:"amount_vnd"`
}

func publicViewOf(p domain.PublicView) PublicInvoiceView {
	items := make([]publicItemView, len(p.Items))
	for i, it := range p.Items {
		items[i] = publicItemView(it)
	}
	return PublicInvoiceView{
		InvoiceNumber: p.Number, Name: p.Name, Status: string(p.Status), PeriodStart: p.PeriodStart.Format(domain.DateLayout),
		PeriodEnd: p.PeriodEnd.Format(domain.DateLayout), TotalVND: p.TotalVND, DueAt: p.DueAt, PaidAt: p.PaidAt, Note: p.Note,
		PaymentCode: p.PaymentCode, Room: p.Room, Landlord: p.Landlord, Recipient: p.Recipient, PaymentSummary: summaryOf(p.Summary),
		Items: items,
	}
}

func meterViewsOf(items []domain.Reading) []MeterReadingView {
	out := make([]MeterReadingView, len(items))
	for i, r := range items {
		out[i] = MeterReadingView{
			ID: r.ID, ChargeID: r.ChargeID, ChargeName: r.ChargeName, ChargeUnit: r.ChargeUnit, InvoiceID: r.InvoiceID,
			ReadOn: r.ReadOn.Format(domain.DateLayout), ReadingValue: r.Value, Consumption: r.Consumption, UnitPriceVND: r.UnitPriceVND,
		}
	}
	return out
}
