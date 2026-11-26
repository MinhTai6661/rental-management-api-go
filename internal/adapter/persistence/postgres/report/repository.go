// Package reportpg is the Postgres adapter of the report context: the reports, the conversations under them, and the
// read-only views of rooms and admins the report rules need.
package reportpg

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/report"
)

// ReportRepository reads and writes reports and their replies.
type ReportRepository struct{ pool *pgxpool.Pool }

// NewReportRepository returns a repository on the pool.
func NewReportRepository(pool *pgxpool.Pool) *ReportRepository { return &ReportRepository{pool: pool} }

// Insert stores a report (status open) and returns its id.
func (r *ReportRepository) Insert(ctx context.Context, n domain.NewReport) (uuid.UUID, error) {
	id, err := postgres.Queries(ctx, r.pool).InsertReport(ctx, sqlcgen.InsertReportParams{
		ReporterID: n.ReporterID, Kind: string(n.Kind), RoomID: n.RoomID, Subject: n.Subject, Body: n.Body, At: n.At,
	})
	return id, postgres.Translate(err)
}

// Get returns the report as a reader sees it (a deleted one too).
func (r *ReportRepository) Get(ctx context.Context, id uuid.UUID) (domain.View, error) {
	row, err := postgres.Queries(ctx, r.pool).GetReportDetail(ctx, id)
	if err != nil {
		return domain.View{}, postgres.Translate(err)
	}
	return viewOf(row), nil
}

// Lock returns the report and holds its row until the transaction ends.
func (r *ReportRepository) Lock(ctx context.Context, id uuid.UUID) (domain.Report, error) {
	row, err := postgres.Queries(ctx, r.pool).LockReport(ctx, id)
	if err != nil {
		return domain.Report{}, postgres.Translate(err)
	}
	return domain.Report{
		ID: row.ID, ReporterID: row.ReporterID, Kind: domain.Kind(row.Kind), RoomID: row.RoomID, RoomLandlordID: row.RoomLandlordID,
		Subject: row.Subject, Body: row.Body, Status: domain.Status(row.Status), CreatedAt: row.CreatedAt, DeletedAt: row.DeletedAt,
	}, nil
}

// SetStatus writes where the report stands.
func (r *ReportRepository) SetStatus(ctx context.Context, id uuid.UUID, status domain.Status, at time.Time) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).SetReportStatus(ctx, sqlcgen.SetReportStatusParams{ID: id, Status: string(status), At: at}))
}

// SoftDelete hides the report from its author.
func (r *ReportRepository) SoftDelete(ctx context.Context, id uuid.UUID, at time.Time) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).SoftDeleteReport(ctx, sqlcgen.SoftDeleteReportParams{ID: id, At: at}))
}

// ListMine returns a page of the author's reports that are not deleted, newest first, and how many match.
func (r *ReportRepository) ListMine(ctx context.Context, authorID uuid.UUID, f domain.Filter, limit, offset int) ([]domain.View, int, error) {
	q := postgres.Queries(ctx, r.pool)
	status, kind, search := filterArgs(f)
	rows, err := q.ListMyReports(ctx, sqlcgen.ListMyReportsParams{
		AuthorID: authorID, Status: status, Kind: kind, Search: search, PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountMyReports(ctx, sqlcgen.CountMyReportsParams{AuthorID: authorID, Status: status, Kind: kind, Search: search})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	return viewsOf(rows), int(total), nil
}

// ListInbox returns a page of the reports in the scope, newest first, and how many match.
func (r *ReportRepository) ListInbox(ctx context.Context, scope domain.InboxScope, f domain.Filter, limit, offset int) ([]domain.View, int, error) {
	q := postgres.Queries(ctx, r.pool)
	status, kind, search := filterArgs(f)
	rows, err := q.ListReportInbox(ctx, sqlcgen.ListReportInboxParams{
		Everything: scope.Everything, Features: scope.Features, LandlordID: scope.LandlordID,
		Status: status, Kind: kind, Search: search, PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountReportInbox(ctx, sqlcgen.CountReportInboxParams{
		Everything: scope.Everything, Features: scope.Features, LandlordID: scope.LandlordID, Status: status, Kind: kind, Search: search,
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	return viewsOf(rows), int(total), nil
}

// InsertReply stores a message and returns it with its author.
func (r *ReportRepository) InsertReply(ctx context.Context, reportID, authorID uuid.UUID, body string, at time.Time) (domain.Reply, error) {
	q := postgres.Queries(ctx, r.pool)
	id, err := q.InsertReportReply(ctx, sqlcgen.InsertReportReplyParams{ReportID: reportID, AuthorID: authorID, Body: body, At: at})
	if err != nil {
		return domain.Reply{}, postgres.Translate(err)
	}
	row, err := q.GetReportReplyDetail(ctx, id)
	if err != nil {
		return domain.Reply{}, postgres.Translate(err)
	}
	return replyOf(row), nil
}

// ListReplies returns a page of the conversation, newest first, and how many messages there are.
func (r *ReportRepository) ListReplies(ctx context.Context, reportID uuid.UUID, limit, offset int) ([]domain.Reply, int, error) {
	q := postgres.Queries(ctx, r.pool)
	rows, err := q.ListReportReplyDetails(ctx, sqlcgen.ListReportReplyDetailsParams{ReportID: reportID, PageLimit: int32(limit), PageOffset: int32(offset)}) //nolint:gosec // bounded by ParsePage
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountReportReplies(ctx, reportID)
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.Reply, len(rows))
	for i, row := range rows {
		out[i] = replyOf(row)
	}
	return out, int(total), nil
}

func viewsOf(rows []sqlcgen.ReportDetail) []domain.View {
	out := make([]domain.View, len(rows))
	for i, row := range rows {
		out[i] = viewOf(row)
	}
	return out
}

func viewOf(row sqlcgen.ReportDetail) domain.View {
	v := domain.View{
		Report: domain.Report{
			ID: row.ID, ReporterID: row.ReporterID, Kind: domain.Kind(row.Kind), RoomID: row.RoomID, RoomLandlordID: row.RoomLandlordID,
			Subject: row.Subject, Body: row.Body, Status: domain.Status(row.Status), CreatedAt: row.CreatedAt, DeletedAt: row.DeletedAt,
		},
		Reporter:     domain.Person{ID: row.ReporterID, Name: row.ReporterName, IsStaff: row.ReporterIsStaff, IsLandlord: row.ReporterIsLandlord},
		RepliesCount: int(row.RepliesCount),
	}
	if row.RoomName != nil {
		v.RoomName = *row.RoomName
	}
	return v
}

func replyOf(row sqlcgen.ReportReplyDetail) domain.Reply {
	return domain.Reply{
		ID: row.ID, ReportID: row.ReportID, Body: row.Body, CreatedAt: row.CreatedAt,
		Author: domain.Person{ID: row.AuthorID, Name: row.AuthorName, IsStaff: row.AuthorIsStaff, IsLandlord: row.AuthorIsLandlord},
	}
}

// filterArgs turns a filter into the optional query arguments; the search word is lowercased and its LIKE wildcards
// escaped.
func filterArgs(f domain.Filter) (status, kind, search *string) {
	if f.Status != nil {
		s := string(*f.Status)
		status = &s
	}
	if f.Kind != nil {
		k := string(*f.Kind)
		kind = &k
	}
	if term := strings.TrimSpace(f.Search); term != "" {
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(term))
		search = &escaped
	}
	return status, kind, search
}
