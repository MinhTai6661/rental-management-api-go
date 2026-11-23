package property

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
	"rental-management/internal/domain/shared"
)

// The property use cases run here against hand-written fakes of the ports: no database, no disk. Each fake embeds
// the interface it stands for, so a method a test does not need panics if it is called by mistake. The call log of
// the fakes tells the order of the calls.

type calls struct{ log []string }

func (c *calls) add(s string) { c.log = append(c.log, s) }

func (c *calls) has(s string) bool {
	for _, l := range c.log {
		if l == s {
			return true
		}
	}
	return false
}

type fakeTx struct{ c *calls }

func (f fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	f.c.add("begin")
	if err := fn(ctx); err != nil {
		f.c.add("rollback")
		return err
	}
	f.c.add("commit")
	return nil
}

type fakeBuildings struct {
	BuildingRepository
	c             *calls
	live          int
	nameTaken     bool
	everHad       bool
	wardGone      bool
	insertErr     error
	rows          map[uuid.UUID]domain.Building
	following     int
	toDelete      []domain.BuildingToDelete
	reached       []uuid.UUID
	inserted      []domain.BuildingData
	updated       []domain.BuildingData
	pushedAddress bool
	pushedGroups  map[domain.Group]bool
	softDeleted   []uuid.UUID
}

func (f *fakeBuildings) LockLandlord(context.Context, uuid.UUID) error     { f.c.add("lock"); return nil }
func (f *fakeBuildings) AnyEver(context.Context, uuid.UUID) (bool, error)  { return f.everHad, nil }
func (f *fakeBuildings) CountLive(context.Context, uuid.UUID) (int, error) { return f.live, nil }
func (f *fakeBuildings) WardExists(context.Context, int32) (bool, error)   { return !f.wardGone, nil }
func (f *fakeBuildings) NameTaken(context.Context, uuid.UUID, string, *uuid.UUID) (bool, error) {
	return f.nameTaken, nil
}

func (f *fakeBuildings) Insert(_ context.Context, _ uuid.UUID, d domain.BuildingData) (uuid.UUID, error) {
	if f.insertErr != nil {
		return uuid.Nil, f.insertErr
	}
	f.c.add("insert building")
	f.inserted = append(f.inserted, d)
	id := uuid.Must(uuid.NewV7())
	if f.rows == nil {
		f.rows = map[uuid.UUID]domain.Building{}
	}
	f.rows[id] = domain.Building{ID: id, BuildingData: d}
	return id, nil
}

func (f *fakeBuildings) Get(_ context.Context, _, id uuid.UUID) (domain.BuildingSummary, error) {
	b, ok := f.rows[id]
	if !ok {
		return domain.BuildingSummary{}, shared.ErrNotFound
	}
	return domain.BuildingSummary{ID: id, Name: b.Name, AddressDetail: b.AddressDetail, PriceVND: b.PriceVND}, nil
}

func (f *fakeBuildings) ForUpdate(_ context.Context, _, id uuid.UUID) (domain.Building, error) {
	b, ok := f.rows[id]
	if !ok {
		return domain.Building{}, shared.ErrNotFound
	}
	return b, nil
}

func (f *fakeBuildings) Update(_ context.Context, _, _ uuid.UUID, d domain.BuildingData) error {
	f.c.add("update building")
	f.updated = append(f.updated, d)
	return nil
}

func (f *fakeBuildings) CountRoomsFollowingPrice(context.Context, uuid.UUID) (int, error) {
	return f.following, nil
}

func (f *fakeBuildings) PushToRooms(_ context.Context, _ uuid.UUID, _ domain.BuildingDefaults, address bool, groups map[domain.Group]bool) ([]uuid.UUID, error) {
	f.c.add("push")
	f.pushedAddress, f.pushedGroups = address, groups
	return f.reached, nil
}

func (f *fakeBuildings) ToDelete(context.Context, uuid.UUID, []uuid.UUID) ([]domain.BuildingToDelete, error) {
	return f.toDelete, nil
}

func (f *fakeBuildings) SoftDelete(_ context.Context, _ uuid.UUID, ids []uuid.UUID) error {
	f.c.add("soft delete buildings")
	f.softDeleted = ids
	return nil
}

type fakeRooms struct {
	RoomRepository
	c         *calls
	live      int
	nameTaken bool
	taken     []string
	refs      []domain.RoomRef
	rooms     map[uuid.UUID]domain.Room
	inserted  []domain.RoomData
	run       *domain.RoomRun
	statusSet []uuid.UUID
	sources   []domain.RoomCopySource
	copied    []domain.RoomCopyPair
	deleted   []uuid.UUID
	followers []uuid.UUID
}

func (f *fakeRooms) CountLive(context.Context, uuid.UUID) (int, error) { return f.live, nil }
func (f *fakeRooms) NameTaken(context.Context, uuid.UUID, *uuid.UUID, string, *uuid.UUID) (bool, error) {
	return f.nameTaken, nil
}

func (f *fakeRooms) NamesTakenInBuilding(context.Context, uuid.UUID, []string) ([]string, error) {
	return f.taken, nil
}

func (f *fakeRooms) Insert(_ context.Context, _ uuid.UUID, d domain.RoomData) (uuid.UUID, error) {
	f.c.add("insert room")
	f.inserted = append(f.inserted, d)
	id := uuid.Must(uuid.NewV7())
	if f.rooms == nil {
		f.rooms = map[uuid.UUID]domain.Room{}
	}
	f.rooms[id] = domain.Room{ID: id, RoomData: d}
	return id, nil
}

func (f *fakeRooms) InsertRun(_ context.Context, _ uuid.UUID, run domain.RoomRun) ([]uuid.UUID, error) {
	f.c.add("insert run")
	f.run = &run
	ids := make([]uuid.UUID, len(run.Names))
	for i := range ids {
		ids[i] = uuid.Must(uuid.NewV7())
	}
	return ids, nil
}

func (f *fakeRooms) Get(_ context.Context, _, id uuid.UUID) (domain.RoomSummary, error) {
	r, ok := f.rooms[id]
	if !ok {
		return domain.RoomSummary{}, shared.ErrNotFound
	}
	s := domain.RoomSummary{ID: id, Name: r.Name, Status: r.Status, PriceVND: r.PriceVND, Inherits: r.Inherits}
	if r.BuildingID != nil {
		s.Building = &domain.BuildingRef{ID: *r.BuildingID}
	}
	return s, nil
}

func (f *fakeRooms) ForUpdate(_ context.Context, _, id uuid.UUID) (domain.Room, error) {
	r, ok := f.rooms[id]
	if !ok {
		return domain.Room{}, shared.ErrNotFound
	}
	return r, nil
}

func (f *fakeRooms) ForUpdateMany(context.Context, uuid.UUID, []uuid.UUID) ([]domain.RoomRef, error) {
	return f.refs, nil
}

func (f *fakeRooms) SetStatus(_ context.Context, _ uuid.UUID, ids []uuid.UUID, _ domain.Status) error {
	f.c.add("set status")
	f.statusSet = ids
	return nil
}

func (f *fakeRooms) SoftDelete(_ context.Context, _ uuid.UUID, ids []uuid.UUID) error {
	f.deleted = ids
	return nil
}

func (f *fakeRooms) FollowingCharges(context.Context, uuid.UUID) ([]uuid.UUID, error) {
	return f.followers, nil
}

func (f *fakeRooms) CopySources(context.Context, uuid.UUID, uuid.UUID) ([]domain.RoomCopySource, error) {
	return f.sources, nil
}

func (f *fakeRooms) Copy(_ context.Context, _, _ uuid.UUID, pairs []domain.RoomCopyPair) error {
	f.c.add("copy rooms")
	f.copied = pairs
	return nil
}

type fakeCharges struct {
	ChargeRepository
	c *calls
}

func (f *fakeCharges) OfBuilding(context.Context, uuid.UUID) ([]domain.Charge, error) {
	return nil, nil
}
func (f *fakeCharges) OfRoom(context.Context, uuid.UUID) ([]domain.Charge, error) { return nil, nil }
func (f *fakeCharges) AddToBuilding(context.Context, uuid.UUID, []domain.ChargeCreate) error {
	f.c.add("charges to building")
	return nil
}

func (f *fakeCharges) AddToRoom(context.Context, uuid.UUID, []domain.ChargeCreate) error {
	f.c.add("charges to room")
	return nil
}

func (f *fakeCharges) AddToRooms(context.Context, []uuid.UUID, []domain.ChargeInput) error {
	f.c.add("charges to rooms")
	return nil
}

func (f *fakeCharges) DeleteOfBuildings(context.Context, []uuid.UUID) error { return nil }

func (f *fakeCharges) Follow(context.Context, []uuid.UUID, uuid.UUID) error {
	f.c.add("follow charges")
	return nil
}

func (f *fakeCharges) CopyOwn(context.Context, []uuid.UUID, []uuid.UUID) error {
	f.c.add("copy own charges")
	return nil
}

type fakeMedia struct {
	MediaRepository
	c        *calls
	items    []domain.Media
	inserted []domain.NewMedia
	places   []domain.MediaPlace
	cover    *domain.Cover
	building []domain.MediaCopy
	rooms    []domain.RoomMediaCopy
}

func (f *fakeMedia) OfOwner(context.Context, domain.MediaOwner) ([]domain.Media, error) {
	return f.items, nil
}

func (f *fakeMedia) OfRooms(context.Context, []uuid.UUID) ([]domain.Media, error) { return nil, nil }
func (f *fakeMedia) Owns(context.Context, uuid.UUID, domain.MediaOwner) (bool, error) {
	return true, nil
}

func (f *fakeMedia) Insert(_ context.Context, _ domain.MediaOwner, m domain.NewMedia) (domain.Media, error) {
	f.c.add("insert media")
	f.inserted = append(f.inserted, m)
	return domain.Media{ID: uuid.Must(uuid.NewV7()), Kind: m.Kind, FileKey: m.FileKey, ThumbKey: m.ThumbKey, DisplayOrder: m.DisplayOrder}, nil
}

func (f *fakeMedia) DemoteCovers(context.Context, domain.MediaOwner) error {
	f.c.add("demote covers")
	return nil
}

func (f *fakeMedia) SetOrder(_ context.Context, _ domain.MediaOwner, p []domain.MediaPlace) error {
	f.c.add("set order")
	f.places = p
	return nil
}

func (f *fakeMedia) SetCover(_ context.Context, _ domain.MediaOwner, c *domain.Cover) ([]uuid.UUID, error) {
	f.cover = c
	return nil, nil
}

func (f *fakeMedia) DeleteOfBuildings(context.Context, []uuid.UUID) ([]string, error) {
	return nil, nil
}

func (f *fakeMedia) CoverRoomsFromBuilding(context.Context, []uuid.UUID) error {
	f.c.add("cover from building")
	return nil
}

func (f *fakeMedia) CopyForBuilding(_ context.Context, _ uuid.UUID, copies []domain.MediaCopy) error {
	f.c.add("copy building media")
	f.building = copies
	return nil
}

func (f *fakeMedia) CopyForRooms(_ context.Context, copies []domain.RoomMediaCopy) error {
	f.c.add("copy room media")
	f.rooms = copies
	return nil
}

// fakeFiles is an in-memory file store.
type fakeFiles struct {
	files   map[string][]byte
	deleted []string
	putErr  error
}

func newFakeFiles() *fakeFiles { return &fakeFiles{files: map[string][]byte{}} }

func (f *fakeFiles) Put(_ context.Context, key string, data []byte, _ string) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.files[key] = data
	return nil
}

func (f *fakeFiles) Open(_ context.Context, key string) (io.ReadCloser, domain.FileInfo, error) {
	data, ok := f.files[key]
	if !ok {
		return nil, domain.FileInfo{}, domain.ErrFileNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), domain.FileInfo{Size: int64(len(data)), ContentType: "image/webp"}, nil
}

func (f *fakeFiles) Copy(_ context.Context, from, to string) error {
	data, ok := f.files[from]
	if !ok {
		return domain.ErrFileNotFound
	}
	f.files[to] = data
	return nil
}

func (f *fakeFiles) Delete(_ context.Context, keys ...string) error {
	for _, k := range keys {
		delete(f.files, k)
		f.deleted = append(f.deleted, k)
	}
	return nil
}

func (f *fakeFiles) keys() []string {
	out := make([]string, 0, len(f.files))
	for k := range f.files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// fakeImages accepts anything except the bytes "bad".
type fakeImages struct{ err error }

func (f fakeImages) Process(_ context.Context, data []byte) (domain.ProcessedImage, error) {
	if f.err != nil {
		return domain.ProcessedImage{}, f.err
	}
	if string(data) == "bad" {
		return domain.ProcessedImage{}, domain.ErrImageCorrupt
	}
	return domain.ProcessedImage{Data: data, ContentType: "image/webp", Ext: "webp", Width: 10, Height: 8, Thumb: data}, nil
}

type fakePlans struct {
	UnlimitedPlans
	buildings, rooms, media *int
	err                     error
}

func (f fakePlans) MaxBuildings(context.Context, uuid.UUID) (*int, error) { return f.buildings, f.err }
func (f fakePlans) MaxRooms(context.Context, uuid.UUID) (*int, error)     { return f.rooms, f.err }
func (f fakePlans) MaxMediaPerOwner(context.Context, uuid.UUID) (*int, error) {
	return f.media, f.err
}

type fakeGuards map[uuid.UUID]domain.Blocker

func (f fakeGuards) Blockers(context.Context, []uuid.UUID) (map[uuid.UUID]domain.Blocker, error) {
	return f, nil
}

type recordingObserver struct {
	statuses []domain.StatusChange
	content  [][]uuid.UUID
	deleted  [][]uuid.UUID
}

func (r *recordingObserver) Deleted(_ context.Context, ids []uuid.UUID) error {
	r.deleted = append(r.deleted, ids)
	return nil
}

func (r *recordingObserver) StatusChanged(_ context.Context, c []domain.StatusChange) error {
	r.statuses = append(r.statuses, c...)
	return nil
}

func (r *recordingObserver) ContentChanged(_ context.Context, ids []uuid.UUID) error {
	r.content = append(r.content, ids)
	return nil
}

// world is one test's set of fakes and the deps built from them.
type world struct {
	c         *calls
	buildings *fakeBuildings
	rooms     *fakeRooms
	charges   *fakeCharges
	media     *fakeMedia
	files     *fakeFiles
	images    fakeImages
	plans     PlanLimits
	guards    RoomGuards
	observer  *recordingObserver
	owner     uuid.UUID
}

func newWorld() *world {
	c := &calls{}
	return &world{
		c: c, buildings: &fakeBuildings{c: c}, rooms: &fakeRooms{c: c}, charges: &fakeCharges{c: c},
		media: &fakeMedia{c: c}, files: newFakeFiles(), observer: &recordingObserver{}, owner: uuid.Must(uuid.NewV7()),
	}
}

func (w *world) deps() Deps {
	return Deps{
		Tx: fakeTx{w.c}, Buildings: w.buildings, Rooms: w.rooms, Charges: w.charges, Media: w.media, Files: w.files,
		Images: w.images, Plans: w.plans, Guards: w.guards, Observer: w.observer,
	}
}

func (w *world) buildingUC() *Buildings   { return NewBuildings(w.deps(), Options{}) }
func (w *world) roomUC() *Rooms           { return NewRooms(w.deps(), Options{}) }
func (w *world) mediaUC(o Options) *Media { return NewMedia(w.deps(), o) }

func ptr[T any](v T) *T { return &v }

// is reports whether err is (or wraps) target.
func is(err, target error) bool { return errors.Is(err, target) }
