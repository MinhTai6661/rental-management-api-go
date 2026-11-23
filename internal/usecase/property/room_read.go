package property

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
	"rental-management/internal/domain/shared"
)

// Rooms is the use cases of a landlord's rooms.
type Rooms struct{ *core }

// NewRooms builds the room use cases.
func NewRooms(d Deps, o Options) *Rooms { return &Rooms{newCore(d, o)} }

// List returns a page of the landlord's rooms and the total.
func (r *Rooms) List(ctx context.Context, landlordID uuid.UUID, f RoomFilter, limit, offset int) ([]domain.RoomSummary, int, error) {
	var nameLike *string
	if name := strings.ToLower(strings.TrimSpace(f.Name)); name != "" {
		like := domain.EscapeLike(name)
		nameLike = &like
	}
	return r.rooms.Search(ctx, landlordID, domain.RoomQuery{
		NameLike: nameLike, Status: f.Status, BuildingID: f.BuildingID, NoBuilding: f.NoBuilding, WardCode: f.WardCode,
		ProvinceCode: f.ProvinceCode, PriceFrom: f.PriceFrom, PriceTo: f.PriceTo, AreaFrom: f.AreaFrom, AreaTo: f.AreaTo,
		Sort: f.Sort, Limit: limit, Offset: offset,
	})
}

// Get returns one room with its charges and pictures; domain.ErrNotFound when it is not the landlord's.
func (r *Rooms) Get(ctx context.Context, landlordID, id uuid.UUID) (domain.RoomDetail, error) {
	summary, err := r.rooms.Get(ctx, landlordID, id)
	if err != nil {
		return domain.RoomDetail{}, notFound(err)
	}
	charges, err := r.charges.OfRoom(ctx, id)
	if err != nil {
		return domain.RoomDetail{}, err
	}
	// A room that follows its building's pictures shows the building's.
	owner := domain.RoomMedia(id)
	if summary.Follows(domain.GroupMedia) && summary.Building != nil {
		owner = domain.BuildingMedia(summary.Building.ID)
	}
	media, err := r.media.OfOwner(ctx, owner)
	if err != nil {
		return domain.RoomDetail{}, err
	}
	return domain.RoomDetail{RoomSummary: summary, Charges: charges, Media: media}, nil
}

// loadBuilding locks a live building of the landlord and returns what it offers its rooms; a building that is not
// theirs is domain.ErrBuildingNotFound.
func (c *core) loadBuilding(ctx context.Context, landlordID, id uuid.UUID) (domain.BuildingDefaults, error) {
	b, err := c.buildings.ForUpdate(ctx, landlordID, id)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return domain.BuildingDefaults{}, domain.ErrBuildingNotFound
		}
		return domain.BuildingDefaults{}, err
	}
	return b.BuildingDefaults, nil
}

// checkRoomName refuses a name another live room in the same building (or outside any building) already uses.
func (c *core) checkRoomName(ctx context.Context, landlordID uuid.UUID, buildingID *uuid.UUID, name string, except *uuid.UUID) error {
	taken, err := c.rooms.NameTaken(ctx, landlordID, buildingID, name, except)
	if err != nil {
		return err
	}
	if taken {
		return domain.ErrRoomNameTaken
	}
	return nil
}

// roomNameTaken turns the unique index refusing a duplicate name (a race the pre-check cannot close) into the same
// error as the pre-check.
func roomNameTaken(err error) error {
	if shared.IsUnique(err) {
		return domain.ErrRoomNameTaken
	}
	return err
}
