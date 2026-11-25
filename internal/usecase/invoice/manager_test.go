package invoice

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invoice"
)

// mgrKit is the kit plus a manager who acts as one and holds (or not) the room of the contract.
type mgrKit struct {
	*kit
	manager uuid.UUID
}

func newMgrKit(t *testing.T, g *ManagerGrant) *mgrKit {
	t.Helper()
	k := &mgrKit{kit: newKit(), manager: id()}
	if g != nil {
		k.store.grants[k.contract.RoomID] = map[uuid.UUID]ManagerGrant{k.manager: *g}
	}
	k.acting.on = true
	return k
}

func (k *mgrKit) create(t *testing.T) (View, error) {
	t.Helper()
	return k.invoices.Create(ctx, k.manager, CreateInput{BillInput: BillInput{}, ContractID: k.contract.ID})
}

func TestManagerOfTheRoomWritesBillsAndIsRecordedAsAuthor(t *testing.T) {
	k := newMgrKit(t, &ManagerGrant{Active: true})
	v, err := k.create(t)
	if err != nil {
		t.Fatal(err)
	}
	if v.CreatedByID == nil || *v.CreatedByID != k.manager {
		t.Fatalf("author: %v", v.CreatedByID)
	}
	if !v.Abilities.Update || !v.Abilities.Delete || v.Abilities.SetStatus || !v.Abilities.Send {
		t.Fatalf("abilities: %+v", v.Abilities)
	}
	// The audit trail names the landlord as the owner, so the entry says the manager acted for them.
	if len(k.rec.events) == 0 || k.rec.events[len(k.rec.events)-1].OwnerID != k.landlord {
		t.Fatalf("owner of the audit entry: %+v", k.rec.events)
	}
}

func TestManagerWithoutTheRoomSeesNothing(t *testing.T) {
	k := newMgrKit(t, nil)
	if _, err := k.create(t); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("create: %v", err)
	}
	if _, err := k.invoices.Periods(ctx, k.manager, k.contract.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("periods: %v", err)
	}
	if _, err := k.invoices.Preview(ctx, k.manager, k.contract.ID, PreviewInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("preview: %v", err)
	}
	if _, err := k.invoices.MeterHistory(ctx, k.manager, k.contract.RoomID, nil, 10, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("meter history: %v", err)
	}
}

func TestHandOverCountsOnlyWhenActingAsManager(t *testing.T) {
	k := newMgrKit(t, &ManagerGrant{Active: true})
	k.acting.on = false // the account is in landlord (or tenant) mode: the hand-over means nothing
	if _, err := k.create(t); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("create outside manager mode: %v", err)
	}
}

func TestManagerNeverTouchesTheBillsOfTheirOwnTenancy(t *testing.T) {
	k := newMgrKit(t, &ManagerGrant{Active: true, Settle: true})
	own := k.store.contract(k.landlord, &k.manager, "P102", 1)
	k.store.grants[own.RoomID] = map[uuid.UUID]ManagerGrant{k.manager: {Active: true, Settle: true}}
	if _, err := k.invoices.Create(ctx, k.manager, CreateInput{ContractID: own.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("create on own tenancy: %v", err)
	}
	if _, err := k.invoices.Periods(ctx, k.manager, own.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("periods of own tenancy: %v", err)
	}
	// A bill the landlord wrote on that contract is not visible either.
	k.acting.on = false
	bill, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: own.ID})
	if err != nil {
		t.Fatal(err)
	}
	k.acting.on = true
	for name, f := range map[string]func() error{
		"get": func() error {
			_, err := k.invoices.Get(ctx, domain.Viewer{UserID: k.manager, AsManager: true}, bill.ID)
			return err
		},
		"send":   func() error { _, err := k.invoices.Send(ctx, k.manager, bill.ID, nil); return err },
		"update": func() error { _, err := k.invoices.Update(ctx, k.manager, bill.ID, UpdateInput{}); return err },
		"status": func() error { _, err := k.invoices.SetStatus(ctx, k.manager, bill.ID, domain.StatusPaid); return err },
		"delete": func() error { return k.invoices.Delete(ctx, k.manager, bill.ID) },
	} {
		if err := f(); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s on own tenancy: %v", name, err)
		}
	}
}

func TestManagerDeletesOnlyTheirOwnDraft(t *testing.T) {
	k := newMgrKit(t, &ManagerGrant{Active: true})
	k.acting.on = false
	theirs, err := k.invoices.Create(ctx, k.landlord, CreateInput{ContractID: k.contract.ID})
	if err != nil {
		t.Fatal(err)
	}
	k.acting.on = true
	if err := k.invoices.Delete(ctx, k.manager, theirs.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("landlord's draft: %v", err)
	}
	mine, err := k.create(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.invoices.Send(ctx, k.manager, mine.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := k.invoices.Delete(ctx, k.manager, mine.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("own bill once sent: %v", err)
	}
}

func TestManagerOwnDraftCanBeDeleted(t *testing.T) {
	k := newMgrKit(t, &ManagerGrant{Active: true})
	mine, err := k.create(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.invoices.Delete(ctx, k.manager, mine.ID); err != nil {
		t.Fatalf("own draft: %v", err)
	}
}

func TestSettlingNeedsPaymentsReview(t *testing.T) {
	k := newMgrKit(t, &ManagerGrant{Active: true})
	if _, err := k.invoices.Create(ctx, k.manager, CreateInput{ContractID: k.contract.ID, AlreadyPaid: true}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("already paid without the right: %v", err)
	}
	bill, err := k.create(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.invoices.Send(ctx, k.manager, bill.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := k.invoices.SetStatus(ctx, k.manager, bill.ID, domain.StatusPaid); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("mark paid without the right: %v", err)
	}
	k.store.grants[k.contract.RoomID][k.manager] = ManagerGrant{Active: true, Settle: true}
	v, err := k.invoices.SetStatus(ctx, k.manager, bill.ID, domain.StatusPaid)
	if err != nil || v.Status != domain.StatusPaid || !v.Abilities.SetStatus {
		t.Fatalf("mark paid with the right: %v %+v", err, v)
	}
}

func TestLandlordIsToldWhenTheManagerSendsOrEditsOnlyIfAsked(t *testing.T) {
	k := newMgrKit(t, &ManagerGrant{Active: true, Notify: true, Name: "Minh"})
	bill, err := k.create(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.invoices.Send(ctx, k.manager, bill.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := k.invoices.Update(ctx, k.manager, bill.ID, UpdateInput{}); err != nil {
		t.Fatal(err)
	}
	var sent, edited int
	for _, n := range k.rec.notes {
		if n.UserID == k.landlord && n.Kind == NotifyManagerBillSent && n.Params["manager"] == "Minh" {
			sent++
		}
		if n.UserID == k.landlord && n.Kind == NotifyManagerBillEdited {
			edited++
		}
	}
	if sent != 1 || edited != 1 {
		t.Fatalf("sent %d edited %d: %+v", sent, edited, k.rec.notes)
	}

	quiet := newMgrKit(t, &ManagerGrant{Active: true})
	b, _ := quiet.create(t)
	if _, err := quiet.invoices.Send(ctx, quiet.manager, b.ID, nil); err != nil {
		t.Fatal(err)
	}
	for _, n := range quiet.rec.notes {
		if n.UserID == quiet.landlord {
			t.Fatalf("the landlord asked not to be told: %+v", n)
		}
	}
}

func TestLandlordWorkingAloneIsNeverReported(t *testing.T) {
	k := newKit()
	v := k.create(t, CreateInput{})
	if _, err := k.invoices.Send(ctx, k.landlord, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	for _, n := range k.rec.notes {
		if n.Kind == NotifyManagerBillSent {
			t.Fatal("the landlord is not told about their own work")
		}
	}
}
