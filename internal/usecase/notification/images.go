package notification

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

// Images is what senders do with the pictures of their popups. The files are written before the transaction that
// records them (the slow part), and taken away again if it fails; files nothing refers to any more go after it commits.
type Images struct{ *core }

// NewImages returns the use case.
func NewImages(deps Deps, o Options) *Images { return &Images{newCore(deps, o)} }

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

// deleteFiles removes files nothing refers to; a failure is logged, the files are only waste.
func (c *core) deleteFiles(ctx context.Context, keys ...string) {
	if len(keys) == 0 {
		return
	}
	if err := c.files.Delete(ctx, keys...); err != nil {
		c.log.Warn("delete announcement picture files", "error", err, "keys", keys)
	}
}

// editable loads the message the sender may change (before the transaction, to refuse early).
func (c *core) editable(ctx context.Context, sender domain.Sender, id uuid.UUID) error {
	announcement, err := c.announcements.Find(ctx, id)
	if err != nil {
		return err
	}
	if !announcement.Owned(sender) {
		return shared.ErrNotFound
	}
	if announcement.RecalledAt != nil {
		return domain.ErrRecalled
	}
	return nil
}

// Upload adds a picture at the end of the popup's pictures.
func (i *Images) Upload(ctx context.Context, sender domain.Sender, id uuid.UUID, data []byte) (domain.Image, error) {
	if err := i.editable(ctx, sender, id); err != nil {
		return domain.Image{}, err
	}
	processed, err := i.imager.Process(ctx, data)
	if err != nil {
		return domain.Image{}, i.imageError(err)
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return domain.Image{}, err
	}
	key := domain.ImageKey(i.clock.Now().UTC(), random, processed.Ext)
	if err := i.files.Put(ctx, key, processed.Data, processed.ContentType); err != nil {
		return domain.Image{}, err
	}
	var added domain.Image
	err = i.tx.WithinTx(ctx, func(ctx context.Context) error {
		announcement, err := i.locked(ctx, sender, id)
		if err != nil {
			return err
		}
		existing, err := i.images.List(ctx, id)
		if err != nil {
			return err
		}
		if max := domain.MaxImages(announcement.SenderKind(), i.opts.ImageMaxPlatform, i.opts.ImageMaxLandlord); len(existing) >= max {
			return &domain.ImageLimitError{Max: max}
		}
		added, err = i.images.Insert(ctx, id, domain.NewImage{
			FileKey: key, ContentType: processed.ContentType, Width: processed.Width, Height: processed.Height, SizeBytes: len(processed.Data),
		}, len(existing))
		if err != nil {
			return err
		}
		return i.audit.Record(ctx, auditOf(AuditAnnouncementImages, sender, id, announcement, map[string]any{"action": "added", "images": len(existing) + 1}))
	})
	if err != nil {
		i.deleteFiles(ctx, key)
		return domain.Image{}, err
	}
	return added, nil
}

// Arrange sets the order and the links of the pictures to the list: pictures that are not listed are removed. A link
// left out (nil) stays as it was and an empty one clears it; only the platform's pictures may carry a link.
func (i *Images) Arrange(ctx context.Context, sender domain.Sender, id uuid.UUID, items []domain.ArrangeItem) ([]domain.Image, error) {
	var removedKeys []string
	err := i.tx.WithinTx(ctx, func(ctx context.Context) error {
		announcement, err := i.locked(ctx, sender, id)
		if err != nil {
			return err
		}
		existing, err := i.images.List(ctx, id)
		if err != nil {
			return err
		}
		byID := make(map[uuid.UUID]domain.Image, len(existing))
		for _, img := range existing {
			byID[img.ID] = img
		}
		listed := make(map[uuid.UUID]bool, len(items))
		links := make([]string, len(items))
		for n, item := range items {
			img, ok := byID[item.ID]
			if !ok || listed[item.ID] {
				return domain.Invalid(domain.Violation{Name: "items", Rule: domain.RuleExists})
			}
			listed[item.ID] = true
			links[n] = img.LinkURL
			if item.LinkURL == nil {
				continue
			}
			link := strings.TrimSpace(*item.LinkURL)
			switch {
			case link == "":
				links[n] = ""
			case announcement.SenderKind() == domain.SenderLandlord:
				return domain.ErrLinkNotAllowed
			case len(link) > 2048 || !domain.SafeURL(link):
				return domain.Invalid(domain.Violation{Name: "items." + itoa(n) + ".link_url", Rule: domain.RuleURLUnsafe})
			default:
				links[n] = link
			}
		}
		for _, img := range existing {
			if !listed[img.ID] {
				if err := i.images.Delete(ctx, id, img.ID); err != nil {
					return err
				}
				removedKeys = append(removedKeys, img.FileKey)
			}
		}
		for n, item := range items {
			if err := i.images.Update(ctx, id, item.ID, n, links[n]); err != nil {
				return err
			}
		}
		return i.audit.Record(ctx, auditOf(AuditAnnouncementImages, sender, id, announcement, map[string]any{"action": "arranged", "images": len(items), "removed": len(removedKeys)}))
	})
	if err != nil {
		return nil, err
	}
	i.deleteFiles(ctx, removedKeys...)
	return i.images.List(ctx, id)
}

// Delete removes one picture and closes up the positions of the others.
func (i *Images) Delete(ctx context.Context, sender domain.Sender, id, imageID uuid.UUID) error {
	var key string
	err := i.tx.WithinTx(ctx, func(ctx context.Context) error {
		announcement, err := i.locked(ctx, sender, id)
		if err != nil {
			return err
		}
		existing, err := i.images.List(ctx, id)
		if err != nil {
			return err
		}
		next := 0
		found := false
		for _, img := range existing {
			if img.ID == imageID {
				found, key = true, img.FileKey
				continue
			}
			if img.DisplayOrder != next {
				if err := i.images.Update(ctx, id, img.ID, next, img.LinkURL); err != nil {
					return err
				}
			}
			next++
		}
		if !found {
			return shared.ErrNotFound
		}
		if err := i.images.Delete(ctx, id, imageID); err != nil {
			return err
		}
		return i.audit.Record(ctx, auditOf(AuditAnnouncementImages, sender, id, announcement, map[string]any{"action": "deleted", "images": next}))
	})
	if err != nil {
		return err
	}
	i.deleteFiles(ctx, key)
	return nil
}

// Open opens a stored picture for the public route; anything that is not a key this context stored is shared.ErrNotFound.
func (i *Images) Open(ctx context.Context, key string) (io.ReadCloser, FileInfo, error) {
	if !strings.HasPrefix(key, domain.ImageKeyPrefix) {
		return nil, FileInfo{}, shared.ErrNotFound
	}
	rc, info, err := i.files.Open(ctx, key)
	if errors.Is(err, domain.ErrFileNotFound) {
		return nil, FileInfo{}, shared.ErrNotFound
	}
	return rc, info, err
}

func itoa(n int) string { return strconv.Itoa(n) }
