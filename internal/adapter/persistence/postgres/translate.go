package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"rental-management/internal/domain/shared"
)

// Postgres error codes of a constraint that refused a row.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
)

// translate turns a driver error into a domain error: no rows becomes shared.ErrNotFound, a unique or foreign key
// violation a *shared.ConstraintError. Any other error is returned as it is.
func translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return shared.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case sqlStateUniqueViolation:
			return &shared.ConstraintError{Kind: shared.Unique, Constraint: pgErr.ConstraintName, Err: err}
		case sqlStateForeignKeyViolation:
			return &shared.ConstraintError{Kind: shared.ForeignKey, Constraint: pgErr.ConstraintName, Err: err}
		}
	}
	return err
}
