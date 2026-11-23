package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"rental-management/internal/domain/shared"
)

func TestTranslate(t *testing.T) {
	if translate(nil) != nil {
		t.Error("nil stays nil")
	}
	if err := translate(fmt.Errorf("get: %w", pgx.ErrNoRows)); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("no rows is not found, got %v", err)
	}

	unique := translate(fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505", ConstraintName: "users_email_key"}))
	var ce *shared.ConstraintError
	if !shared.IsUnique(unique) || !errors.As(unique, &ce) || ce.Constraint != "users_email_key" {
		t.Errorf("a unique violation becomes a ConstraintError, got %v", unique)
	}
	var pgErr *pgconn.PgError
	if !errors.As(unique, &pgErr) {
		t.Error("the driver error stays reachable through Unwrap")
	}
	if fk := translate(&pgconn.PgError{Code: "23503"}); !shared.IsForeignKey(fk) {
		t.Errorf("a foreign key violation becomes a ConstraintError, got %v", fk)
	}

	other := &pgconn.PgError{Code: "57014"}
	if !errors.Is(translate(other), other) {
		t.Error("other errors are returned as they are")
	}
	again := translate(unique)
	if !shared.IsUnique(again) {
		t.Error("translating twice must not lose the kind")
	}
}
