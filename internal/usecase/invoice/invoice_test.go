package invoice

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invoice"
)

var ctx = context.Background()

func ptr[T any](v T) *T { return &v }

func date(s string) time.Time {
	d, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

// kit is a landlord with a tenant who holds a running contract on P101 that started two months ago (three periods have
// started), and a monthly rent of 3,000,000 VND.
type kit struct {
	*world
	contract *domain.ContractFacts
}

func newKit() *kit {
	w := newWorld()
	return &kit{world: w, contract: w.store.contract(w.landlord, &w.tenant, "P101", 2)}
}

func (k *kit) create(t *testing.T, in CreateInput) View {
	t.Helper()
	in.ContractID = k.contract.ID
	v, err := k.invoices.Create(ctx, k.landlord, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return v
}

func violation(t *testing.T, err error, field, reason string) *domain.RuleViolation {
	t.Helper()
	var v *domain.RuleViolation
	if !errors.As(err, &v) || v.Field != field || v.Reason != reason {
		t.Fatalf("want violation %s/%s, got %v", field, reason, err)
	}
	return v
}

func (k *kit) electricity() domain.Charge {
	return k.store.charge(k.contract, domain.UnitPerKWh, "Electricity", 3500)
}

func reading(c domain.Charge, value float64, previous *float64) []domain.ReadingInput {
	return []domain.ReadingInput{{ChargeID: c.ID, Value: value, Previous: previous}}
}

// --- creating bills

func TestCreateWritesADraftOfTheOldestPeriodWithoutABillAndTellsNobody(t *testing.T) {
	k := newKit()
	v := k.create(t, CreateInput{Note: ptr("Thanks")})

	if v.Status != domain.StatusDraft || v.SentAt != nil || v.SentVia != nil || v.Delivery != domain.DeliveryApp {
		t.Fatalf("a new bill is a draft: %+v", v.Invoice)
	}
	if v.PeriodStart.Format(domain.DateLayout) != "2026-09-23" || v.PeriodEnd.Format(domain.DateLayout) != "2026-10-22" {
		t.Errorf("the oldest period: %v - %v", v.PeriodStart, v.PeriodEnd)
	}
	if v.TotalVND != 3_000_000 || len(v.Items) != 1 || v.Items[0].Kind != domain.KindRent || v.Items[0].Description != RentDescription {
		t.Errorf("rent only: %+v", v.Items)
	}
	if v.CreatedByID == nil || *v.CreatedByID != k.landlord || v.Note == nil || *v.Note != "Thanks" {
		t.Errorf("created by the landlord with a note: %+v", v.Invoice)
	}
	if v.Name != "09/2026 - P101" || !strings.HasPrefix(v.PaymentLabel, "HD T09-P101") || !strings.HasPrefix(v.Number, "INV-202609-") {
		t.Errorf("name %q label %q number %q", v.Name, v.PaymentLabel, v.Number)
	}
	if want := domain.EndOfDay(date("2026-11-30"), time.UTC); !v.DueAt.Equal(want) {
		t.Errorf("due a week from today, end of the day: %v", v.DueAt)
	}
	if !strings.HasPrefix(v.PaymentCode, v.PaymentLabel+" ") || len(v.PaymentCode) > domain.MaxCodeLength {
		t.Errorf("transfer text %q", v.PaymentCode)
	}
	if len(k.rec.mails) != 0 || len(k.rec.notes) != 0 {
		t.Errorf("a draft tells nobody: %v %v", k.rec.mails, k.rec.notes)
	}
	if got := k.rec.actions(); !slices.Equal(got, []string{AuditCreated}) || k.rec.events[0].OwnerID != k.landlord || k.rec.events[0].ActorID != k.landlord {
		t.Errorf("audit: %v", got)
	}
	if v.Summary.RemainingVND != 3_000_000 || !v.Abilities.Update || !v.Abilities.SetStatus {
		t.Errorf("summary %+v abilities %+v", v.Summary, v.Abilities)
	}
}

func TestCreateTakesTheNextPeriodEachTimeAndRefusesABilledOneAndOneThatNeverStarted(t *testing.T) {
	k := newKit()
	first := k.create(t, CreateInput{})
	second := k.create(t, CreateInput{})
	if !second.PeriodStart.After(first.PeriodStart) {
		t.Fatalf("the second bill is for the next period: %v %v", first.PeriodStart, second.PeriodStart)
	}

	// Asking for a period that has a bill is refused: one bill per period.
	_, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: k.contract.ID, PeriodStart: &first.PeriodStart})
	if !errors.Is(err, domain.ErrPeriodBilled) {
		t.Errorf("a billed period: %v", err)
	}
	// A date that starts no started period.
	_, err = k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: k.contract.ID, PeriodStart: ptr(date("2027-01-23"))})
	_ = violation(t, err, domain.FieldPeriodStart, domain.ReasonInvalidPeriod)

	third := k.create(t, CreateInput{PeriodStart: ptr(date("2026-11-23"))})
	if third.PeriodStart.Format(domain.DateLayout) != "2026-11-23" {
		t.Errorf("the period asked for: %v", third.PeriodStart)
	}
	// Everything is billed now: without a choice the last period is picked, which has a bill.
	if _, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: k.contract.ID}); !errors.Is(err, domain.ErrPeriodBilled) {
		t.Errorf("nothing left to bill: %v", err)
	}
	if n, _ := k.store.CountOfContract(ctx, k.contract.ID); n != 3 {
		t.Errorf("three bills, got %d", n)
	}
}

func TestCreateSaysWhyThereIsNothingToBill(t *testing.T) {
	k := newKit()
	future := k.store.contract(k.landlord, &k.tenant, "P102", 0)
	future.StartsOn = date("2027-03-01")
	_, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: future.ID})
	_ = violation(t, err, domain.FieldPeriodStart, domain.ReasonNothingToBill)

	byDay := k.store.contract(k.landlord, &k.tenant, "P103", 0)
	byDay.BilledByDay = true
	if byDay.StartsOn.After(date("2026-11-23")) {
		t.Fatal("setup")
	}
	// A stay by the day without an end date is billed month by month, so it has periods; with a future start it needs the end date.
	byDay.StartsOn = date("2027-03-01")
	_, err = k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: byDay.ID})
	_ = violation(t, err, domain.FieldPeriodStart, domain.ReasonDayNeedsEnd)
}

func TestCreateOnlyForARunningContractOfTheLandlord(t *testing.T) {
	k := newKit()
	k.contract.Status = "ended"
	if _, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: k.contract.ID}); !errors.Is(err, domain.ErrContractNotRunning) {
		t.Errorf("an ended contract: %v", err)
	}
	k.contract.Status = "active"

	other := id()
	if _, err := k.invoices.Create(ctx, other, CreateInput{ContractID: k.contract.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord's contract is not found: %v", err)
	}
	if _, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: id()}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a contract that does not exist: %v", err)
	}
	if n, _ := k.store.CountOfContract(ctx, k.contract.ID); n != 0 {
		t.Errorf("nothing was made: %d", n)
	}
}

func TestCreateBillsRentFixedChargesReadingsAndExtraLines(t *testing.T) {
	k := newKit()
	k.contract.OccupantCount = 3
	k.store.charge(k.contract, domain.UnitPerPeriod, "Cleaning", 100_000)
	k.store.charge(k.contract, domain.UnitPerPerson, "Water", 50_000)
	electricity := k.electricity()
	k.store.charge(k.contract, domain.UnitOneTime, "Set-up", 500_000)

	v := k.create(t, CreateInput{BillInput: BillInput{
		Readings: reading(electricity, 1100, ptr(1000.0)), Extras: []domain.Extra{{Description: "Repair", AmountVND: 20_000}},
	}})

	// 3,000,000 rent + 100,000 + 3 x 50,000 + 100 kWh x 3,500 + 500,000 one-time + 20,000.
	var descriptions []string
	for _, it := range v.Items {
		descriptions = append(descriptions, it.Description)
	}
	want := []string{RentDescription, "Cleaning", "Water", "Electricity (1000 → 1100)", "Set-up", "Repair"}
	if v.TotalVND != 4_120_000 || !slices.Equal(descriptions, want) {
		t.Fatalf("total %d lines %v", v.TotalVND, descriptions)
	}
	if len(v.Readings) != 1 || v.Readings[0].Value != 1100 || v.Readings[0].Consumption != 100 || v.Readings[0].Previous() != 1000 {
		t.Errorf("the reading is kept as history: %+v", v.Readings)
	}
	// The one-time fee goes on the first bill only.
	second := k.create(t, CreateInput{})
	if second.TotalVND != 3_000_000+100_000+150_000 {
		t.Errorf("the next bill carries the fixed charges, the meter left out uses nothing: %d", second.TotalVND)
	}
}

func TestReadingsAreCheckedWhenABillIsMade(t *testing.T) {
	k := newKit()
	electricity := k.electricity()
	fixed := k.store.charge(k.contract, domain.UnitPerPeriod, "Internet", 100_000)

	// A meter that was never read needs the index it started from.
	_, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: k.contract.ID})
	if v := violation(t, err, domain.FieldReadings, domain.ReasonNewReadingRequired); v.Params["service"] != "Electricity" {
		t.Errorf("params: %v", v.Params)
	}
	_, err = k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: k.contract.ID, BillInput: BillInput{Readings: reading(electricity, 1100, nil)}})
	_ = violation(t, err, domain.FieldReadings, domain.ReasonPreviousReadingRequired)

	// A reading for a charge that is not metered.
	_, err = k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: k.contract.ID, BillInput: BillInput{Readings: append(reading(electricity, 1100, ptr(1000.0)), reading(fixed, 5, ptr(0.0))...)}})
	_ = violation(t, err, domain.FieldReadings, domain.ReasonUnknownService)

	k.create(t, CreateInput{BillInput: BillInput{Readings: reading(electricity, 1100, ptr(1000.0))}})
	// The next bill is counted from the stored reading; a lower index is refused, a higher one charged.
	_, err = k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: k.contract.ID, BillInput: BillInput{Readings: reading(electricity, 1050, nil)}})
	if v := violation(t, err, domain.FieldReadings, domain.ReasonReadingLowerThanPrevious); v.Params["previous"] != "1100" {
		t.Errorf("params: %v", v.Params)
	}
	next := k.create(t, CreateInput{BillInput: BillInput{Readings: reading(electricity, 1300, nil)}})
	if next.TotalVND != 3_000_000+100_000+200*3500 {
		t.Errorf("200 kWh from the stored index: %d", next.TotalVND)
	}
	if n := len(k.store.readings); n != 2 {
		t.Errorf("only the bills that were made recorded readings: %d", n)
	}
}

func TestAMeterLeftOutKeepsItsPreviousIndexSoNothingIsConsumed(t *testing.T) {
	k := newKit()
	electricity := k.electricity()
	first := k.create(t, CreateInput{BillInput: BillInput{Readings: reading(electricity, 1500, ptr(1000.0))}})
	if first.Items[1].Quantity != 500 {
		t.Fatalf("500 kWh: %+v", first.Items)
	}
	second := k.create(t, CreateInput{})
	if len(second.Readings) != 1 || second.Readings[0].Value != 1500 || second.Readings[0].Consumption != 0 || len(second.Items) != 1 {
		t.Errorf("index kept, nothing consumed: %+v %+v", second.Readings, second.Items)
	}
}

func TestThePriceOfAReadingAndTheRentCanBeChangedForOneBill(t *testing.T) {
	k := newKit()
	electricity := k.electricity()
	price := int64(4000)
	in := reading(electricity, 1100, ptr(1000.0))
	in[0].UnitPriceVND = &price
	v := k.create(t, CreateInput{BillInput: BillInput{Readings: in, RentUnitPriceVND: ptr(int64(2_500_000))}})
	if v.TotalVND != 2_500_000+100*4000 || v.Items[0].UnitPriceVND != 2_500_000 {
		t.Errorf("total %d", v.TotalVND)
	}
	if k.contract.PriceVND != 3_000_000 || k.store.charges[k.contract.RoomID][0].AmountVND != 3500 {
		t.Error("the contract and the charge keep their price")
	}
}

func TestCreateAlreadyPaidIsSentAndPaidAtOnceCollectsTheMoneyAndTellsNobody(t *testing.T) {
	k := newKit()
	v := k.create(t, CreateInput{AlreadyPaid: true})

	if v.Status != domain.StatusPaid || v.PaidAt == nil || v.SentAt == nil || v.SentVia == nil || *v.SentVia != domain.SourceManual {
		t.Fatalf("sent and paid: %+v", v.Invoice)
	}
	if len(k.ledger.settled) != 1 || k.ledger.settled[0].AmountVND != 3_000_000 || k.ledger.settled[0].ActorID != k.landlord {
		t.Errorf("the money counts as collected: %+v", k.ledger.settled)
	}
	if len(k.rec.mails) != 0 || len(k.rec.notes) != 0 {
		t.Errorf("nothing left to ask for: %v %v", k.rec.mails, k.rec.notes)
	}
	if got := k.rec.actions(); !slices.Equal(got, []string{AuditCreated, AuditSent, AuditStatusChanged}) {
		t.Errorf("audit: %v", got)
	}
}

func TestADraftRefusesTheNameTheCodeAndTheDeliveryItWasGiven(t *testing.T) {
	k := newKit()
	link := domain.DeliveryLink
	v := k.create(t, CreateInput{Name: ptr("  "), PaymentLabel: ptr("P101 T11"), Delivery: &link, DueOn: ptr(date("2026-12-25"))})
	if v.Name != "09/2026 - P101" {
		t.Errorf("an empty name takes the usual: %q", v.Name)
	}
	if v.PaymentLabel != "P101 T11" || !strings.HasPrefix(v.PaymentCode, "P101 T11 ") || len(v.PaymentCode) != len("P101 T11 ")+domain.TailLength {
		t.Errorf("the landlord's words and a random tail: %q %q", v.PaymentLabel, v.PaymentCode)
	}
	if v.Delivery != domain.DeliveryLink || !v.DueAt.Equal(domain.EndOfDay(date("2026-12-25"), time.UTC)) {
		t.Errorf("delivery %s due %v", v.Delivery, v.DueAt)
	}
	// A name the landlord wrote is kept as it is.
	named := k.create(t, CreateInput{Name: ptr("Hóa đơn đặc biệt")})
	if named.Name != "Hóa đơn đặc biệt" {
		t.Errorf("name: %q", named.Name)
	}
}

func TestTheSameWordsOnTwoBillsStillMakeDifferentCodes(t *testing.T) {
	k := newKit()
	other := k.store.contract(k.landlord, &k.tenant, "P102", 0)
	one := k.create(t, CreateInput{PaymentLabel: ptr("P101 T11")})
	two, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: other.ID, PaymentLabel: ptr("P101 T11")})
	if err != nil {
		t.Fatal(err)
	}
	if one.PaymentCode == two.PaymentCode {
		t.Errorf("transfer texts are unique: %q", one.PaymentCode)
	}
}

func TestANumberOrCodeThatClashesIsDrawnAgain(t *testing.T) {
	// A bill made at the same moment took the number or the transfer text between the check and the insert: the database
	// refuses it, and the bill is made again with new random parts.
	k := newKit()
	k.store.clashes = 2
	v := k.create(t, CreateInput{})
	if k.store.clashes != 0 || v.ID == uuid.Nil {
		t.Fatalf("the bill was made after the clashes: %d", k.store.clashes)
	}
	if n, _ := k.store.CountOfContract(ctx, k.contract.ID); n != 1 {
		t.Errorf("one bill: %d", n)
	}

	// A source that only repeats cannot find a free number: it gives up with an error and no bill.
	twin := k.store.contract(k.landlord, &k.tenant, "P101", 2)
	k.random.fixed = true
	k.create(t, CreateInput{PeriodStart: ptr(date("2026-10-23"))})
	if _, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: twin.ID, PeriodStart: ptr(date("2026-10-23"))}); err == nil {
		t.Fatal("the same number and the same tail again")
	}
	if n, _ := k.store.CountOfContract(ctx, twin.ID); n != 0 {
		t.Errorf("no bill: %d", n)
	}
	// A source that moves on finds a free one.
	k.random.fixed = false
	if _, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: twin.ID, PeriodStart: ptr(date("2026-10-23"))}); err != nil {
		t.Errorf("fresh draws: %v", err)
	}
}

func TestABillForAContractNobodyHoldsIsPassedOnByLinkWithNobodyTold(t *testing.T) {
	k := newKit()
	blank := k.store.contract(k.landlord, nil, "P102", 0)
	name := "Anh Minh"
	blank.TenantName = &name

	asked := domain.DeliveryApp
	v, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: blank.ID, Delivery: &asked})
	if err != nil {
		t.Fatal(err)
	}
	if v.Delivery != domain.DeliveryLink || v.Contract.HasAccount() || v.Contract.Recipient() != "Anh Minh" {
		t.Fatalf("asking for the app changes nothing without an account: %+v", v.Delivery)
	}
	// A link is for a bill that was sent.
	if _, err := k.invoices.ShareLink(ctx, k.landlord, v.ID); !errors.Is(err, domain.ErrLinkNeedsSent) {
		t.Errorf("a draft has no link: %v", err)
	}
	sent, err := k.invoices.Send(ctx, k.landlord, v.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(k.rec.mails) != 0 || len(k.rec.notes) != 0 {
		t.Errorf("nobody is told, nothing is mailed: %v %v", k.rec.mails, k.rec.notes)
	}
	if !sent.Share.Live(k.clock.Now()) {
		t.Error("the link is made when the bill is sent, so it is there to copy")
	}
}

// --- previews

func TestPreviewShowsTheBillWithoutSavingAnythingAndEvenForABilledPeriod(t *testing.T) {
	k := newKit()
	electricity := k.electricity()
	p, err := k.invoices.Preview(ctx, k.landlord, k.contract.ID, PreviewInput{BillInput: BillInput{
		Readings: reading(electricity, 1100, ptr(1000.0)), Extras: []domain.Extra{{Description: "Repair", AmountVND: 20_000}},
	}})
	if err != nil || p.TotalVND != 3_000_000+350_000+20_000 || len(p.Items) != 3 || p.Period.Index != 0 {
		t.Fatalf("preview: %+v, %v", p, err)
	}
	if len(k.store.invoices) != 0 || len(k.store.readings) != 0 {
		t.Error("a preview never records anything")
	}

	k.create(t, CreateInput{BillInput: BillInput{Readings: reading(electricity, 1100, ptr(1000.0))}})
	start := date("2026-09-23")
	if _, err := k.invoices.Preview(ctx, k.landlord, k.contract.ID, PreviewInput{PeriodStart: &start}); err != nil {
		t.Errorf("a billed period can be previewed: %v", err)
	}
	if _, err := k.invoices.Preview(ctx, id(), k.contract.ID, PreviewInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
}

func TestPeriodsListTheStartedPeriodsWithTheirBillAndTheNameAndWordsANewBillWouldGet(t *testing.T) {
	k := newKit()
	k.create(t, CreateInput{})
	rows, err := k.invoices.Periods(ctx, k.landlord, k.contract.ID)
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows: %+v, %v", rows, err)
	}
	if rows[0].Billed == nil || rows[1].Billed != nil || rows[2].Billed != nil {
		t.Errorf("only the first has a bill: %+v", rows)
	}
	if rows[1].DefaultName != "10/2026 - P101" || rows[1].DefaultPaymentLabel != "HD T10-P101" {
		t.Errorf("suggestions: %q %q", rows[1].DefaultName, rows[1].DefaultPaymentLabel)
	}
	// The number in the month counts the bills the landlord already has that month (this is the second).
	if rows[0].DefaultPaymentLabel != "HD T09-P101" {
		t.Errorf("the first bill of the month: %q", rows[0].DefaultPaymentLabel)
	}
	if _, err := k.invoices.Periods(ctx, id(), k.contract.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
	empty := k.store.contract(k.landlord, &k.tenant, "P102", 0)
	empty.StartsOn = date("2027-01-01")
	if rows, err := k.invoices.Periods(ctx, k.landlord, empty.ID); err != nil || rows == nil || len(rows) != 0 {
		t.Errorf("a contract with no period yet: %v, %v", rows, err)
	}
}

func TestTheNumberInTheMonthCountsTheLandlordsBills(t *testing.T) {
	k := newKit()
	other := k.store.contract(k.landlord, &k.tenant, "P102", 0)
	one := k.create(t, CreateInput{PeriodStart: ptr(date("2026-11-23"))})
	two, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: other.ID})
	if err != nil {
		t.Fatal(err)
	}
	// "A name with nothing usable falls back to the number": here the rooms have names, so both labels use them.
	if one.PaymentLabel != "HD T11-P101" || two.PaymentLabel != "HD T11-P102" {
		t.Errorf("labels %q %q", one.PaymentLabel, two.PaymentLabel)
	}
	odd := k.store.contract(k.landlord, &k.tenant, "★", 0)
	three, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: odd.ID})
	if err != nil || three.PaymentLabel != "HD T11-003" {
		t.Errorf("the third bill of the month is numbered 003: %q %v", three.PaymentLabel, err)
	}
}

// --- listing and reading

func TestListPagesTheBillsNewestFirstWithHasMore(t *testing.T) {
	k := newKit()
	k.contract.StartsOn = domain.AddMonths(date("2026-11-23"), -6)
	for range 7 {
		k.create(t, CreateInput{})
	}
	first, err := k.invoices.List(ctx, k.landlordView(), k.contract.ID, 5, 0)
	if err != nil || len(first.Items) != 5 || !first.HasMore || first.Total != 7 {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	second, err := k.invoices.List(ctx, k.landlordView(), k.contract.ID, 5, 5)
	if err != nil || len(second.Items) != 2 || second.HasMore || second.Total != 7 {
		t.Fatalf("second page: %+v, %v", second, err)
	}
	if !first.Items[0].PeriodStart.After(first.Items[4].PeriodStart) || !first.Items[4].PeriodStart.After(second.Items[0].PeriodStart) {
		t.Error("newest period first, across pages")
	}
	huge, err := k.invoices.List(ctx, k.landlordView(), k.contract.ID, 5000, 0)
	if err != nil || len(huge.Items) != 7 {
		t.Errorf("a page is capped at %d, got %d", MaxPageSize, len(huge.Items))
	}
	if _, err := k.invoices.List(ctx, domain.Viewer{UserID: id(), AsLandlord: true}, k.contract.ID, 5, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord's contract: %v", err)
	}
}

func TestATenantOnlySeesTheBillsSentToTheirAccountAndNeverADraft(t *testing.T) {
	k := newKit()
	draft := k.create(t, CreateInput{})
	sent := k.create(t, CreateInput{})
	if _, err := k.invoices.Send(ctx, k.landlord, sent.ID, nil); err != nil {
		t.Fatal(err)
	}
	link := domain.DeliveryLink
	byLink := k.create(t, CreateInput{Delivery: &link})
	if _, err := k.invoices.Send(ctx, k.landlord, byLink.ID, nil); err != nil {
		t.Fatal(err)
	}

	page, err := k.invoices.List(ctx, k.tenantView(), k.contract.ID, 20, 0)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != sent.ID || page.Total != 1 {
		t.Fatalf("the tenant sees the one sent by the app: %+v, %v", page, err)
	}
	if a := page.Items[0].Abilities; a != (domain.Abilities{}) {
		t.Errorf("a tenant may do nothing: %+v", a)
	}
	if _, err := k.invoices.Get(ctx, k.tenantView(), sent.ID); err != nil {
		t.Errorf("their own bill: %v", err)
	}
	for name, hidden := range map[string]uuid.UUID{"a draft": draft.ID, "a bill passed on by link": byLink.ID} {
		if _, err := k.invoices.Get(ctx, k.tenantView(), hidden); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s is not found: %v", name, err)
		}
	}
	other := domain.Viewer{UserID: id()}
	if _, err := k.invoices.List(ctx, other, k.contract.ID, 20, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another tenant's contract: %v", err)
	}
	if _, err := k.invoices.Get(ctx, other, sent.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another tenant's bill: %v", err)
	}
	if _, err := k.invoices.Get(ctx, domain.Viewer{UserID: id(), AsLandlord: true}, sent.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord's bill: %v", err)
	}
	// A landlord who is also a tenant elsewhere does not see these as a tenant.
	if _, err := k.invoices.Get(ctx, domain.Viewer{UserID: k.landlord}, sent.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a landlord acting as a tenant: %v", err)
	}
}

func TestMeterHistoryOfARoomIsTheLandlordsAndPaged(t *testing.T) {
	k := newKit()
	electricity := k.electricity()
	k.create(t, CreateInput{BillInput: BillInput{Readings: reading(electricity, 1100, ptr(1000.0))}})
	k.create(t, CreateInput{BillInput: BillInput{Readings: reading(electricity, 1300, nil)}})

	page, err := k.invoices.MeterHistory(ctx, k.landlord, k.contract.RoomID, nil, 10, 0)
	if err != nil || page.Total != 2 || len(page.Items) != 2 || page.Items[0].Consumption != 200 || page.Items[1].Consumption != 100 {
		t.Fatalf("newest first: %+v, %v", page, err)
	}
	one, _ := k.invoices.MeterHistory(ctx, k.landlord, k.contract.RoomID, &electricity.ID, 1, 1)
	if one.Total != 2 || len(one.Items) != 1 || one.Items[0].Consumption != 100 {
		t.Errorf("page two: %+v", one)
	}
	if _, err := k.invoices.MeterHistory(ctx, id(), k.contract.RoomID, nil, 10, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord's room: %v", err)
	}
}

// --- correcting and deleting

func TestUpdateRebuildsTheLinesFromTheNewInputAndKeepsThePeriod(t *testing.T) {
	k := newKit()
	v := k.create(t, CreateInput{Note: ptr("first")})
	if _, err := k.invoices.Send(ctx, k.landlord, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	k.rec.notes, k.rec.mails, k.rec.events = nil, nil, nil

	updated, err := k.invoices.Update(ctx, k.landlord, v.ID, UpdateInput{
		BillInput: BillInput{RentUnitPriceVND: ptr(int64(2_500_000)), Extras: []domain.Extra{{Description: "Repair", AmountVND: 200_000}}},
		Note:      Some[*string](ptr("Corrected")), ResendEmail: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.TotalVND != 2_700_000 || len(updated.Items) != 2 || updated.Items[1].Kind != domain.KindOther || *updated.Note != "Corrected" {
		t.Errorf("rebuilt: %+v", updated.Invoice)
	}
	if !updated.PeriodStart.Equal(v.PeriodStart) || updated.Number != v.Number || updated.PaymentCode != v.PaymentCode {
		t.Error("the period, the number and the transfer text stay")
	}
	if len(k.rec.mails) != 1 || !slices.Equal(k.rec.kinds(), []string{NotifyInvoiceUpdated}) || !slices.Equal(k.rec.actions(), []string{AuditUpdated}) {
		t.Errorf("mails %d notes %v audit %v", len(k.rec.mails), k.rec.kinds(), k.rec.actions())
	}

	// null clears the note; leaving it out keeps it.
	cleared, _ := k.invoices.Update(ctx, k.landlord, v.ID, UpdateInput{Note: Some[*string](nil)})
	if cleared.Note != nil {
		t.Errorf("note cleared: %v", cleared.Note)
	}
	kept, _ := k.invoices.Update(ctx, k.landlord, v.ID, UpdateInput{Note: Opt[*string]{}, Name: ptr("New name")})
	if kept.Name != "New name" || kept.Note != nil {
		t.Errorf("name %q note %v", kept.Name, kept.Note)
	}
	// An empty name leaves the name it has.
	if same, _ := k.invoices.Update(ctx, k.landlord, v.ID, UpdateInput{Name: ptr("")}); same.Name != "New name" {
		t.Errorf("an empty name changes nothing: %q", same.Name)
	}
}

func TestUpdateOfADraftIsNotEmailedAndNobodyIsToldBeforeItIsSent(t *testing.T) {
	k := newKit()
	v := k.create(t, CreateInput{})
	if _, err := k.invoices.Update(ctx, k.landlord, v.ID, UpdateInput{ResendEmail: true}); err != nil {
		t.Fatal(err)
	}
	if len(k.rec.mails) != 0 || len(k.rec.notes) != 0 {
		t.Errorf("a draft is emailed when it is sent: %v %v", k.rec.mails, k.rec.notes)
	}
}

func TestUpdateKeepsTheTailOfTheTransferTextWhenOnlyTheWordsChange(t *testing.T) {
	k := newKit()
	v := k.create(t, CreateInput{})
	tail, ok := domain.TailOf(v.PaymentCode)
	if !ok {
		t.Fatal("the code has a tail")
	}
	updated, err := k.invoices.Update(ctx, k.landlord, v.ID, UpdateInput{PaymentLabel: ptr("P102-T11")})
	if err != nil || updated.PaymentLabel != "P102-T11" || updated.PaymentCode != "P102-T11 "+tail {
		t.Fatalf("new words, same tail: %q %v", updated.PaymentCode, err)
	}
	if same, _ := k.invoices.Update(ctx, k.landlord, v.ID, UpdateInput{PaymentLabel: ptr("")}); same.PaymentCode != "P102-T11 "+tail {
		t.Errorf("empty words change nothing: %q", same.PaymentCode)
	}
}

func TestUpdateMovingTheDueDateStartsTheRemindersAgain(t *testing.T) {
	k := newKit()
	v := k.create(t, CreateInput{DueOn: ptr(date("2026-11-26"))})
	if _, err := k.invoices.Send(ctx, k.landlord, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := k.auto.SendReminders(ctx); n != 1 {
		t.Fatalf("a reminder: %d", n)
	}
	if _, err := k.invoices.Update(ctx, k.landlord, v.ID, UpdateInput{DueOn: ptr(date("2026-11-26"))}); err != nil {
		t.Fatal(err)
	}
	if n, _ := k.auto.SendReminders(ctx); n != 0 {
		t.Errorf("the same date keeps the reminder done: %d", n)
	}
	if _, err := k.invoices.Update(ctx, k.landlord, v.ID, UpdateInput{DueOn: ptr(date("2026-11-27"))}); err != nil {
		t.Fatal(err)
	}
	k.clock.t = testNow.Add(24 * time.Hour)
	if n, _ := k.auto.SendReminders(ctx); n != 1 {
		t.Errorf("a new due date is reminded again: %d", n)
	}
}

func TestEditingCountsFromWhatCameBeforeTheBillAndAFailedEditChangesNothing(t *testing.T) {
	k := newKit()
	electricity := k.electricity()
	v := k.create(t, CreateInput{BillInput: BillInput{Readings: reading(electricity, 1100, ptr(1000.0))}})

	// Its own reading is not the baseline: 1200 counts from the 1000 it started from, not from 1100.
	p, err := k.invoices.PreviewUpdate(ctx, k.landlord, v.ID, BillInput{Readings: reading(electricity, 1200, ptr(1000.0))})
	if err != nil || p.TotalVND != 3_000_000+200*3500 {
		t.Fatalf("preview of the correction: %+v, %v", p, err)
	}
	price := int64(4000)
	in := reading(electricity, 1200, ptr(1000.0))
	in[0].UnitPriceVND = &price
	u, err := k.invoices.Update(ctx, k.landlord, v.ID, UpdateInput{BillInput: BillInput{Readings: in}})
	if err != nil || u.TotalVND != 3_800_000 || u.Readings[0].Consumption != 200 || u.Readings[0].UnitPriceVND != 4000 {
		t.Fatalf("corrected: %+v, %v", u, err)
	}

	// Nothing came before this bill, so without the starting index it cannot be counted.
	_, err = k.invoices.Update(ctx, k.landlord, v.ID, UpdateInput{BillInput: BillInput{Readings: reading(electricity, 1300, nil)}})
	_ = violation(t, err, domain.FieldReadings, domain.ReasonPreviousReadingRequired)
	page, _ := k.invoices.MeterHistory(ctx, k.landlord, k.contract.RoomID, nil, 10, 0)
	if page.Total != 1 || page.Items[0].Value != 1200 {
		t.Errorf("the failed edit changed nothing: %+v", page)
	}

	// A later bill is counted from this one; correcting this one does not look at the later one.
	later := k.create(t, CreateInput{BillInput: BillInput{Readings: reading(electricity, 1500, nil)}})
	if later.Readings[0].Previous() != 1200 {
		t.Fatalf("the later bill counts from 1200: %+v", later.Readings)
	}
	again, err := k.invoices.Update(ctx, k.landlord, v.ID, UpdateInput{BillInput: BillInput{Readings: reading(electricity, 1250, ptr(1000.0))}})
	if err != nil || again.Readings[0].Value != 1250 {
		t.Errorf("correcting the older bill: %+v, %v", again.Readings, err)
	}
	if page, _ := k.invoices.MeterHistory(ctx, k.landlord, k.contract.RoomID, nil, 10, 0); page.Total != 2 {
		t.Errorf("the history has one reading per bill: %d", page.Total)
	}
}

func TestABillThatIsPaidOrBeingPaidCannotBeCorrectedOrRemoved(t *testing.T) {
	k := newKit()
	v := k.create(t, CreateInput{})
	if _, err := k.invoices.Send(ctx, k.landlord, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	// Somebody is paying it.
	k.ledger.totals[v.ID] = domain.Totals{PendingVND: 1000}
	if _, err := k.invoices.Update(ctx, k.landlord, v.ID, UpdateInput{}); !errors.Is(err, domain.ErrLocked) {
		t.Errorf("update while a payment waits: %v", err)
	}
	if err := k.invoices.Delete(ctx, k.landlord, v.ID); !errors.Is(err, domain.ErrLocked) {
		t.Errorf("delete while a payment waits: %v", err)
	}
	if _, err := k.invoices.PreviewUpdate(ctx, k.landlord, v.ID, BillInput{}); !errors.Is(err, domain.ErrLocked) {
		t.Errorf("preview of a correction: %v", err)
	}
	k.ledger.totals[v.ID] = domain.Totals{}
	if _, err := k.invoices.SetStatus(ctx, k.landlord, v.ID, domain.StatusPaid); err != nil {
		t.Fatal(err)
	}
	k.ledger.totals[v.ID] = domain.Totals{}
	if _, err := k.invoices.Update(ctx, k.landlord, v.ID, UpdateInput{}); !errors.Is(err, domain.ErrLocked) {
		t.Errorf("update of a paid bill: %v", err)
	}
	if err := k.invoices.Delete(ctx, k.landlord, v.ID); !errors.Is(err, domain.ErrLocked) {
		t.Errorf("delete of a paid bill: %v", err)
	}
	// Other people cannot touch it at all.
	if _, err := k.invoices.Update(ctx, id(), v.ID, UpdateInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
	if err := k.invoices.Delete(ctx, id(), v.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
}

func TestDeleteFreesThePeriodAndTakesTheReadingsItRecordedWithIt(t *testing.T) {
	k := newKit()
	electricity := k.electricity()
	payload := BillInput{Readings: reading(electricity, 1500, ptr(1000.0))}
	v := k.create(t, CreateInput{BillInput: payload})

	if err := k.invoices.Delete(ctx, k.landlord, v.ID); err != nil {
		t.Fatal(err)
	}
	if len(k.store.invoices) != 0 || len(k.store.readings) != 0 {
		t.Errorf("removed for good: %d bills %d readings", len(k.store.invoices), len(k.store.readings))
	}
	if !slices.Equal(k.ledger.purged, []uuid.UUID{v.ID}) {
		t.Errorf("the ledger takes its payments away: %v", k.ledger.purged)
	}
	last := k.rec.events[len(k.rec.events)-1]
	if last.Action != AuditDeleted || last.Meta["number"] != v.Number {
		t.Errorf("audit: %+v", last)
	}
	// Same period, same readings: accepted again, starting from the starting index.
	again := k.create(t, CreateInput{BillInput: payload})
	if again.PeriodStart != v.PeriodStart {
		t.Errorf("the period can be billed again: %v", again.PeriodStart)
	}
}

// --- sending, status

func TestSendMakesTheDraftVisibleEmailsAndNotifiesTheTenantAndSendingAgainOnlyEmails(t *testing.T) {
	k := newKit()
	v := k.create(t, CreateInput{})
	sent, err := k.invoices.Send(ctx, k.landlord, v.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sent.Status != domain.StatusSent || sent.SentAt == nil || sent.SentVia == nil || *sent.SentVia != domain.SourceManual || sent.SentByID == nil || *sent.SentByID != k.landlord {
		t.Fatalf("sent by hand: %+v", sent.Invoice)
	}
	if len(k.rec.mails) != 1 || k.rec.mails[0].To != "tenant@example.vn" || k.rec.mails[0].TotalVND != 3_000_000 || !strings.HasPrefix(k.rec.mails[0].URL, "https://app.example.vn/my-rooms/") {
		t.Errorf("mail: %+v", k.rec.mails)
	}
	if len(k.rec.notes) != 1 {
		t.Fatalf("notes: %v", k.rec.kinds())
	}
	n := k.rec.notes[0]
	if n.Kind != NotifyInvoiceSent || n.UserID != k.tenant || n.Audience != AudienceTenant || n.Params["amount_vnd"] != "3000000" || n.Params["room"] != "P101" ||
		n.Params["invoice_id"] != v.ID.String() || !strings.Contains(n.URL, "?pay="+v.ID.String()) {
		t.Errorf("notification: %+v", n)
	}

	// Sent again: only the email goes out.
	if _, err := k.invoices.Send(ctx, k.landlord, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	if len(k.rec.mails) != 2 || len(k.rec.notes) != 1 {
		t.Errorf("mails %d notes %d", len(k.rec.mails), len(k.rec.notes))
	}
	if got := k.rec.actions(); !slices.Equal(got, []string{AuditCreated, AuditSent, AuditSent}) || k.rec.events[2].Meta["first_time"] != false {
		t.Errorf("audit: %v", got)
	}

	// A bill that is paid cannot be sent.
	if _, err := k.invoices.SetStatus(ctx, k.landlord, v.ID, domain.StatusPaid); err != nil {
		t.Fatal(err)
	}
	if _, err := k.invoices.Send(ctx, k.landlord, v.ID, nil); !errors.Is(err, domain.ErrAlreadyPaid) {
		t.Errorf("a paid bill: %v", err)
	}
	if _, err := k.invoices.Send(ctx, id(), v.ID, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
	if _, err := k.invoices.Send(ctx, k.tenant, v.ID, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a tenant is not the landlord: %v", err)
	}
}

func TestSendByLinkNotifiesNobodyAndSendingByTheAppLaterReachesTheAccount(t *testing.T) {
	k := newKit()
	link := domain.DeliveryLink
	v := k.create(t, CreateInput{Delivery: &link})
	sent, err := k.invoices.Send(ctx, k.landlord, v.ID, nil)
	if err != nil || sent.Delivery != domain.DeliveryLink || !sent.Share.Live(k.clock.Now()) {
		t.Fatalf("by link: %+v, %v", sent.Delivery, err)
	}
	if len(k.rec.mails) != 0 || len(k.rec.notes) != 0 {
		t.Errorf("an account is told only when it goes by the app: %v %v", k.rec.mails, k.rec.notes)
	}
	app := domain.DeliveryApp
	again, err := k.invoices.Send(ctx, k.landlord, v.ID, &app)
	if err != nil || again.Delivery != domain.DeliveryApp || len(k.rec.mails) != 1 {
		t.Errorf("by the app: %v mails %d", err, len(k.rec.mails))
	}
	if len(k.rec.notes) != 0 {
		t.Errorf("it was already sent, so no new notice: %v", k.rec.kinds())
	}
}

func TestAMailOrNoticeThatFailsNeverUndoesTheBill(t *testing.T) {
	k := newKit()
	k.rec.failMsg = true
	v := k.create(t, CreateInput{})
	sent, err := k.invoices.Send(ctx, k.landlord, v.ID, nil)
	if err != nil || sent.Status != domain.StatusSent {
		t.Fatalf("the bill is sent anyway: %v", err)
	}
}

func TestASentBillWithAnAccountWithoutAnEmailIsOnlyNotified(t *testing.T) {
	k := newKit()
	k.contract.TenantEmail = nil
	v := k.create(t, CreateInput{})
	if _, err := k.invoices.Send(ctx, k.landlord, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	if len(k.rec.mails) != 0 || len(k.rec.notes) != 1 {
		t.Errorf("mails %d notes %d", len(k.rec.mails), len(k.rec.notes))
	}
}

func TestSetStatusMovesABetweenSentAndPaidAndRecordsThePaymentTimes(t *testing.T) {
	k := newKit()
	v := k.create(t, CreateInput{})

	// A draft cannot be marked sent by hand, only sent or settled; and no bill goes back to a draft.
	if _, err := k.invoices.SetStatus(ctx, k.landlord, v.ID, domain.StatusSent); !errors.Is(err, domain.ErrSendFirst) {
		t.Errorf("draft to sent: %v", err)
	}
	if _, err := k.invoices.SetStatus(ctx, k.landlord, v.ID, domain.StatusDraft); !errors.Is(err, domain.ErrSendFirst) {
		t.Errorf("to a draft: %v", err)
	}
	if _, err := k.invoices.Send(ctx, k.landlord, v.ID, nil); err != nil {
		t.Fatal(err)
	}

	paid, err := k.invoices.SetStatus(ctx, k.landlord, v.ID, domain.StatusPaid)
	if err != nil || paid.Status != domain.StatusPaid || paid.PaidAt == nil || !paid.PaidAt.Equal(k.clock.Now()) {
		t.Fatalf("paid: %+v, %v", paid.Invoice, err)
	}
	if len(k.ledger.settled) != 1 || k.ledger.settled[0].AmountVND != 3_000_000 || k.ledger.settled[0].InvoiceID != v.ID {
		t.Errorf("the money still owed is written down as collected: %+v", k.ledger.settled)
	}

	reopened, err := k.invoices.SetStatus(ctx, k.landlord, v.ID, domain.StatusSent)
	if err != nil || reopened.Status != domain.StatusSent || reopened.PaidAt != nil {
		t.Fatalf("back to unpaid: %+v, %v", reopened.Invoice, err)
	}
	if !slices.Equal(k.ledger.voided, []uuid.UUID{v.ID}) {
		t.Errorf("what was paid no longer counts: %v", k.ledger.voided)
	}
	// Setting the same status again changes nothing.
	same, _ := k.invoices.SetStatus(ctx, k.landlord, v.ID, domain.StatusSent)
	if same.Status != domain.StatusSent {
		t.Error("sent stays sent")
	}
	if got := k.rec.actions(); !slices.Equal(got, []string{AuditCreated, AuditSent, AuditStatusChanged, AuditStatusChanged}) {
		t.Errorf("audit: %v", got)
	}
	last := k.rec.events[len(k.rec.events)-1]
	if last.Meta["from"] != "paid" || last.Meta["to"] != "sent" {
		t.Errorf("the change is in the trail: %v", last.Meta)
	}
}

func TestSetStatusSettlesOnlyWhatIsStillOwedAndRefusesWhileAPaymentWaits(t *testing.T) {
	k := newKit()
	v := k.create(t, CreateInput{})
	if _, err := k.invoices.Send(ctx, k.landlord, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	k.ledger.totals[v.ID] = domain.Totals{PaidVND: 1_000_000}
	if _, err := k.invoices.SetStatus(ctx, k.landlord, v.ID, domain.StatusPaid); err != nil {
		t.Fatal(err)
	}
	if len(k.ledger.settled) != 1 || k.ledger.settled[0].AmountVND != 2_000_000 {
		t.Errorf("only the rest: %+v", k.ledger.settled)
	}

	w := newKit()
	pending := w.create(t, CreateInput{})
	if _, err := w.invoices.Send(ctx, w.landlord, pending.ID, nil); err != nil {
		t.Fatal(err)
	}
	w.ledger.totals[pending.ID] = domain.Totals{PendingVND: 1000}
	if _, err := w.invoices.SetStatus(ctx, w.landlord, pending.ID, domain.StatusPaid); !errors.Is(err, domain.ErrReviewPendingFirst) {
		t.Errorf("a payment waits for review: %v", err)
	}
	if got, _ := w.invoices.Get(ctx, w.landlordView(), pending.ID); got.Status != domain.StatusSent {
		t.Errorf("nothing changed: %s", got.Status)
	}
}

func TestMarkingABillPaidWorksWithoutAPaymentLedger(t *testing.T) {
	k := newKit()
	k.deps.Ledger = nil // the default: no payment context yet
	k.rebuild()
	v := k.create(t, CreateInput{})
	if _, err := k.invoices.Send(ctx, k.landlord, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	paid, err := k.invoices.SetStatus(ctx, k.landlord, v.ID, domain.StatusPaid)
	if err != nil || paid.Status != domain.StatusPaid || paid.PaidAt == nil || paid.Summary.RemainingVND != 3_000_000 {
		t.Fatalf("status and time are set, the summary has no payments to show: %+v, %v", paid.Invoice, err)
	}
	if back, err := k.invoices.SetStatus(ctx, k.landlord, v.ID, domain.StatusSent); err != nil || back.PaidAt != nil {
		t.Errorf("back to unpaid: %v", err)
	}
}

func TestASettledDraftBecomesSentWithoutTellingAnybody(t *testing.T) {
	k := newKit()
	v := k.create(t, CreateInput{})
	paid, err := k.invoices.SetStatus(ctx, k.landlord, v.ID, domain.StatusPaid)
	if err != nil || paid.Status != domain.StatusPaid || paid.SentAt == nil {
		t.Fatalf("settled directly: %+v, %v", paid.Invoice, err)
	}
	if len(k.ledger.settled) != 1 || len(k.rec.mails) != 0 || len(k.rec.notes) != 0 {
		t.Errorf("settled %d mails %d notes %d", len(k.ledger.settled), len(k.rec.mails), len(k.rec.notes))
	}
	if _, err := k.invoices.SetStatus(ctx, id(), v.ID, domain.StatusPaid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
}

// --- share links and the public bill

func TestTheShareLinkIsTheSameEachTimeUntilItIsReplacedWithdrawnOrRunsOut(t *testing.T) {
	k := newKit()
	v := k.create(t, CreateInput{})
	if _, err := k.invoices.ShareLink(ctx, k.landlord, v.ID); !errors.Is(err, domain.ErrLinkNeedsSent) {
		t.Fatalf("a draft has no link: %v", err)
	}
	if _, err := k.invoices.Send(ctx, k.landlord, v.ID, nil); err != nil {
		t.Fatal(err)
	}

	one, err := k.invoices.ShareLink(ctx, k.landlord, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	two, _ := k.invoices.ShareLink(ctx, k.landlord, v.ID)
	if one.URL != two.URL || !strings.HasPrefix(one.URL, "https://app.example.vn/bill?token=") || !one.ExpiresAt.Equal(k.clock.Now().Add(90*24*time.Hour)) {
		t.Fatalf("the same link: %q %q", one.URL, two.URL)
	}
	stored := k.store.invoices[v.ID].Share
	if strings.Contains(stored.TokenHash, tokenOf(one.URL)) || stored.Sealed == "" {
		t.Errorf("only the hash and the sealed copy are stored: %+v", stored)
	}
	created := 0
	for _, a := range k.rec.actions() {
		if a == AuditLinkCreated {
			created++
		}
	}
	if created != 1 {
		t.Errorf("the link was made once: %d", created)
	}

	// A new link kills the one passed on before.
	fresh, err := k.invoices.RegenerateShareLink(ctx, k.landlord, v.ID)
	if err != nil || fresh.URL == one.URL {
		t.Fatalf("regenerate: %v", err)
	}
	if _, err := k.public.View(ctx, tokenOf(one.URL)); !errors.Is(err, domain.ErrLinkInvalid) {
		t.Errorf("the old link: %v", err)
	}
	if _, err := k.public.View(ctx, tokenOf(fresh.URL)); err != nil {
		t.Errorf("the new link: %v", err)
	}

	// A copy that cannot be opened (the key changed) is replaced, not shown wrong.
	k.sealer.broken = true
	replaced, err := k.invoices.ShareLink(ctx, k.landlord, v.ID)
	if err != nil || replaced.URL == fresh.URL {
		t.Errorf("replaced: %v", err)
	}
	k.sealer.broken = false

	if err := k.invoices.RevokeShareLink(ctx, k.landlord, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := k.public.View(ctx, tokenOf(replaced.URL)); !errors.Is(err, domain.ErrLinkInvalid) {
		t.Errorf("a withdrawn link: %v", err)
	}
	if err := k.invoices.RevokeShareLink(ctx, k.landlord, v.ID); err != nil {
		t.Errorf("withdrawing twice is fine: %v", err)
	}

	live, _ := k.invoices.ShareLink(ctx, k.landlord, v.ID)
	k.clock.t = k.clock.t.Add(91 * 24 * time.Hour)
	if _, err := k.public.View(ctx, tokenOf(live.URL)); !errors.Is(err, domain.ErrLinkInvalid) {
		t.Errorf("an expired link: %v", err)
	}
	// A link that ran out is made again when asked for.
	again, err := k.invoices.ShareLink(ctx, k.landlord, v.ID)
	if err != nil || again.URL == live.URL {
		t.Errorf("a new one when it ran out: %v", err)
	}

	for name, op := range map[string]func(uuid.UUID) error{
		"share":      func(who uuid.UUID) error { _, err := k.invoices.ShareLink(ctx, who, v.ID); return err },
		"regenerate": func(who uuid.UUID) error { _, err := k.invoices.RegenerateShareLink(ctx, who, v.ID); return err },
		"revoke":     func(who uuid.UUID) error { return k.invoices.RevokeShareLink(ctx, who, v.ID) },
	} {
		if err := op(id()); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("another landlord cannot %s: %v", name, err)
		}
	}
}

func tokenOf(url string) string { return url[strings.Index(url, "token=")+len("token="):] }

func TestAWrongExpiredWithdrawnOrDraftLinkAllGiveTheSameAnswer(t *testing.T) {
	k := newKit()
	v := k.create(t, CreateInput{})
	if _, err := k.invoices.Send(ctx, k.landlord, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	link, _ := k.invoices.ShareLink(ctx, k.landlord, v.ID)
	token := tokenOf(link.URL)

	if _, err := k.public.View(ctx, token); err != nil {
		t.Fatalf("the link works: %v", err)
	}
	// The bill goes back to a draft (deleted and made again is another bill; here the state is forced).
	k.store.invoices[v.ID].Status = domain.StatusDraft
	k.store.invoices[v.ID].SentAt, k.store.invoices[v.ID].SentVia = nil, nil

	unknown := strings.Repeat("z", 64)
	for name, bad := range map[string]string{
		"a draft": token, "unknown": unknown, "wrong length": "short", "empty": "", "wrong characters": strings.Repeat("!", 64),
	} {
		_, err := k.public.View(ctx, bad)
		if !errors.Is(err, domain.ErrLinkInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestThePublicBillHasNoIdsAndNoPrivateData(t *testing.T) {
	k := newKit()
	name := "Anh Minh"
	k.contract.TenantName = &name
	k.contract.TenantAccountName = nil
	k.store.charge(k.contract, domain.UnitPerPeriod, "Internet", 100_000)
	v := k.create(t, CreateInput{Note: ptr("Pay soon")})
	if _, err := k.invoices.Send(ctx, k.landlord, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	link, _ := k.invoices.ShareLink(ctx, k.landlord, v.ID)

	view, err := k.public.View(ctx, tokenOf(link.URL))
	if err != nil {
		t.Fatal(err)
	}
	if view.Number != v.Number || view.Room != "P101" || view.Landlord != "Landlord" || view.Recipient != "Anh Minh" || view.TotalVND != 3_100_000 ||
		view.Summary.RemainingVND != 3_100_000 || len(view.Items) != 2 || view.PaymentCode != v.PaymentCode || *view.Note != "Pay soon" {
		t.Errorf("view: %+v", view)
	}
	// Nothing in the view is an id (of the bill, the contract, the room, a person).
	if hasUUID(view) {
		t.Error("the public view must carry no ids")
	}
}

// hasUUID says whether a value holds a uuid anywhere in its fields.
func hasUUID(v any) bool {
	return containsUUID(valueOf(v))
}

// --- automatic sending and reminders

func TestAutomaticSendingOnlyForPlansWithItAndNeverTwiceForAPeriod(t *testing.T) {
	k := newKit()
	k.store.charge(k.contract, domain.UnitPerPeriod, "Internet", 100_000)
	k.store.charge(k.contract, domain.UnitPerKWh, "Electricity", 3500) // meters cannot be known here
	other := id()
	free := k.store.contract(other, &k.tenant, "F1", 0)
	k.plans[k.landlord] = true

	n, err := k.auto.SendAutomatic(ctx)
	if err != nil || n != 1 {
		t.Fatalf("sent %d: %v", n, err)
	}
	if got, _ := k.store.CountOfContract(ctx, free.ID); got != 0 {
		t.Error("a plan without automatic invoicing sends nothing")
	}
	bills := k.store.invoices
	var v *domain.Invoice
	for _, inv := range bills {
		v = inv
	}
	if v.ContractID != k.contract.ID || v.Status != domain.StatusSent || v.SentVia == nil || *v.SentVia != domain.SourceAutomatic || v.SentByID != nil || v.CreatedByID != nil {
		t.Fatalf("an automatic bill: %+v", v)
	}
	// The current period, the rent and the fixed charges only.
	if v.PeriodStart.Format(domain.DateLayout) != "2026-11-23" || v.TotalVND != 3_100_000 || len(v.Items) != 2 {
		t.Errorf("period %v total %d lines %d", v.PeriodStart, v.TotalVND, len(v.Items))
	}
	if len(k.rec.mails) != 1 || !slices.Equal(k.rec.kinds(), []string{NotifyInvoiceSent}) {
		t.Errorf("mails %d notes %v", len(k.rec.mails), k.rec.kinds())
	}
	if got := k.rec.actions(); !slices.Equal(got, []string{AuditCreated, AuditSent}) || k.rec.events[0].ActorID != uuid.Nil {
		t.Errorf("the system did it: %v", got)
	}

	// Running again does not bill the same period twice.
	if n, err := k.auto.SendAutomatic(ctx); err != nil || n != 0 || len(k.store.invoices) != 1 {
		t.Errorf("again: %d %v", n, err)
	}
	// By hand the landlord cannot bill that period again either.
	if _, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: k.contract.ID, PeriodStart: &v.PeriodStart}); !errors.Is(err, domain.ErrPeriodBilled) {
		t.Errorf("by hand: %v", err)
	}
	// The next period is billed when it starts.
	k.clock.t = testNow.Add(40 * 24 * time.Hour)
	if n, _ := k.auto.SendAutomatic(ctx); n != 1 {
		t.Errorf("a new period: %d", n)
	}
}

func TestAutomaticSendingPassesABillOfAContractNobodyHoldsOnByLink(t *testing.T) {
	k := newKit()
	blank := k.store.contract(k.landlord, nil, "P102", 0)
	k.contract.Status = "ended"
	k.plans[k.landlord] = true
	if n, err := k.auto.SendAutomatic(ctx); err != nil || n != 1 {
		t.Fatalf("sent %d: %v", n, err)
	}
	var v *domain.Invoice
	for _, inv := range k.store.invoices {
		v = inv
	}
	if v.ContractID != blank.ID || v.Delivery != domain.DeliveryLink || !v.Share.Live(k.clock.Now()) {
		t.Errorf("by link: %+v", v)
	}
	if len(k.rec.mails) != 0 || len(k.rec.notes) != 0 {
		t.Errorf("nobody to tell: %v %v", k.rec.mails, k.rec.notes)
	}
}

func TestAutomaticSendingScansEveryBatch(t *testing.T) {
	k := newKit()
	k.plans[k.landlord] = true
	for i := range batch + 5 {
		k.store.contract(k.landlord, &k.tenant, "R"+string(rune('A'+i%26))+string(rune('a'+i/26%26)), 0)
	}
	k.store.contract(k.landlord, &k.tenant, "x", 0)
	want := len(k.store.contracts)
	n, err := k.auto.SendAutomatic(ctx)
	if err != nil || n != want {
		t.Errorf("sent %d of %d: %v", n, want, err)
	}
}

func TestRemindersGoOutThreeDaysBeforeAndTheDayAfterTheDueDateAndOnlyOnce(t *testing.T) {
	k := newKit()
	k.contract.StartsOn = domain.AddMonths(date("2026-11-23"), -8)
	mk := func(due string, status domain.Status, delivery *domain.Delivery) *domain.Invoice {
		v := k.create(t, CreateInput{DueOn: nil, Delivery: delivery})
		inv := k.store.invoices[v.ID]
		inv.DueAt = domain.EndOfDay(date(due), time.UTC)
		if status != domain.StatusDraft {
			if _, err := k.invoices.Send(ctx, k.landlord, v.ID, nil); err != nil {
				t.Fatal(err)
			}
		}
		if status == domain.StatusPaid {
			k.store.invoices[v.ID].Status = domain.StatusPaid
			now := k.clock.Now()
			k.store.invoices[v.ID].PaidAt = &now
		}
		return inv
	}
	soon := mk("2026-11-26", domain.StatusSent, nil)
	over := mk("2026-11-22", domain.StatusSent, nil)
	mk("2026-11-25", domain.StatusSent, nil)  // not a day that calls for a reminder
	mk("2026-11-26", domain.StatusDraft, nil) // a draft is nobody's business yet
	mk("2026-11-26", domain.StatusPaid, nil)  // already paid
	link := domain.DeliveryLink
	byLink := mk("2026-11-22", domain.StatusSent, &link)
	k.rec.notes, k.rec.mails, k.rec.events = nil, nil, nil
	_, _ = soon, byLink

	n, err := k.auto.SendReminders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// soon: the tenant (1); overdue: tenant + landlord (2); overdue by link: the landlord only (1).
	if n != 4 {
		t.Fatalf("notices: %d %v", n, k.rec.kinds())
	}
	byKind := map[string]int{}
	for _, note := range k.rec.notes {
		byKind[note.Kind+"/"+note.Audience]++
	}
	want := map[string]int{
		NotifyInvoiceDueSoon + "/tenant": 1, NotifyInvoiceOverdue + "/tenant": 1, NotifyTenantInvoiceOverdue + "/landlord": 2,
	}
	for key, count := range want {
		if byKind[key] != count {
			t.Errorf("%s: want %d got %d (%v)", key, count, byKind[key], byKind)
		}
	}
	for _, note := range k.rec.notes {
		if note.Kind == NotifyTenantInvoiceOverdue {
			if note.UserID != k.landlord || note.Params["tenant"] != "Tenant" || !strings.HasPrefix(note.URL, "/contracts/") {
				t.Errorf("the landlord's notice: %+v", note)
			}
		}
	}
	_ = over

	// Once each: running it again the same day sends nothing.
	if n, err := k.auto.SendReminders(ctx); err != nil || n != 0 {
		t.Errorf("again: %d %v", n, err)
	}
}

func TestARemindedBillWhoseRemainingAmountIsCoveredIsLeftAlone(t *testing.T) {
	k := newKit()
	paying := k.create(t, CreateInput{})
	k.store.invoices[paying.ID].DueAt = domain.EndOfDay(date("2026-11-26"), time.UTC)
	if _, err := k.invoices.Send(ctx, k.landlord, paying.ID, nil); err != nil {
		t.Fatal(err)
	}
	// Even a payment still waiting for review covers it.
	k.ledger.totals[paying.ID] = domain.Totals{PaidVND: 1_000_000, PendingVND: 2_000_000}
	k.rec.notes = nil
	if n, err := k.auto.SendReminders(ctx); err != nil || n != 0 || len(k.rec.notes) != 0 {
		t.Errorf("nothing left to ask: %d %v", n, err)
	}
	// Partly covered still gets the reminder.
	k.store.reminded = map[string]map[uuid.UUID]time.Time{}
	k.ledger.totals[paying.ID] = domain.Totals{PaidVND: 1_000_000}
	if n, _ := k.auto.SendReminders(ctx); n != 1 {
		t.Errorf("partly paid: %d", n)
	}
}

// --- the demo seed

func TestSeedDemoGivesTheDemoContractsBillsOfEveryKindAndIsSafeToRepeat(t *testing.T) {
	k := newKit()
	k.store.contracts[k.contract.ID].RoomName = "P101"
	ended := k.store.contract(k.landlord, &k.tenant, "P103", 6)
	ended.Status = "ended"
	end := domain.AddMonths(date("2026-11-23"), -1)
	ended.EndsOn = &end
	electricity := k.store.charge(ended, domain.UnitPerKWh, "Electricity", 3500)
	k.store.charges[k.contract.RoomID] = []domain.Charge{{ID: id(), Name: "Electricity", Kind: "electricity", Unit: domain.UnitPerKWh, AmountVND: 3500}}
	_ = electricity

	seeder := NewSeeder(k.deps, k.opts)
	made, err := seeder.SeedDemo(ctx, k.landlord)
	if err != nil || made != 4 {
		t.Fatalf("made %d: %v", made, err)
	}
	statuses := map[domain.Status]int{}
	links := 0
	for _, inv := range k.store.invoices {
		statuses[inv.Status]++
		if inv.Delivery == domain.DeliveryLink {
			links++
		}
	}
	if statuses[domain.StatusPaid] != 1 || statuses[domain.StatusSent] != 2 || statuses[domain.StatusDraft] != 1 || links != 1 {
		t.Errorf("a paid, a sent, a sent by link and a draft: %v links %d", statuses, links)
	}
	if again, err := seeder.SeedDemo(ctx, k.landlord); err != nil || again != 0 {
		t.Errorf("safe to repeat: %d %v", again, err)
	}
	// Missing demo contracts are no error.
	if none, err := NewSeeder(newWorld().deps, k.opts).SeedDemo(ctx, k.landlord); err != nil || none != 0 {
		t.Errorf("nothing to seed on: %d %v", none, err)
	}
}
