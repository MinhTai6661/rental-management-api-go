// Package shared holds the domain errors every context uses: a row that does not exist and a write a database
// constraint refused. The persistence adapter produces them, so a use case can react without knowing the database.
package shared

import "errors"

// ErrNotFound is what a repository returns when the row asked for does not exist.
var ErrNotFound = errors.New("not found")

// ConstraintKind tells which kind of database constraint refused a write.
type ConstraintKind int

const (
	// Unique is a unique constraint refusing a duplicate row.
	Unique ConstraintKind = iota + 1
	// ForeignKey is a foreign key pointing at a row that does not exist.
	ForeignKey
)

// ConstraintError is a write refused by a database constraint. Repositories return it so services can react
// (for example "email already taken") without knowing which database is underneath.
type ConstraintError struct {
	Kind       ConstraintKind
	Constraint string // the name of the constraint, as the database reports it
	Err        error  // the underlying driver error
}

func (e *ConstraintError) Error() string {
	kind := "unique"
	if e.Kind == ForeignKey {
		kind = "foreign key"
	}
	return kind + " constraint " + e.Constraint + " refused the row"
}

func (e *ConstraintError) Unwrap() error { return e.Err }

// IsUnique reports whether err is a unique constraint refusing a duplicate row.
func IsUnique(err error) bool { return isKind(err, Unique) }

// IsForeignKey reports whether err is a foreign key pointing at a row that does not exist.
func IsForeignKey(err error) bool { return isKind(err, ForeignKey) }

func isKind(err error, kind ConstraintKind) bool {
	var ce *ConstraintError
	return errors.As(err, &ce) && ce.Kind == kind
}
