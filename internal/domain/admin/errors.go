// Package admin holds the rules of the platform's staff work: who may manage and give roles to whom, landlord
// applications and profiles, the overridable system settings, the published legal documents and the data subject's
// rights. It is plain Go: no database, no HTTP, no framework.
package admin

import (
	"errors"
	"fmt"
	"strings"
)

// The errors of the admin use cases. They say what went wrong in the domain's words; the HTTP adapter decides the
// status, the code and the fields the client sees. An account, application or document that does not exist is
// shared.ErrNotFound.
var (
	// ErrForbidden: the staff member may not do this to this account.
	ErrForbidden = errors.New("not allowed to do this to this account")
	// ErrRolesEmpty: an account must keep at least one role.
	ErrRolesEmpty = errors.New("an account needs at least one role")
	// ErrUnknownRole: a role that does not exist was to be given.
	ErrUnknownRole = errors.New("unknown role")
	// ErrStaffRolesMixed: a staff role was to be given together with another kind of role.
	ErrStaffRolesMixed = errors.New("staff roles cannot be mixed with other roles")
	// ErrNotLandlord: the account does not hold the landlord role.
	ErrNotLandlord = errors.New("account is not a landlord")
	// ErrAlreadyLandlord, ErrNotRenter and ErrAlreadyPending: why somebody may not apply to become a landlord.
	ErrAlreadyLandlord = errors.New("account is already a landlord")
	ErrNotRenter       = errors.New("only a tenant account can apply")
	ErrAlreadyPending  = errors.New("an application is already waiting")
	// ErrNotReviewable: the application was already answered.
	ErrNotReviewable = errors.New("application is not waiting for a review")
	// ErrUnknownKind: not a legal document kind.
	ErrUnknownKind = errors.New("unknown legal document kind")
	// ErrStaffCannotErase: staff accounts are not erased by their owners.
	ErrStaffCannotErase = errors.New("staff accounts cannot erase themselves")
	// ErrPasswordWrong: the password given does not match.
	ErrPasswordWrong = errors.New("password is incorrect")
	// ErrTooManyIDs and ErrNoIDs: a bulk request outside 1..MaxBulkIDs ids.
	ErrTooManyIDs = errors.New("too many ids")
	ErrNoIDs      = errors.New("no ids")
)

// MaxBulkIDs is the most accounts or applications one bulk request may name.
const MaxBulkIDs = 100

// HasActiveContractsError: the landlord still has running contracts, so the role cannot be taken away.
type HasActiveContractsError struct{ Count int }

func (e *HasActiveContractsError) Error() string {
	return fmt.Sprintf("landlord has %d running contracts", e.Count)
}

// ErasureBlockedError: the account is part of a contract that still runs, so it cannot be erased yet.
type ErasureBlockedError struct{ Blockers []string }

func (e *ErasureBlockedError) Error() string {
	return "account cannot be erased: " + strings.Join(e.Blockers, ", ")
}

// Violation is one failed rule of a field; Rule is a key of the API's validation messages.
type Violation struct {
	Name   string
	Rule   string
	Params map[string]string
}

// ValidationError is an input that broke one or more rules.
type ValidationError struct{ Violations []Violation }

func (e *ValidationError) Error() string {
	names := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		names[i] = v.Name + ":" + v.Rule
	}
	return "invalid input: " + strings.Join(names, ", ")
}

// violations collects the broken rules of an input and turns them into one error.
type violations []Violation

func (v *violations) add(name, rule string, params map[string]string) {
	*v = append(*v, Violation{Name: name, Rule: rule, Params: params})
}

func (v violations) err() error {
	if len(v) == 0 {
		return nil
	}
	return &ValidationError{Violations: v}
}

// Rule keys shared with the API's validation catalog.
const (
	ruleRequired  = "required"
	ruleMaxString = "max_string"
	ruleOneOf     = "oneof"
	ruleMinNumber = "min_number"
	ruleMaxNumber = "max_number"
	ruleInteger   = "integer"
)

func maxLen(n int) map[string]string { return map[string]string{"max": fmt.Sprint(n)} }

// Invalid is a ValidationError of one rule of one field.
func Invalid(name, rule string, params map[string]string) error {
	return &ValidationError{Violations: []Violation{{Name: name, Rule: rule, Params: params}}}
}

// RuleOneOf, RuleRequired and RuleMaxString name the generic rules the use cases refer to.
const (
	RuleOneOf     = ruleOneOf
	RuleRequired  = ruleRequired
	RuleMaxString = ruleMaxString
	RuleInteger   = ruleInteger
)
