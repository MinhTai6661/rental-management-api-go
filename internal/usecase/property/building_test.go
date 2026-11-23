package property

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
	"rental-management/internal/domain/shared"
)

func TestCreateBuildingRefusedPastThePlanLimit(t *testing.T) {
	w := newWorld()
	w.plans = fakePlans{buildings: ptr(2)}
	w.buildings.live = 2
	_, err := w.buildingUC().Create(context.Background(), w.owner, BuildingInput{Name: "A", AddressDetail: "x"})
	var limit *domain.BuildingLimitError
	if !errors.As(err, &limit) || limit.Limit != 2 {
		t.Fatalf("want a limit error of 2, got %v", err)
	}
	if w.c.has("insert building") || !w.c.has("rollback") || !w.c.has("lock") {
		t.Errorf("the limit is checked under the landlord lock and nothing is stored: %v", w.c.log)
	}

	w.buildings.live = 1
	if _, err := w.buildingUC().Create(context.Background(), w.owner, BuildingInput{Name: "A", AddressDetail: "x"}); err != nil {
		t.Fatalf("below the limit: %v", err)
	}
	if !w.c.has("insert building") || !w.c.has("commit") {
		t.Errorf("%v", w.c.log)
	}
}

func TestCreateBuildingNormalizesAndValidates(t *testing.T) {
	w := newWorld()
	uc := w.buildingUC()
	_, err := uc.Create(context.Background(), w.owner, BuildingInput{Name: "  ", AddressDetail: " ", MinPeriod: ptr(int32(2))})
	var invalid *domain.ValidationError
	if !errors.As(err, &invalid) || len(invalid.Violations) != 3 {
		t.Fatalf("name, address and the half rental period are refused: %v", err)
	}
	if len(w.c.log) != 0 {
		t.Errorf("an invalid request opens no transaction: %v", w.c.log)
	}

	area := 18.456
	if _, err := uc.Create(context.Background(), w.owner, BuildingInput{Name: " Nha A ", AddressDetail: " 12 Le Loi ", Area: &area}); err != nil {
		t.Fatal(err)
	}
	got := w.buildings.inserted[0]
	if got.Name != "Nha A" || got.AddressDetail != "12 Le Loi" || *got.Area != 18.46 {
		t.Errorf("text is trimmed and the area rounded: %+v", got)
	}
}

func TestBuildingNameTaken(t *testing.T) {
	w := newWorld()
	w.buildings.nameTaken = true
	_, err := w.buildingUC().Create(context.Background(), w.owner, BuildingInput{Name: "A", AddressDetail: "x"})
	if !is(err, domain.ErrBuildingNameTaken) {
		t.Fatalf("the pre-check: %v", err)
	}

	// A race the pre-check cannot close: the unique index refuses the row.
	w = newWorld()
	w.buildings.insertErr = &shared.ConstraintError{Kind: shared.Unique, Constraint: "buildings_name_key"}
	_, err = w.buildingUC().Create(context.Background(), w.owner, BuildingInput{Name: "A", AddressDetail: "x"})
	if !is(err, domain.ErrBuildingNameTaken) {
		t.Fatalf("the unique index: %v", err)
	}
}

func TestUpdateBuildingPushesChangesToTheRoomsThatFollow(t *testing.T) {
	w := newWorld()
	id := uuid.Must(uuid.NewV7())
	r1, r2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	w.buildings.rows = map[uuid.UUID]domain.Building{id: {ID: id, BuildingData: domain.BuildingData{
		Name: "A", BuildingDefaults: domain.BuildingDefaults{AddressDetail: "x", PriceVND: ptr(int64(100))},
	}}}
	w.buildings.reached = []uuid.UUID{r1, r2, r1}

	_, err := w.buildingUC().Update(context.Background(), w.owner, id, BuildingInput{Name: "A", AddressDetail: "y", PriceVND: ptr(int64(200))})
	if err != nil {
		t.Fatal(err)
	}
	if !w.buildings.pushedAddress || !w.buildings.pushedGroups[domain.GroupPrice] || w.buildings.pushedGroups[domain.GroupArea] {
		t.Errorf("address %v, groups %v", w.buildings.pushedAddress, w.buildings.pushedGroups)
	}
	if len(w.observer.content) != 1 || len(w.observer.content[0]) != 2 {
		t.Errorf("the observer hears each reached room once: %v", w.observer.content)
	}

	// Without a change nobody is told.
	w.observer.content = nil
	w.buildings.reached = nil
	if _, err := w.buildingUC().Update(context.Background(), w.owner, id, BuildingInput{Name: "A", AddressDetail: "y", PriceVND: ptr(int64(200))}); err != nil {
		t.Fatal(err)
	}
	if len(w.observer.content) != 0 {
		t.Errorf("%v", w.observer.content)
	}
}

func TestUpdateBuildingCannotDropARentThatRoomsFollow(t *testing.T) {
	w := newWorld()
	id := uuid.Must(uuid.NewV7())
	w.buildings.rows = map[uuid.UUID]domain.Building{id: {ID: id}}
	w.buildings.following = 3
	_, err := w.buildingUC().Update(context.Background(), w.owner, id, BuildingInput{Name: "A", AddressDetail: "x"})
	var inUse *domain.BuildingPriceInUseError
	if !errors.As(err, &inUse) || inUse.Count != 3 || w.c.has("update building") {
		t.Fatalf("got %v, log %v", err, w.c.log)
	}
	if _, err := w.buildingUC().Update(context.Background(), w.owner, uuid.Must(uuid.NewV7()), BuildingInput{Name: "A", AddressDetail: "x"}); !is(err, domain.ErrNotFound) {
		t.Errorf("a building that is not theirs: %v", err)
	}
}

func TestDeleteBuildingWithRoomsIsRefused(t *testing.T) {
	w := newWorld()
	full, empty := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	w.buildings.toDelete = []domain.BuildingToDelete{{ID: full, Name: "Full", RoomsCount: 3}}
	err := w.buildingUC().Delete(context.Background(), w.owner, full)
	var hasRooms *domain.BuildingHasRoomsError
	if !errors.As(err, &hasRooms) || hasRooms.Count != 3 || w.c.has("soft delete buildings") {
		t.Fatalf("got %v, log %v", err, w.c.log)
	}

	// A list: the empty one goes, the full one is reported.
	w.buildings.toDelete = []domain.BuildingToDelete{{ID: full, Name: "Full", RoomsCount: 3}, {ID: empty, Name: "Empty"}}
	res, err := w.buildingUC().BulkDelete(context.Background(), w.owner, []uuid.UUID{full, empty})
	if err != nil || len(res.Done) != 1 || res.Done[0] != empty || len(res.Failed) != 1 || res.Failed[0].Name != "Full" {
		t.Fatalf("%+v, %v", res, err)
	}

	// Ids that are not all theirs are refused whole.
	if _, err := w.buildingUC().BulkDelete(context.Background(), w.owner, []uuid.UUID{full, empty, uuid.Must(uuid.NewV7())}); !is(err, domain.ErrItemsNotFound) {
		t.Errorf("%v", err)
	}
	w.buildings.toDelete = nil
	if err := w.buildingUC().Delete(context.Background(), w.owner, full); !is(err, domain.ErrNotFound) {
		t.Errorf("one path id that is not theirs: %v", err)
	}
}

func TestCopyBuildingDuplicatesTheFilesAndTheRooms(t *testing.T) {
	w := newWorld()
	src := uuid.Must(uuid.NewV7())
	w.buildings.rows = map[uuid.UUID]domain.Building{src: {ID: src, BuildingData: domain.BuildingData{
		Name: "A", BuildingDefaults: domain.BuildingDefaults{AddressDetail: "x", PriceVND: ptr(int64(5))},
	}}}
	w.files.files["property/2026/11/a.webp"] = []byte("a")
	w.files.files["property/2026/11/a_thumb.jpg"] = []byte("t")
	w.media.items = []domain.Media{{ID: uuid.Must(uuid.NewV7()), FileKey: "property/2026/11/a.webp", ThumbKey: "property/2026/11/a_thumb.jpg", Kind: domain.KindCover}}
	follower, own := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	w.rooms.sources = []domain.RoomCopySource{{ID: follower, InheritsCharges: true, InheritsMedia: true}, {ID: own}}

	res, err := w.buildingUC().Copy(context.Background(), w.owner, src, " Copy ", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.RoomsCopied != 2 || len(w.rooms.copied) != 2 || w.rooms.copied[0].Source != follower || w.rooms.copied[1].Source != own {
		t.Fatalf("copied %d: %+v", res.RoomsCopied, w.rooms.copied)
	}
	if w.buildings.inserted[0].Name != "Copy" || *w.buildings.inserted[0].PriceVND != 5 {
		t.Errorf("the copy keeps the details under the new name: %+v", w.buildings.inserted[0])
	}
	if len(w.files.files) != 4 || len(w.media.building) != 1 {
		t.Errorf("every file is duplicated once: %v", w.files.keys())
	}
	for _, step := range []string{"follow charges", "copy own charges", "cover from building"} {
		if !w.c.has(step) {
			t.Errorf("missing %q in %v", step, w.c.log)
		}
	}
	if w.media.cover == nil {
		t.Error("the new building's cover is set from its copied pictures")
	}
}

func TestCopyBuildingFailureRemovesTheDuplicatedFiles(t *testing.T) {
	w := newWorld()
	src := uuid.Must(uuid.NewV7())
	w.buildings.rows = map[uuid.UUID]domain.Building{src: {ID: src}}
	w.files.files["property/2026/11/a.webp"] = []byte("a")
	w.files.files["property/2026/11/a_thumb.jpg"] = []byte("t")
	w.media.items = []domain.Media{{ID: uuid.Must(uuid.NewV7()), FileKey: "property/2026/11/a.webp", ThumbKey: "property/2026/11/a_thumb.jpg"}}
	w.buildings.nameTaken = true

	if _, err := w.buildingUC().Copy(context.Background(), w.owner, src, "Taken", false); !is(err, domain.ErrBuildingNameTaken) {
		t.Fatalf("%v", err)
	}
	if len(w.files.files) != 2 {
		t.Errorf("the staged copies are deleted again: %v", w.files.keys())
	}
	if _, err := w.buildingUC().Copy(context.Background(), w.owner, src, "  ", false); err == nil {
		t.Error("a blank name is refused")
	}
}

func TestEnsureFirstBuildingOnlyForALandlordWithoutOne(t *testing.T) {
	w := newWorld()
	created, err := w.buildingUC().EnsureFirstBuilding(context.Background(), w.owner, FirstBuilding{Name: "Demo", AddressDetail: "x"})
	if err != nil || !created {
		t.Fatalf("created %v, %v", created, err)
	}
}
