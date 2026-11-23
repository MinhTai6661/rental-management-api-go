package property

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// buildingWithDefaults puts a building with a rent and an area in the fake store.
func buildingWithDefaults(w *world) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	if w.buildings.rows == nil {
		w.buildings.rows = map[uuid.UUID]domain.Building{}
	}
	w.buildings.rows[id] = domain.Building{ID: id, BuildingData: domain.BuildingData{Name: "A", BuildingDefaults: domain.BuildingDefaults{
		WardCode: ptr(int32(4)), AddressDetail: "12 Le Loi", PriceVND: ptr(int64(2_500_000)), Area: ptr(20.0),
		MinPeriod: ptr(int32(3)), PeriodUnit: ptr("month"), Description: ptr("Quiet"),
	}}}
	return id
}

func TestCreateRoomFollowsTheBuildingForWhatItDoesNotSet(t *testing.T) {
	w := newWorld()
	b := buildingWithDefaults(w)
	own := 30.0
	_, err := w.roomUC().Create(context.Background(), w.owner, RoomInput{BuildingID: &b, Name: " P1 ", Area: &own})
	if err != nil {
		t.Fatal(err)
	}
	got := w.rooms.inserted[0]
	if got.Name != "P1" || got.PriceVND != 2_500_000 || *got.Area != 30 || got.MinPeriod != 3 || got.AddressDetail != "12 Le Loi" {
		t.Errorf("values in force %+v", got)
	}
	want := []domain.Group{domain.GroupPrice, domain.GroupRentalPeriod, domain.GroupDescription, domain.GroupCharges, domain.GroupMedia}
	if !slices.Equal(got.Inherits, want) {
		t.Errorf("follows %v, want %v", got.Inherits, want)
	}
	for _, step := range []string{"follow charges", "cover from building"} {
		if !w.c.has(step) {
			t.Errorf("missing %q in %v", step, w.c.log)
		}
	}
	if w.c.has("charges to room") {
		t.Errorf("a room that follows the charges stores none of its own: %v", w.c.log)
	}
}

func TestCreateRoomOutsideABuildingNeedsItsOwnAddressAndRent(t *testing.T) {
	w := newWorld()
	_, err := w.roomUC().Create(context.Background(), w.owner, RoomInput{Name: "A1"})
	var invalid *domain.ValidationError
	if !errors.As(err, &invalid) || len(invalid.Violations) != 2 {
		t.Fatalf("got %v", err)
	}
	if _, err := w.roomUC().Create(context.Background(), w.owner, RoomInput{Name: "A1", BuildingID: ptr(uuid.Must(uuid.NewV7()))}); !is(err, domain.ErrBuildingNotFound) {
		t.Errorf("a building that is not theirs: %v", err)
	}
	status := domain.StatusRented
	addr, price := "1 A", int64(1)
	if _, err := w.roomUC().Create(context.Background(), w.owner, RoomInput{Name: "A1", AddressDetail: &addr, PriceVND: &price, Status: &status}); err != nil {
		t.Fatal(err)
	}
	if len(w.observer.statuses) != 1 || w.observer.statuses[0].To != domain.StatusRented {
		t.Errorf("a room born rented is announced: %v", w.observer.statuses)
	}
	if !w.c.has("charges to room") {
		t.Errorf("a room outside a building owns its charges: %v", w.c.log)
	}
}

func TestCreateRoomRespectsThePlanLimitAndNames(t *testing.T) {
	w := newWorld()
	b := buildingWithDefaults(w)
	w.plans = fakePlans{rooms: ptr(5)}
	w.rooms.live = 5
	_, err := w.roomUC().Create(context.Background(), w.owner, RoomInput{BuildingID: &b, Name: "P1"})
	var limit *domain.RoomLimitError
	if !errors.As(err, &limit) || limit.Limit != 5 {
		t.Fatalf("%v", err)
	}
	w.rooms.live = 0
	w.rooms.nameTaken = true
	if _, err := w.roomUC().Create(context.Background(), w.owner, RoomInput{BuildingID: &b, Name: "P1"}); !is(err, domain.ErrRoomNameTaken) {
		t.Errorf("%v", err)
	}
}

func TestBulkStatusSkipsRoomsABlockerHolds(t *testing.T) {
	w := newWorld()
	busy, free, already := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	w.rooms.refs = []domain.RoomRef{
		{ID: busy, Name: "busy", Status: domain.StatusRented},
		{ID: free, Name: "free", Status: domain.StatusRented},
		{ID: already, Name: "already", Status: domain.StatusAvailable},
	}
	w.guards = fakeGuards{busy: {ActiveContract: true}}
	res, err := w.roomUC().BulkSetStatus(context.Background(), w.owner, []uuid.UUID{busy, free, already}, domain.StatusAvailable)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 1 || res.Failed[0].ID != busy || !is(res.Failed[0].Err, domain.ErrRoomStatusLocked) {
		t.Errorf("failed %+v", res.Failed)
	}
	if !slices.Equal(res.Done, []uuid.UUID{already, free}) {
		t.Errorf("done %v: a room already in the status counts as done", res.Done)
	}
	if !slices.Equal(w.rooms.statusSet, []uuid.UUID{free}) || len(w.observer.statuses) != 1 || w.observer.statuses[0].From != domain.StatusRented {
		t.Errorf("only the moved room is changed and announced: %v %v", w.rooms.statusSet, w.observer.statuses)
	}
}

func TestBulkDeleteRoomsReportsTheBlockedOnes(t *testing.T) {
	w := newWorld()
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	w.rooms.refs = []domain.RoomRef{{ID: a, Name: "a"}, {ID: b, Name: "b"}}
	w.guards = fakeGuards{a: {OpenListing: true}}
	res, err := w.roomUC().BulkDelete(context.Background(), w.owner, []uuid.UUID{a, b})
	if err != nil || len(res.Failed) != 1 || !is(res.Failed[0].Err, domain.ErrRoomHasListing) || !slices.Equal(w.rooms.deleted, []uuid.UUID{b}) {
		t.Fatalf("%+v %v", res, err)
	}
	if err := w.roomUC().Delete(context.Background(), w.owner, a); !is(err, domain.ErrNotFound) {
		t.Errorf("a path id that is not in the answer is not found: %v", err)
	}
	if _, err := w.roomUC().BulkDelete(context.Background(), w.owner, []uuid.UUID{a, b, uuid.Must(uuid.NewV7())}); !is(err, domain.ErrItemsNotFound) {
		t.Errorf("%v", err)
	}
}

func TestCreateRunNamesTheRoomsAndRefusesCollisions(t *testing.T) {
	w := newWorld()
	b := buildingWithDefaults(w)
	res, err := w.roomUC().CreateRun(context.Background(), w.owner, b, RunInput{NamePrefix: "P", From: 8, To: 10, Padding: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.IDs) != 3 || !slices.Equal(w.rooms.run.Names, []string{"P008", "P009", "P010"}) {
		t.Fatalf("%+v %+v", res, w.rooms.run)
	}
	if w.rooms.run.PriceVND != 2_500_000 || !w.c.has("follow charges") {
		t.Errorf("a run follows the building: %+v %v", w.rooms.run, w.c.log)
	}

	w.rooms.taken = []string{"p1", "p2", "p3", "p4", "p5", "p6"}
	_, err = w.roomUC().CreateRun(context.Background(), w.owner, b, RunInput{NamePrefix: "P", From: 1, To: 6})
	var taken *domain.RoomNamesTakenError
	if !errors.As(err, &taken) || len(taken.Names) != 5 {
		t.Errorf("at most five names are listed: %v", err)
	}

	if _, err := w.roomUC().CreateRun(context.Background(), w.owner, b, RunInput{NamePrefix: "P", From: 1, To: 500}); err == nil {
		t.Error("too many rooms")
	}
	var many *domain.RangeTooManyError
	_, err = w.roomUC().CreateRun(context.Background(), w.owner, b, RunInput{NamePrefix: "P", From: 1, To: 500})
	if !errors.As(err, &many) || many.Max != 100 {
		t.Errorf("%v", err)
	}
	if _, err := w.roomUC().CreateRun(context.Background(), w.owner, uuid.Must(uuid.NewV7()), RunInput{From: 1, To: 2}); !is(err, domain.ErrNotFound) {
		t.Errorf("a building that is not theirs: %v", err)
	}
}

func TestCreateRunWithOwnChargesStoresThemForEveryRoom(t *testing.T) {
	w := newWorld()
	b := buildingWithDefaults(w)
	_, err := w.roomUC().CreateRun(context.Background(), w.owner, b, RunInput{
		From: 1, To: 2, Charges: []domain.ChargeInput{{Name: "Wifi", Kind: "internet", AmountVND: 1, Unit: "per_period"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !w.c.has("charges to rooms") || w.c.has("follow charges") {
		t.Errorf("%v", w.c.log)
	}
}
