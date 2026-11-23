// Package payment is the domain of how a bill is paid and how a landlord is paid: the payment methods a landlord
// accepts (a bank account, a MoMo number), the payments a tenant sends or a landlord writes down (all or part of a
// bill), how a payment is confirmed, rejected, withdrawn or voided, and how the confirmed payments settle a bill. It
// imports no framework, driver or clock: the use cases load the data, pass "now" in and act on the answer. Money is
// whole VND (int64).
package payment

import (
	"time"

	"github.com/google/uuid"
)

// Status is where a payment stands.
type Status string

// Payment statuses.
const (
	// StatusPending: sent by the tenant, waiting for the landlord to check that the money arrived.
	StatusPending Status = "pending"
	// StatusConfirmed: the money arrived; it counts towards the bill.
	StatusConfirmed Status = "confirmed"
	StatusRejected  Status = "rejected"
	// StatusVoided: was confirmed, then the landlord reopened the bill; kept in the history but no longer counts.
	StatusVoided Status = "voided"
)

// ParseStatus reads a status code.
func ParseStatus(s string) (Status, bool) {
	switch st := Status(s); st {
	case StatusPending, StatusConfirmed, StatusRejected, StatusVoided:
		return st, true
	}
	return "", false
}

// The codes of the payment methods. A payment method is stored by its code, so a new one (an online gateway) is a new
// driver and a new code, nothing else.
const (
	// MethodManualProof is a bank transfer the landlord checks by hand, with a screenshot as proof.
	MethodManualProof = "manual_proof"
	// MethodMomo is a MoMo wallet transfer checked by hand like a bank transfer.
	MethodMomo = "momo"
	// MethodRecorded is the method of a payment the landlord wrote down by hand: it has no driver, nobody paid through
	// the app.
	MethodRecorded = "recorded"
)

// Limits of the data.
const (
	MaxNoteLength   = 500
	MaxReasonLength = 500
	MaxMoneyVND     = 1_000_000_000_000
	// MaxProofFiles is the most proof pictures a payment may hold, whatever the settings say.
	MaxProofFiles = 10
)

// Payment is money paid (or claimed paid) on a bill.
type Payment struct {
	ID        uuid.UUID
	InvoiceID uuid.UUID
	// PayerID is the tenant's account; nil for a payment the landlord wrote down (the person has no account).
	PayerID *uuid.UUID
	Method  string
	// AmountVND is whole VND, at least 1.
	AmountVND int64
	Status    Status
	Note      *string
	// ProofKeys are the storage keys of the proof pictures (private files).
	ProofKeys []string
	// ExternalReference is the id of the payment at an online gateway, when there is one.
	ExternalReference *string
	ReviewedByID      *uuid.UUID
	ReviewedAt        *time.Time
	ReviewNote        *string
	// RecordedByID is the landlord who wrote the payment down; nil for a payment a tenant sent.
	RecordedByID *uuid.UUID
	PaidAt       *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Recorded says whether the landlord wrote the payment down (no tenant account paid it).
func (p Payment) Recorded() bool { return p.RecordedByID != nil }

// InvoiceStatus is where a bill stands, as payments see it.
type InvoiceStatus string

// Bill statuses.
const (
	InvoiceDraft InvoiceStatus = "draft"
	InvoiceSent  InvoiceStatus = "sent"
	InvoicePaid  InvoiceStatus = "paid"
)

// InvoiceFacts is what payments need to know about a bill, its contract and its room.
type InvoiceFacts struct {
	ID         uuid.UUID
	ContractID uuid.UUID
	RoomID     uuid.UUID
	RoomName   string
	LandlordID uuid.UUID
	// TenantID is nil for the blank contract of a room that nobody with an account holds.
	TenantID *uuid.UUID
	Number   string
	Name     string
	// PaymentCode is the transfer text of the bill (its words and a random tail).
	PaymentCode string
	TotalVND    int64
	Status      InvoiceStatus
	DueAt       time.Time
}

// Label is what people call the bill (older bills have only their number).
func (i InvoiceFacts) Label() string {
	if i.Name != "" {
		return i.Name
	}
	return i.Number
}

// Person is somebody a payment names (the tenant who paid).
type Person struct {
	ID   uuid.UUID
	Name string
	// Email is nil for an account with none (a tenant who signed in with a phone number).
	Email *string
}

// Detail is a payment with the bill it is on and who paid it, as the lists and the detail show it.
type Detail struct {
	Payment
	Payer   *Person
	Invoice InvoiceFacts
}

// Viewer is who is looking at payments: a landlord (the payments on their rooms' bills) or a tenant (their own).
type Viewer struct {
	UserID     uuid.UUID
	AsLandlord bool
}

// Config holds the settings of a payment method (the account to pay into...), by field name.
type Config map[string]string

// MethodSetting is how a landlord set up one payment method.
type MethodSetting struct {
	Method string
	// Config is nil when the landlord never saved the method.
	Config   Config
	IsActive bool
}

// StoredMethod is how a landlord set up a payment method, as it is kept: the settings are encrypted (ConfigSealed), so
// the database never holds the account number in the clear.
type StoredMethod struct {
	Method       string
	ConfigSealed string
	IsActive     bool
}
