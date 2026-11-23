package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/property"
)

// MediaRepository reads and writes the pictures of buildings and rooms and the cover each keeps.
type MediaRepository struct{ pool *pgxpool.Pool }

// NewMediaRepository returns a repository on the pool.
func NewMediaRepository(pool *pgxpool.Pool) *MediaRepository { return &MediaRepository{pool: pool} }

// OfOwner lists the pictures of a room or building in order.
func (r *MediaRepository) OfOwner(ctx context.Context, o domain.MediaOwner) ([]domain.Media, error) {
	q := queries(ctx, r.pool)
	var rows []sqlcgen.Medium
	var err error
	if o.IsRoom() {
		rows, err = q.ListMediaOfRooms(ctx, []uuid.UUID{o.ID()})
	} else {
		rows, err = q.ListMediaOfBuildings(ctx, []uuid.UUID{o.ID()})
	}
	if err != nil {
		return nil, translate(err)
	}
	return mediasOf(rows), nil
}

// OfRooms lists the pictures of several rooms.
func (r *MediaRepository) OfRooms(ctx context.Context, roomIDs []uuid.UUID) ([]domain.Media, error) {
	rows, err := queries(ctx, r.pool).ListMediaOfRooms(ctx, roomIDs)
	if err != nil {
		return nil, translate(err)
	}
	return mediasOf(rows), nil
}

// Owns says whether the room or building is a live one of the landlord.
func (r *MediaRepository) Owns(ctx context.Context, landlordID uuid.UUID, o domain.MediaOwner) (bool, error) {
	q := queries(ctx, r.pool)
	var ok bool
	var err error
	if o.IsRoom() {
		ok, err = q.OwnsRoom(ctx, sqlcgen.OwnsRoomParams{ID: o.ID(), LandlordID: landlordID})
	} else {
		ok, err = q.OwnsBuilding(ctx, sqlcgen.OwnsBuildingParams{ID: o.ID(), LandlordID: landlordID})
	}
	return ok, translate(err)
}

// Insert stores a picture.
func (r *MediaRepository) Insert(ctx context.Context, o domain.MediaOwner, m domain.NewMedia) (domain.Media, error) {
	roomID, buildingID := ownerColumns(o)
	row, err := queries(ctx, r.pool).InsertMedia(ctx, sqlcgen.InsertMediaParams{
		RoomID: roomID, BuildingID: buildingID, Kind: m.Kind, FileKey: m.FileKey, ThumbKey: m.ThumbKey,
		ContentType: m.ContentType, SizeBytes: int32(m.SizeBytes), //nolint:gosec // at most MEDIA_MAX_UPLOAD_BYTES
		Width: int32(m.Width), Height: int32(m.Height), //nolint:gosec // at most MEDIA_MAX_DIMENSION
		OriginalName: m.OriginalName, DisplayOrder: int16(m.DisplayOrder), //nolint:gosec // at most the picture limit
	})
	if err != nil {
		return domain.Media{}, translate(err)
	}
	return mediaOf(row), nil
}

// Replace puts a new image in place of a picture; shared.ErrNotFound when it is not the owner's.
func (r *MediaRepository) Replace(ctx context.Context, o domain.MediaOwner, id uuid.UUID, f domain.MediaFile) (domain.Media, error) {
	roomID, buildingID := ownerColumns(o)
	row, err := queries(ctx, r.pool).ReplaceMediaFile(ctx, sqlcgen.ReplaceMediaFileParams{
		ID: id, RoomID: roomID, BuildingID: buildingID, FileKey: f.FileKey, ThumbKey: f.ThumbKey,
		ContentType: f.ContentType, SizeBytes: int32(f.SizeBytes), //nolint:gosec // at most MEDIA_MAX_UPLOAD_BYTES
		Width: int32(f.Width), Height: int32(f.Height), //nolint:gosec // at most MEDIA_MAX_DIMENSION
		OriginalName: f.OriginalName,
	})
	if err != nil {
		return domain.Media{}, translate(err)
	}
	return mediaOf(row), nil
}

// Delete removes a picture and returns its file keys; shared.ErrNotFound when it is not the owner's.
func (r *MediaRepository) Delete(ctx context.Context, o domain.MediaOwner, id uuid.UUID) ([]string, error) {
	roomID, buildingID := ownerColumns(o)
	gone, err := queries(ctx, r.pool).DeleteMedia(ctx, sqlcgen.DeleteMediaParams{ID: id, RoomID: roomID, BuildingID: buildingID})
	if err != nil {
		return nil, translate(err)
	}
	return []string{gone.FileKey, gone.ThumbKey}, nil
}

// Renumber closes the gaps of the positions after a delete.
func (r *MediaRepository) Renumber(ctx context.Context, o domain.MediaOwner) error {
	roomID, buildingID := ownerColumns(o)
	return translate(queries(ctx, r.pool).RenumberMedia(ctx, sqlcgen.RenumberMediaParams{RoomID: roomID, BuildingID: buildingID}))
}

// SetOrder stores the kind and position of every picture of the owner.
func (r *MediaRepository) SetOrder(ctx context.Context, o domain.MediaOwner, places []domain.MediaPlace) error {
	roomID, buildingID := ownerColumns(o)
	params := sqlcgen.SetMediaOrderParams{RoomID: roomID, BuildingID: buildingID}
	for _, p := range places {
		params.Ids = append(params.Ids, p.ID)
		params.Kinds = append(params.Kinds, p.Kind)
		params.Positions = append(params.Positions, int16(p.Position)) //nolint:gosec // at most the picture limit
	}
	return translate(queries(ctx, r.pool).SetMediaOrder(ctx, params))
}

// DemoteCovers turns the cover of the owner into a photo.
func (r *MediaRepository) DemoteCovers(ctx context.Context, o domain.MediaOwner) error {
	roomID, buildingID := ownerColumns(o)
	return translate(queries(ctx, r.pool).DemoteMediaCovers(ctx, sqlcgen.DemoteMediaCoversParams{RoomID: roomID, BuildingID: buildingID}))
}

// StopFollowing makes a room that followed its building's pictures own its pictures.
func (r *MediaRepository) StopFollowing(ctx context.Context, roomID uuid.UUID) error {
	return translate(queries(ctx, r.pool).StopFollowingBuildingMedia(ctx, roomID))
}

// SetCover stores the cover of the owner (nil = none). A building's cover is pushed to the rooms that follow its
// pictures, which are returned.
func (r *MediaRepository) SetCover(ctx context.Context, o domain.MediaOwner, c *domain.Cover) ([]uuid.UUID, error) {
	var key, thumb *string
	if c != nil {
		key, thumb = &c.Key, &c.ThumbKey
	}
	q := queries(ctx, r.pool)
	if o.IsRoom() {
		return nil, translate(q.SetRoomCover(ctx, sqlcgen.SetRoomCoverParams{ID: o.ID(), CoverKey: key, CoverThumbKey: thumb}))
	}
	if err := q.SetBuildingCover(ctx, sqlcgen.SetBuildingCoverParams{ID: o.ID(), CoverKey: key, CoverThumbKey: thumb}); err != nil {
		return nil, translate(err)
	}
	followers, err := q.PushBuildingCover(ctx, o.ID())
	return followers, translate(err)
}

// CoverRoomsFromBuilding gives the rooms the cover of their building.
func (r *MediaRepository) CoverRoomsFromBuilding(ctx context.Context, roomIDs []uuid.UUID) error {
	return translate(queries(ctx, r.pool).SetRoomCoversFromBuilding(ctx, roomIDs))
}

// DeleteOfBuildings removes every picture of the buildings and returns the file keys.
func (r *MediaRepository) DeleteOfBuildings(ctx context.Context, buildingIDs []uuid.UUID) ([]string, error) {
	files, err := queries(ctx, r.pool).DeleteMediaOfBuildings(ctx, buildingIDs)
	var keys []string
	for _, f := range files {
		keys = append(keys, f.FileKey, f.ThumbKey)
	}
	return keys, translate(err)
}

// DeleteOfRooms removes every picture of the rooms and returns the file keys.
func (r *MediaRepository) DeleteOfRooms(ctx context.Context, roomIDs []uuid.UUID) ([]string, error) {
	files, err := queries(ctx, r.pool).DeleteMediaOfRooms(ctx, roomIDs)
	var keys []string
	for _, f := range files {
		keys = append(keys, f.FileKey, f.ThumbKey)
	}
	return keys, translate(err)
}

// CopyForBuilding records the duplicated pictures of a building.
func (r *MediaRepository) CopyForBuilding(ctx context.Context, buildingID uuid.UUID, copies []domain.MediaCopy) error {
	params := sqlcgen.CopyBuildingMediaParams{BuildingID: buildingID}
	for _, c := range copies {
		params.SourceIds = append(params.SourceIds, c.SourceID)
		params.FileKeys = append(params.FileKeys, c.File)
		params.ThumbKeys = append(params.ThumbKeys, c.Thumb)
	}
	return translate(queries(ctx, r.pool).CopyBuildingMedia(ctx, params))
}

// CopyForRooms records the duplicated pictures of rooms.
func (r *MediaRepository) CopyForRooms(ctx context.Context, copies []domain.RoomMediaCopy) error {
	params := sqlcgen.CopyRoomMediaParams{}
	for _, c := range copies {
		params.RoomIds = append(params.RoomIds, c.RoomID)
		params.SourceIds = append(params.SourceIds, c.SourceID)
		params.FileKeys = append(params.FileKeys, c.File)
		params.ThumbKeys = append(params.ThumbKeys, c.Thumb)
	}
	return translate(queries(ctx, r.pool).CopyRoomMedia(ctx, params))
}
