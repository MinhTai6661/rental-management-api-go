package adminpg

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/admin"
)

// ApplicationRepository stores landlord applications.
type ApplicationRepository struct{ pool *pgxpool.Pool }

// NewApplicationRepository returns a repository on the pool.
func NewApplicationRepository(pool *pgxpool.Pool) *ApplicationRepository {
	return &ApplicationRepository{pool: pool}
}

// row is the shape every query of an application returns.
type row = sqlcgen.AdminGetLandlordApplicationRow

func applicationOf(r row) domain.Application {
	return domain.Application{
		ID: r.ID, UserID: r.UserID, ApplicantName: r.ApplicantName, ApplicantEmail: r.ApplicantEmail,
		BusinessName: r.BusinessName, Phone: r.Phone, WardCode: r.WardCode, AddressDetail: r.AddressDetail, Note: r.Note,
		Status: domain.ApplicationStatus(r.Status), ReviewedByID: r.ReviewedByID, ReviewedAt: r.ReviewedAt,
		ReviewNote: r.ReviewNote, CreatedAt: r.CreatedAt,
	}
}

// Insert stores a new application; a second one waiting for the same person is a unique ConstraintError.
func (r *ApplicationRepository) Insert(ctx context.Context, a domain.Application) error {
	_, err := postgres.Queries(ctx, r.pool).AdminInsertLandlordApplication(ctx, sqlcgen.AdminInsertLandlordApplicationParams{
		ID: a.ID, UserID: a.UserID, BusinessName: a.BusinessName, Phone: a.Phone, WardCode: a.WardCode,
		AddressDetail: a.AddressDetail, Note: a.Note, Now: a.CreatedAt,
	})
	return postgres.Translate(err)
}

// Get returns an application.
func (r *ApplicationRepository) Get(ctx context.Context, id uuid.UUID) (domain.Application, error) {
	v, err := postgres.Queries(ctx, r.pool).AdminGetLandlordApplication(ctx, id)
	return applicationOf(v), postgres.Translate(err)
}

// Lock returns an application and locks it until the transaction ends.
func (r *ApplicationRepository) Lock(ctx context.Context, id uuid.UUID) (domain.Application, error) {
	v, err := postgres.Queries(ctx, r.pool).AdminLockLandlordApplication(ctx, id)
	return applicationOf(row(v)), postgres.Translate(err)
}

// LatestOfUser returns the newest application of the person.
func (r *ApplicationRepository) LatestOfUser(ctx context.Context, userID uuid.UUID) (domain.Application, error) {
	v, err := postgres.Queries(ctx, r.pool).AdminLatestLandlordApplicationOfUser(ctx, userID)
	return applicationOf(row(v)), postgres.Translate(err)
}

// HasOpen reports whether an application of the person waits for a review.
func (r *ApplicationRepository) HasOpen(ctx context.Context, userID uuid.UUID) (bool, error) {
	ok, err := postgres.Queries(ctx, r.pool).AdminHasOpenLandlordApplication(ctx, userID)
	return ok, postgres.Translate(err)
}

// DeleteOpen removes the application of the person that waits for a review and reports whether there was one.
func (r *ApplicationRepository) DeleteOpen(ctx context.Context, userID uuid.UUID) (bool, error) {
	n, err := postgres.Queries(ctx, r.pool).AdminDeleteOpenLandlordApplication(ctx, userID)
	return n > 0, postgres.Translate(err)
}

// List returns a page of the applications in a status and how many there are.
func (r *ApplicationRepository) List(ctx context.Context, f domain.ApplicationQuery, limit, offset int) ([]domain.Application, int, error) {
	q := postgres.Queries(ctx, r.pool)
	total, err := q.AdminCountLandlordApplications(ctx, sqlcgen.AdminCountLandlordApplicationsParams{Status: string(f.Status), Search: f.Search})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	rows, err := q.AdminListLandlordApplications(ctx, sqlcgen.AdminListLandlordApplicationsParams{
		Status: string(f.Status), Search: f.Search, Sort: string(f.Sort),
		PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by the use case
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.Application, len(rows))
	for i, v := range rows {
		out[i] = applicationOf(row(v))
	}
	return out, int(total), nil
}

// CountsByStatus counts every application by status.
func (r *ApplicationRepository) CountsByStatus(ctx context.Context) (map[domain.ApplicationStatus]int, error) {
	rows, err := postgres.Queries(ctx, r.pool).AdminCountLandlordApplicationsByStatus(ctx)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make(map[domain.ApplicationStatus]int, len(rows))
	for _, v := range rows {
		out[domain.ApplicationStatus(v.Status)] = int(v.Total)
	}
	return out, nil
}

// History returns the other applications of the same person, newest first.
func (r *ApplicationRepository) History(ctx context.Context, userID, except uuid.UUID) ([]domain.Application, error) {
	rows, err := postgres.Queries(ctx, r.pool).AdminListLandlordApplicationHistory(ctx, sqlcgen.AdminListLandlordApplicationHistoryParams{UserID: userID, ExceptID: except})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.Application, len(rows))
	for i, v := range rows {
		out[i] = applicationOf(row(v))
	}
	return out, nil
}

// Review records the answer to an application.
func (r *ApplicationRepository) Review(ctx context.Context, id uuid.UUID, status domain.ApplicationStatus, reviewer uuid.UUID, note string, now time.Time) error {
	var n *string
	if note != "" {
		n = &note
	}
	return postgres.Translate(postgres.Queries(ctx, r.pool).AdminReviewLandlordApplication(ctx, sqlcgen.AdminReviewLandlordApplicationParams{
		ID: id, Status: string(status), ReviewerID: &reviewer, ReviewNote: n, Now: now,
	}))
}
