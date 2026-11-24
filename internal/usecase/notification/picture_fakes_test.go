package notification

import (
	"bytes"
	"context"
	"io"
	"slices"
	"sort"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

// impressionFake keeps what people did with popups in memory.
type impressionFake struct{ rows map[[2]uuid.UUID]domain.Seen }

func newImpressionFake() *impressionFake {
	return &impressionFake{rows: map[[2]uuid.UUID]domain.Seen{}}
}

func (r *impressionFake) Seen(_ context.Context, userID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]domain.Seen, error) {
	out := map[uuid.UUID]domain.Seen{}
	for _, id := range ids {
		if s, ok := r.rows[[2]uuid.UUID{id, userID}]; ok {
			out[id] = s
		}
	}
	return out, nil
}

func (r *impressionFake) Upsert(_ context.Context, id, userID uuid.UUID, s domain.Seen) error {
	r.rows[[2]uuid.UUID{id, userID}] = s
	return nil
}

// imageFake keeps the pictures of popups in memory.
type imageFake struct{ rows map[uuid.UUID][]domain.Image }

func newImageFake() *imageFake { return &imageFake{rows: map[uuid.UUID][]domain.Image{}} }

func (r *imageFake) List(_ context.Context, id uuid.UUID) ([]domain.Image, error) {
	out := slices.Clone(r.rows[id])
	sort.SliceStable(out, func(i, j int) bool { return out[i].DisplayOrder < out[j].DisplayOrder })
	return out, nil
}

func (r *imageFake) Insert(_ context.Context, id uuid.UUID, n domain.NewImage, order int) (domain.Image, error) {
	img := domain.Image{ID: uuid.New(), FileKey: n.FileKey, ContentType: n.ContentType, Width: n.Width, Height: n.Height, SizeBytes: n.SizeBytes, LinkURL: n.LinkURL, DisplayOrder: order}
	r.rows[id] = append(r.rows[id], img)
	return img, nil
}

func (r *imageFake) Update(_ context.Context, id, imageID uuid.UUID, order int, link string) error {
	for i := range r.rows[id] {
		if r.rows[id][i].ID == imageID {
			r.rows[id][i].DisplayOrder, r.rows[id][i].LinkURL = order, link
			return nil
		}
	}
	return shared.ErrNotFound
}

func (r *imageFake) Delete(_ context.Context, id, imageID uuid.UUID) error {
	before := len(r.rows[id])
	r.rows[id] = slices.DeleteFunc(r.rows[id], func(i domain.Image) bool { return i.ID == imageID })
	if len(r.rows[id]) == before {
		return shared.ErrNotFound
	}
	return nil
}

// fileFake keeps picture files in memory and remembers what was deleted.
type fileFake struct {
	files   map[string][]byte
	deleted []string
}

func newFileFake() *fileFake { return &fileFake{files: map[string][]byte{}} }

func (f *fileFake) Put(_ context.Context, key string, data []byte, _ string) error {
	f.files[key] = data
	return nil
}

func (f *fileFake) Open(_ context.Context, key string) (io.ReadCloser, FileInfo, error) {
	data, ok := f.files[key]
	if !ok {
		return nil, FileInfo{}, domain.ErrFileNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), FileInfo{Size: int64(len(data)), ContentType: "image/webp"}, nil
}

func (f *fileFake) Delete(_ context.Context, keys ...string) error {
	for _, k := range keys {
		delete(f.files, k)
	}
	f.deleted = append(f.deleted, keys...)
	return nil
}

// imagerFake accepts every upload except the ones starting with "bad".
type imagerFake struct{ err error }

func (p imagerFake) Process(_ context.Context, data []byte) (domain.ProcessedImage, error) {
	if p.err != nil {
		return domain.ProcessedImage{}, p.err
	}
	return domain.ProcessedImage{Data: data, ContentType: "image/webp", Ext: "webp", Width: 800, Height: 600}, nil
}

// statsFake answers with what the test set and remembers the queries.
type statsFake struct {
	messages domain.Messages
	totals   domain.Totals
	queries  []domain.StatsQuery
}

func (s *statsFake) Messages(_ context.Context, q domain.StatsQuery) (domain.Messages, error) {
	s.queries = append(s.queries, q)
	return s.messages, nil
}

func (s *statsFake) Totals(_ context.Context, q domain.StatsQuery, recipients int) (domain.Totals, error) {
	s.queries = append(s.queries, q)
	t := s.totals
	if q.WantsNotification() {
		t.Notification = &domain.NotificationTotals{Recipients: recipients}
	}
	return t, nil
}

func (s *statsFake) Series(_ context.Context, q domain.StatsQuery) ([]domain.DayPoint, error) {
	s.queries = append(s.queries, q)
	days, err := domain.Days(q.First, q.Last)
	if err != nil {
		return nil, err
	}
	out := make([]domain.DayPoint, len(days))
	for i, d := range days {
		out[i] = domain.DayPoint{Day: d}
	}
	return out, nil
}
