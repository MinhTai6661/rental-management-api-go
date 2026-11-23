package contract

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
)

// world is one test's stack: the in-memory store behind every port, a landlord with a room, a tenant, and the use
// cases on top.
type world struct {
	t         *testing.T
	store     *fakeStore
	notifier  *recordingNotifier
	audit     *recordingAudit
	rollbacks int
	clock     time.Time
	landlord  uuid.UUID
	tenant    uuid.UUID
	other     uuid.UUID
	room      *domain.RoomFacts
	contracts *Contracts
	rentals   *Rentals
	links     *RoomLinks
	deps      Deps
}

var testNow = time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC) // 10:00 in Vietnam

func newWorld(t *testing.T, plans PlanLimits) *world {
	t.Helper()
	w := &world{t: t, store: newStore(testNow), notifier: &recordingNotifier{}, audit: &recordingAudit{}, clock: testNow}
	w.landlord, w.tenant, w.other = w.store.user("Landlord"), w.store.user("Tenant"), w.store.user("Other")
	w.room = w.store.room(w.landlord, "P101", domain.RoomAvailable)
	w.deps = Deps{
		Tx: fakeTx{rollbacks: &w.rollbacks}, Contracts: w.store, Rooms: w.store, RoomState: w.store, Notifier: w.notifier,
		Audit: w.audit, Plans: plans, Clock: ClockFunc(func() time.Time { return w.clock }),
	}
	w.contracts, w.rentals, w.links = NewContracts(w.deps, Options{}), NewRentals(w.deps, Options{}), NewRoomLinks(w.deps, Options{})
	return w
}

func (w *world) today() time.Time { return domain.Date(2026, 11, 23) }

func (w *world) register(tenant uuid.UUID, room *domain.RoomFacts) domain.Detail {
	w.t.Helper()
	d, err := w.contracts.Register(context.Background(), tenant, RegisterInput{RoomID: room.ID, StartsOn: w.today()})
	if err != nil {
		w.t.Fatalf("register: %v", err)
	}
	return d
}

func (w *world) landlordView() domain.Viewer {
	return domain.Viewer{UserID: w.landlord, AsLandlord: true}
}

func (w *world) approve(id uuid.UUID) domain.Detail {
	w.t.Helper()
	active := domain.StatusActive
	d, err := w.contracts.Update(context.Background(), w.landlord, id, UpdateInput{Status: &active})
	if err != nil {
		w.t.Fatalf("approve: %v", err)
	}
	return d
}

func is(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestRegisterCreatesPendingContractAndTellsTheLandlord(t *testing.T) {
	w := newWorld(t, nil)
	d := w.register(w.tenant, w.room)
	if d.Status != domain.StatusPending || d.TenantID == nil || *d.TenantID != w.tenant || d.PriceVND != 3_000_000 || d.OccupantCount != 1 || d.ConfirmedAt == nil {
		t.Errorf("registration = %+v", d)
	}
	if w.room.Status != domain.RoomAvailable {
		t.Error("the room stays on the market")
	}
	if got := w.notifier.kinds(); !slices.Equal(got, []string{"registration_confirmed:landlord"}) {
		t.Errorf("notifications = %v", got)
	}
	if n := w.notifier.sent[0]; n.UserID != w.landlord || n.URL != "/contracts/"+d.ID.String() || n.Params["room"] != "P101" || n.Params["tenant"] != "Tenant" {
		t.Errorf("notification = %+v", n)
	}
	if got := w.audit.actions(); !slices.Equal(got, []string{AuditRegistered}) {
		t.Errorf("audit = %v", got)
	}
	// The landlord sees it at once, in the tenant's list too.
	if _, total, _ := w.contracts.List(context.Background(), w.landlordView(), ListFilter{}, 20, 0); total != 1 {
		t.Errorf("landlord sees %d", total)
	}
}

func TestRegisterRefusals(t *testing.T) {
	ctx := context.Background()
	in := func(w *world) RegisterInput { return RegisterInput{RoomID: w.room.ID, StartsOn: w.today()} }

	t.Run("own room", func(t *testing.T) {
		w := newWorld(t, nil)
		_, err := w.contracts.Register(ctx, w.landlord, in(w))
		is(t, err, domain.ErrOwnRoom)
	})
	t.Run("unknown room", func(t *testing.T) {
		w := newWorld(t, nil)
		_, err := w.contracts.Register(ctx, w.tenant, RegisterInput{RoomID: uuid.New(), StartsOn: w.today()})
		is(t, err, domain.ErrRoomNotFound)
	})
	t.Run("deleted room", func(t *testing.T) {
		w := newWorld(t, nil)
		w.room.Live = false
		_, err := w.contracts.Register(ctx, w.tenant, in(w))
		is(t, err, domain.ErrRoomNotFound)
	})
	t.Run("rented or under repair", func(t *testing.T) {
		for _, status := range []string{domain.RoomRented, "maintenance"} {
			w := newWorld(t, nil)
			w.room.Status = status
			_, err := w.contracts.Register(ctx, w.tenant, in(w))
			is(t, err, domain.ErrRoomNotVacant)
		}
	})
	t.Run("no approved listing", func(t *testing.T) {
		w := newWorld(t, nil)
		w.room.Listed = false
		_, err := w.contracts.Register(ctx, w.tenant, in(w))
		is(t, err, domain.ErrRoomNotListed)
		if len(w.store.contracts) != 0 {
			t.Error("a refused registration stores nothing")
		}
		w.room.Listed = true
		w.register(w.tenant, w.room)
	})
	t.Run("a tenant holds it", func(t *testing.T) {
		w := newWorld(t, nil)
		w.store.put(w.room, &w.other, domain.StatusActive)
		_, err := w.contracts.Register(ctx, w.tenant, in(w))
		is(t, err, domain.ErrRoomNotVacant)
	})
	t.Run("twice", func(t *testing.T) {
		w := newWorld(t, nil)
		w.register(w.tenant, w.room)
		_, err := w.contracts.Register(ctx, w.tenant, in(w))
		is(t, err, domain.ErrAlreadyRegistered)
		// Another tenant may still register for the same room.
		w.register(w.other, w.room)
	})
	t.Run("a refused registration can be repeated after a rejection", func(t *testing.T) {
		w := newWorld(t, nil)
		d := w.register(w.tenant, w.room)
		w.store.contracts[d.ID].Status = domain.StatusRejected
		w.register(w.tenant, w.room)
	})
	t.Run("starts in the past", func(t *testing.T) {
		w := newWorld(t, nil)
		_, err := w.contracts.Register(ctx, w.tenant, RegisterInput{RoomID: w.room.ID, StartsOn: w.today().AddDate(0, 0, -1)})
		is(t, err, domain.ErrStartInPast)
	})
	t.Run("today is the date in Vietnam", func(t *testing.T) {
		w := newWorld(t, nil)
		w.clock = time.Date(2026, 11, 22, 20, 0, 0, 0, time.UTC) // already the 23rd there
		if _, err := w.contracts.Register(ctx, w.tenant, in(w)); err != nil {
			t.Errorf("registering for the Vietnamese today must work, got %v", err)
		}
	})
	t.Run("ends before it starts", func(t *testing.T) {
		w := newWorld(t, nil)
		end := w.today()
		_, err := w.contracts.Register(ctx, w.tenant, RegisterInput{RoomID: w.room.ID, StartsOn: w.today(), EndsOn: &end})
		is(t, err, domain.ErrEndNotAfterStart)
	})
	t.Run("occupants", func(t *testing.T) {
		w := newWorld(t, nil)
		for _, n := range []int{-1, 51} {
			_, err := w.contracts.Register(ctx, w.tenant, RegisterInput{RoomID: w.room.ID, StartsOn: w.today(), OccupantCount: n})
			is(t, err, domain.ErrOccupantsInvalid)
		}
	})
	t.Run("below the minimum rental period", func(t *testing.T) {
		w := newWorld(t, nil)
		w.room.MinPeriod, w.room.PeriodUnit = 3, domain.UnitMonth
		end := domain.Date(2027, 2, 22) // 3 months from 23 Nov end on 23 Feb
		_, err := w.contracts.Register(ctx, w.tenant, RegisterInput{RoomID: w.room.ID, StartsOn: w.today(), EndsOn: &end})
		var below *domain.BelowMinimumPeriodError
		if !errors.As(err, &below) || below.Earliest.Format(domain.DateLayout) != "2027-02-23" {
			t.Fatalf("error = %v", err)
		}
		ok := domain.Date(2027, 2, 23)
		if _, err := w.contracts.Register(ctx, w.tenant, RegisterInput{RoomID: w.room.ID, StartsOn: w.today(), EndsOn: &ok}); err != nil {
			t.Errorf("exactly the minimum period is fine: %v", err)
		}
	})
	t.Run("pending limit of the plan", func(t *testing.T) {
		one := 1
		w := newWorld(t, fixedPlans{pending: &one})
		w.register(w.tenant, w.room)
		second := w.store.room(w.landlord, "P102", domain.RoomAvailable)
		_, err := w.contracts.Register(ctx, w.tenant, RegisterInput{RoomID: second.ID, StartsOn: w.today()})
		var limit *domain.RegistrationLimitError
		if !errors.As(err, &limit) || limit.Limit != 1 {
			t.Fatalf("error = %v", err)
		}
		// Another tenant has their own allowance.
		w.register(w.other, second)
	})
}

func TestApproveMakesTheRoomRentedAndRejectsTheOthers(t *testing.T) {
	w := newWorld(t, nil)
	winner := w.register(w.tenant, w.room)
	rival := w.register(w.other, w.room)
	w.notifier.sent = nil
	d := w.approve(winner.ID)
	if d.Status != domain.StatusActive {
		t.Fatalf("status = %s", d.Status)
	}
	if w.room.Status != domain.RoomRented || !slices.Equal(w.store.statuses, []string{"P101:rented"}) {
		t.Errorf("room = %s %v", w.room.Status, w.store.statuses)
	}
	if w.store.contracts[rival.ID].Status != domain.StatusRejected {
		t.Error("the rival registration must be rejected")
	}
	if got := w.notifier.kinds(); !slices.Equal(got, []string{"registration_approved:tenant"}) {
		t.Errorf("notifications = %v", got)
	}
	if n := w.notifier.sent[0]; n.UserID != w.tenant || n.URL != "/my-rooms/"+winner.ID.String() {
		t.Errorf("notification = %+v", n)
	}
	if !slices.Contains(w.audit.actions(), AuditUpdated) {
		t.Errorf("audit = %v", w.audit.actions())
	}
}

func TestRejectTellsTheTenantAndLeavesTheRoom(t *testing.T) {
	w := newWorld(t, nil)
	d := w.register(w.tenant, w.room)
	w.notifier.sent = nil
	rejected := domain.StatusRejected
	got, err := w.contracts.Update(context.Background(), w.landlord, d.ID, UpdateInput{Status: &rejected})
	if err != nil || got.Status != domain.StatusRejected || w.room.Status != domain.RoomAvailable {
		t.Fatalf("reject = %+v, %v", got, err)
	}
	if k := w.notifier.kinds(); !slices.Equal(k, []string{"registration_rejected:tenant"}) {
		t.Errorf("notifications = %v", k)
	}
}

func TestUpdateRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("second running contract", func(t *testing.T) {
		w := newWorld(t, nil)
		w.store.put(w.room, &w.other, domain.StatusActive)
		pending := w.store.put(w.room, &w.tenant, domain.StatusPending)
		active := domain.StatusActive
		_, err := w.contracts.Update(ctx, w.landlord, pending.ID, UpdateInput{Status: &active})
		is(t, err, domain.ErrRoomOccupied)
		if w.rollbacks != 1 {
			t.Error("the refused change must roll back")
		}
	})
	t.Run("invalid transition", func(t *testing.T) {
		w := newWorld(t, nil)
		pending := w.store.put(w.room, &w.tenant, domain.StatusPending)
		ended := domain.StatusEnded
		_, err := w.contracts.Update(ctx, w.landlord, pending.ID, UpdateInput{Status: &ended})
		is(t, err, domain.ErrInvalidTransition)
	})
	t.Run("another landlord's contract is not found", func(t *testing.T) {
		w := newWorld(t, nil)
		c := w.store.put(w.room, &w.tenant, domain.StatusPending)
		note := Some[*string](nil)
		_, err := w.contracts.Update(ctx, w.other, c.ID, UpdateInput{Notes: note})
		is(t, err, domain.ErrNotFound)
	})
	t.Run("running contract limit of the plan", func(t *testing.T) {
		one := 1
		w := newWorld(t, fixedPlans{contracts: &one})
		w.store.put(w.store.room(w.landlord, "P102", domain.RoomRented), &w.other, domain.StatusActive)
		pending := w.store.put(w.room, &w.tenant, domain.StatusPending)
		active := domain.StatusActive
		_, err := w.contracts.Update(ctx, w.landlord, pending.ID, UpdateInput{Status: &active})
		var limit *domain.ContractLimitError
		if !errors.As(err, &limit) || limit.Limit != 1 {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("the end date must follow the start", func(t *testing.T) {
		w := newWorld(t, nil)
		c := w.store.put(w.room, &w.tenant, domain.StatusPending)
		end := c.StartsOn
		_, err := w.contracts.Update(ctx, w.landlord, c.ID, UpdateInput{EndsOn: Some(&end)})
		is(t, err, domain.ErrEndNotAfterStart)
	})
	t.Run("minimum period is checked when the dates change", func(t *testing.T) {
		w := newWorld(t, nil)
		w.room.MinPeriod = 3
		c := w.store.put(w.room, &w.tenant, domain.StatusPending)
		soon := c.StartsOn.AddDate(0, 1, 0)
		_, err := w.contracts.Update(ctx, w.landlord, c.ID, UpdateInput{EndsOn: Some(&soon)})
		var below *domain.BelowMinimumPeriodError
		if !errors.As(err, &below) {
			t.Fatalf("error = %v", err)
		}
		// Open-ended always qualifies; a note alone does not re-check the dates.
		if _, err := w.contracts.Update(ctx, w.landlord, c.ID, UpdateInput{EndsOn: Some[*time.Time](nil)}); err != nil {
			t.Errorf("open-ended: %v", err)
		}
		// Rejecting does not check the dates either.
		rejected := domain.StatusRejected
		if _, err := w.contracts.Update(ctx, w.landlord, c.ID, UpdateInput{Status: &rejected, EndsOn: Some(&soon)}); err != nil {
			t.Errorf("rejecting: %v", err)
		}
	})
	t.Run("occupants", func(t *testing.T) {
		w := newWorld(t, nil)
		c := w.store.put(w.room, &w.tenant, domain.StatusPending)
		zero := 0
		_, err := w.contracts.Update(ctx, w.landlord, c.ID, UpdateInput{OccupantCount: &zero})
		is(t, err, domain.ErrOccupantsInvalid)
	})
}

func TestUpdateEditsTermsAndOnlyBlankContractsTakeTheTenantName(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	held := w.store.put(w.room, &w.tenant, domain.StatusPending)
	price, occupants, name := int64(3_500_000), 3, "Somebody"
	deposit := int64(1_000_000)
	d, err := w.contracts.Update(ctx, w.landlord, held.ID, UpdateInput{
		PriceVND: &price, OccupantCount: &occupants, DepositVND: Some(&deposit), TenantName: Some(&name),
	})
	if err != nil || d.PriceVND != price || d.OccupantCount != 3 || *d.DepositVND != deposit {
		t.Fatalf("update = %+v, %v", d, err)
	}
	if d.TenantName != nil {
		t.Error("the name of an account is never overwritten")
	}
	blank := w.store.put(w.store.room(w.landlord, "P102", domain.RoomRented), nil, domain.StatusActive)
	d, err = w.contracts.Update(ctx, w.landlord, blank.ID, UpdateInput{TenantName: Some(&name)})
	if err != nil || d.TenantName == nil || *d.TenantName != name {
		t.Errorf("a blank contract takes the name: %+v, %v", d, err)
	}
	// Clearing with null.
	if d, _ = w.contracts.Update(ctx, w.landlord, blank.ID, UpdateInput{TenantName: Some[*string](nil)}); d.TenantName != nil {
		t.Error("null clears the name")
	}
}

func TestEndingAContractFreesTheRoom(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	d := w.approve(w.register(w.tenant, w.room).ID)
	w.notifier.sent = nil
	ended := domain.StatusEnded
	got, err := w.contracts.Update(ctx, w.landlord, d.ID, UpdateInput{Status: &ended})
	if err != nil || got.Status != domain.StatusEnded || got.EndsOn == nil || !got.EndsOn.Equal(w.today()) {
		t.Fatalf("end = %+v, %v", got, err)
	}
	if w.room.Status != domain.RoomAvailable {
		t.Errorf("room = %s", w.room.Status)
	}
	if k := w.notifier.kinds(); !slices.Equal(k, []string{"contract_ended:tenant", "contract_ended:landlord"}) {
		t.Errorf("both sides are told, got %v", k)
	}
}

func TestReopeningAnEndedContract(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	ended := w.store.put(w.room, &w.tenant, domain.StatusEnded)
	approved := domain.CancellationApproved
	ended.Cancellation = domain.Cancellation{Status: &approved}
	d := w.approve(ended.ID)
	if d.Status != domain.StatusActive || d.Cancellation.Status != nil || w.room.Status != domain.RoomRented {
		t.Fatalf("reopened = %+v, room %s", d, w.room.Status)
	}
	// Only one contract can run in a room: a second ended one cannot be reopened meanwhile.
	other := w.store.put(w.room, &w.other, domain.StatusEnded)
	active := domain.StatusActive
	_, err := w.contracts.Update(ctx, w.landlord, other.ID, UpdateInput{Status: &active})
	is(t, err, domain.ErrRoomOccupied)
	// A finished contract can be edited without reopening it.
	note := "still editable"
	if d, err := w.contracts.Update(ctx, w.landlord, other.ID, UpdateInput{Notes: Some(&note)}); err != nil || d.Status != domain.StatusEnded || *d.Notes != note {
		t.Errorf("edit = %+v, %v", d, err)
	}
}

func TestAContractSomebodyHoldsDisplacesTheBlankOne(t *testing.T) {
	w := newWorld(t, nil)
	w.room.Status = domain.RoomRented
	if err := w.links.Rented(context.Background(), w.room.ID); err != nil {
		t.Fatal(err)
	}
	blank := w.landlordContracts()[0]
	if blank.TenantID != nil || blank.Status != domain.StatusActive {
		t.Fatalf("blank = %+v", blank)
	}
	// The room is rented, so nobody can register for it; the landlord reopens a registration of the past instead.
	pending := w.store.put(w.room, &w.tenant, domain.StatusPending)
	d := w.approve(pending.ID)
	if d.Status != domain.StatusActive {
		t.Fatalf("approved = %+v", d)
	}
	if w.store.contracts[blank.ID].Status != domain.StatusEnded {
		t.Error("the blank contract must give way")
	}
	if !slices.Contains(w.audit.actions(), AuditTerminated) {
		t.Errorf("audit = %v", w.audit.actions())
	}
	if w.room.Status != domain.RoomRented || len(w.store.statuses) != 0 {
		t.Errorf("the room stays rented without a status change: %v", w.store.statuses)
	}
}

func (w *world) landlordContracts() []domain.Detail {
	w.t.Helper()
	items, _, err := w.contracts.List(context.Background(), w.landlordView(), ListFilter{}, 100, 0)
	if err != nil {
		w.t.Fatal(err)
	}
	return items
}

func TestBlankContractOpensAndCloses(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)

	// Not rented: nothing to open.
	if err := w.links.Rented(ctx, w.room.ID); err != nil || len(w.landlordContracts()) != 0 {
		t.Fatalf("an available room gets no blank contract: %v", err)
	}
	w.room.Status = domain.RoomRented
	if err := w.links.Rented(ctx, w.room.ID); err != nil {
		t.Fatal(err)
	}
	list := w.landlordContracts()
	if len(list) != 1 {
		t.Fatalf("blank contracts = %d", len(list))
	}
	b := list[0]
	if b.TenantID != nil || b.Status != domain.StatusActive || b.PriceVND != w.room.PriceVND || b.OccupantCount != 1 ||
		!b.StartsOn.Equal(w.today()) || b.ConfirmedAt == nil || !b.Unassigned() {
		t.Errorf("blank = %+v", b.Contract)
	}
	if !slices.Equal(w.audit.actions(), []string{AuditCreated}) || w.audit.events[0].Meta["unassigned"] != true {
		t.Errorf("audit = %v", w.audit.events)
	}
	// Opening again changes nothing: a contract already runs.
	if err := w.links.Rented(ctx, w.room.ID); err != nil || len(w.landlordContracts()) != 1 {
		t.Errorf("second open: %v, %d contracts", err, len(w.landlordContracts()))
	}
	// A blank contract does not lock the room.
	occupied, err := w.links.Occupied(ctx, []uuid.UUID{w.room.ID})
	if err != nil || len(occupied) != 0 {
		t.Errorf("occupied = %v, %v", occupied, err)
	}
	// The room leaves rented: the blank contract ends today.
	if err := w.links.Released(ctx, []uuid.UUID{w.room.ID}); err != nil {
		t.Fatal(err)
	}
	if got := w.store.contracts[b.ID]; got.Status != domain.StatusEnded || got.EndsOn == nil || !got.EndsOn.Equal(w.today()) {
		t.Errorf("closed = %+v", got)
	}
	if err := w.links.Released(ctx, nil); err != nil {
		t.Error(err)
	}
	// A deleted or unknown room is ignored.
	if err := w.links.Rented(ctx, uuid.New()); err != nil {
		t.Errorf("unknown room: %v", err)
	}
	w.room.Live = false
	w.room.Status = domain.RoomRented
	before := len(w.store.contracts)
	if err := w.links.Rented(ctx, w.room.ID); err != nil || len(w.store.contracts) != before {
		t.Errorf("a deleted room gets no blank contract: %v", err)
	}
}

func TestOccupiedMeansATenantHoldsARunningContract(t *testing.T) {
	w := newWorld(t, nil)
	w.store.put(w.room, &w.tenant, domain.StatusActive)
	free := w.store.room(w.landlord, "P102", domain.RoomAvailable)
	w.store.put(free, &w.tenant, domain.StatusPending)
	got, err := w.links.Occupied(context.Background(), []uuid.UUID{w.room.ID, free.ID})
	if err != nil || !got[w.room.ID] || got[free.ID] {
		t.Errorf("occupied = %v, %v", got, err)
	}
}

func TestTerminate(t *testing.T) {
	ctx := context.Background()
	reason := "Rules broken"
	t.Run("a running contract ends today and the room is free", func(t *testing.T) {
		w := newWorld(t, nil)
		d := w.approve(w.register(w.tenant, w.room).ID)
		w.notifier.sent = nil
		got, err := w.contracts.Terminate(ctx, w.landlord, d.ID, &reason)
		if err != nil || got.Status != domain.StatusEnded || !got.EndsOn.Equal(w.today()) || w.room.Status != domain.RoomAvailable {
			t.Fatalf("terminate = %+v, %v (room %s)", got, err, w.room.Status)
		}
		if k := w.notifier.kinds(); !slices.Equal(k, []string{"contract_ended:tenant", "contract_ended:landlord"}) || w.notifier.sent[0].Params["reason"] != reason {
			t.Errorf("notifications = %v %+v", k, w.notifier.sent)
		}
		if !slices.Contains(w.audit.actions(), AuditTerminated) {
			t.Errorf("audit = %v", w.audit.actions())
		}
	})
	t.Run("an open request is answered as approved", func(t *testing.T) {
		w := newWorld(t, nil)
		d := w.approve(w.register(w.tenant, w.room).ID)
		if _, err := w.contracts.RequestCancellation(ctx, w.tenant, d.ID, nil); err != nil {
			t.Fatal(err)
		}
		got, err := w.contracts.Terminate(ctx, w.landlord, d.ID, nil)
		if err != nil || *got.Cancellation.Status != domain.CancellationApproved {
			t.Errorf("terminate = %+v, %v", got, err)
		}
	})
	t.Run("a waiting registration is rejected", func(t *testing.T) {
		w := newWorld(t, nil)
		d := w.register(w.tenant, w.room)
		got, err := w.contracts.Terminate(ctx, w.landlord, d.ID, nil)
		if err != nil || got.Status != domain.StatusRejected || got.EndsOn != nil || w.room.Status != domain.RoomAvailable {
			t.Errorf("terminate = %+v, %v", got, err)
		}
	})
	t.Run("an ended contract cannot be terminated", func(t *testing.T) {
		w := newWorld(t, nil)
		c := w.store.put(w.room, &w.tenant, domain.StatusEnded)
		_, err := w.contracts.Terminate(ctx, w.landlord, c.ID, nil)
		is(t, err, domain.ErrInvalidTransition)
	})
	t.Run("not the landlord's", func(t *testing.T) {
		w := newWorld(t, nil)
		c := w.store.put(w.room, &w.tenant, domain.StatusActive)
		_, err := w.contracts.Terminate(ctx, w.other, c.ID, nil)
		is(t, err, domain.ErrNotFound)
	})
}

func TestCancellationFlow(t *testing.T) {
	ctx := context.Background()
	reason := "Moving to another city"
	t.Run("request, then the landlord approves", func(t *testing.T) {
		w := newWorld(t, nil)
		d := w.approve(w.register(w.tenant, w.room).ID)
		w.notifier.sent = nil
		got, err := w.contracts.RequestCancellation(ctx, w.tenant, d.ID, &reason)
		if err != nil || got.Status != domain.StatusActive || *got.Cancellation.Status != domain.CancellationRequested || *got.Cancellation.Reason != reason {
			t.Fatalf("request = %+v, %v", got, err)
		}
		if w.room.Status != domain.RoomRented {
			t.Error("the contract keeps running meanwhile")
		}
		if k := w.notifier.kinds(); !slices.Equal(k, []string{"cancellation_requested:landlord"}) {
			t.Errorf("notifications = %v", k)
		}
		// An open request cannot be sent again.
		_, err = w.contracts.RequestCancellation(ctx, w.tenant, d.ID, nil)
		is(t, err, domain.ErrCancellationNotAllowed)

		w.notifier.sent = nil
		ended, err := w.contracts.ApproveCancellation(ctx, w.landlord, d.ID)
		if err != nil || ended.Status != domain.StatusEnded || *ended.Cancellation.Status != domain.CancellationApproved || w.room.Status != domain.RoomAvailable {
			t.Fatalf("approve = %+v, %v", ended, err)
		}
		if k := w.notifier.kinds(); !slices.Equal(k, []string{"contract_ended:tenant", "contract_ended:landlord"}) {
			t.Errorf("notifications = %v", k)
		}
	})
	t.Run("decline keeps it running and a new request is allowed", func(t *testing.T) {
		w := newWorld(t, nil)
		d := w.approve(w.register(w.tenant, w.room).ID)
		if _, err := w.contracts.RequestCancellation(ctx, w.tenant, d.ID, &reason); err != nil {
			t.Fatal(err)
		}
		w.notifier.sent = nil
		note := "Deposit is not settled"
		got, err := w.contracts.DeclineCancellation(ctx, w.landlord, d.ID, &note)
		if err != nil || got.Status != domain.StatusActive || *got.Cancellation.Status != domain.CancellationDeclined || *got.Cancellation.ResponseNote != note {
			t.Fatalf("decline = %+v, %v", got, err)
		}
		if k := w.notifier.kinds(); !slices.Equal(k, []string{"cancellation_declined:tenant"}) {
			t.Errorf("notifications = %v", k)
		}
		again, err := w.contracts.RequestCancellation(ctx, w.tenant, d.ID, &reason)
		if err != nil || again.Cancellation.ResponseNote != nil {
			t.Errorf("new request = %+v, %v", again, err)
		}
		// The tenant can take an open request back, once.
		back, err := w.contracts.WithdrawCancellation(ctx, w.tenant, d.ID)
		if err != nil || back.Cancellation.Status != nil {
			t.Errorf("withdraw = %+v, %v", back, err)
		}
		_, err = w.contracts.WithdrawCancellation(ctx, w.tenant, d.ID)
		is(t, err, domain.ErrCancellationNotAllowed)
	})
	t.Run("who may do what", func(t *testing.T) {
		w := newWorld(t, nil)
		running := w.store.put(w.room, &w.tenant, domain.StatusActive)
		// Another tenant does not see it.
		_, err := w.contracts.RequestCancellation(ctx, w.other, running.ID, nil)
		is(t, err, domain.ErrNotFound)
		// The landlord cannot ask, and cannot answer a request nobody made.
		_, err = w.contracts.RequestCancellation(ctx, w.landlord, running.ID, nil)
		is(t, err, domain.ErrNotFound)
		_, err = w.contracts.ApproveCancellation(ctx, w.landlord, running.ID)
		is(t, err, domain.ErrCancellationNotAllowed)
		_, err = w.contracts.DeclineCancellation(ctx, w.landlord, running.ID, nil)
		is(t, err, domain.ErrCancellationNotAllowed)
		// Another landlord cannot answer.
		other := w.store.room(w.other, "X1", domain.RoomRented)
		_ = other
		_, err = w.contracts.ApproveCancellation(ctx, w.other, running.ID)
		is(t, err, domain.ErrNotFound)
		// Only a running contract can be cancelled.
		for _, st := range []domain.Status{domain.StatusPending, domain.StatusEnded, domain.StatusRejected} {
			c := w.store.put(w.store.room(w.landlord, "R"+string(st), domain.RoomAvailable), &w.tenant, st)
			_, err = w.contracts.RequestCancellation(ctx, w.tenant, c.ID, nil)
			is(t, err, domain.ErrCancellationNotAllowed)
		}
	})
}

func TestBulkAnswerReportsPartialFailures(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	p2 := w.store.room(w.landlord, "P102", domain.RoomAvailable)
	p3 := w.store.room(w.landlord, "P103", domain.RoomAvailable)
	ok := w.store.put(w.room, &w.tenant, domain.StatusPending)
	running := w.store.put(p2, &w.tenant, domain.StatusActive) // not waiting: refused
	rival := w.store.put(p3, &w.tenant, domain.StatusPending)
	taken := w.store.put(p3, &w.other, domain.StatusPending)
	w.store.rooms[p3.ID].Status = domain.RoomAvailable

	res, err := w.contracts.BulkAnswer(ctx, w.landlord, []uuid.UUID{ok.ID, running.ID, rival.ID, taken.ID}, AnswerApprove)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Done, []uuid.UUID{ok.ID, rival.ID}) {
		t.Errorf("done = %v", res.Done)
	}
	// The second pending contract of P103 was rejected by the first approval, so it is not waiting any more.
	if len(res.Failed) != 2 || res.Failed[0].ID != running.ID || res.Failed[1].ID != taken.ID {
		t.Fatalf("failed = %+v", res.Failed)
	}
	for _, f := range res.Failed {
		is(t, f.Err, domain.ErrInvalidTransition)
		if f.Name == "" {
			t.Error("a refused contract is named by its room")
		}
	}
	if w.store.contracts[ok.ID].Status != domain.StatusActive || w.store.contracts[taken.ID].Status != domain.StatusRejected {
		t.Error("statuses after the bulk answer")
	}

	// Reject: a contract with a running rival cannot... rejecting needs nothing of the room.
	again := w.store.put(w.store.room(w.landlord, "P104", domain.RoomAvailable), &w.other, domain.StatusPending)
	res, err = w.contracts.BulkAnswer(ctx, w.landlord, []uuid.UUID{again.ID}, AnswerReject)
	if err != nil || len(res.Done) != 1 || w.store.contracts[again.ID].Status != domain.StatusRejected {
		t.Errorf("reject = %+v, %v", res, err)
	}

	// An id of somebody else's contract refuses the whole request.
	foreign := w.store.put(w.store.room(w.other, "Z1", domain.RoomAvailable), &w.tenant, domain.StatusPending)
	_, err = w.contracts.BulkAnswer(ctx, w.landlord, []uuid.UUID{foreign.ID}, AnswerApprove)
	is(t, err, domain.ErrItemsNotFound)
}

func TestBulkApproveFailsAMemberThatHitsTheRoomRule(t *testing.T) {
	w := newWorld(t, nil)
	w.store.put(w.room, &w.other, domain.StatusActive)
	pending := w.store.put(w.room, &w.tenant, domain.StatusPending)
	res, err := w.contracts.BulkAnswer(context.Background(), w.landlord, []uuid.UUID{pending.ID}, AnswerApprove)
	if err != nil || len(res.Done) != 0 || len(res.Failed) != 1 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	is(t, res.Failed[0].Err, domain.ErrRoomOccupied)
}

func TestVisibilityByRole(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	mine := w.store.put(w.room, &w.tenant, domain.StatusPending)
	foreignRoom := w.store.room(w.other, "Z1", domain.RoomAvailable)
	foreign := w.store.put(foreignRoom, &w.other, domain.StatusPending)

	landlordList, total, _ := w.contracts.List(ctx, w.landlordView(), ListFilter{}, 20, 0)
	if total != 1 || landlordList[0].ID != mine.ID {
		t.Errorf("a landlord sees the contracts of their rooms, got %d", total)
	}
	tenantList, total, _ := w.contracts.List(ctx, domain.Viewer{UserID: w.tenant}, ListFilter{}, 20, 0)
	if total != 1 || tenantList[0].ID != mine.ID {
		t.Errorf("a tenant sees their own, got %d", total)
	}
	if _, err := w.contracts.Get(ctx, w.landlordView(), foreign.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a landlord does not see somebody else's contract: %v", err)
	}
	if _, err := w.contracts.Get(ctx, domain.Viewer{UserID: w.tenant}, foreign.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a tenant does not see somebody else's contract: %v", err)
	}
	// The same account in the other role sees the other side.
	if _, total, _ := w.contracts.List(ctx, domain.Viewer{UserID: w.landlord}, ListFilter{}, 20, 0); total != 0 {
		t.Errorf("the landlord acting as tenant sees %d", total)
	}
	// A contract that is not confirmed yet stays invisible to the landlord.
	hidden := w.store.put(w.room, &w.other, domain.StatusPending)
	hidden.ConfirmedAt = nil
	if _, total, _ := w.contracts.List(ctx, w.landlordView(), ListFilter{}, 20, 0); total != 1 {
		t.Errorf("an unconfirmed contract is hidden, got %d", total)
	}
	// A deleted room hides its contracts from the landlord.
	w.room.Live = false
	if _, total, _ := w.contracts.List(ctx, w.landlordView(), ListFilter{}, 20, 0); total != 0 {
		t.Errorf("contracts of a deleted room are hidden, got %d", total)
	}
}

func TestListFilterAndSearchPattern(t *testing.T) {
	status, cancel := domain.StatusActive, domain.CancellationRequested
	q := queryOf(ListFilter{Status: &status, CancellationStatus: &cancel, Search: "  50%_Off ", Sort: "-starts_on"}, 10, 20)
	if len(q.Statuses) != 1 || q.Statuses[0] != status || q.CancellationStatus != &cancel || q.Limit != 10 || q.Offset != 20 || q.Sort != "-starts_on" {
		t.Errorf("query = %+v", q)
	}
	if q.SearchLike == nil || *q.SearchLike != `50\%\_off` {
		t.Errorf("the search is trimmed, lower-cased and escaped, got %v", q.SearchLike)
	}
	if queryOf(ListFilter{Search: "   "}, 1, 0).SearchLike != nil {
		t.Error("a blank search is no search")
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	t.Run("landlord removes a running contract and frees the room", func(t *testing.T) {
		w := newWorld(t, nil)
		d := w.approve(w.register(w.tenant, w.room).ID)
		if err := w.contracts.Delete(ctx, w.landlordView(), d.ID); err != nil {
			t.Fatal(err)
		}
		if !w.store.deleted[d.ID] || w.room.Status != domain.RoomAvailable {
			t.Errorf("deleted=%v room=%s", w.store.deleted[d.ID], w.room.Status)
		}
		if !slices.Contains(w.audit.actions(), AuditDeleted) {
			t.Errorf("audit = %v", w.audit.actions())
		}
	})
	t.Run("a tenant withdraws a waiting registration but not a running contract", func(t *testing.T) {
		w := newWorld(t, nil)
		pending := w.register(w.tenant, w.room)
		running := w.store.put(w.store.room(w.landlord, "P102", domain.RoomRented), &w.tenant, domain.StatusActive)
		is(t, w.contracts.Delete(ctx, domain.Viewer{UserID: w.tenant}, running.ID), domain.ErrNotDeletable)
		if err := w.contracts.Delete(ctx, domain.Viewer{UserID: w.tenant}, pending.ID); err != nil {
			t.Fatal(err)
		}
		// Somebody else's contract does not exist for them.
		is(t, w.contracts.Delete(ctx, domain.Viewer{UserID: w.other}, running.ID), domain.ErrNotFound)
		is(t, w.contracts.Delete(ctx, domain.Viewer{UserID: w.other, AsLandlord: true}, running.ID), domain.ErrNotFound)
	})
}

func TestReorder(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	rooms := []*domain.RoomFacts{w.room, w.store.room(w.landlord, "P102", domain.RoomAvailable), w.store.room(w.landlord, "P103", domain.RoomAvailable)}
	var ids []uuid.UUID
	for i, r := range rooms {
		c := w.store.put(r, &w.tenant, domain.StatusPending)
		c.DisplayOrder = i + 1
		ids = append(ids, c.ID)
	}
	if err := w.contracts.Reorder(ctx, w.landlord, []uuid.UUID{ids[2], ids[0]}); err != nil {
		t.Fatal(err)
	}
	got := []int{w.store.contracts[ids[0]].DisplayOrder, w.store.contracts[ids[1]].DisplayOrder, w.store.contracts[ids[2]].DisplayOrder}
	if !slices.Equal(got, []int{3, 2, 1}) {
		t.Errorf("orders = %v", got)
	}
	is(t, w.contracts.Reorder(ctx, w.landlord, []uuid.UUID{uuid.New()}), domain.ErrItemsNotFound)
	foreign := w.store.put(w.store.room(w.other, "Z1", domain.RoomAvailable), &w.tenant, domain.StatusPending)
	is(t, w.contracts.Reorder(ctx, w.landlord, []uuid.UUID{foreign.ID}), domain.ErrItemsNotFound)
}

func TestNewContractsGoOnTop(t *testing.T) {
	w := newWorld(t, nil)
	first := w.register(w.tenant, w.room)
	second := w.register(w.other, w.room)
	if second.DisplayOrder >= first.DisplayOrder {
		t.Errorf("the newer contract must come first: %d then %d", second.DisplayOrder, first.DisplayOrder)
	}
}

func TestRentals(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	active := w.store.put(w.room, &w.tenant, domain.StatusActive)
	w.store.put(w.store.room(w.landlord, "P102", domain.RoomAvailable), &w.tenant, domain.StatusPending)
	w.store.put(w.store.room(w.landlord, "P103", domain.RoomAvailable), &w.tenant, domain.StatusEnded)
	w.store.put(w.store.room(w.landlord, "P104", domain.RoomAvailable), &w.tenant, domain.StatusRejected)
	w.store.put(w.store.room(w.landlord, "P105", domain.RoomAvailable), &w.other, domain.StatusActive)

	page, err := w.rentals.List(ctx, w.tenant, GroupHistory, 20, 0)
	if err != nil || page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("history = %+v, %v", page, err)
	}
	if page.Counts[GroupActive] != 1 || page.Counts[GroupPending] != 1 || page.Counts[GroupHistory] != 2 {
		t.Errorf("counts = %v (they ignore the listed group)", page.Counts)
	}
	all, _ := w.rentals.List(ctx, w.tenant, "", 20, 0)
	if all.Total != 4 {
		t.Errorf("all = %d", all.Total)
	}
	paged, _ := w.rentals.List(ctx, w.tenant, "", 3, 3)
	if len(paged.Items) != 1 || paged.Total != 4 {
		t.Errorf("paged = %d of %d", len(paged.Items), paged.Total)
	}

	d, err := w.rentals.Get(ctx, w.tenant, active.ID)
	if err != nil || d.Room.Name != "P101" || len(d.Charges) != 1 {
		t.Errorf("detail = %+v, %v", d, err)
	}
	if _, err := w.rentals.Get(ctx, w.other, active.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("anybody else's contract is not found: %v", err)
	}
	if _, err := w.rentals.Get(ctx, w.landlord, active.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the landlord is not the renter: %v", err)
	}
	if GroupStatuses("nope") != nil {
		t.Error("an unknown group lists everything")
	}
}

func TestRentalsFilterByStateOfMoneyAndCarryTheBillingSummary(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	due := w.store.put(w.room, &w.tenant, domain.StatusActive)
	review := w.store.put(w.store.room(w.landlord, "P102", domain.RoomAvailable), &w.tenant, domain.StatusActive)
	settled := w.store.put(w.store.room(w.landlord, "P103", domain.RoomAvailable), &w.tenant, domain.StatusActive)
	ended := w.store.put(w.store.room(w.landlord, "P104", domain.RoomAvailable), &w.tenant, domain.StatusEnded)
	next := w.store.now.AddDate(0, 0, 3)
	w.store.billing = map[uuid.UUID]domain.Billing{
		due.ID:    {DueCount: 2, DueAmountVND: 900_000, Partial: true, NextDueAt: &next, UnpaidInvoices: 2},
		review.ID: {ReviewCount: 1, UnpaidInvoices: 1},
		ended.ID:  {DueCount: 1, DueAmountVND: 10},
	}
	_ = settled

	count := func(state domain.PaymentState) RentalPage {
		t.Helper()
		page, err := w.rentals.ListByPayment(ctx, w.tenant, GroupActive, &state, 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	if p := count(domain.PaymentDue); p.Total != 1 || p.Items[0].ID != due.ID || p.Items[0].Billing.DueAmountVND != 900_000 || !p.Items[0].Billing.Partial {
		t.Errorf("due: %+v", p.Items)
	}
	if p := count(domain.PaymentReview); p.Total != 1 || p.Items[0].ID != review.ID {
		t.Errorf("review: %+v", p.Items)
	}
	if p := count(domain.PaymentSettled); p.Total != 1 || p.Items[0].ID != settled.ID {
		t.Errorf("settled: %+v", p.Items)
	}
	// The counts are of the running contracts and ignore the filter and the tab: the ended one is not in them.
	if got := count(domain.PaymentDue).PaymentCounts; got != (domain.PaymentCounts{All: 3, Due: 1, Review: 1, Settled: 1}) {
		t.Errorf("payment counts: %+v", got)
	}
	// Without a filter every contract of the tab comes with its summary.
	all, err := w.rentals.List(ctx, w.tenant, GroupActive, 20, 0)
	if err != nil || all.Total != 3 || all.PaymentCounts.All != 3 {
		t.Errorf("all: %+v, %v", all, err)
	}
}

func TestListFilterReachesTheLandlordsQuery(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	owing := w.store.put(w.room, &w.tenant, domain.StatusActive)
	w.store.put(w.store.room(w.landlord, "P102", domain.RoomAvailable), &w.other, domain.StatusActive)
	w.store.billing = map[uuid.UUID]domain.Billing{owing.ID: {UnpaidInvoices: 2}}

	items, total, err := w.contracts.List(ctx, domain.Viewer{UserID: w.landlord, AsLandlord: true}, ListFilter{HasUnpaidInvoice: true}, 20, 0)
	if err != nil || total != 1 || items[0].ID != owing.ID || items[0].Billing.UnpaidInvoices != 2 {
		t.Errorf("has_unpaid_invoice: %d, %+v, %v", total, items, err)
	}
	if _, total, _ = w.contracts.List(ctx, domain.Viewer{UserID: w.landlord, AsLandlord: true}, ListFilter{}, 20, 0); total != 2 {
		t.Errorf("without the filter: %d", total)
	}
}

func TestSeederIsIdempotent(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	w.store.room(w.landlord, "P102", domain.RoomAvailable)
	w.store.room(w.landlord, "P103", domain.RoomAvailable)
	seeder := NewSeeder(w.deps, Options{}, w.store)

	if err := seeder.SeedDemo(ctx, w.landlord, w.tenant); err != nil {
		t.Fatal(err)
	}
	page, _ := w.rentals.List(ctx, w.tenant, "", 20, 0)
	if page.Counts[GroupActive] != 1 || page.Counts[GroupPending] != 1 || page.Counts[GroupHistory] != 1 {
		t.Errorf("counts = %v", page.Counts)
	}
	if w.room.Status != domain.RoomRented {
		t.Error("the room of the running contract is rented")
	}
	if err := seeder.SeedDemo(ctx, w.landlord, w.tenant); err != nil {
		t.Fatal(err)
	}
	if again, _ := w.rentals.List(ctx, w.tenant, "", 20, 0); again.Total != 3 {
		t.Errorf("a second run adds nothing, got %d", again.Total)
	}
	// Without the demo rooms there is nothing to do.
	empty := newWorld(t, nil)
	if err := NewSeeder(empty.deps, Options{}, empty.store).SeedDemo(ctx, empty.landlord, empty.tenant); err != nil {
		t.Errorf("no rooms: %v", err)
	}
}

func TestRoomStatusFailureUndoesTheChange(t *testing.T) {
	w := newWorld(t, nil)
	d := w.register(w.tenant, w.room)
	w.store.setterErr = errors.New("boom")
	active := domain.StatusActive
	if _, err := w.contracts.Update(context.Background(), w.landlord, d.ID, UpdateInput{Status: &active}); err == nil {
		t.Fatal("a failing room status must fail the change")
	}
	if w.rollbacks == 0 {
		t.Error("the transaction must roll back")
	}
}
