package property

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"strings"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// Media is the use cases of the pictures of rooms and buildings.
type Media struct{ *core }

// NewMedia builds the picture use cases.
func NewMedia(d Deps, o Options) *Media { return &Media{newCore(d, o)} }

// ---- keys and staging ----

// newMediaKeys builds the storage keys of a picture and its thumbnail from the date and 16 random bytes.
func (c *core) newMediaKeys(ext string) (file, thumb string, err error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", "", err
	}
	file, thumb = domain.MediaKeys(c.clock.Now().UTC(), random, ext)
	return file, thumb, nil
}

// staged is a processed picture whose files are already in storage but not yet in a row.
type staged struct {
	image       domain.ProcessedImage
	file, thumb string
}

func (st staged) keys() []string { return []string{st.file, st.thumb} }

// stage verifies the bytes of an upload and writes the picture and its thumbnail to storage. The files exist
// before the transaction that records them, so the caller deletes them again when that transaction fails.
func (c *core) stage(ctx context.Context, data []byte) (staged, error) {
	img, err := c.images.Process(ctx, data)
	if err != nil {
		return staged{}, c.imageError(err)
	}
	file, thumb, err := c.newMediaKeys(img.Ext)
	if err != nil {
		return staged{}, err
	}
	if err := c.files.Put(ctx, file, img.Data, img.ContentType); err != nil {
		return staged{}, err
	}
	if err := c.files.Put(ctx, thumb, img.Thumb, "image/webp"); err != nil {
		c.deleteFiles(ctx, []string{file})
		return staged{}, err
	}
	return staged{image: img, file: file, thumb: thumb}, nil
}

// imageError adds the limits to what the image checks refuse.
func (c *core) imageError(err error) error {
	switch {
	case errors.Is(err, domain.ErrImageDimensions):
		return &domain.ImageDimensionsError{Max: c.opts.MaxDimension}
	case errors.Is(err, domain.ErrImageTooLarge):
		return &domain.FileTooLargeError{MaxKB: c.opts.MaxUploadBytes / 1024}
	}
	return err
}

// ---- owner helpers (inside a transaction) ----

// lockOwner locks the room or building of the landlord and says whether a room follows its building's pictures;
// domain.ErrNotFound when it is not theirs.
func (c *core) lockOwner(ctx context.Context, landlordID uuid.UUID, o domain.MediaOwner) (follows bool, err error) {
	if o.IsRoom() {
		room, err := c.rooms.ForUpdate(ctx, landlordID, o.ID())
		if err != nil {
			return false, notFound(err)
		}
		return room.Follows(domain.GroupMedia), nil
	}
	_, err = c.buildings.ForUpdate(ctx, landlordID, o.ID())
	return false, notFound(err)
}

// refreshCover sets the cover of the owner from its pictures (rule PickCover; none empties it). A building's cover
// is pushed to the rooms that follow its pictures, which are returned: their listing content changed.
func (c *core) refreshCover(ctx context.Context, o domain.MediaOwner) ([]uuid.UUID, error) {
	media, err := c.media.OfOwner(ctx, o)
	if err != nil {
		return nil, err
	}
	rows := make([]domain.MediaRow, len(media))
	for i, m := range media {
		rows[i] = domain.MediaRow{ID: m.ID, Kind: m.Kind, DisplayOrder: m.DisplayOrder}
	}
	var cover *domain.Cover
	if best := domain.PickCover(rows); best != nil {
		for i := range media {
			if media[i].ID == best.ID {
				cover = &domain.Cover{Key: media[i].FileKey, ThumbKey: media[i].ThumbKey}
			}
		}
	}
	return c.media.SetCover(ctx, o, cover)
}

// mediaChanged tells the observer which rooms show other pictures now: the room itself, or the rooms following a
// building.
func (c *core) mediaChanged(ctx context.Context, o domain.MediaOwner, followers []uuid.UUID) error {
	if o.IsRoom() {
		return c.contentChanged(ctx, []uuid.UUID{o.ID()})
	}
	return c.contentChanged(ctx, followers)
}

// mediaLimit is how many pictures one room or building may have: the plan's, else the configured default.
func (c *core) mediaLimit(ctx context.Context, landlordID uuid.UUID) (int, error) {
	limit, err := c.plans.MaxMediaPerOwner(ctx, landlordID)
	if err != nil {
		return 0, err
	}
	if limit == nil {
		return c.opts.MaxMediaPerOwner, nil
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
		return domain.KindPhoto, nil
	case domain.KindPhoto, domain.KindCover:
		return kind, nil
	}
	return "", domain.ErrPictureKindInvalid
}

// ---- use cases ----

// Add stores a new picture of a room or building. A room that followed its building's pictures starts having its
// own. A cover replaces the previous cover (which becomes a photo).
func (m *Media) Add(ctx context.Context, landlordID uuid.UUID, o domain.MediaOwner, in Upload) (domain.Media, error) {
	kind, err := validKind(in.Kind)
	if err != nil {
		return domain.Media{}, err
	}
	if err := m.owns(ctx, landlordID, o); err != nil {
		return domain.Media{}, err
	}
	st, err := m.stage(ctx, in.Data)
	if err != nil {
		return domain.Media{}, err
	}
	var row domain.Media
	err = m.tx.WithinTx(ctx, func(ctx context.Context) error {
		follows, err := m.lockOwner(ctx, landlordID, o)
		if err != nil {
			return err
		}
		media, err := m.media.OfOwner(ctx, o)
		if err != nil {
			return err
		}
		limit, err := m.mediaLimit(ctx, landlordID)
		if err != nil {
			return err
		}
		if domain.LimitReached(&limit, len(media), 1) {
			return &domain.MediaLimitError{Max: limit}
		}
		if kind == domain.KindCover {
			if err := m.media.DemoteCovers(ctx, o); err != nil {
				return err
			}
		}
		if follows {
			if err := m.media.StopFollowing(ctx, o.ID()); err != nil {
				return err
			}
		}
		next := 1
		if len(media) > 0 {
			next = media[len(media)-1].DisplayOrder + 1
		}
		row, err = m.media.Insert(ctx, o, domain.NewMedia{
			Kind: kind, FileKey: st.file, ThumbKey: st.thumb, ContentType: st.image.ContentType,
			SizeBytes: len(st.image.Data), Width: st.image.Width, Height: st.image.Height,
			OriginalName: nonEmpty(in.FileName), DisplayOrder: next,
		})
		if err != nil {
			return err
		}
		followers, err := m.refreshCover(ctx, o)
		if err != nil {
			return err
		}
		return m.mediaChanged(ctx, o, followers)
	})
	if err != nil {
		m.deleteFiles(ctx, st.keys())
		return domain.Media{}, err
	}
	return row, nil
}

// ReplaceFile puts a new image in place of an existing picture, keeping its place and kind. The new files get new
// keys, so a cached URL of the old picture never shows the new one.
func (m *Media) ReplaceFile(ctx context.Context, landlordID uuid.UUID, o domain.MediaOwner, mediaID uuid.UUID, in Upload) (domain.Media, error) {
	if err := m.owns(ctx, landlordID, o); err != nil {
		return domain.Media{}, err
	}
	st, err := m.stage(ctx, in.Data)
	if err != nil {
		return domain.Media{}, err
	}
	var row domain.Media
	var old []string
	err = m.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := m.lockOwner(ctx, landlordID, o); err != nil {
			return err
		}
		media, err := m.media.OfOwner(ctx, o)
		if err != nil {
			return err
		}
		idx := indexOfMedia(media, mediaID)
		if idx < 0 {
			return domain.ErrNotFound
		}
		old = []string{media[idx].FileKey, media[idx].ThumbKey}
		row, err = m.media.Replace(ctx, o, mediaID, domain.MediaFile{
			FileKey: st.file, ThumbKey: st.thumb, ContentType: st.image.ContentType, SizeBytes: len(st.image.Data),
			Width: st.image.Width, Height: st.image.Height, OriginalName: nonEmpty(in.FileName),
		})
		if err != nil {
			return err
		}
		followers, err := m.refreshCover(ctx, o)
		if err != nil {
			return err
		}
		return m.mediaChanged(ctx, o, followers)
	})
	if err != nil {
		m.deleteFiles(ctx, st.keys())
		return domain.Media{}, err
	}
	m.deleteFiles(ctx, old)
	return row, nil
}

func indexOfMedia(media []domain.Media, id uuid.UUID) int {
	for i := range media {
		if media[i].ID == id {
			return i
		}
	}
	return -1
}

// Arrange sets the order and the cover of the pictures: items lists every picture once, in the new order, with at
// most one marked as the cover.
func (m *Media) Arrange(ctx context.Context, landlordID uuid.UUID, o domain.MediaOwner, items []domain.ArrangeItem) ([]domain.Media, error) {
	err := m.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := m.lockOwner(ctx, landlordID, o); err != nil {
			return err
		}
		media, err := m.media.OfOwner(ctx, o)
		if err != nil {
			return err
		}
		current := make([]uuid.UUID, len(media))
		for i, x := range media {
			current[i] = x.ID
		}
		if err := domain.CheckArrangement(current, items); err != nil {
			return err
		}
		// The partial unique index allows one cover: demote the old one before any new one is set.
		if err := m.media.DemoteCovers(ctx, o); err != nil {
			return err
		}
		places := make([]domain.MediaPlace, len(items))
		for i, it := range items {
			places[i] = domain.MediaPlace{ID: it.ID, Kind: it.Kind, Position: i + 1}
		}
		if err := m.media.SetOrder(ctx, o, places); err != nil {
			return err
		}
		followers, err := m.refreshCover(ctx, o)
		if err != nil {
			return err
		}
		return m.mediaChanged(ctx, o, followers)
	})
	if err != nil {
		return nil, err
	}
	return m.media.OfOwner(ctx, o)
}

// Delete removes a picture and its files.
func (m *Media) Delete(ctx context.Context, landlordID uuid.UUID, o domain.MediaOwner, mediaID uuid.UUID) error {
	var files []string
	err := m.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := m.lockOwner(ctx, landlordID, o); err != nil {
			return err
		}
		gone, err := m.media.Delete(ctx, o, mediaID)
		if err != nil {
			return notFound(err)
		}
		files = gone
		if err := m.media.Renumber(ctx, o); err != nil {
			return err
		}
		followers, err := m.refreshCover(ctx, o)
		if err != nil {
			return err
		}
		return m.mediaChanged(ctx, o, followers)
	})
	if err != nil {
		return err
	}
	m.deleteFiles(ctx, files)
	return nil
}

// Open opens a stored picture for the public route; anything that is not a key this context stored is
// domain.ErrNotFound.
func (m *Media) Open(ctx context.Context, key string) (io.ReadCloser, domain.FileInfo, error) {
	if !strings.HasPrefix(key, domain.MediaKeyPrefix) {
		return nil, domain.FileInfo{}, domain.ErrNotFound
	}
	rc, info, err := m.files.Open(ctx, key)
	if errors.Is(err, domain.ErrFileNotFound) {
		return nil, domain.FileInfo{}, domain.ErrNotFound
	}
	return rc, info, err
}
