package property

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// The interfaces below are what the property use cases need from the outside world. The adapters implement them
// structurally (they never import this package), so the rules are tested with fakes and no database or disk.
// Repository lookups return shared.ErrNotFound when there is no such row; writes refused by a constraint come back
// as *shared.ConstraintError.

// TxManager runs fn in one transaction. Repository calls made with the ctx given to fn join it, and so do the
// RoomGuards and RoomObserver ports (the transaction travels in ctx).
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// BuildingRepository stores buildings. Every method is scoped to the landlord that owns them.
type BuildingRepository interface {
	// Search returns a page of the landlord's live buildings and how many match in all.
	Search(ctx context.Context, landlordID uuid.UUID, q domain.BuildingQuery) ([]domain.BuildingSummary, int, error)
	// Get returns one building as a list shows it (shared.ErrNotFound when it is not the landlord's).
	Get(ctx context.Context, landlordID, id uuid.UUID) (domain.BuildingSummary, error)
	// ForUpdate locks a live building of the landlord until the transaction ends.
	ForUpdate(ctx context.Context, landlordID, id uuid.UUID) (domain.Building, error)
	// Insert stores a building at the end of the landlord's list and returns its id.
	Insert(ctx context.Context, landlordID uuid.UUID, d domain.BuildingData) (uuid.UUID, error)
	Update(ctx context.Context, landlordID, id uuid.UUID, d domain.BuildingData) error
	// NameTaken says whether another live building of the landlord has the name, in any case.
	NameTaken(ctx context.Context, landlordID uuid.UUID, name string, except *uuid.UUID) (bool, error)
	CountLive(ctx context.Context, landlordID uuid.UUID) (int, error)
	// CountRoomsFollowingPrice counts the live rooms that take their rent from the building.
	CountRoomsFollowingPrice(ctx context.Context, id uuid.UUID) (int, error)
	// PushToRooms copies what changed in a building into the rooms that follow it (the address into all of
	// them) and returns the rooms it touched.
	PushToRooms(ctx context.Context, id uuid.UUID, after domain.BuildingDefaults, address bool, groups map[domain.Group]bool) ([]uuid.UUID, error)
	// ToDelete returns the buildings among ids that are the landlord's, with their room counts.
	ToDelete(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) ([]domain.BuildingToDelete, error)
	SoftDelete(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) error
	// Order lists the landlord's buildings in their stored order.
	Order(ctx context.Context, landlordID uuid.UUID) ([]domain.OrderedID, error)
	SetOrder(ctx context.Context, landlordID uuid.UUID, moves []domain.Position) error
	// AnyEver says whether the landlord ever had a building, deleted ones included.
	AnyEver(ctx context.Context, landlordID uuid.UUID) (bool, error)
	WardExists(ctx context.Context, code int32) (bool, error)
	// FirstWardCode returns the first seeded ward, or shared.ErrNotFound when there is none.
	FirstWardCode(ctx context.Context) (int32, error)
	// LockLandlord serialises the transactions that count a landlord's things against a plan limit.
	LockLandlord(ctx context.Context, landlordID uuid.UUID) error
}

// RoomRepository stores rooms. Every method is scoped to the landlord that owns them.
type RoomRepository interface {
	// Search returns a page of the landlord's live rooms and how many match in all.
	Search(ctx context.Context, landlordID uuid.UUID, q domain.RoomQuery) ([]domain.RoomSummary, int, error)
	// Get returns one room as a list shows it (shared.ErrNotFound when it is not the landlord's).
	Get(ctx context.Context, landlordID, id uuid.UUID) (domain.RoomSummary, error)
	// ForUpdate locks a live room of the landlord until the transaction ends.
	ForUpdate(ctx context.Context, landlordID, id uuid.UUID) (domain.Room, error)
	// ForUpdateMany locks the live rooms of the landlord among ids (fewer rows when some are not theirs).
	ForUpdateMany(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) ([]domain.RoomRef, error)
	// Insert stores a room at the end of the landlord's list and returns its id.
	Insert(ctx context.Context, landlordID uuid.UUID, d domain.RoomData) (uuid.UUID, error)
	// InsertRun stores a run of rooms at the end of the list and returns their ids in the order of the names.
	InsertRun(ctx context.Context, landlordID uuid.UUID, run domain.RoomRun) ([]uuid.UUID, error)
	Update(ctx context.Context, landlordID, id uuid.UUID, d domain.RoomData) error
	// NameTaken says whether another live room of the building (or outside any building) has the name.
	NameTaken(ctx context.Context, landlordID uuid.UUID, buildingID *uuid.UUID, name string, except *uuid.UUID) (bool, error)
	// NamesTakenInBuilding returns the names, as stored, of the live rooms of the building whose lower case name is
	// among names (given in lower case).
	NamesTakenInBuilding(ctx context.Context, buildingID uuid.UUID, names []string) ([]string, error)
	CountLive(ctx context.Context, landlordID uuid.UUID) (int, error)
	SetStatus(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID, status domain.Status) error
	SoftDelete(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) error
	// Order lists the landlord's rooms in their stored order.
	Order(ctx context.Context, landlordID uuid.UUID) ([]domain.OrderedID, error)
	SetOrder(ctx context.Context, landlordID uuid.UUID, moves []domain.Position) error
	// FollowingCharges lists the live rooms that take their charges from the building.
	FollowingCharges(ctx context.Context, buildingID uuid.UUID) ([]uuid.UUID, error)
	// CopySources lists the live rooms of a building in display order, with what a copy needs to know.
	CopySources(ctx context.Context, landlordID, buildingID uuid.UUID) ([]domain.RoomCopySource, error)
	// Copy makes the copies of rooms inside another building, after the landlord's last room, empty and with
	// their values and inheritance.
	Copy(ctx context.Context, landlordID, toBuilding uuid.UUID, pairs []domain.RoomCopyPair) error
}

// ChargeRepository stores the charges (services and utilities) of buildings and rooms.
type ChargeRepository interface {
	OfBuilding(ctx context.Context, buildingID uuid.UUID) ([]domain.Charge, error)
	OfRoom(ctx context.Context, roomID uuid.UUID) ([]domain.Charge, error)
	AddToBuilding(ctx context.Context, buildingID uuid.UUID, items []domain.ChargeCreate) error
	AddToRoom(ctx context.Context, roomID uuid.UUID, items []domain.ChargeCreate) error
	// AddToRooms gives every room the same own charges, in the order given.
	AddToRooms(ctx context.Context, roomIDs []uuid.UUID, items []domain.ChargeInput) error
	Update(ctx context.Context, id uuid.UUID, in domain.ChargeInput, order int) error
	Delete(ctx context.Context, ids []uuid.UUID) error
	DeleteOfBuildings(ctx context.Context, buildingIDs []uuid.UUID) error
	// Follow makes the rooms carry exactly the live charges of the building (copies of its charges).
	Follow(ctx context.Context, roomIDs []uuid.UUID, buildingID uuid.UUID) error
	// Detach turns the copies a room holds into charges of its own.
	Detach(ctx context.Context, roomIDs []uuid.UUID) error
	// CopyOwn gives the new rooms copies of the own charges of the source rooms (same index, same room).
	CopyOwn(ctx context.Context, newIDs, sourceIDs []uuid.UUID) error
}

// MediaRepository stores the pictures of buildings and rooms and the cover each keeps for its list.
type MediaRepository interface {
	OfOwner(ctx context.Context, o domain.MediaOwner) ([]domain.Media, error)
	// OfRooms lists the pictures of several rooms.
	OfRooms(ctx context.Context, roomIDs []uuid.UUID) ([]domain.Media, error)
	// Owns says whether the room or building is a live one of the landlord.
	Owns(ctx context.Context, landlordID uuid.UUID, o domain.MediaOwner) (bool, error)
	Insert(ctx context.Context, o domain.MediaOwner, m domain.NewMedia) (domain.Media, error)
	// Replace puts a new image in place of a picture (shared.ErrNotFound when it is not the owner's).
	Replace(ctx context.Context, o domain.MediaOwner, id uuid.UUID, f domain.MediaFile) (domain.Media, error)
	// Delete removes a picture and returns its file keys (shared.ErrNotFound when it is not the owner's).
	Delete(ctx context.Context, o domain.MediaOwner, id uuid.UUID) ([]string, error)
	// Renumber closes the gaps of the positions after a delete.
	Renumber(ctx context.Context, o domain.MediaOwner) error
	SetOrder(ctx context.Context, o domain.MediaOwner, places []domain.MediaPlace) error
	// DemoteCovers turns the cover of the owner into a photo.
	DemoteCovers(ctx context.Context, o domain.MediaOwner) error
	// StopFollowing makes a room that followed its building's pictures own its pictures.
	StopFollowing(ctx context.Context, roomID uuid.UUID) error
	// SetCover stores the cover of the owner (nil = none). A building's cover is pushed to the rooms that follow
	// its pictures, which are returned.
	SetCover(ctx context.Context, o domain.MediaOwner, c *domain.Cover) ([]uuid.UUID, error)
	// CoverRoomsFromBuilding gives the rooms the cover of their building.
	CoverRoomsFromBuilding(ctx context.Context, roomIDs []uuid.UUID) error
	// DeleteOfBuildings and DeleteOfRooms remove every picture of them and return the file keys (the file, then
	// its thumbnail, for each picture).
	DeleteOfBuildings(ctx context.Context, buildingIDs []uuid.UUID) ([]string, error)
	DeleteOfRooms(ctx context.Context, roomIDs []uuid.UUID) ([]string, error)
	CopyForBuilding(ctx context.Context, buildingID uuid.UUID, copies []domain.MediaCopy) error
	CopyForRooms(ctx context.Context, copies []domain.RoomMediaCopy) error
}

// FileStore keeps the picture files. A key is written once and never overwritten, so a URL built from it can be
// cached for ever.
type FileStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Open returns the content of key (domain.ErrFileNotFound when there is none, or the key is malformed).
	Open(ctx context.Context, key string) (io.ReadCloser, domain.FileInfo, error)
	// Copy duplicates a file under another key (domain.ErrFileNotFound when the source is missing).
	Copy(ctx context.Context, from, to string) error
	// Delete removes the files; keys that do not exist are not an error.
	Delete(ctx context.Context, keys ...string) error
}

// ImageProcessor verifies an uploaded picture and makes what to store: the picture (WebP or JPEG) and its
// thumbnail. A refusal is one of domain.ErrImageType, ErrImageCorrupt, ErrImageDimensions and ErrImageTooLarge.
type ImageProcessor interface {
	Process(ctx context.Context, data []byte) (domain.ProcessedImage, error)
}

// Ports to what later phases own. Each has a default that changes nothing, so the context works on its own and a
// later one is wired in cmd/rental-management without touching this one.

// PlanLimits says how much a landlord's plan allows; nil means unlimited. The plan context implements it.
type PlanLimits interface {
	MaxBuildings(ctx context.Context, landlordID uuid.UUID) (*int, error)
	MaxRooms(ctx context.Context, landlordID uuid.UUID) (*int, error)
	// MaxMediaPerOwner is the number of pictures one room or building may have.
	MaxMediaPerOwner(ctx context.Context, landlordID uuid.UUID) (*int, error)
}

// UnlimitedPlans is the default: no plan limits anything.
type UnlimitedPlans struct{}

func (UnlimitedPlans) MaxBuildings(context.Context, uuid.UUID) (*int, error)     { return nil, nil }
func (UnlimitedPlans) MaxRooms(context.Context, uuid.UUID) (*int, error)         { return nil, nil }
func (UnlimitedPlans) MaxMediaPerOwner(context.Context, uuid.UUID) (*int, error) { return nil, nil }

// RoomGuards tells what stops rooms from being changed or deleted (a running contract, an open listing). It runs
// in the transaction of ctx, so the answer is read in the same snapshot as the change.
type RoomGuards interface {
	Blockers(ctx context.Context, roomIDs []uuid.UUID) (map[uuid.UUID]domain.Blocker, error)
}

// NoGuards is the default: nothing blocks a room.
type NoGuards struct{}

func (NoGuards) Blockers(context.Context, []uuid.UUID) (map[uuid.UUID]domain.Blocker, error) {
	return nil, nil
}

// RoomObserver is told about changes of rooms, in the transaction that makes them (the one of ctx), so a later
// context can react atomically (open or close a blank contract, send a listing back to draft).
type RoomObserver interface {
	StatusChanged(ctx context.Context, changes []domain.StatusChange) error
	ContentChanged(ctx context.Context, roomIDs []uuid.UUID) error
	// Deleted tells that rooms were deleted (a blank contract of a deleted room ends).
	Deleted(ctx context.Context, roomIDs []uuid.UUID) error
}

// NoObserver is the default: nobody listens.
type NoObserver struct{}

func (NoObserver) StatusChanged(context.Context, []domain.StatusChange) error { return nil }
func (NoObserver) ContentChanged(context.Context, []uuid.UUID) error          { return nil }
func (NoObserver) Deleted(context.Context, []uuid.UUID) error                 { return nil }

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// ClockFunc turns a function (time.Now) into a Clock.
type ClockFunc func() time.Time

// Now calls the function.
func (f ClockFunc) Now() time.Time { return f() }
