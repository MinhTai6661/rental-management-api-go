package notification

import (
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

// Limits of the criteria.
const (
	MaxUserIDs        = 50
	MaxActiveDays     = 90
	MaxRegisteredDays = 3650
	// PlanNone in PlanCodes means "people with no paid plan".
	PlanNone = "none"
)

// Criteria narrow who a platform message reaches. They are AND between the criteria and OR inside one criterion, and
// apply to the "everyone" and "custom" scopes only.
type Criteria struct {
	Roles         []string
	ProvinceCodes []int
	WardCodes     []int
	// PlanCodes are plan codes; PlanNone stands for "no plan".
	PlanCodes []string
	// RegisteredWithinDays: signed up in the last N days; RegisteredAtLeastDays: signed up at least N days ago.
	RegisteredWithinDays  *int
	RegisteredAtLeastDays *int
	// ActiveWithinDays: used a session in the last N days (at most MaxActiveDays).
	ActiveWithinDays *int
	// RegisteredBefore is a date: signed up before that day.
	RegisteredBefore *time.Time
	UserIDs          []uuid.UUID
}

// IsEmpty reports whether no criterion is set.
func (c Criteria) IsEmpty() bool {
	return len(c.Roles) == 0 && len(c.ProvinceCodes) == 0 && len(c.WardCodes) == 0 && len(c.PlanCodes) == 0 &&
		c.RegisteredWithinDays == nil && c.RegisteredAtLeastDays == nil && c.ActiveWithinDays == nil &&
		c.RegisteredBefore == nil && len(c.UserIDs) == 0
}

// Normalize cleans the criteria: only known roles, no repeats, no blank or non-positive values, day counts capped, at
// most MaxUserIDs ids; lists are sorted so the stored JSON is stable.
func (c Criteria) Normalize() Criteria {
	out := Criteria{
		Roles:                 cleanStrings(c.Roles, func(s string) bool { return authz.Seniority(s) > 0 }),
		ProvinceCodes:         cleanInts(c.ProvinceCodes),
		WardCodes:             cleanInts(c.WardCodes),
		PlanCodes:             cleanStrings(c.PlanCodes, func(string) bool { return true }),
		RegisteredWithinDays:  cleanDays(c.RegisteredWithinDays, MaxRegisteredDays),
		RegisteredAtLeastDays: cleanDays(c.RegisteredAtLeastDays, MaxRegisteredDays),
		ActiveWithinDays:      cleanDays(c.ActiveWithinDays, MaxActiveDays),
		RegisteredBefore:      c.RegisteredBefore,
	}
	seen := map[uuid.UUID]struct{}{}
	for _, id := range c.UserIDs {
		if id == uuid.Nil || len(out.UserIDs) >= MaxUserIDs {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out.UserIDs = append(out.UserIDs, id)
	}
	return out
}

func cleanStrings(in []string, keep func(string) bool) []string {
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || !keep(s) || slices.Contains(out, s) {
			continue
		}
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

func cleanInts(in []int) []int {
	var out []int
	for _, n := range in {
		if n <= 0 || slices.Contains(out, n) {
			continue
		}
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func cleanDays(n *int, limit int) *int {
	if n == nil || *n < 1 {
		return nil
	}
	v := min(*n, limit)
	return &v
}

// staffRoles are the roles that run the platform, most senior last.
func staffRoles() []string { return []string{authz.RoleAdmin, authz.RoleSuperAdmin} }

// StaffRolesBelow are the staff roles a sender of the role may write to: a super admin may name admins; nobody else may
// name any staff role.
func StaffRolesBelow(senderRole string) []string {
	if senderRole == authz.RoleSuperAdmin {
		return []string{authz.RoleAdmin}
	}
	return nil
}

// ExcludedStaffRoles are the staff roles a message of the sender never reaches: every staff role except the ones below
// the sender that the criteria name.
func ExcludedStaffRoles(senderRole string, c Criteria) []string {
	below := StaffRolesBelow(senderRole)
	var out []string
	for _, r := range staffRoles() {
		if slices.Contains(below, r) && slices.Contains(c.Roles, r) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// ReachesStaff reports whether the criteria name a staff role.
func (c Criteria) ReachesStaff() bool {
	for _, r := range c.Roles {
		if authz.IsStaff(r) {
			return true
		}
	}
	return false
}
