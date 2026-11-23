// Package invoice holds the use cases of the bills of a rental contract: the landlord previews, writes (as a draft),
// corrects, sends, settles and deletes them and passes them on by link, a tenant reads the bills sent to them, anybody
// with a link reads one bill, and two daily commands send the bills of plans with automatic invoicing and remind
// people of bills about to fall due. It declares the ports it needs (ports.go) and imports no framework, driver or
// other context: the contracts, the room charges, the payments, the notifications, the audit trail, the mail and the
// plan features are ports too.
package invoice

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invoice"
	"rental-management/internal/domain/shared"
)

// Deps are the collaborators of the use cases. Nil optional ports get their no-op defaults.
type Deps struct {
	Tx        TxManager
	Invoices  InvoiceRepository
	Contracts ContractReader
	Charges   ChargeReader
	Ledger    PaymentLedger
	Notifier  Notifier
	Audit     AuditRecorder
	Mailer    InvoiceMailer
	Plans     PlanFeatures
	Tokens    ShareTokens
	Sealer    Sealer
	Random    Random
	Clock     Clock
	Log       *slog.Logger
}

// Options are the settings of the use cases.
type Options struct {
	// Location is the business time zone: "today" is the calendar date there. Default UTC+7 (Vietnam).
	Location *time.Location
	// DueDays is how many days after today a bill falls due when the landlord gives no date (default 7).
	DueDays int
	// ShareTTL is how long a share link works (default 90 days).
	ShareTTL time.Duration
	// ReminderDays is how many days before the due date the reminder goes out (default 3).
	ReminderDays int
	// WebURL is the origin of the web app: the links of bills point there.
	WebURL string
}

// core is what the use case groups share: the ports, the settings and the helpers every group needs.
type core struct {
	tx        TxManager
	invoices  InvoiceRepository
	contracts ContractReader
	charges   ChargeReader
	ledger    PaymentLedger
	notifier  Notifier
	audit     AuditRecorder
	mailer    InvoiceMailer
	plans     PlanFeatures
	tokens    ShareTokens
	sealer    Sealer
	random    Random
	clock     Clock
	log       *slog.Logger
	opts      Options
}

func newCore(d Deps, o Options) *core {
	c := &core{
		tx: d.Tx, invoices: d.Invoices, contracts: d.Contracts, charges: d.Charges, ledger: d.Ledger, notifier: d.Notifier,
		audit: d.Audit, mailer: d.Mailer, plans: d.Plans, tokens: d.Tokens, sealer: d.Sealer, random: d.Random,
		clock: d.Clock, log: d.Log, opts: o,
	}
	if c.ledger == nil {
		c.ledger = NoLedger{}
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
	if c.plans == nil {
		c.plans = NoPlanFeatures{}
	}
	if c.random == nil {
		c.random = CryptoRandom{}
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.clock == nil {
		c.clock = ClockFunc(time.Now)
	}
	if c.opts.Location == nil {
		c.opts.Location = time.FixedZone("ICT", 7*60*60)
	}
	if c.opts.DueDays <= 0 {
		c.opts.DueDays = 7
	}
	if c.opts.ShareTTL <= 0 {
		c.opts.ShareTTL = 90 * 24 * time.Hour
	}
	if c.opts.ReminderDays <= 0 {
		c.opts.ReminderDays = 3
	}
	return c
}

func (c *core) today() time.Time { return domain.Today(c.clock.Now(), c.opts.Location) }

// notFound turns a repository's "no such row" into the domain's not found.
func notFound(err error) error {
	if errors.Is(err, shared.ErrNotFound) {
		return domain.ErrNotFound
	}
	return err
}

// landlordView is the landlord as a viewer of bills.
func landlordView(id uuid.UUID) domain.Viewer { return domain.Viewer{UserID: id, AsLandlord: true} }

// lockContract locks the contract of a landlord's bill; somebody else's contract is not found.
func (c *core) lockContract(ctx context.Context, landlordID, id uuid.UUID) (domain.ContractFacts, error) {
	facts, err := c.contracts.Lock(ctx, id)
	if err != nil {
		return domain.ContractFacts{}, notFound(err)
	}
	if facts.LandlordID != landlordID {
		return domain.ContractFacts{}, domain.ErrNotFound
	}
	return facts, nil
}

// billedSet is the set of the period start dates of the contract that have a bill.
func (c *core) billedSet(ctx context.Context, contractID uuid.UUID) (map[time.Time]bool, error) {
	billed, err := c.invoices.BilledPeriods(ctx, contractID)
	if err != nil {
		return nil, err
	}
	set := make(map[time.Time]bool, len(billed))
	for _, b := range billed {
		set[b.PeriodStart] = true
	}
	return set, nil
}

// compose works out the lines and readings of a bill for the period. withCarry completes the readings of a bill the
// landlord writes (a meter left out keeps its index); the automatic bill carries the fixed charges only and does not.
// editing is the bill being corrected: it is counted from what was there before it.
func (c *core) compose(ctx context.Context, f domain.ContractFacts, p domain.Period, in BillInput, withCarry bool, editing *uuid.UUID) ([]domain.Item, int64, []domain.Reading, error) {
	charges, err := c.charges.ChargesOf(ctx, f.RoomID)
	if err != nil {
		return nil, 0, nil, err
	}
	var stored map[uuid.UUID]float64
	if metered := domain.MeteredIDs(charges); len(metered) > 0 {
		if stored, err = c.invoices.LastReadings(ctx, metered, editing); err != nil {
			return nil, 0, nil, err
		}
	}
	readings := in.Readings
	if withCarry {
		if readings, err = domain.CarryOver(readings, charges, stored); err != nil {
			return nil, 0, nil, err
		}
	}
	used, err := domain.Consumptions(readings, charges, stored)
	if err != nil {
		return nil, 0, nil, err
	}
	rent := f.PriceVND
	if in.RentUnitPriceVND != nil {
		rent = *in.RentUnitPriceVND
	}
	items, total := domain.Lines(domain.LineInput{
		Units: p.Units, PeriodIndex: p.Index, Occupants: f.OccupantCount, RentUnitPriceVND: rent,
		RentDescription: RentDescription, Charges: charges, Consumption: used, Extras: in.Extras,
	})
	return items, total, domain.Readings(used), nil
}

// dueAt is the end of the day a bill falls due: the given date, else DueDays from today.
func (c *core) dueAt(on *time.Time) time.Time {
	day := c.today().AddDate(0, 0, c.opts.DueDays)
	if on != nil {
		day = *on
	}
	return domain.EndOfDay(day, c.opts.Location)
}

// record writes an audit event about a bill.
func (c *core) record(ctx context.Context, action string, actor uuid.UUID, inv domain.Invoice, f domain.ContractFacts, meta map[string]any) error {
	return c.audit.Record(ctx, AuditEvent{
		Action: action, ActorID: actor, InvoiceID: inv.ID, ContractID: inv.ContractID, RoomID: f.RoomID, OwnerID: f.LandlordID,
		TargetUserID: f.TenantID, Meta: meta,
	})
}

// totalsOf asks the payment ledger about the bills.
func (c *core) totalsOf(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domain.Totals, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]domain.Totals{}, nil
	}
	return c.ledger.Totals(ctx, ids)
}

// views adds what the viewer may do and how much is paid to bills, with one question to the ledger for all of them.
func (c *core) views(ctx context.Context, v domain.Viewer, details []domain.Detail) ([]View, error) {
	ids := make([]uuid.UUID, len(details))
	for i, d := range details {
		ids[i] = d.ID
	}
	totals, err := c.totalsOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]View, len(details))
	for i, d := range details {
		t := totals[d.ID]
		out[i] = View{Detail: d, Summary: domain.SummaryOf(d.TotalVND, t), Abilities: domain.AbilitiesFor(v, d.Status, t)}
	}
	return out, nil
}

func (c *core) view(ctx context.Context, v domain.Viewer, d domain.Detail) (View, error) {
	views, err := c.views(ctx, v, []domain.Detail{d})
	if err != nil {
		return View{}, err
	}
	return views[0], nil
}

// billURL is the address of the bill in the web app (the tenant's notice and email open it).
func (c *core) billURL(inv domain.Invoice) string {
	return "/my-rooms/" + inv.ContractID.String() + "?pay=" + inv.ID.String()
}

// notifyTenant tells the tenant of the bill's contract. The bill is already stored, so a failure is logged and never
// undoes it. A bill passed on by link, or one for a contract nobody holds, has no account to tell.
func (c *core) notifyTenant(ctx context.Context, d domain.Detail, kind string) bool {
	if d.Contract.TenantID == nil || !d.Delivery.ReachesAccount() {
		return false
	}
	c.notify(ctx, Notification{UserID: *d.Contract.TenantID, Kind: kind, Params: c.params(d), URL: c.billURL(d.Invoice), Audience: AudienceTenant})
	return true
}

func (c *core) notify(ctx context.Context, n Notification) {
	if err := c.notifier.Notify(ctx, n); err != nil {
		c.log.Warn("notify about an invoice", "error", err, "kind", n.Kind, "user_id", n.UserID)
	}
}

func (c *core) params(d domain.Detail) map[string]string {
	return map[string]string{
		"invoice_number": d.Label(), "room": d.Contract.RoomName, "amount_vnd": itoa(d.TotalVND),
		"due_date":   domain.Today(d.DueAt, c.opts.Location).Format(domain.DateLayout),
		"invoice_id": d.ID.String(), "contract_id": d.ContractID.String(),
	}
}

// mail emails the bill to the account of its tenant, when it goes by the app and the account has an email. Like the
// notice, a failure is logged and never undoes the bill.
func (c *core) mail(ctx context.Context, d domain.Detail) {
	t := d.Contract
	if !d.Delivery.ReachesAccount() || t.TenantID == nil || t.TenantEmail == nil || *t.TenantEmail == "" {
		return
	}
	name := ""
	if t.TenantAccountName != nil {
		name = *t.TenantAccountName
	}
	err := c.mailer.SendInvoice(ctx, InvoiceMail{
		To: *t.TenantEmail, FullName: name, Room: t.RoomName, Name: d.Label(), PeriodFrom: d.PeriodStart, PeriodTo: d.PeriodEnd,
		Items: d.Items, TotalVND: d.TotalVND, DueAt: d.DueAt, Note: d.Note, URL: strings.TrimRight(c.opts.WebURL, "/") + c.billURL(d.Invoice),
	})
	if err != nil {
		c.log.Warn("email an invoice", "error", err, "invoice_id", d.ID)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
