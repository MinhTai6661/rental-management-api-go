package shared

import (
	"errors"
	"fmt"
	"testing"
)

func TestConstraintErrorKinds(t *testing.T) {
	unique := fmt.Errorf("create: %w", &ConstraintError{Kind: Unique, Constraint: "users_email_key", Err: errors.New("pg")})
	fk := &ConstraintError{Kind: ForeignKey, Constraint: "users_ward_code_fkey"}

	if !IsUnique(unique) || IsForeignKey(unique) {
		t.Error("a wrapped unique violation is unique, not a foreign key")
	}
	if !IsForeignKey(fk) || IsUnique(fk) {
		t.Error("a foreign key violation is not unique")
	}
	if IsUnique(errors.New("other")) || IsUnique(nil) || IsForeignKey(ErrNotFound) {
		t.Error("other errors are neither")
	}
	if !errors.Is(unique, unique.(interface{ Unwrap() error }).Unwrap()) {
		t.Error("the error must unwrap")
	}
	if unique.Error() == "" || fk.Error() == "" {
		t.Error("errors must describe themselves")
	}
}
