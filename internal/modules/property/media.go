package property

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	"rental-management/pkg/apperr"
	"rental-management/pkg/imaging"
	"rental-management/pkg/storage"
)

// mediaPrefix is the start of every key this module stores; the public route serves nothing else.
const mediaPrefix = "property/"

// MediaOwner is what a picture belongs to: a room or a building. Both work the same way.
type MediaOwner struct {
	room bool
	id   uuid.UUID
}

// RoomMedia is the pictures of a room.
func RoomMedia(id uuid.UUID) MediaOwner { return MediaOwner{room: true, id: id} }

// BuildingMedia is the pictures of a building.
func BuildingMedia(id uuid.UUID) MediaOwner { return MediaOwner{id: id} }

// ids gives the two owner columns of the media queries: the one that matches is set, the other is nil.
func (o MediaOwner) ids() (room, building *uuid.UUID) {
	if o.room {
		return &o.id, nil
	}
	return nil, &o.id
}

// Upload is a picture a client sent.
type Upload struct {
	FileName string
	Data     []byte
	// Kind is "photo" (the default when empty) or "cover".
	Kind string
}

// ArrangeItem is one picture of a new arrangement: the order of the list is the order of the pictures.
type ArrangeItem struct {
	ID   uuid.UUID
	Kind string
}

// MaxUploadBytes is the size limit of one picture upload.
func (s *Service) MaxUploadBytes() int { return s.opts.MaxUploadBytes }

// ---- keys and staging ----

// mediaKeys builds the storage keys of a picture and its thumbnail from the date and 16 random bytes. The name is
// unguessable and never reused, so the files can be cached for ever.
func mediaKeys(now time.Time, random [16]byte, ext string) (file, thumb string) {
	base := fmt.Sprintf("%s%04d/%02d/%s", mediaPrefix, now.Year(), int(now.Month()), hex.EncodeToString(random[:]))
	return base + "." + ext, base + "_thumb.jpg"
}

func (s *Service) newMediaKeys(ext string) (file, thumb string, err error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", "", err
	}
	file, thumb = mediaKeys(s.now().UTC(), random, ext)
	return file, thumb, nil
}

// staged is a processed picture whose files are already in storage but not yet in a row.
type staged struct {
	result      imaging.Result
	file, thumb string
}

func (st staged) keys() []string { return []string{st.file, st.thumb} }

// stage verifies the bytes of an upload and writes the picture and its thumbnail to storage. The files exist
// before the transaction that records them, so the caller deletes them again when that transaction fails.
func (s *Service) stage(ctx context.Context, data []byte) (staged, error) {
	res, err := s.images.Process(ctx, data)
	if err != nil {
		return staged{}, s.imageError(err)
	}
	file, thumb, err := s.newMediaKeys(res.Ext)
	if err != nil {
		return staged{}, err
	}
	if err := s.storage.Put(ctx, file, res.Data, res.ContentType); err != nil {
		return staged{}, err
	}
	if err := s.storage.Put(ctx, thumb, res.Thumb, "image/jpeg"); err != nil {
		s.deleteFiles(ctx, []string{file})
		return staged{}, err
	}
	return staged{result: res, file: file, thumb: thumb}, nil
}

// imageError turns what the image checks refuse into the 422 on the file field.
func (s *Service) imageError(err error) error {
	field := func(rule string, params map[string]string) error {
		return apperr.Invalid(apperr.Field{Name: "file", Rule: rule, Params: params})
	}
	switch {
	case errors.Is(err, imaging.ErrUnsupported), errors.Is(err, imaging.ErrAnimated):
		return field(apperr.RuleImageType, nil)
	case errors.Is(err, imaging.ErrDimensions):
		return field(apperr.RuleImageDimensions, map[string]string{"max": strconv.Itoa(s.opts.MaxDimension)})
	case errors.Is(err, imaging.ErrCorrupt):
		return field(apperr.RuleImageCorrupt, nil)
	case errors.Is(err, imaging.ErrTooLarge):
		return field(apperr.RuleFileTooLarge, map[string]string{"max_kb": strconv.Itoa(s.opts.MaxUploadBytes / 1024)})
	}
	return err
}

// ---- owner helpers (inside a transaction) ----

// lockOwner locks the room or building of the landlord and says whether a room follows its building's pictures; 404
// when it is not theirs.
func lockOwner(ctx context.Context, q *sqlcgen.Queries, landlordID uuid.UUID, o MediaOwner) (follows bool, err error) {
	if o.room {
		row, err := q.GetRoomForUpdate(ctx, sqlcgen.GetRoomForUpdateParams{ID: o.id, LandlordID: landlordID})
		if err != nil {
			return false, notFoundOr(err)
		}
		return row.InheritsMedia, nil
	}
	_, err = q.GetBuildingForUpdate(ctx, sqlcgen.GetBuildingForUpdateParams{ID: o.id, LandlordID: landlordID})
	return false, notFoundOr(err)
}

func notFoundOr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound()
	}
	return err
}

func ownerMedia(ctx context.Context, q *sqlcgen.Queries, o MediaOwner) ([]sqlcgen.Medium, error) {
	if o.room {
		return q.ListMediaOfRooms(ctx, []uuid.UUID{o.id})
	}
	return q.ListMediaOfBuildings(ctx, []uuid.UUID{o.id})
}

// owns is a cheap ownership check done before the expensive image work, so a stranger's request costs nothing.
func (s *Service) owns(ctx context.Context, landlordID uuid.UUID, o MediaOwner) error {
	q := s.store.Queries()
	var ok bool
	var err error
	if o.room {
		ok, err = q.OwnsRoom(ctx, sqlcgen.OwnsRoomParams{ID: o.id, LandlordID: landlordID})
	} else {
		ok, err = q.OwnsBuilding(ctx, sqlcgen.OwnsBuildingParams{ID: o.id, LandlordID: landlordID})
	}
	if err != nil {
		return err
	}
	if !ok {
		return apperr.NotFound()
	}
	return nil
}

// refreshCover sets the cover of the owner from its pictures (rule pickCover; none empties it). A building's cover is
// pushed to the rooms that follow its pictures, which are returned: their listing content changed.
func refreshCover(ctx context.Context, q *sqlcgen.Queries, o MediaOwner) ([]uuid.UUID, error) {
	media, err := ownerMedia(ctx, q, o)
	if err != nil {
		return nil, err
	}
	rows := make([]mediaRow, len(media))
	for i, m := range media {
		rows[i] = mediaRow{ID: m.ID, Kind: m.Kind, DisplayOrder: int(m.DisplayOrder)}
	}
	var key, thumb *string
	if best := pickCover(rows); best != nil {
		for i := range media {
			if media[i].ID == best.ID {
				key, thumb = &media[i].FileKey, &media[i].ThumbKey
			}
		}
	}
	if o.room {
		return nil, q.SetRoomCover(ctx, sqlcgen.SetRoomCoverParams{ID: o.id, CoverKey: key, CoverThumbKey: thumb})
	}
	if err := q.SetBuildingCover(ctx, sqlcgen.SetBuildingCoverParams{ID: o.id, CoverKey: key, CoverThumbKey: thumb}); err != nil {
		return nil, err
	}
	return q.PushBuildingCover(ctx, o.id)
}

// changed tells the observer which rooms show other pictures now: the room itself, or the rooms following a building.
func (s *Service) mediaChanged(ctx context.Context, tx pgx.Tx, o MediaOwner, followers []uuid.UUID) error {
	if o.room {
		return s.contentChanged(ctx, tx, []uuid.UUID{o.id})
	}
	return s.contentChanged(ctx, tx, followers)
}

// mediaLimit is how many pictures one room or building may have: the plan's, else the configured default.
func (s *Service) mediaLimit(ctx context.Context, landlordID uuid.UUID) (int, error) {
	limit, err := s.plans.MaxMediaPerOwner(ctx, landlordID)
	if err != nil {
		return 0, err
	}
	if limit == nil {
		return s.opts.MaxMediaPerOwner, nil
	}
	return *limit, nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func validKind(kind string) (string, error) {
	switch kind {
	case "":
		return KindPhoto, nil
	case KindPhoto, KindCover:
		return kind, nil
	}
	return "", apperr.Invalid(apperr.Field{Name: "kind", Rule: apperr.RuleOneOf})
}

// ---- use cases ----

// AddMedia stores a new picture of a room or building. A room that followed its building's pictures starts having
// its own. A cover replaces the previous cover (which becomes a photo).
func (s *Service) AddMedia(ctx context.Context, landlordID uuid.UUID, o MediaOwner, in Upload) (MediaView, error) {
	kind, err := validKind(in.Kind)
	if err != nil {
		return MediaView{}, err
	}
	if err := s.owns(ctx, landlordID, o); err != nil {
		return MediaView{}, err
	}
	st, err := s.stage(ctx, in.Data)
	if err != nil {
		return MediaView{}, err
	}
	var row sqlcgen.Medium
	err = s.store.InTx(ctx, func(tx pgx.Tx, q *sqlcgen.Queries) error {
		follows, err := lockOwner(ctx, q, landlordID, o)
		if err != nil {
			return err
		}
		media, err := ownerMedia(ctx, q, o)
		if err != nil {
			return err
		}
		limit, err := s.mediaLimit(ctx, landlordID)
		if err != nil {
			return err
		}
		if limitReached(&limit, len(media), 1) {
			return apperr.Conflict(apperr.CodeMediaLimitReached).WithParams(map[string]string{"max": strconv.Itoa(limit)})
		}
		roomID, buildingID := o.ids()
		if kind == KindCover {
			if err := q.DemoteMediaCovers(ctx, sqlcgen.DemoteMediaCoversParams{RoomID: roomID, BuildingID: buildingID}); err != nil {
				return err
			}
		}
		if follows {
			if err := q.StopFollowingBuildingMedia(ctx, o.id); err != nil {
				return err
			}
		}
		next := int16(1)
		if len(media) > 0 {
			next = media[len(media)-1].DisplayOrder + 1
		}
		row, err = q.InsertMedia(ctx, sqlcgen.InsertMediaParams{
			RoomID: roomID, BuildingID: buildingID, Kind: kind, FileKey: st.file, ThumbKey: st.thumb,
			ContentType: st.result.ContentType, SizeBytes: int32(len(st.result.Data)), //nolint:gosec // at most MEDIA_MAX_UPLOAD_BYTES
			Width: int32(st.result.Width), Height: int32(st.result.Height), //nolint:gosec // at most MEDIA_MAX_DIMENSION
			OriginalName: nonEmpty(in.FileName), DisplayOrder: next,
		})
		if err != nil {
			return err
		}
		followers, err := refreshCover(ctx, q, o)
		if err != nil {
			return err
		}
		return s.mediaChanged(ctx, tx, o, followers)
	})
	if err != nil {
		s.deleteFiles(ctx, st.keys())
		return MediaView{}, err
	}
	return mediaView(row, s.storage), nil
}

// ReplaceMediaFile puts a new image in place of an existing picture, keeping its place and kind. The new files get
// new keys, so a cached URL of the old picture never shows the new one.
func (s *Service) ReplaceMediaFile(ctx context.Context, landlordID uuid.UUID, o MediaOwner, mediaID uuid.UUID, in Upload) (MediaView, error) {
	if err := s.owns(ctx, landlordID, o); err != nil {
		return MediaView{}, err
	}
	st, err := s.stage(ctx, in.Data)
	if err != nil {
		return MediaView{}, err
	}
	var row sqlcgen.Medium
	var old []string
	err = s.store.InTx(ctx, func(tx pgx.Tx, q *sqlcgen.Queries) error {
		if _, err := lockOwner(ctx, q, landlordID, o); err != nil {
			return err
		}
		media, err := ownerMedia(ctx, q, o)
		if err != nil {
			return err
		}
		idx := indexOfMedia(media, mediaID)
		if idx < 0 {
			return apperr.NotFound()
		}
		old = []string{media[idx].FileKey, media[idx].ThumbKey}
		roomID, buildingID := o.ids()
		row, err = q.ReplaceMediaFile(ctx, sqlcgen.ReplaceMediaFileParams{
			ID: mediaID, RoomID: roomID, BuildingID: buildingID, FileKey: st.file, ThumbKey: st.thumb,
			ContentType: st.result.ContentType, SizeBytes: int32(len(st.result.Data)), //nolint:gosec // at most MEDIA_MAX_UPLOAD_BYTES
			Width: int32(st.result.Width), Height: int32(st.result.Height), //nolint:gosec // at most MEDIA_MAX_DIMENSION
			OriginalName: nonEmpty(in.FileName),
		})
		if err != nil {
			return err
		}
		followers, err := refreshCover(ctx, q, o)
		if err != nil {
			return err
		}
		return s.mediaChanged(ctx, tx, o, followers)
	})
	if err != nil {
		s.deleteFiles(ctx, st.keys())
		return MediaView{}, err
	}
	s.deleteFiles(ctx, old)
	return mediaView(row, s.storage), nil
}

func indexOfMedia(media []sqlcgen.Medium, id uuid.UUID) int {
	for i := range media {
		if media[i].ID == id {
			return i
		}
	}
	return -1
}

// ArrangeMedia sets the order and the cover of the pictures: items lists every picture once, in the new order, with
// at most one marked as the cover.
func (s *Service) ArrangeMedia(ctx context.Context, landlordID uuid.UUID, o MediaOwner, items []ArrangeItem) ([]MediaView, error) {
	err := s.store.InTx(ctx, func(tx pgx.Tx, q *sqlcgen.Queries) error {
		if _, err := lockOwner(ctx, q, landlordID, o); err != nil {
			return err
		}
		media, err := ownerMedia(ctx, q, o)
		if err != nil {
			return err
		}
		current := make([]uuid.UUID, len(media))
		for i, m := range media {
			current[i] = m.ID
		}
		rule := make([]arrangeItem, len(items))
		for i, it := range items {
			rule[i] = arrangeItem(it)
		}
		if failure := checkArrangement(current, rule); failure != "" {
			return apperr.Invalid(apperr.Field{Name: "items", Rule: failure})
		}
		roomID, buildingID := o.ids()
		// The partial unique index allows one cover: demote the old one before any new one is set.
		if err := q.DemoteMediaCovers(ctx, sqlcgen.DemoteMediaCoversParams{RoomID: roomID, BuildingID: buildingID}); err != nil {
			return err
		}
		params := sqlcgen.SetMediaOrderParams{RoomID: roomID, BuildingID: buildingID}
		for i, it := range items {
			params.Ids = append(params.Ids, it.ID)
			params.Kinds = append(params.Kinds, it.Kind)
			params.Positions = append(params.Positions, int16(i+1)) //nolint:gosec // at most the picture limit
		}
		if err := q.SetMediaOrder(ctx, params); err != nil {
			return err
		}
		followers, err := refreshCover(ctx, q, o)
		if err != nil {
			return err
		}
		return s.mediaChanged(ctx, tx, o, followers)
	})
	if err != nil {
		return nil, err
	}
	media, err := ownerMedia(ctx, s.store.Queries(), o)
	if err != nil {
		return nil, err
	}
	return mediaViews(media, s.storage), nil
}

// DeleteMedia removes a picture and its files.
func (s *Service) DeleteMedia(ctx context.Context, landlordID uuid.UUID, o MediaOwner, mediaID uuid.UUID) error {
	var files []string
	err := s.store.InTx(ctx, func(tx pgx.Tx, q *sqlcgen.Queries) error {
		if _, err := lockOwner(ctx, q, landlordID, o); err != nil {
			return err
		}
		roomID, buildingID := o.ids()
		gone, err := q.DeleteMedia(ctx, sqlcgen.DeleteMediaParams{ID: mediaID, RoomID: roomID, BuildingID: buildingID})
		if err != nil {
			return notFoundOr(err)
		}
		files = []string{gone.FileKey, gone.ThumbKey}
		if err := q.RenumberMedia(ctx, sqlcgen.RenumberMediaParams{RoomID: roomID, BuildingID: buildingID}); err != nil {
			return err
		}
		followers, err := refreshCover(ctx, q, o)
		if err != nil {
			return err
		}
		return s.mediaChanged(ctx, tx, o, followers)
	})
	if err != nil {
		return err
	}
	s.deleteFiles(ctx, files)
	return nil
}

// OpenMedia opens a stored picture for the public route; anything that is not a key this module stored is a 404.
func (s *Service) OpenMedia(ctx context.Context, key string) (io.ReadCloser, storage.Info, error) {
	if !strings.HasPrefix(key, mediaPrefix) || !storage.ValidKey(key) {
		return nil, storage.Info{}, apperr.NotFound()
	}
	rc, info, err := s.storage.Open(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, storage.Info{}, apperr.NotFound()
	}
	return rc, info, err
}

// ---- copying a building's pictures ----

// stagedCopy is a duplicate of a picture's files; the row is made in the transaction.
type stagedCopy struct {
	SourceID    uuid.UUID
	File, Thumb string
}

// mediaCopies are the duplicated files of a building copy: its own pictures, and those of each room that has its own
// pictures (by the id of the source room).
type mediaCopies struct {
	building []stagedCopy
	rooms    map[uuid.UUID][]stagedCopy
	used     map[uuid.UUID]bool
}

func (m *mediaCopies) all() []stagedCopy {
	out := append([]stagedCopy(nil), m.building...)
	for _, list := range m.rooms {
		out = append(out, list...)
	}
	return out
}

func keysOf(copies []stagedCopy) []string {
	keys := make([]string, 0, 2*len(copies))
	for _, c := range copies {
		keys = append(keys, c.File, c.Thumb)
	}
	return keys
}

// unusedKeys are the duplicated files no row ended up using (a room that changed in between).
func (m *mediaCopies) unusedKeys() []string {
	var unused []stagedCopy
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
func (s *Service) stageMediaCopies(ctx context.Context, landlordID, buildingID uuid.UUID, withRooms bool) (_ *mediaCopies, err error) {
	q := s.store.Queries()
	out := &mediaCopies{rooms: map[uuid.UUID][]stagedCopy{}, used: map[uuid.UUID]bool{}}
	defer func() {
		if err != nil {
			s.deleteFiles(ctx, keysOf(out.all()))
		}
	}()

	own, err := q.ListMediaOfBuildings(ctx, []uuid.UUID{buildingID})
	if err != nil {
		return nil, err
	}
	if out.building, err = s.duplicateFiles(ctx, own); err != nil {
		return nil, err
	}
	if !withRooms {
		return out, nil
	}
	rooms, err := q.ListRoomsOfBuildingForCopy(ctx, sqlcgen.ListRoomsOfBuildingForCopyParams{BuildingID: &buildingID, LandlordID: landlordID})
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
	roomMedia, err := q.ListMediaOfRooms(ctx, ownRooms)
	if err != nil {
		return nil, err
	}
	byRoom := map[uuid.UUID][]sqlcgen.Medium{}
	for _, m := range roomMedia {
		byRoom[*m.RoomID] = append(byRoom[*m.RoomID], m)
	}
	for roomID, list := range byRoom {
		copies, err := s.duplicateFiles(ctx, list)
		out.rooms[roomID] = copies // keep what was made even on error so the deferred cleanup sees it
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// duplicateFiles copies the files of pictures under new keys of the same kind.
func (s *Service) duplicateFiles(ctx context.Context, media []sqlcgen.Medium) ([]stagedCopy, error) {
	var out []stagedCopy
	for _, m := range media {
		ext := m.FileKey[strings.LastIndex(m.FileKey, ".")+1:]
		file, thumb, err := s.newMediaKeys(ext)
		if err != nil {
			return out, err
		}
		if err := s.storage.Copy(ctx, m.FileKey, file); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				s.log.Warn("copy building: source picture file missing", "key", m.FileKey)
				continue
			}
			return out, err
		}
		if err := s.storage.Copy(ctx, m.ThumbKey, thumb); err != nil {
			s.deleteFiles(ctx, []string{file})
			if errors.Is(err, storage.ErrNotFound) {
				s.log.Warn("copy building: source thumbnail file missing", "key", m.ThumbKey)
				continue
			}
			return out, err
		}
		out = append(out, stagedCopy{SourceID: m.ID, File: file, Thumb: thumb})
	}
	return out, nil
}

// insertBuildingMediaCopies makes the rows of the building's duplicated pictures and sets its cover.
func insertBuildingMediaCopies(ctx context.Context, q *sqlcgen.Queries, buildingID uuid.UUID, copies *mediaCopies) error {
	if len(copies.building) == 0 {
		return nil
	}
	params := sqlcgen.CopyBuildingMediaParams{BuildingID: buildingID}
	for _, c := range copies.building {
		params.SourceIds = append(params.SourceIds, c.SourceID)
		params.FileKeys = append(params.FileKeys, c.File)
		params.ThumbKeys = append(params.ThumbKeys, c.Thumb)
		copies.used[c.SourceID] = true
	}
	if err := q.CopyBuildingMedia(ctx, params); err != nil {
		return err
	}
	_, err := refreshCover(ctx, q, BuildingMedia(buildingID))
	return err
}

// insertRoomMediaCopies makes the rows of the rooms' duplicated pictures (destinations by source room id) and sets
// each such room's cover.
func insertRoomMediaCopies(ctx context.Context, q *sqlcgen.Queries, copies *mediaCopies, destinations map[uuid.UUID]uuid.UUID) error {
	params := sqlcgen.CopyRoomMediaParams{}
	var touched []uuid.UUID
	for source, dest := range destinations {
		list := copies.rooms[source]
		if len(list) == 0 {
			continue
		}
		touched = append(touched, dest)
		for _, c := range list {
			params.RoomIds = append(params.RoomIds, dest)
			params.SourceIds = append(params.SourceIds, c.SourceID)
			params.FileKeys = append(params.FileKeys, c.File)
			params.ThumbKeys = append(params.ThumbKeys, c.Thumb)
			copies.used[c.SourceID] = true
		}
	}
	if len(touched) == 0 {
		return nil
	}
	if err := q.CopyRoomMedia(ctx, params); err != nil {
		return err
	}
	for _, id := range touched {
		if _, err := refreshCover(ctx, q, RoomMedia(id)); err != nil {
			return err
		}
	}
	return nil
}
