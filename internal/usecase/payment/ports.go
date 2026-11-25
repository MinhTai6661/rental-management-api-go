package payment

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/payment"
)

// The interfaces below are what the payment use cases need from the outside world. The adapters implement them
// structurally (they never import this package), so the rules are tested with fakes and no database.
// Repository lookups return shared.ErrNotFound when there is no such row.

// TxManager runs fn in one transaction. Repository calls made with the ctx given to fn join it, and so do the
// other ports implemented over the same database.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// PaymentRepository stores payments. Reads are scoped to a viewer: a landlord sees the payments on the bills of their
// own rooms, a tenant the ones they made.
type PaymentRepository interface {
	// Get returns a payment as the viewer sees it (shared.ErrNotFound for anybody else's).
	Get(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.Detail, error)
	// ProofKeys returns the storage keys of the proof pictures of a payment, whoever it belongs to (the caller has
	// checked a signed link).
	ProofKeys(ctx context.Context, id uuid.UUID) ([]string, error)
	// ForUpdate returns a payment and locks it until the transaction ends.
	ForUpdate(ctx context.Context, id uuid.UUID) (domain.Payment, error)
	// ListOfInvoice returns a page of the payments of a bill, oldest first, and how many there are in all.
	ListOfInvoice(ctx context.Context, invoiceID uuid.UUID, limit, offset int) ([]domain.Detail, int, error)
	// ListOfLandlord returns a page of the payments on the bills of a landlord's rooms, newest first (nil status keeps
	// them all), and how many there are in all.
	ListOfLandlord(ctx context.Context, landlordID uuid.UUID, status *domain.Status, limit, offset int) ([]domain.Detail, int, error)
	// Insert stores a payment and returns its id.
	Insert(ctx context.Context, p domain.Payment) (uuid.UUID, error)
	// SaveReview writes what a review changes: the status, who reviewed it and when, the note and the paid time.
	SaveReview(ctx context.Context, p domain.Payment) error
	// Delete removes a payment for good.
	Delete(ctx context.Context, id uuid.UUID) error
	// Totals returns the confirmed and the pending money of each bill (a bill with no payment is left out).
	Totals(ctx context.Context, invoiceIDs []uuid.UUID) (map[uuid.UUID]domain.Totals, error)
	// VoidConfirmed marks the confirmed payments of a bill voided, so they no longer count.
	VoidConfirmed(ctx context.Context, invoiceID uuid.UUID) error
	// DeleteOfInvoice removes every payment of a bill and returns the keys of their proof files.
	DeleteOfInvoice(ctx context.Context, invoiceID uuid.UUID) ([]string, error)
}

// MethodRepository stores the payment methods of landlords, one row per landlord and method.
type MethodRepository interface {
	// ListOfLandlord returns what the landlord saved.
	ListOfLandlord(ctx context.Context, landlordID uuid.UUID) ([]domain.StoredMethod, error)
	// Get returns one saved method (shared.ErrNotFound when the landlord never saved it).
	Get(ctx context.Context, landlordID uuid.UUID, method string) (domain.StoredMethod, error)
	// Upsert saves a method and says whether it was new.
	Upsert(ctx context.Context, landlordID uuid.UUID, m domain.StoredMethod) (created bool, err error)
	// Delete removes a saved method and says whether there was one.
	Delete(ctx context.Context, landlordID uuid.UUID, method string) (bool, error)
}

// Invoices tells the facts about bills that payments need (a read-only view of what the invoice, contract and property
// contexts own).
type Invoices interface {
	// Get returns a bill as the viewer sees it: a landlord the bills of their rooms' contracts, a tenant the bills sent to
	// them (shared.ErrNotFound for anybody else's).
	Get(ctx context.Context, v domain.Viewer, id uuid.UUID) (domain.InvoiceFacts, error)
	// ByID returns a bill for the system, whoever it belongs to.
	ByID(ctx context.Context, id uuid.UUID) (domain.InvoiceFacts, error)
	// Lock returns a bill and locks it until the transaction ends, so the payments of one bill are made one at a time.
	Lock(ctx context.Context, id uuid.UUID) (domain.InvoiceFacts, error)
	// SentOfRoom returns the sent bills of a landlord's live room of that name (any case), oldest period first (the
	// demo seed).
	SentOfRoom(ctx context.Context, landlordID uuid.UUID, room string) ([]domain.InvoiceFacts, error)
}

// InvoiceStatus is what payments ask of bills: the bill moves to paid when the confirmed money covers it. The invoice
// context implements it (see cmd/rental-management), so the status rules stay where they live.
type InvoiceStatus interface {
	// MarkPaid sets a sent bill of the landlord paid. It is called inside the transaction of the payment, with the bill
	// locked, after the payment that covers it was confirmed.
	MarkPaid(ctx context.Context, landlordID, invoiceID uuid.UUID) error
}

// FileInfo describes a stored file.
type FileInfo struct {
	Size        int64
	ContentType string
}

// FileStore keeps the proof files. A key is written once and never overwritten.
type FileStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Open returns the content of key (domain.ErrFileNotFound when there is none).
	Open(ctx context.Context, key string) (io.ReadCloser, FileInfo, error)
	// Delete removes the files; keys that do not exist are not an error.
	Delete(ctx context.Context, keys ...string) error
}

// ProcessedImage is a verified picture ready to store: the picture and its small copy.
type ProcessedImage struct {
	Data        []byte
	ContentType string
	// Ext is "webp" or "jpg".
	Ext string
	// Thumb is a WebP.
	Thumb []byte
}

// ImageProcessor verifies an uploaded picture and makes what to store. A refusal is one of domain.ErrImageType,
// ErrImageCorrupt, ErrImageDimensions and ErrImageTooLarge.
type ImageProcessor interface {
	Process(ctx context.Context, data []byte) (ProcessedImage, error)
}

// ProofLinks makes and checks the signed, expiring links to proof files: a link is the only way to read a proof, so
// only the two sides of a payment (who are given one) can.
type ProofLinks interface {
	// Link returns the absolute address of a proof file of a payment that works until expires.
	Link(paymentID uuid.UUID, file string, expires time.Time) string
	// Valid says whether the signature belongs to the file of the payment and the link has not expired at now.
	Valid(paymentID uuid.UUID, file string, expires int64, signature string, now time.Time) bool
}

// Sealer encrypts the settings of a payment method (where the landlord's money goes), so a copy of the database alone
// does not show them.
type Sealer interface {
	Seal(plain string) (string, error)
	Open(sealed string) (string, error)
}

// Ports to what later phases own. Each has a default that changes nothing, so the context works on its own and a
// later one is wired in cmd/rental-management without touching this one.

// Notification is a message for one user about a payment.
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
	ActorID   uuid.UUID
	PaymentID uuid.UUID
	InvoiceID uuid.UUID
	// ContractID and RoomID are uuid.Nil for what is not about a bill (a payment method).
	ContractID uuid.UUID
	RoomID     uuid.UUID
	// OwnerID is the landlord whose data it is.
	OwnerID      uuid.UUID
	TargetUserID *uuid.UUID
	// Meta never holds proof pictures or whole account numbers.
	Meta map[string]any
}

// AuditRecorder writes the audit trail; it runs in the transaction of ctx.
type AuditRecorder interface {
	Record(ctx context.Context, e AuditEvent) error
}

// NoAudit is the default: nothing is recorded.
type NoAudit struct{}

// Record does nothing.
func (NoAudit) Record(context.Context, AuditEvent) error { return nil }

// ReviewMail is the email that tells a tenant what the landlord decided about their payment.
type ReviewMail struct {
	To        string
	FullName  string
	Room      string
	Invoice   string
	AmountVND int64
	Confirmed bool
	// Reason is why a payment was rejected.
	Reason *string
	// URL opens the rental in the app.
	URL string
}

// PaymentMailer emails the decision about a payment to its payer.
type PaymentMailer interface {
	SendReview(ctx context.Context, m ReviewMail) error
}

// NoMailer is the default: nothing is emailed.
type NoMailer struct{}

// SendReview does nothing.
func (NoMailer) SendReview(context.Context, ReviewMail) error { return nil }

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// ClockFunc turns a function (time.Now) into a Clock.
type ClockFunc func() time.Time

// Now calls the function.
func (f ClockFunc) Now() time.Time { return f() }
