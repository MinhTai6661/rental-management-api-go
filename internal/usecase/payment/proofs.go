package payment

import (
	"context"
	"errors"
	"io"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/payment"
)

// storeProofs verifies the proof pictures and writes them with their small copies, before any transaction (it is the
// slow part). It returns the key of each picture. When one fails, what was already written is removed again.
func (c *core) storeProofs(ctx context.Context, pictures [][]byte) ([]string, error) {
	keys := make([]string, 0, len(pictures))
	for i, data := range pictures {
		img, err := c.images.Process(ctx, data)
		if err != nil {
			c.removeFiles(ctx, keys)
			if isImageRefusal(err) {
				return nil, &ProofError{Index: i, Err: err}
			}
			return nil, err
		}
		key := domain.NewProofKey(c.clock.Now(), uuid.New(), img.Ext)
		if err := c.files.Put(ctx, key, img.Data, img.ContentType); err != nil {
			c.removeFiles(ctx, keys)
			return nil, err
		}
		keys = append(keys, key)
		if err := c.files.Put(ctx, domain.ThumbKey(key), img.Thumb, "image/webp"); err != nil {
			c.removeFiles(ctx, keys)
			return nil, err
		}
	}
	return keys, nil
}

func isImageRefusal(err error) bool {
	for _, target := range []error{domain.ErrImageType, domain.ErrImageCorrupt, domain.ErrImageDimensions, domain.ErrImageTooLarge} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// removeFiles deletes proof pictures (and their small copies) best effort: the failure is logged, and the files are
// private and unreferenced, so a leftover does no harm.
func (c *core) removeFiles(ctx context.Context, keys []string) {
	if len(keys) == 0 {
		return
	}
	all := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		all = append(all, k, domain.ThumbKey(k), domain.LegacyThumbKey(k))
	}
	if err := c.files.Delete(context.WithoutCancel(ctx), all...); err != nil {
		c.log.Warn("remove proof files", "error", err, "files", len(all))
	}
}

// OpenProof opens a proof picture (file is "<index>.webp" or "<index>_thumb.webp") of a payment for the signed link
// that reached it: anything wrong with the link or the file is domain.ErrNotFound. A payment with no small copy shows
// the full picture instead.
func (s *Payments) OpenProof(ctx context.Context, paymentID uuid.UUID, file string, expires int64, signature string) (io.ReadCloser, FileInfo, error) {
	if !s.links.Valid(paymentID, file, expires, signature, s.clock.Now()) {
		return nil, FileInfo{}, domain.ErrNotFound
	}
	index, thumb, ok := domain.ParseProofFile(file)
	if !ok {
		return nil, FileInfo{}, domain.ErrNotFound
	}
	keys, err := s.payments.ProofKeys(ctx, paymentID)
	if err != nil {
		return nil, FileInfo{}, notFound(err)
	}
	if index >= len(keys) {
		return nil, FileInfo{}, domain.ErrNotFound
	}
	if thumb {
		// Proofs stored before thumbnails became WebP have a "_thumb.jpg" copy instead.
		for _, key := range []string{domain.ThumbKey(keys[index]), domain.LegacyThumbKey(keys[index])} {
			rc, info, err := s.files.Open(ctx, key)
			if err == nil || !errors.Is(err, domain.ErrFileNotFound) {
				return rc, info, err
			}
		}
	}
	rc, info, err := s.files.Open(ctx, keys[index])
	if errors.Is(err, domain.ErrFileNotFound) {
		return nil, FileInfo{}, domain.ErrNotFound
	}
	return rc, info, err
}

// proofLinks makes the signed links of the proof pictures of a payment.
func (c *core) proofLinks(paymentID uuid.UUID, keys []string) []ProofLink {
	expires := c.clock.Now().Add(c.opts.ProofLinkTTL)
	out := make([]ProofLink, len(keys))
	for i := range keys {
		out[i] = ProofLink{
			Index: i, URL: c.links.Link(paymentID, domain.ProofFileName(i, false), expires),
			ThumbnailURL: c.links.Link(paymentID, domain.ProofFileName(i, true), expires),
		}
	}
	return out
}

// viewsOf adds the signed links to payments.
func (c *core) viewsOf(details []domain.Detail) []View {
	out := make([]View, len(details))
	for i, d := range details {
		out[i] = View{Detail: d, Proofs: c.proofLinks(d.ID, d.ProofKeys)}
	}
	return out
}

func (c *core) viewOf(d domain.Detail) View { return c.viewsOf([]domain.Detail{d})[0] }
