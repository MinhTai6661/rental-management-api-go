// Package auditpg is the database side of the audit trail: the two partitioned tables (audit_logs and audit_activity)
// and the monthly partitions. It returns domain types and translates driver errors (see postgres.Translate). Partitions
// are made and dropped with plain DDL, the one place this adapter does not go through the generated queries (a
// statement cannot take a table name as a parameter).
package auditpg

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/audit"
	"rental-management/internal/domain/shared"
)

// Repository reads and writes the trail.
type Repository struct{ pool *pgxpool.Pool }

// NewRepository returns a repository on the pool.
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// row is one entry as the insert statement reads it from its JSON array.
type row struct {
	CreatedAt    time.Time       `json:"created_at"`
	ActorID      *uuid.UUID      `json:"actor_id"`
	ActorRole    *string         `json:"actor_role"`
	ActingForID  *uuid.UUID      `json:"acting_for_id"`
	Action       string          `json:"action"`
	RoomID       *uuid.UUID      `json:"room_id"`
	ContractID   *uuid.UUID      `json:"contract_id"`
	InvoiceID    *uuid.UUID      `json:"invoice_id"`
	PaymentID    *uuid.UUID      `json:"payment_id"`
	TargetUserID *uuid.UUID      `json:"target_user_id"`
	Changes      json.RawMessage `json:"changes"`
	Meta         json.RawMessage `json:"meta"`
	IP           *string         `json:"ip"`
	UserAgent    *string         `json:"user_agent"`
	SessionID    *uuid.UUID      `json:"session_id"`
	RequestID    *string         `json:"request_id"`
}

// Append writes the entries, one statement per table; isolated puts the write in a savepoint (see postgres.Isolated).
func (r *Repository) Append(ctx context.Context, entries []domain.Entry, isolated bool) error {
	var important, routine []row
	for _, e := range entries {
		out, err := rowOf(e)
		if err != nil {
			return err
		}
		if e.Action.Tier() == domain.Routine {
			routine = append(routine, out)
		} else {
			important = append(important, out)
		}
	}
	write := func(ctx context.Context) error {
		q := postgres.Queries(ctx, r.pool)
		if len(important) > 0 {
			raw, err := json.Marshal(important)
			if err != nil {
				return err
			}
			if err := q.InsertAuditLogs(ctx, raw); err != nil {
				return postgres.Translate(err)
			}
		}
		if len(routine) > 0 {
			raw, err := json.Marshal(routine)
			if err != nil {
				return err
			}
			return postgres.Translate(q.InsertAuditActivity(ctx, raw))
		}
		return nil
	}
	if isolated {
		return postgres.Isolated(ctx, write)
	}
	return write(ctx)
}

func rowOf(e domain.Entry) (row, error) {
	out := row{
		CreatedAt: e.OccurredAt, ActorID: e.ActorID, ActingForID: e.ActingFor, Action: string(e.Action),
		RoomID: e.Refs.RoomID, ContractID: e.Refs.ContractID, InvoiceID: e.Refs.InvoiceID, PaymentID: e.Refs.PaymentID,
		TargetUserID: e.Refs.TargetUserID, ActorRole: nonEmpty(e.ActorRole), IP: nonEmpty(e.IP), UserAgent: nonEmpty(e.UserAgent),
		SessionID: e.SessionID, RequestID: nonEmpty(e.RequestID),
	}
	var err error
	if out.Changes, err = payload(e.Changes); err != nil {
		return row{}, err
	}
	if out.Meta, err = payload(e.Meta); err != nil {
		return row{}, err
	}
	return out, nil
}

// payload is the JSON of a changes or meta map; nothing is JSON null, which the insert stores as NULL.
func payload(m map[string]any) (json.RawMessage, error) {
	if len(m) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(m)
	return raw, err
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Search returns the newest entries first from the tiers the query asks for.
func (r *Repository) Search(ctx context.Context, q domain.Query) ([]domain.Record, error) {
	f := q.Filter
	p := sqlcgen.SearchAuditEntriesParams{
		IncludeImportant: q.Important, IncludeRoutine: q.Routine,
		ActorID: f.ActorID, TargetUserID: f.TargetUserID, RoomID: f.RoomID, ContractID: f.ContractID, InvoiceID: f.InvoiceID,
		FromAt: q.From, BeforeAt: q.Before, Involving: q.Involving,
		ExcludeStaff: q.ExcludeStaff, ExcludeSettings: q.ExcludeSettings,
		BranchLimit: int32(q.BranchLimit), PageLimit: int32(q.Limit), PageOffset: int32(q.Offset), //nolint:gosec // bounded by the use case
	}
	if f.ActorRole != "" {
		p.ActorRole = &f.ActorRole
	}
	if f.Action != nil {
		a := string(*f.Action)
		p.Action = &a
	}
	rows, err := postgres.Queries(ctx, r.pool).SearchAuditEntries(ctx, p)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.Record, 0, len(rows))
	for _, x := range rows {
		rec, err := recordOf(x)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func recordOf(x sqlcgen.SearchAuditEntriesRow) (domain.Record, error) {
	rec := domain.Record{
		ID: x.ID, Tier: domain.Tier(x.Tier), ActorName: deref(x.ActorName), TargetName: deref(x.TargetName),
		Entry: domain.Entry{
			Action: domain.Action(x.Action), ActorID: x.ActorID, ActorRole: deref(x.ActorRole), ActingFor: x.ActingForID,
			Refs: domain.References{
				RoomID: x.RoomID, ContractID: x.ContractID, InvoiceID: x.InvoiceID, PaymentID: x.PaymentID, TargetUserID: x.TargetUserID,
			},
			IP: deref(x.Ip), UserAgent: deref(x.UserAgent), SessionID: x.SessionID, RequestID: deref(x.RequestID), OccurredAt: x.CreatedAt,
		},
	}
	var err error
	if rec.Changes, err = decode(x.Changes); err != nil {
		return domain.Record{}, err
	}
	if rec.Meta, err = decode(x.Meta); err != nil {
		return domain.Record{}, err
	}
	return rec, nil
}

func decode(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode audit payload: %w", err)
	}
	return m, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ScrubPersonalData blanks the address, device and session of every entry the person made, in both tables.
func (r *Repository) ScrubPersonalData(ctx context.Context, userID uuid.UUID) error {
	q := postgres.Queries(ctx, r.pool)
	if err := q.ScrubAuditLogsOfUser(ctx, &userID); err != nil {
		return postgres.Translate(err)
	}
	return postgres.Translate(q.ScrubAuditActivityOfUser(ctx, &userID))
}

// OwnerOf is the landlord of a room, a contract or an invoice, or shared.ErrNotFound.
func (r *Repository) OwnerOf(ctx context.Context, subject domain.Subject, id uuid.UUID) (uuid.UUID, error) {
	q := postgres.Queries(ctx, r.pool)
	var (
		owner uuid.UUID
		err   error
	)
	switch subject {
	case domain.SubjectRoom:
		owner, err = q.AuditRoomOwner(ctx, id)
	case domain.SubjectContract:
		owner, err = q.AuditContractOwner(ctx, id)
	case domain.SubjectInvoice:
		owner, err = q.AuditInvoiceOwner(ctx, id)
	default:
		return uuid.Nil, shared.ErrNotFound
	}
	return owner, postgres.Translate(err)
}

// CreatePartition makes the partition of table for the month (nothing happens when it exists). It fails when rows of
// that month already sit in the default partition.
func (r *Repository) CreatePartition(ctx context.Context, table string, month time.Time) error {
	if table != domain.TableImportant && table != domain.TableRoutine {
		return fmt.Errorf("not an audit table: %q", table)
	}
	from := domain.MonthStart(month)
	stmt := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')",
		pgx.Identifier{domain.PartitionName(table, from)}.Sanitize(), pgx.Identifier{table}.Sanitize(),
		from.Format(time.RFC3339), from.AddDate(0, 1, 0).Format(time.RFC3339))
	_, err := r.pool.Exec(ctx, stmt)
	return postgres.Translate(err)
}

// Partitions lists the partitions of both tables.
func (r *Repository) Partitions(ctx context.Context) ([]domain.Partition, error) {
	rows, err := r.pool.Query(ctx, `SELECT parent.relname, child.relname
		FROM pg_inherits i
		JOIN pg_class child ON child.oid = i.inhrelid
		JOIN pg_class parent ON parent.oid = i.inhparent
		JOIN pg_namespace ns ON ns.oid = parent.relnamespace
		WHERE ns.nspname = current_schema() AND parent.relname IN ($1, $2)
		ORDER BY child.relname`, domain.TableImportant, domain.TableRoutine)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	defer rows.Close()
	var out []domain.Partition
	for rows.Next() {
		var p domain.Partition
		if err := rows.Scan(&p.Table, &p.Name); err != nil {
			return nil, postgres.Translate(err)
		}
		p.Month, _ = domain.ParsePartition(p.Table, p.Name)
		out = append(out, p)
	}
	return out, postgres.Translate(rows.Err())
}

// DropPartition removes a monthly partition of the trail and its rows. The default partitions and any other table are
// refused.
func (r *Repository) DropPartition(ctx context.Context, name string) error {
	for _, table := range []string{domain.TableImportant, domain.TableRoutine} {
		if _, ok := domain.ParsePartition(table, name); ok {
			_, err := r.pool.Exec(ctx, "DROP TABLE "+pgx.Identifier{name}.Sanitize())
			return postgres.Translate(err)
		}
	}
	return fmt.Errorf("not a monthly partition of the audit trail: %q", name)
}
