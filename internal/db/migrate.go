package db

import (
	"context"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"rental-management/db/migrations"
)

// Migrate runs a goose command against the pool's database: "up" applies every pending migration, "down" rolls back
// the latest one, "status" prints what is applied. A Postgres advisory lock keeps two instances from migrating at once.
// Progress is written to out.
func Migrate(ctx context.Context, pool *pgxpool.Pool, command string, out io.Writer) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("create migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}

	switch command {
	case "up":
		results, err := provider.Up(ctx)
		if err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		if len(results) == 0 {
			_, _ = fmt.Fprintln(out, "nothing to migrate")
		}
		for _, r := range results {
			_, _ = fmt.Fprintf(out, "applied %s (%s)\n", r.Source.Path, r.Duration)
		}
	case "down":
		r, err := provider.Down(ctx)
		if err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
		_, _ = fmt.Fprintf(out, "rolled back %s (%s)\n", r.Source.Path, r.Duration)
	case "status":
		statuses, err := provider.Status(ctx)
		if err != nil {
			return fmt.Errorf("migration status: %w", err)
		}
		for _, s := range statuses {
			state := "pending"
			if s.State == goose.StateApplied {
				state = "applied " + s.AppliedAt.Format("2006-01-02 15:04:05")
			}
			_, _ = fmt.Fprintf(out, "%-40s %s\n", s.Source.Path, state)
		}
	default:
		return fmt.Errorf("unknown migrate command %q (want up|down|status)", command)
	}
	return nil
}
