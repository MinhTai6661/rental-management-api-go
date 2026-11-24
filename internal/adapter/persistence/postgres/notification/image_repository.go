package notificationpg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

// ImageRepository stores the pictures of popups.
type ImageRepository struct{ pool *pgxpool.Pool }

// NewImageRepository returns a repository on the pool.
func NewImageRepository(pool *pgxpool.Pool) *ImageRepository { return &ImageRepository{pool: pool} }

// List returns the pictures of the popup in order.
func (r *ImageRepository) List(ctx context.Context, announcementID uuid.UUID) ([]domain.Image, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListAnnouncementImages(ctx, []uuid.UUID{announcementID})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.Image, len(rows))
	for i, row := range rows {
		out[i] = imageOf(row)
	}
	return out, nil
}

// Insert adds a picture at the given position.
func (r *ImageRepository) Insert(ctx context.Context, announcementID uuid.UUID, img domain.NewImage, order int) (domain.Image, error) {
	row, err := postgres.Queries(ctx, r.pool).InsertAnnouncementImage(ctx, sqlcgen.InsertAnnouncementImageParams{
		AnnouncementID: announcementID, FileKey: img.FileKey, ContentType: img.ContentType, Width: int32(img.Width), Height: int32(img.Height), //nolint:gosec // checked by the image processor
		SizeBytes: int32(img.SizeBytes), LinkUrl: nonEmptyPtr(img.LinkURL), DisplayOrder: int16(order), //nolint:gosec // at most 20 pictures
	})
	if err != nil {
		return domain.Image{}, postgres.Translate(err)
	}
	return imageOf(row), nil
}

// Update sets the position and link ("" for none) of a picture.
func (r *ImageRepository) Update(ctx context.Context, announcementID, imageID uuid.UUID, order int, link string) error {
	n, err := postgres.Queries(ctx, r.pool).UpdateAnnouncementImage(ctx, sqlcgen.UpdateAnnouncementImageParams{
		ID: imageID, AnnouncementID: announcementID, DisplayOrder: int16(order), LinkUrl: nonEmptyPtr(link), //nolint:gosec // at most 20 pictures
	})
	if err != nil {
		return postgres.Translate(err)
	}
	if n == 0 {
		return shared.ErrNotFound
	}
	return nil
}

// Delete removes a picture's row.
func (r *ImageRepository) Delete(ctx context.Context, announcementID, imageID uuid.UUID) error {
	n, err := postgres.Queries(ctx, r.pool).DeleteAnnouncementImage(ctx, sqlcgen.DeleteAnnouncementImageParams{ID: imageID, AnnouncementID: announcementID})
	if err != nil {
		return postgres.Translate(err)
	}
	if n == 0 {
		return shared.ErrNotFound
	}
	return nil
}
