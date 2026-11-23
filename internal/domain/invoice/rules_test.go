package invoice

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestResolveDeliveryReachesAnAccountInTheAppUnlessTheLandlordChoseTheLink(t *testing.T) {
	app, link := DeliveryApp, DeliveryLink
	for name, tc := range map[string]struct {
		hasAccount bool
		asked      *Delivery
		want       Delivery
	}{
		"an account, nothing asked": {true, nil, DeliveryApp},
		"an account, the app":       {true, &app, DeliveryApp},
		"an account, the link":      {true, &link, DeliveryLink},
		"nobody holds it, nothing":  {false, nil, DeliveryLink},
		"nobody holds it, the app":  {false, &app, DeliveryLink},
		"nobody holds it, the link": {false, &link, DeliveryLink},
	} {
		if got := ResolveDelivery(tc.hasAccount, tc.asked); got != tc.want {
			t.Errorf("%s: got %s, want %s", name, got, tc.want)
		}
	}
	if !DeliveryApp.ReachesAccount() || DeliveryLink.ReachesAccount() {
		t.Error("only the app reaches an account")
	}
	if d, ok := ParseDelivery("link"); !ok || d != DeliveryLink {
		t.Error("parse link")
	}
	if _, ok := ParseDelivery("post"); ok {
		t.Error("an unknown delivery")
	}
}

func TestPlanStatusChange(t *testing.T) {
	for name, tc := range map[string]struct {
		from, to Status
		settle   bool
		err      error
	}{
		"sent to paid":                   {StatusSent, StatusPaid, false, nil},
		"paid back to sent":              {StatusPaid, StatusSent, false, nil},
		"paid stays paid":                {StatusPaid, StatusPaid, false, nil},
		"a draft whose money is in hand": {StatusDraft, StatusPaid, true, nil},
		"a draft cannot be marked sent":  {StatusDraft, StatusSent, false, ErrSendFirst},
		"a bill cannot go back to draft": {StatusSent, StatusDraft, false, ErrSendFirst},
		"a draft stays a draft":          {StatusDraft, StatusDraft, false, ErrSendFirst},
	} {
		settle, err := PlanStatusChange(tc.from, tc.to)
		if !errors.Is(err, tc.err) || settle != tc.settle {
			t.Errorf("%s: got %v, %v", name, settle, err)
		}
	}
}

func TestLockedAndAbilities(t *testing.T) {
	landlord, tenant := Viewer{AsLandlord: true}, Viewer{}
	for name, tc := range map[string]struct {
		status Status
		totals Totals
		locked bool
	}{
		"a draft":                {StatusDraft, Totals{}, false},
		"a sent bill":            {StatusSent, Totals{}, false},
		"a paid bill":            {StatusPaid, Totals{}, true},
		"a sent bill part paid":  {StatusSent, Totals{PaidVND: 1}, true},
		"a sent bill being paid": {StatusSent, Totals{PendingVND: 1}, true},
	} {
		if got := Locked(tc.status, tc.totals); got != tc.locked {
			t.Errorf("%s: locked = %v", name, got)
		}
		a := AbilitiesFor(landlord, tc.status, tc.totals)
		if a.Update == tc.locked || a.Delete == tc.locked || !a.SetStatus || a.Send != CanSend(tc.status) {
			t.Errorf("%s: landlord abilities %+v", name, a)
		}
		if AbilitiesFor(tenant, tc.status, tc.totals) != (Abilities{}) {
			t.Errorf("%s: a tenant may do nothing", name)
		}
	}
	if CanSend(StatusPaid) || !CanSend(StatusSent) || !CanSend(StatusDraft) {
		t.Error("only a paid bill cannot be sent")
	}
}

func TestAMonthlyTenantOnlySeesSentBillsForTheApp(t *testing.T) {
	for name, tc := range map[string]struct {
		status   Status
		delivery Delivery
		want     bool
	}{
		"draft":        {StatusDraft, DeliveryApp, false},
		"sent by app":  {StatusSent, DeliveryApp, true},
		"paid by app":  {StatusPaid, DeliveryApp, true},
		"sent by link": {StatusSent, DeliveryLink, false},
	} {
		if got := VisibleToTenant(tc.status, tc.delivery); got != tc.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestValidShareToken(t *testing.T) {
	good := strings.Repeat("aB3-_", 12) + "abcd"
	for token, want := range map[string]bool{
		good: true, good[:63]: false, good + "a": false, strings.Repeat("a", 63) + "!": false, "": false,
		strings.Repeat("a", 63) + " ": false, "not-a-real-token": false,
	} {
		if got := ValidShareToken(token); got != want {
			t.Errorf("ValidShareToken(%q) = %v", token, got)
		}
	}
}

func TestShareIsLiveOnlyWhileTheLinkCanStillBeOpenedAndCopied(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	future, past := now.Add(time.Hour), now.Add(-time.Hour)
	for name, tc := range map[string]struct {
		share Share
		want  bool
	}{
		"working":            {Share{TokenHash: "h", Sealed: "s", ExpiresAt: &future}, true},
		"expired":            {Share{TokenHash: "h", Sealed: "s", ExpiresAt: &past}, false},
		"none":               {Share{}, false},
		"made before sealed": {Share{TokenHash: "h", ExpiresAt: &future}, false},
	} {
		if got := tc.share.Live(now); got != tc.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestReminderFor(t *testing.T) {
	today := day("2026-11-23")
	for due, want := range map[string]Reminder{
		"2026-11-26": ReminderDueSoon, "2026-11-22": ReminderOverdue, "2026-11-23": ReminderNone,
		"2026-11-27": ReminderNone, "2026-11-21": ReminderNone, "2026-11-25": ReminderNone,
	} {
		if got := ReminderFor(day(due), today, 3); got != want {
			t.Errorf("due %s: got %d, want %d", due, got, want)
		}
	}
}

func TestSummaryOfWhatIsStillToBePaid(t *testing.T) {
	for name, tc := range map[string]struct {
		total  int64
		totals Totals
		left   int64
	}{
		"nothing paid":        {3_000_000, Totals{}, 3_000_000},
		"part paid":           {3_000_000, Totals{PaidVND: 1_000_000}, 2_000_000},
		"part waiting":        {3_000_000, Totals{PaidVND: 1_000_000, PendingVND: 500_000}, 1_500_000},
		"more than the total": {1000, Totals{PaidVND: 2000}, 0},
	} {
		if got := SummaryOf(tc.total, tc.totals); got.RemainingVND != tc.left || got.PaidVND != tc.totals.PaidVND {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestContractFactsAndInvoiceLabels(t *testing.T) {
	account, name := "Account Name", "Anh Minh"
	id := uuid.New()
	for label, tc := range map[string]struct {
		facts ContractFacts
		who   string
		has   bool
	}{
		"an account":      {ContractFacts{TenantID: &id, TenantAccountName: &account, TenantName: &name}, "Account Name", true},
		"written by hand": {ContractFacts{TenantName: &name}, "Anh Minh", false},
		"nobody named":    {ContractFacts{}, "", false},
	} {
		if tc.facts.Recipient() != tc.who || tc.facts.HasAccount() != tc.has {
			t.Errorf("%s: %q %v", label, tc.facts.Recipient(), tc.facts.HasAccount())
		}
	}
	if (ContractFacts{Status: "active"}).Running() != true || (ContractFacts{Status: "ended"}).Running() {
		t.Error("only an active contract runs")
	}
	if (Invoice{Number: "INV-1"}).Label() != "INV-1" || (Invoice{Number: "INV-1", Name: "04/2026 - P101"}).Label() != "04/2026 - P101" {
		t.Error("label")
	}
}

func TestIsRefusal(t *testing.T) {
	if !IsRefusal(ErrNotFound) || !IsRefusal(violation(FieldReadings, ReasonUnknownService, nil)) || IsRefusal(errors.New("disk")) {
		t.Error("refusals are the domain's reasons only")
	}
}
