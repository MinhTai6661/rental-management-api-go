package property

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// roomOwner puts a room that has pictures of its own in the fake store.
func roomOwner(w *world) domain.MediaOwner {
	id := uuid.Must(uuid.NewV7())
	w.rooms.rooms = map[uuid.UUID]domain.Room{id: {ID: id}}
	return domain.RoomMedia(id)
}

func TestMediaAddKeepsTheOrderAndDemotesTheOldCover(t *testing.T) {
	w := newWorld()
	w.media.items = []domain.Media{{ID: uuid.Must(uuid.NewV7()), Kind: domain.KindPhoto, DisplayOrder: 4}}
	o := roomOwner(w)
	m, err := w.mediaUC(Options{MaxMediaPerOwner: 10}).Add(context.Background(), w.owner, o, Upload{FileName: "a.png", Data: []byte("img"), Kind: domain.KindCover})
	if err != nil {
		t.Fatal(err)
	}
	if m.DisplayOrder != 5 || m.Kind != domain.KindCover || !w.c.has("demote covers") {
		t.Errorf("%+v %v", m, w.c.log)
	}
	if len(w.files.files) != 2 || len(w.observer.content) != 1 {
		t.Errorf("the picture and its thumbnail are stored and the room's listing is told: %v %v", w.files.keys(), w.observer.content)
	}
	if w.media.inserted[0].OriginalName == nil || *w.media.inserted[0].OriginalName != "a.png" {
		t.Errorf("%+v", w.media.inserted[0])
	}
	if _, err := w.mediaUC(Options{}).Add(context.Background(), w.owner, o, Upload{Data: []byte("x"), Kind: "banner"}); !is(err, domain.ErrPictureKindInvalid) {
		t.Errorf("%v", err)
	}
}

func TestMediaLimitReachedDeletesTheStagedFiles(t *testing.T) {
	w := newWorld()
	w.media.items = []domain.Media{{ID: uuid.Must(uuid.NewV7())}, {ID: uuid.Must(uuid.NewV7())}}
	o := roomOwner(w)
	_, err := w.mediaUC(Options{MaxMediaPerOwner: 2}).Add(context.Background(), w.owner, o, Upload{Data: []byte("img")})
	var limit *domain.MediaLimitError
	if !errors.As(err, &limit) || limit.Max != 2 {
		t.Fatalf("%v", err)
	}
	if len(w.files.files) != 0 || len(w.files.deleted) != 2 {
		t.Errorf("a refused upload leaves no file behind: %v", w.files.keys())
	}

	// The plan's limit wins over the configured default.
	w.plans = fakePlans{media: ptr(3)}
	if _, err := w.mediaUC(Options{MaxMediaPerOwner: 2}).Add(context.Background(), w.owner, o, Upload{Data: []byte("img")}); err != nil {
		t.Errorf("room for a third: %v", err)
	}
}

func TestMediaAddRefusesWhatTheImageChecksRefuse(t *testing.T) {
	w := newWorld()
	o := roomOwner(w)
	w.images = fakeImages{err: domain.ErrImageDimensions}
	_, err := w.mediaUC(Options{MaxDimension: 4096, MaxUploadBytes: 2 << 20}).Add(context.Background(), w.owner, o, Upload{Data: []byte("x")})
	var dim *domain.ImageDimensionsError
	if !errors.As(err, &dim) || dim.Max != 4096 {
		t.Errorf("the limit is added to the refusal: %v", err)
	}
	w.images = fakeImages{err: domain.ErrImageTooLarge}
	_, err = w.mediaUC(Options{MaxUploadBytes: 2 << 20}).Add(context.Background(), w.owner, o, Upload{Data: []byte("x")})
	var big *domain.FileTooLargeError
	if !errors.As(err, &big) || big.MaxKB != 2048 {
		t.Errorf("%v", err)
	}
	if len(w.files.files) != 0 {
		t.Errorf("nothing is stored for a refused picture: %v", w.files.keys())
	}
}

func TestMediaArrangeChecksTheListAndSetsTheOrder(t *testing.T) {
	w := newWorld()
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	w.media.items = []domain.Media{
		{ID: a, Kind: domain.KindPhoto, DisplayOrder: 1, FileKey: "property/a.webp", ThumbKey: "property/a_thumb.jpg"},
		{ID: b, Kind: domain.KindPhoto, DisplayOrder: 2, FileKey: "property/b.webp", ThumbKey: "property/b_thumb.jpg"},
	}
	o := roomOwner(w)
	uc := w.mediaUC(Options{})
	ctx := context.Background()

	if _, err := uc.Arrange(ctx, w.owner, o, []domain.ArrangeItem{{ID: a, Kind: domain.KindPhoto}}); !is(err, domain.ErrArrangementMismatch) {
		t.Errorf("%v", err)
	}
	if _, err := uc.Arrange(ctx, w.owner, o, []domain.ArrangeItem{{ID: a, Kind: domain.KindCover}, {ID: b, Kind: domain.KindCover}}); !is(err, domain.ErrArrangementManyCovers) {
		t.Errorf("%v", err)
	}
	if _, err := uc.Arrange(ctx, w.owner, o, []domain.ArrangeItem{{ID: b, Kind: domain.KindCover}, {ID: a, Kind: domain.KindPhoto}}); err != nil {
		t.Fatal(err)
	}
	if len(w.media.places) != 2 || w.media.places[0] != (domain.MediaPlace{ID: b, Kind: domain.KindCover, Position: 1}) ||
		w.media.places[1].Position != 2 {
		t.Errorf("positions follow the list: %+v", w.media.places)
	}
	if !w.c.has("demote covers") || w.media.cover == nil {
		t.Errorf("the old cover is demoted before the new is set, then the cover is refreshed: %v", w.c.log)
	}
}

func TestMediaOpenServesOnlyWhatTheContextStored(t *testing.T) {
	w := newWorld()
	w.files.files["property/2026/11/a.webp"] = []byte("img")
	w.files.files["other/2026/11/a.webp"] = []byte("img")
	uc := w.mediaUC(Options{})
	rc, info, err := uc.Open(context.Background(), "property/2026/11/a.webp")
	if err != nil || info.Size != 3 {
		t.Fatalf("%+v %v", info, err)
	}
	_ = rc.Close()
	for _, key := range []string{"other/2026/11/a.webp", "property/2026/11/missing.webp"} {
		if _, _, err := uc.Open(context.Background(), key); !is(err, domain.ErrNotFound) {
			t.Errorf("%s: %v", key, err)
		}
	}
}
