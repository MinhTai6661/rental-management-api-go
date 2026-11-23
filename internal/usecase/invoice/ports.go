package invoice

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invoice"
)

// The interfaces below are what the invoice use cases need from the outside world. The adapters implement them
// structurally (they never import this package), so the rules are tested with fakes and no database.
// Repository lookups return shared.ErrNotFound when there is no such row; writes refused by a constraint come back
// as *shared.ConstraintError.

// TxManager runs fn in one transaction. Repository calls made with the ctx given to fn join it, and so do the
// other ports implemented over the same database.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// The names of the unique constraints a write can trip on, so the use cases can tell a period that is already billed
// from a number or a transfer text that clashed (which is retried with new random parts).
const (
	ConstraintPeriod = "invoices_contract_period_key"
)

// InvoiceRepository stores bills with their lines and meter readings. Reads are scoped to a viewer: a landlord sees
// the bills of the contracts on their own rooms, a tenant the bills sent to them.
type InvoiceRepository interface {
	// Get returns a bill as the viewer sees it (shared.ErrNotFound for anybody else's).
	Get(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.Detail, error)
	// ForUpdate is Get and locks the bill until the transaction ends.
	ForUpdate(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.Detail, error)
	// List returns a page of the bills of a contract the viewer sees, newest period first, and how many there are in
	// all. limit+1 rows are not needed: the caller compares offset+len with total.
	List(ctx context.Context, v domain.Viewer, contractID uuid.UUID, limit, offset int) ([]domain.Detail, int, error)
	// FindByShareHash returns the bill whose link has that hash (shared.ErrNotFound when there is none).
	FindByShareHash(ctx context.Context, hash string) (domain.Detail, error)
	// Insert stores a bill with its lines and readings and returns its id.
	Insert(ctx context.Context, inv domain.Invoice) (uuid.UUID, error)
	// Replace writes what a correction changes: the total, the due date, the note, the name and transfer words, the
	// lines and the readings (a reading of a meter that stays keeps its place in the history). It clears the
	// reminders already sent when the due date moved.
	Replace(ctx context.Context, inv domain.Invoice) error
	// SaveState writes the status, the sending and payment times and the delivery.
	SaveState(ctx context.Context, inv domain.Invoice) error
	// SaveShare writes the link of the bill (an empty Share removes it).
	SaveShare(ctx context.Context, id uuid.UUID, s domain.Share) error
	// Delete removes the bill with its lines and readings for good, so the period can be billed again.
	Delete(ctx context.Context, id uuid.UUID) error
	// BilledPeriods returns the periods of the contract that have a bill.
	BilledPeriods(ctx context.Context, contractID uuid.UUID) ([]domain.BilledPeriod, error)
	// CountOfContract counts the bills of a contract.
	CountOfContract(ctx context.Context, contractID uuid.UUID) (int, error)
	// MonthCounts counts the landlord's bills per month of their period start between two dates ("2026-04" -> n).
	MonthCounts(ctx context.Context, landlordID uuid.UUID, from, to time.Time) (map[string]int, error)
	// NumberTaken and CodeTaken say whether an invoice number or transfer text is in use (the bill exceptID aside).
	NumberTaken(ctx context.Context, number string) (bool, error)
	CodeTaken(ctx context.Context, code string, exceptID uuid.UUID) (bool, error)
	// LastReadings returns the last stored index of each meter. When a bill is being corrected, its own readings and
	// whatever was recorded after them do not count: the bill is counted from what was there before it.
	LastReadings(ctx context.Context, chargeIDs []uuid.UUID, editing *uuid.UUID) (map[uuid.UUID]float64, error)
	// ReadingHistory returns a page of the meter readings of a room, newest first, and how many there are in all; a
	// non-nil chargeID keeps one meter.
	ReadingHistory(ctx context.Context, roomID uuid.UUID, chargeID *uuid.UUID, limit, offset int) ([]domain.Reading, int, error)
	// Due returns sent bills whose due date falls in one of the windows and that have not had that reminder yet, with
	// their contract facts, after the id (keyset), at most limit.
	Due(ctx context.Context, soonFrom, soonTo, overdueFrom, overdueTo time.Time, after uuid.UUID, limit int) ([]domain.Detail, error)
	// MarkReminded records the reminder of each bill.
	MarkReminded(ctx context.Context, kind domain.Reminder, ids []uuid.UUID, on time.Time) error
}

// ContractReader tells the facts about contracts that billing needs (a read-only view of what the contract and
// property contexts own).
type ContractReader interface {
	// Get returns the contract as the viewer sees it (shared.ErrNotFound for anybody else's).
	Get(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.ContractFacts, error)
	// Lock returns a contract and locks it until the transaction ends, so the bills of one contract are made one at a
	// time (shared.ErrNotFound when there is none, or it was deleted).
	Lock(ctx context.Context, id uuid.UUID) (domain.ContractFacts, error)
	// Billable returns running contracts the landlord can see, after the id (keyset), at most limit.
	Billable(ctx context.Context, after uuid.UUID, limit int) ([]domain.ContractFacts, error)
	// FindByRoomName returns the latest contract of the landlord's live room of that name (the demo seed).
	FindByRoomName(ctx context.Context, landlordID uuid.UUID, room string) (domain.ContractFacts, error)
}

// ChargeReader tells the charges of rooms (a read-only view of what the property context owns).
type ChargeReader interface {
	// ChargesOf returns the live charges of a room in their order.
	ChargesOf(ctx context.Context, roomID uuid.UUID) ([]domain.Charge, error)
	// RoomOf says whether the room is a live room of the landlord (shared.ErrNotFound otherwise) and returns its name.
	RoomOf(ctx context.Context, landlordID, roomID uuid.UUID) (string, error)
}

// Ports to what later phases own. Each has a default that changes nothing, so the context works on its own and a
// later one is wired in cmd/rental-management without touching this one.

// Settlement is money the landlord says arrived, written down when a bill is marked paid by hand.
type Settlement struct {
	InvoiceID uuid.UUID
	// ActorID is the landlord who settled it.
	ActorID   uuid.UUID
	AmountVND int64
	At        time.Time
}

// PaymentLedger is what the bills need to know and ask of payments. The payment context implements it; until it
// exists NoLedger does, and a bill can still be marked paid and sent back (only the status and paid_at change). A
// settlement must only record the money: the invoice use case sets the status itself, so the ledger never calls back.
type PaymentLedger interface {
	// Totals returns, for each bill, the money received and the money waiting for the landlord's check.
	Totals(ctx context.Context, invoiceIDs []uuid.UUID) (map[uuid.UUID]domain.Totals, error)
	// Settle records a confirmed payment for money still owed when a bill is created paid or marked paid by hand.
	Settle(ctx context.Context, s Settlement) error
	// Void takes back the confirmed payments of a bill that is marked unpaid again, so it can be paid again.
	Void(ctx context.Context, invoiceID uuid.UUID) error
	// Purge removes what is left of the payments of a bill that is deleted (only rejected ones can be).
	Purge(ctx context.Context, invoiceID uuid.UUID) error
}

// NoLedger is the default: no payment was ever made or asked for.
type NoLedger struct{}

// Totals reports nothing paid and nothing pending.
func (NoLedger) Totals(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]domain.Totals, error) {
	return make(map[uuid.UUID]domain.Totals, len(ids)), nil
}

// Settle does nothing.
func (NoLedger) Settle(context.Context, Settlement) error { return nil }

// Void does nothing.
func (NoLedger) Void(context.Context, uuid.UUID) error { return nil }

// Purge does nothing.
func (NoLedger) Purge(context.Context, uuid.UUID) error { return nil }

// Notification is a message for one user about a bill.
type Notification struct {
	UserID uuid.UUID
	// Kind is one of the Notify* constants; the web translates it with Params.
	Kind     string
	Params   map[string]string
	URL      string
	Audience string
}

// Notifier tells a user about something that happened.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// NoNotifier is the default: nobody is told.
type NoNotifier struct{}

// Notify does nothing.
func (NoNotifier) Notify(context.Context, Notification) error { return nil }

// AuditEvent is something worth keeping in the audit trail.
type AuditEvent struct {
	// Action is one of the Audit* constants.
	Action string
	// ActorID is who did it (uuid.Nil for the system).
	ActorID    uuid.UUID
	InvoiceID  uuid.UUID
	ContractID uuid.UUID
	RoomID     uuid.UUID
	// OwnerID is the landlord whose data it is.
	OwnerID      uuid.UUID
	TargetUserID *uuid.UUID
	Meta         map[string]any
}

// AuditRecorder writes the audit trail; it runs in the transaction of ctx.
type AuditRecorder interface {
	Record(ctx context.Context, e AuditEvent) error
}

// NoAudit is the default: nothing is recorded.
type NoAudit struct{}

// Record does nothing.
func (NoAudit) Record(context.Context, AuditEvent) error { return nil }

// InvoiceMail is the email that carries a bill to the account of its tenant.
type InvoiceMail struct {
	To       string
	FullName string
	Room     string
	Name     string
	// From and To are the first and last day of the period.
	PeriodFrom time.Time
	PeriodTo   time.Time
	Items      []domain.Item
	TotalVND   int64
	DueAt      time.Time
	Note       *string
	// URL opens the bill in the app.
	URL string
}

// InvoiceMailer emails a bill to the account of its tenant. Bills passed on by link, and contracts nobody with an
// account holds, are never emailed.
type InvoiceMailer interface {
	SendInvoice(ctx context.Context, m InvoiceMail) error
}

// NoMailer is the default: nothing is emailed.
type NoMailer struct{}

// SendInvoice does nothing.
func (NoMailer) SendInvoice(context.Context, InvoiceMail) error { return nil }

// PlanFeatures says what the plan of a landlord switches on. The plan context implements it.
type PlanFeatures interface {
	// AutoInvoicing says whether the landlord's plan sends the bill of each period by itself.
	AutoInvoicing(ctx context.Context, landlordID uuid.UUID) (bool, error)
}

// NoPlanFeatures is the default: no plan switches anything on, so nothing is sent automatically.
type NoPlanFeatures struct{}

// AutoInvoicing is off.
func (NoPlanFeatures) AutoInvoicing(context.Context, uuid.UUID) (bool, error) { return false, nil }

// ShareTokens makes and hashes the secrets of share links.
type ShareTokens interface {
	// New returns a random token of domain.ShareTokenLength characters and the hash to look it up by.
	New() (token, hash string, err error)
	// Hash is the hash of a token as a client sends it.
	Hash(token string) string
}

// Sealer encrypts the secret of a share link, so the landlord can copy the same link again while a copy of the
// database alone does not give the links away.
type Sealer interface {
	Seal(plain string) (string, error)
	Open(sealed string) (string, error)
}

// Random draws the random parts of the numbers and transfer texts.
type Random interface {
	// Pick returns n characters drawn from alphabet.
	Pick(alphabet string, n int) (string, error)
}

// CryptoRandom is the default Random: the system's secure random source.
type CryptoRandom struct{}

// Pick draws n characters of alphabet.
func (CryptoRandom) Pick(alphabet string, n int) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("draw random characters: %w", err)
		}
		out[i] = alphabet[v.Int64()]
	}
	return string(out), nil
}

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// ClockFunc turns a function (time.Now) into a Clock.
type ClockFunc func() time.Time

// Now calls the function.
func (f ClockFunc) Now() time.Time { return f() }
