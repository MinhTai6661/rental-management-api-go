package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/config"
	auditpg "rental-management/internal/adapter/persistence/postgres/audit"
	audituc "rental-management/internal/usecase/audit"
	"rental-management/pkg/logger"
)

// auditing is the use cases of the audit trail, on Postgres.
type auditing struct {
	recorder   *audituc.Recorder
	reader     *audituc.Reader
	maintainer *audituc.Maintainer
}

// newAuditRecorder builds the recorder the other contexts write through. It holds nothing but the pool, so every
// constructor makes its own: an action then lands in the trail whichever way the use cases were built (API, command, seed).
func newAuditRecorder(log *slog.Logger, pool *pgxpool.Pool) *audituc.Recorder {
	return audituc.NewRecorder(auditpg.NewRepository(pool), audituc.ClockFunc(time.Now), log)
}

// newAudit builds the readers of the trail and the maintenance of its partitions.
func newAudit(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool) auditing {
	repo := auditpg.NewRepository(pool)
	clock := audituc.ClockFunc(time.Now)
	return auditing{
		recorder: audituc.NewRecorder(repo, clock, log),
		reader:   audituc.NewReader(repo, repo),
		maintainer: audituc.NewMaintainer(repo, clock, log, audituc.RetentionOptions{
			PartitionsAhead: cfg.AuditPartitionsAhead, ImportantMonths: cfg.AuditImportantRetentionMonths, RoutineDays: cfg.AuditRoutineRetentionDays,
		}),
	}
}

// maintainAudit makes the coming months' partitions and drops the expired ones; it is the job of
// `audit-maintain` and of the worker.
func maintainAudit(ctx context.Context, a auditing) (string, error) {
	r, err := a.maintainer.Run(ctx)
	return fmt.Sprintf("created %d partitions, dropped %d", r.Created, r.Dropped), err
}

// runAuditMaintain runs `rental-management audit-maintain`: it makes the partitions of the audit tables for the
// current month and AUDIT_PARTITIONS_AHEAD more, and drops whole the partitions past the retention of their tier
// (AUDIT_IMPORTANT_RETENTION_MONTHS, AUDIT_ROUTINE_RETENTION_DAYS). Safe to run again and at the same time as itself.
func runAuditMaintain(ctx context.Context, out io.Writer) error {
	cfg, err := loadWithDatabase()
	if err != nil {
		return err
	}
	pool, err := openPool(ctx, cfg, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	log, err := logger.New(os.Stderr, cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return err
	}
	summary, err := maintainAudit(ctx, newAudit(cfg, log, pool))
	_, _ = fmt.Fprintln(out, summary)
	return err
}
