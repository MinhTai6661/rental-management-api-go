package adminpg

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/admin"
)

// LegalRepository stores the published legal documents (only ever added to) and the terms an account accepted.
type LegalRepository struct{ pool *pgxpool.Pool }

// NewLegalRepository returns a repository on the pool.
func NewLegalRepository(pool *pgxpool.Pool) *LegalRepository { return &LegalRepository{pool: pool} }

// docRow is the shape every query of a document returns.
type docRow = sqlcgen.AdminGetLegalDocumentRow

func documentOf(r docRow) (domain.Document, error) {
	d := domain.Document{
		ID: r.ID, Kind: domain.LegalKind(r.Kind), Version: r.Version, PublishedByID: r.PublishedByID,
		PublishedByName: r.PublishedByName, PublishedAt: r.CreatedAt,
	}
	if err := json.Unmarshal(r.Content, &d.Content); err != nil {
		return d, err
	}
	return d, json.Unmarshal(r.ChangeSummary, &d.Summary)
}

func documentsOf[T ~struct {
	ID              uuid.UUID
	Kind            string
	Version         string
	Content         []byte
	ChangeSummary   []byte
	PublishedByID   *uuid.UUID
	CreatedAt       time.Time
	PublishedByName string
}](rows []T) ([]domain.Document, error) {
	out := make([]domain.Document, len(rows))
	for i, v := range rows {
		d, err := documentOf(docRow(v))
		if err != nil {
			return nil, err
		}
		out[i] = d
	}
	return out, nil
}

// Insert adds a new version; a version already published for the kind is a unique ConstraintError.
func (r *LegalRepository) Insert(ctx context.Context, d domain.Document, now time.Time) error {
	content, err := json.Marshal(d.Content)
	if err != nil {
		return err
	}
	summary, err := json.Marshal(d.Summary)
	if err != nil {
		return err
	}
	return postgres.Translate(postgres.Queries(ctx, r.pool).AdminInsertLegalDocument(ctx, sqlcgen.AdminInsertLegalDocumentParams{
		ID: d.ID, Kind: string(d.Kind), Version: d.Version, Content: content, ChangeSummary: summary,
		PublishedByID: d.PublishedByID, Now: now,
	}))
}

// Latest returns the newest version of a document, or shared.ErrNotFound.
func (r *LegalRepository) Latest(ctx context.Context, kind domain.LegalKind) (domain.Document, error) {
	v, err := postgres.Queries(ctx, r.pool).AdminLatestLegalDocument(ctx, string(kind))
	if err != nil {
		return domain.Document{}, postgres.Translate(err)
	}
	return documentOf(docRow(v))
}

// LatestVersion is the version of the newest document of any kind, or shared.ErrNotFound.
func (r *LegalRepository) LatestVersion(ctx context.Context) (string, error) {
	v, err := postgres.Queries(ctx, r.pool).AdminLatestLegalVersion(ctx)
	return v, postgres.Translate(err)
}

// Get returns one version of a document, or shared.ErrNotFound when the id belongs to the other kind.
func (r *LegalRepository) Get(ctx context.Context, kind domain.LegalKind, id uuid.UUID) (domain.Document, error) {
	v, err := postgres.Queries(ctx, r.pool).AdminGetLegalDocument(ctx, sqlcgen.AdminGetLegalDocumentParams{ID: id, Kind: string(kind)})
	if err != nil {
		return domain.Document{}, postgres.Translate(err)
	}
	return documentOf(v)
}

// List returns a page of the versions of a document, newest first, and how many there are.
func (r *LegalRepository) List(ctx context.Context, kind domain.LegalKind, limit, offset int) ([]domain.Document, int, error) {
	q := postgres.Queries(ctx, r.pool)
	total, err := q.AdminCountLegalDocuments(ctx, string(kind))
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	rows, err := q.AdminListLegalDocuments(ctx, sqlcgen.AdminListLegalDocumentsParams{
		Kind: string(kind), PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by the use case
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	docs, err := documentsOf(rows)
	return docs, int(total), err
}

// After returns the versions published after the time, newest first.
func (r *LegalRepository) After(ctx context.Context, since time.Time, limit int) ([]domain.Document, error) {
	rows, err := postgres.Queries(ctx, r.pool).AdminListLegalDocumentsAfter(ctx, sqlcgen.AdminListLegalDocumentsAfterParams{
		Since: since, PageLimit: int32(limit), //nolint:gosec // bounded by the use case
	})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	return documentsOf(rows)
}

// NewestPerKind returns the newest version of each kind.
func (r *LegalRepository) NewestPerKind(ctx context.Context) ([]domain.Document, error) {
	rows, err := postgres.Queries(ctx, r.pool).AdminLatestLegalDocumentPerKind(ctx)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	return documentsOf(rows)
}

// AcceptedAt is when the account last accepted the terms (nil: never).
func (r *LegalRepository) AcceptedAt(ctx context.Context, userID uuid.UUID) (*time.Time, error) {
	t, err := postgres.Queries(ctx, r.pool).AdminGetUserTerms(ctx, userID)
	return t, postgres.Translate(err)
}

// SetAccepted records that the account accepted the version.
func (r *LegalRepository) SetAccepted(ctx context.Context, userID uuid.UUID, version string, now time.Time) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).AdminSetUserTerms(ctx, sqlcgen.AdminSetUserTermsParams{ID: userID, Version: &version, Now: now}))
}
