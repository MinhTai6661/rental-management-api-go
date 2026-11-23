package paymenthttp

import (
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/payment"
	usecase "rental-management/internal/usecase/payment"
)

// The bodies of the payment responses. The json tags are the contract with the web app. Money is whole VND.

// ProofView is a proof picture of a payment: private, so the links are signed and expire.
type ProofView struct {
	Index        int    `json:"index"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"`
}

// PayerView is the tenant who paid.
type PayerView struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// InvoiceRefView is the bill a payment is on.
type InvoiceRefView struct {
	ID            uuid.UUID `json:"id"`
	InvoiceNumber string    `json:"invoice_number"`
	Name          string    `json:"name"`
	TotalVND      int64     `json:"total_amount_vnd"`
	ContractID    uuid.UUID `json:"contract_id"`
	RoomName      string    `json:"room_name"`
}

// PaymentView is a payment in the lists and the answers.
type PaymentView struct {
	ID         uuid.UUID   `json:"id"`
	InvoiceID  uuid.UUID   `json:"invoice_id"`
	Method     string      `json:"method"`
	AmountVND  int64       `json:"amount_vnd"`
	Status     string      `json:"status"`
	Note       *string     `json:"note"`
	Proofs     []ProofView `json:"proofs"`
	ReviewNote *string     `json:"review_note"`
	ReviewedAt *time.Time  `json:"reviewed_at"`
	PaidAt     *time.Time  `json:"paid_at"`
	CreatedAt  time.Time   `json:"created_at"`
	// Recorded is true for money the landlord wrote down: nobody with an account paid it, so there is no payer.
	Recorded bool           `json:"recorded"`
	Payer    *PayerView     `json:"payer"`
	Invoice  InvoiceRefView `json:"invoice"`
}

func paymentViewOf(v usecase.View) PaymentView {
	out := PaymentView{
		ID: v.ID, InvoiceID: v.InvoiceID, Method: v.Method, AmountVND: v.AmountVND, Status: string(v.Status), Note: v.Note,
		Proofs: make([]ProofView, len(v.Proofs)), ReviewNote: v.ReviewNote, ReviewedAt: v.ReviewedAt, PaidAt: v.PaidAt,
		CreatedAt: v.CreatedAt, Recorded: v.Recorded(),
		Invoice: InvoiceRefView{
			ID: v.Invoice.ID, InvoiceNumber: v.Invoice.Number, Name: v.Invoice.Label(), TotalVND: v.Invoice.TotalVND,
			ContractID: v.Invoice.ContractID, RoomName: v.Invoice.RoomName,
		},
	}
	for i, p := range v.Proofs {
		out.Proofs[i] = ProofView{Index: p.Index, URL: p.URL, ThumbnailURL: p.ThumbnailURL}
	}
	if v.Payer != nil {
		out.Payer = &PayerView{ID: v.Payer.ID, Name: v.Payer.Name}
	}
	return out
}

func paymentViewsOf(views []usecase.View) []PaymentView {
	out := make([]PaymentView, len(views))
	for i, v := range views {
		out[i] = paymentViewOf(v)
	}
	return out
}

// InstructionsView is what a tenant needs to pay a bill with one payment method. The fields of the other kind of method
// are left out.
type InstructionsView struct {
	Method          string `json:"method"`
	AmountVND       int64  `json:"amount_vnd"`
	TransferContent string `json:"transfer_content"`
	QRURL           string `json:"qr_url"`
	BankCode        string `json:"bank_code,omitempty"`
	BankName        string `json:"bank_name,omitempty"`
	AccountNumber   string `json:"account_number,omitempty"`
	AccountName     string `json:"account_name,omitempty"`
	Phone           string `json:"phone,omitempty"`
	MoMoLink        string `json:"momo_link,omitempty"`
}

func instructionsViewsOf(list []domain.Instructions) []InstructionsView {
	out := make([]InstructionsView, len(list))
	for i, in := range list {
		out[i] = InstructionsView{
			Method: in.Method, AmountVND: in.AmountVND, TransferContent: in.TransferContent, QRURL: in.QRURL, BankCode: in.BankCode,
			BankName: in.BankName, AccountNumber: in.AccountNumber, AccountName: in.AccountName, Phone: in.Phone, MoMoLink: in.MoMoLink,
		}
	}
	return out
}

// MethodView is how a landlord set up one payment method.
type MethodView struct {
	Method   string            `json:"method"`
	Config   map[string]string `json:"config"`
	IsActive bool              `json:"is_active"`
}

func methodViewOf(m domain.MethodSetting) MethodView {
	return MethodView{Method: m.Method, Config: m.Config, IsActive: m.IsActive}
}
