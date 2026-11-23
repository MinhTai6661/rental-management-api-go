package property

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// Copy makes a new building with the details, charges and (with includeRooms) rooms of an existing one. The name
// must be free. Copied rooms keep their name, values and inheritance but are empty (available, or maintenance as
// before): no tenant, contract, listing or manager comes along. Pictures are duplicated (new files, so deleting
// one copy never touches the other): the building's own, and those of the rooms that have their own; a room that
// follows the building's pictures shows the new building's.
func (b *Buildings) Copy(ctx context.Context, landlordID, sourceID uuid.UUID, name string, includeRooms bool) (domain.CopyResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.CopyResult{}, domain.Invalid(domain.Violation{Name: "name", Rule: domain.RuleRequired})
	}
	if err := b.owns(ctx, landlordID, domain.BuildingMedia(sourceID)); err != nil {
		return domain.CopyResult{}, err
	}
	// The files are duplicated before the transaction (like an upload); they are deleted again if it fails.
	copies, err := b.stageMediaCopies(ctx, landlordID, sourceID, includeRooms)
	if err != nil {
		return domain.CopyResult{}, err
	}
	var id uuid.UUID
	copied := 0
	err = b.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := b.buildings.LockLandlord(ctx, landlordID); err != nil {
			return err
		}
		source, err := b.buildings.ForUpdate(ctx, landlordID, sourceID)
		if err != nil {
			return notFound(err)
		}
		if err := b.checkBuildingLimit(ctx, landlordID); err != nil {
			return err
		}
		charges, err := b.charges.OfBuilding(ctx, sourceID)
		if err != nil {
			return err
		}
		templates := make([]domain.ChargeCreate, len(charges))
		for i, c := range charges {
			templates[i] = domain.ChargeCreate{
				Input: domain.ChargeInput{Name: c.Name, Kind: c.Kind, AmountVND: c.AmountVND, Unit: c.Unit, Note: c.Note}, Order: i + 1,
			}
		}
		in := BuildingInput{
			Name: name, WardCode: source.WardCode, AddressDetail: source.AddressDetail, Description: source.Description,
			PriceVND: source.PriceVND, Area: source.Area, MinPeriod: source.MinPeriod, PeriodUnit: source.PeriodUnit,
		}
		if id, err = b.insertBuilding(ctx, landlordID, in, templates); err != nil {
			return err
		}
		// The building's cover must exist before its rooms are copied: the rooms that follow it show that cover.
		if err := b.insertBuildingMediaCopies(ctx, id, copies); err != nil {
			return err
		}
		if includeRooms {
			copied, err = b.copyRooms(ctx, landlordID, sourceID, id, copies)
		}
		return err
	})
	if err != nil {
		b.deleteFiles(ctx, keysOf(copies.all()))
		return domain.CopyResult{}, buildingNameTaken(err)
	}
	b.deleteFiles(ctx, copies.unusedKeys())
	detail, err := b.Get(ctx, landlordID, id)
	return domain.CopyResult{Building: detail, RoomsCopied: copied}, err
}

// owns is a cheap ownership check done before the expensive file work, so a stranger's request costs nothing.
func (c *core) owns(ctx context.Context, landlordID uuid.UUID, o domain.MediaOwner) error {
	ok, err := c.media.Owns(ctx, landlordID, o)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrNotFound
	}
	return nil
}

// copyRooms copies the live rooms of one building into another, inside the caller's transaction; it returns how many.
func (b *Buildings) copyRooms(ctx context.Context, landlordID, from, to uuid.UUID, copies *mediaCopies) (int, error) {
	rooms, err := b.rooms.CopySources(ctx, landlordID, from)
	if err != nil || len(rooms) == 0 {
		return 0, err
	}
	if err := b.checkRoomLimit(ctx, landlordID, len(rooms)); err != nil {
		return 0, err
	}
	pairs := make([]domain.RoomCopyPair, len(rooms))
	var followCharge, followMedia, ownCharge, ownSource []uuid.UUID
	ownMedia := map[uuid.UUID]uuid.UUID{} // source room -> its copy, for the rooms with pictures of their own
	for i, r := range rooms {
		fresh, err := uuid.NewV7()
		if err != nil {
			return 0, err
		}
		pairs[i] = domain.RoomCopyPair{Source: r.ID, Dest: fresh}
		if r.InheritsCharges {
			followCharge = append(followCharge, fresh)
		} else {
			ownCharge, ownSource = append(ownCharge, fresh), append(ownSource, r.ID)
		}
		if r.InheritsMedia {
			followMedia = append(followMedia, fresh)
		} else {
			ownMedia[r.ID] = fresh
		}
	}
	if err := b.rooms.Copy(ctx, landlordID, to, pairs); err != nil {
		return 0, err
	}
	if err := b.follow(ctx, followCharge, to); err != nil {
		return 0, err
	}
	if len(ownCharge) > 0 {
		if err := b.charges.CopyOwn(ctx, ownCharge, ownSource); err != nil {
			return 0, err
		}
	}
	if len(followMedia) > 0 {
		if err := b.media.CoverRoomsFromBuilding(ctx, followMedia); err != nil {
			return 0, err
		}
	}
	if err := b.insertRoomMediaCopies(ctx, copies, ownMedia); err != nil {
		return 0, err
	}
	return len(rooms), nil
}

// mediaCopies are the duplicated files of a building copy: its own pictures, and those of each room that has its own
// pictures (by the id of the source room).
type mediaCopies struct {
	building []domain.MediaCopy
	rooms    map[uuid.UUID][]domain.MediaCopy
	used     map[uuid.UUID]bool
}

func (m *mediaCopies) all() []domain.MediaCopy {
	out := append([]domain.MediaCopy(nil), m.building...)
	for _, list := range m.rooms {
		out = append(out, list...)
	}
	return out
}

func keysOf(copies []domain.MediaCopy) []string {
	keys := make([]string, 0, 2*len(copies))
	for _, c := range copies {
		keys = append(keys, c.File, c.Thumb)
	}
	return keys
}

// unusedKeys are the duplicated files no row ended up using (a room that changed in between).
func (m *mediaCopies) unusedKeys() []string {
	var unused []domain.MediaCopy
	for _, c := range m.all() {
		if !m.used[c.SourceID] {
			unused = append(unused, c)
		}
	}
	return keysOf(unused)
}

// stageMediaCopies duplicates the files of a building's pictures (and, with rooms, of the pictures of the rooms that
// have their own) under new keys, before the transaction. A source file that is gone is skipped, not an error. On a
// failure the files made so far are deleted.
func (c *core) stageMediaCopies(ctx context.Context, landlordID, buildingID uuid.UUID, withRooms bool) (_ *mediaCopies, err error) {
	out := &mediaCopies{rooms: map[uuid.UUID][]domain.MediaCopy{}, used: map[uuid.UUID]bool{}}
	defer func() {
		if err != nil {
			c.deleteFiles(ctx, keysOf(out.all()))
		}
	}()

	own, err := c.media.OfOwner(ctx, domain.BuildingMedia(buildingID))
	if err != nil {
		return nil, err
	}
	if out.building, err = c.duplicateFiles(ctx, own); err != nil {
		return nil, err
	}
	if !withRooms {
		return out, nil
	}
	rooms, err := c.rooms.CopySources(ctx, landlordID, buildingID)
	if err != nil {
		return nil, err
	}
	var ownRooms []uuid.UUID
	for _, r := range rooms {
		if !r.InheritsMedia {
			ownRooms = append(ownRooms, r.ID)
		}
	}
	if len(ownRooms) == 0 {
		return out, nil
	}
	roomMedia, err := c.media.OfRooms(ctx, ownRooms)
	if err != nil {
		return nil, err
	}
	byRoom := map[uuid.UUID][]domain.Media{}
	for _, m := range roomMedia {
		byRoom[*m.RoomID] = append(byRoom[*m.RoomID], m)
	}
	for roomID, list := range byRoom {
		copies, err := c.duplicateFiles(ctx, list)
		out.rooms[roomID] = copies // keep what was made even on error so the deferred cleanup sees it
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// duplicateFiles copies the files of pictures under new keys of the same kind.
func (c *core) duplicateFiles(ctx context.Context, media []domain.Media) ([]domain.MediaCopy, error) {
	var out []domain.MediaCopy
	for _, m := range media {
		ext := m.FileKey[strings.LastIndex(m.FileKey, ".")+1:]
		file, thumb, err := c.newMediaKeys(ext)
		if err != nil {
			return out, err
		}
		if err := c.files.Copy(ctx, m.FileKey, file); err != nil {
			if errors.Is(err, domain.ErrFileNotFound) {
				c.log.Warn("copy building: source picture file missing", "key", m.FileKey)
				continue
			}
			return out, err
		}
		if err := c.files.Copy(ctx, m.ThumbKey, thumb); err != nil {
			c.deleteFiles(ctx, []string{file})
			if errors.Is(err, domain.ErrFileNotFound) {
				c.log.Warn("copy building: source thumbnail file missing", "key", m.ThumbKey)
				continue
			}
			return out, err
		}
		out = append(out, domain.MediaCopy{SourceID: m.ID, File: file, Thumb: thumb})
	}
	return out, nil
}

// insertBuildingMediaCopies makes the rows of the building's duplicated pictures and sets its cover.
func (c *core) insertBuildingMediaCopies(ctx context.Context, buildingID uuid.UUID, copies *mediaCopies) error {
	if len(copies.building) == 0 {
		return nil
	}
	for _, m := range copies.building {
		copies.used[m.SourceID] = true
	}
	if err := c.media.CopyForBuilding(ctx, buildingID, copies.building); err != nil {
		return err
	}
	_, err := c.refreshCover(ctx, domain.BuildingMedia(buildingID))
	return err
}

// insertRoomMediaCopies makes the rows of the rooms' duplicated pictures (destinations by source room id) and sets
// each such room's cover.
func (c *core) insertRoomMediaCopies(ctx context.Context, copies *mediaCopies, destinations map[uuid.UUID]uuid.UUID) error {
	var rows []domain.RoomMediaCopy
	var touched []uuid.UUID
	for source, dest := range destinations {
		list := copies.rooms[source]
		if len(list) == 0 {
			continue
		}
		touched = append(touched, dest)
		for _, m := range list {
			rows = append(rows, domain.RoomMediaCopy{RoomID: dest, MediaCopy: m})
			copies.used[m.SourceID] = true
		}
	}
	if len(touched) == 0 {
		return nil
	}
	if err := c.media.CopyForRooms(ctx, rows); err != nil {
		return err
	}
	for _, id := range touched {
		if _, err := c.refreshCover(ctx, domain.RoomMedia(id)); err != nil {
			return err
		}
	}
	return nil
}
