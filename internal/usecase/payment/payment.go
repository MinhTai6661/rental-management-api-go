// Package payment holds the use cases of how bills are paid and how landlords are paid: a landlord sets up the payment
// methods they accept, a tenant pays all or part of a sent bill (with proof pictures the landlord can see), the
// landlord confirms or rejects it or writes down money that came in outside the app, and the confirmed payments settle
// the bill. It also answers the invoice context's questions about the money of its bills (the payment ledger). It
// declares the ports it needs (ports.go) and imports no framework, driver or other context: the bills, the files, the
// signed links, the notifications, the audit trail and the mail are ports too.
package payment

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/payment"
	"rental-management/internal/domain/shared"
)

// Deps are the collaborators of the use cases. Nil optional ports get their no-op defaults.
type Deps struct {
	Tx       TxManager
	Payments PaymentRepository
	Methods  MethodRepository
	Invoices Invoices
	Status   InvoiceStatus
	Drivers  *Drivers
	Files    FileStore
	Images   ImageProcessor
	Links    ProofLinks
	Sealer   Sealer
	Notifier Notifier
	Audit    AuditRecorder
	Mailer   PaymentMailer
	Clock    Clock
	Log      *slog.Logger
}

// Options are the settings of the use cases.
type Options struct {
	// Location is the business time zone: a day the landlord names ends at its last second there. Default UTC+7.
	Location *time.Location
	// MaxProofFiles is how many proof pictures a payment may carry (default 3).
	MaxProofFiles int
	// ProofLinkTTL is how long the link to a proof works (default 2 hours).
	ProofLinkTTL time.Duration
	// WebURL is the origin of the web app: the links of the mails point there.
	WebURL string
}

// core is what the use case groups share: the ports, the settings and the helpers every group needs.
type core struct {
	tx       TxManager
	payments PaymentRepository
	methods  MethodRepository
	invoices Invoices
	status   InvoiceStatus
	drivers  *Drivers
	files    FileStore
	images   ImageProcessor
	links    ProofLinks
	sealer   Sealer
	notifier Notifier
	audit    AuditRecorder
	mailer   PaymentMailer
	clock    Clock
	log      *slog.Logger
	opts     Options
}

func newCore(d Deps, o Options) *core {
	if o.Location == nil {
		o.Location = time.FixedZone("ICT", 7*60*60)
	}
	if o.MaxProofFiles <= 0 {
		o.MaxProofFiles = 3
	}
	if o.ProofLinkTTL <= 0 {
		o.ProofLinkTTL = 2 * time.Hour
	}
	c := &core{
		tx: d.Tx, payments: d.Payments, methods: d.Methods, invoices: d.Invoices, status: d.Status, drivers: d.Drivers,
		files: d.Files, images: d.Images, links: d.Links, sealer: d.Sealer, notifier: d.Notifier, audit: d.Audit,
		mailer: d.Mailer, clock: d.Clock, log: d.Log, opts: o,
	}
	if c.drivers == nil {
		c.drivers = NewDrivers(o.MaxProofFiles)
	}
	if c.notifier == nil {
		c.notifier = NoNotifier{}
	}
	if c.audit == nil {
		c.audit = NoAudit{}
	}
	if c.mailer == nil {
		c.mailer = NoMailer{}
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.clock == nil {
		c.clock = ClockFunc(time.Now)
	}
	return c
}

// notFound turns a repository's "no such row" into the domain's not found.
func notFound(err error) error {
	if errors.Is(err, shared.ErrNotFound) {
		return domain.ErrNotFound
	}
	return err
}

func landlordView(id uuid.UUID) domain.Viewer { return domain.Viewer{UserID: id, AsLandlord: true} }

// totalsOf returns the money of one bill.
func (c *core) totalsOf(ctx context.Context, invoiceID uuid.UUID) (domain.Totals, error) {
	totals, err := c.payments.Totals(ctx, []uuid.UUID{invoiceID})
	return totals[invoiceID], err
}

// record writes an audit event about a payment.
func (c *core) record(ctx context.Context, action string, actor uuid.UUID, inv domain.InvoiceFacts, paymentID uuid.UUID, payer *uuid.UUID, meta map[string]any) error {
	return c.audit.Record(ctx, AuditEvent{
		Action: action, ActorID: actor, PaymentID: paymentID, InvoiceID: inv.ID, ContractID: inv.ContractID, RoomID: inv.RoomID,
		OwnerID: inv.LandlordID, TargetUserID: payer, Meta: meta,
	})
}

// derefID is the id a pointer holds, uuid.Nil for none.
func derefID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

// notify tells a user about a payment. The payment is already stored, so a failure is logged and never undoes it.
func (c *core) notify(ctx context.Context, n Notification) {
	if err := c.notifier.Notify(ctx, n); err != nil {
		c.log.Warn("notify about a payment", "error", err, "kind", n.Kind, "user_id", n.UserID)
	}
}

func (c *core) params(d domain.Detail) map[string]string {
	payer := ""
	if d.Payer != nil {
		payer = d.Payer.Name
	}
	return map[string]string{
		"room": d.Invoice.RoomName, "tenant": payer, "invoice_number": d.Invoice.Label(), "amount_vnd": itoa(d.AmountVND),
		"invoice_id": d.Invoice.ID.String(), "contract_id": d.Invoice.ContractID.String(),
	}
}

// notifyLandlord tells the landlord a tenant sent a payment, with what is still owed once it is counted.
func (c *core) notifyLandlord(ctx context.Context, d domain.Detail) {
	remaining := int64(0)
	if t, err := c.totalsOf(ctx, d.Invoice.ID); err == nil {
		remaining = domain.Remaining(d.Invoice.TotalVND, t)
	}
	params := c.params(d)
	params["remaining_vnd"] = itoa(remaining)
	c.notify(ctx, Notification{
		UserID: d.Invoice.LandlordID, Kind: NotifyPaymentSubmitted, Params: params,
		URL: "/contracts/" + d.Invoice.ContractID.String(), Audience: AudienceLandlord,
	})
}

// notifyPayer tells the tenant what the landlord decided, in the app and by email. A payment the landlord wrote down
// has nobody to tell.
func (c *core) notifyPayer(ctx context.Context, d domain.Detail, kind string) {
	if d.PayerID == nil {
		return
	}
	c.notify(ctx, Notification{
		UserID: *d.PayerID, Kind: kind, Params: c.params(d), URL: "/my-rooms/" + d.Invoice.ContractID.String(), Audience: AudienceTenant,
	})
	if d.Payer == nil || d.Payer.Email == nil || *d.Payer.Email == "" {
		return
	}
	err := c.mailer.SendReview(ctx, ReviewMail{
		To: *d.Payer.Email, FullName: d.Payer.Name, Room: d.Invoice.RoomName, Invoice: d.Invoice.Label(), AmountVND: d.AmountVND,
		Confirmed: kind == NotifyPaymentConfirmed, Reason: d.ReviewNote,
		URL: strings.TrimRight(c.opts.WebURL, "/") + "/my-rooms/" + d.Invoice.ContractID.String(),
	})
	if err != nil {
		c.log.Warn("email a payment decision", "error", err, "payment_id", d.ID)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// endOfDay is the last second of a calendar date in the business time zone.
func (c *core) endOfDay(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 23, 59, 59, 0, c.opts.Location)
}

// today is the calendar date now in the business time zone, at midnight UTC.
func (c *core) today() time.Time {
	n := c.clock.Now().In(c.opts.Location)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}
