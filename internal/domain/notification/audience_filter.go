package notification

import (
	"slices"
	"time"

	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

// AudienceFilter is a fully resolved definition of who a message goes to: exactly the parameters of the SQL function
// announcement_audience, which the count, the delivery and the "does this person belong" check all call, so they cannot
// disagree. Every decision (who may be reached, which dates) is made in ResolveFilter; the database only evaluates it.
type AudienceFilter struct {
	// Scope is 'all', 'landlords', 'tenants', 'my_tenants' (running confirmed contracts of LandlordID) or 'picked'
	// (my_tenants restricted to Picked).
	Scope      string
	LandlordID *uuid.UUID
	Picked     []uuid.UUID
	// Roles nil = no role criterion.
	Roles []string
	// ExcludedRoles are never reached (the staff the sender may not write to).
	ExcludedRoles                            []string
	ProvinceCodes, WardCodes                 []int
	PlanCodes                                []string
	PlanNone                                 bool
	CreatedFrom, CreatedUntil, CreatedBefore *time.Time
	ActiveSince                              *time.Time
	UserIDs                                  []uuid.UUID
	Now                                      time.Time
}

var landlordExcludedRoles = []string{authz.RoleAdmin, authz.RoleSuperAdmin}

// ResolveFilter turns an audience into the filter the database evaluates. It refuses a scope the sender may not use
// and a custom scope without criteria. Criteria apply to "everyone" and "custom" only; staff are reached only when the
// criteria name them and they are below the sender (D10).
func ResolveFilter(scope Scope, sender Sender, c Criteria, picked []uuid.UUID, now time.Time) (AudienceFilter, error) {
	kind := sender.Kind()
	if kind == "" || !scope.AllowedFor(kind) {
		return AudienceFilter{}, ErrScopeNotAllowed
	}
	f := AudienceFilter{Now: now, ExcludedRoles: landlordExcludedRoles}
	switch scope {
	case ScopeLandlords:
		f.Scope = "landlords"
	case ScopeTenants:
		f.Scope = "tenants"
	case ScopeMyTenants, ScopeMyPeople:
		f.Scope = "my_tenants"
	case ScopeSelectedPeople:
		f.Scope = "picked"
		f.Picked = append([]uuid.UUID{}, picked...)
	default:
		f.Scope = "all"
	}
	if kind == SenderLandlord {
		id := sender.UserID
		f.LandlordID = &id
		return f, nil
	}

	// Platform senders: the criteria narrow "everyone" and "custom", and decide which staff may be reached.
	crit := Criteria{}
	if scope == ScopeEveryone || scope == ScopeCustom {
		crit = c.Normalize()
	}
	if scope == ScopeCustom && crit.IsEmpty() {
		return AudienceFilter{}, ErrAudienceEmpty
	}
	f.ExcludedRoles = ExcludedStaffRoles(sender.Role, crit)
	if len(crit.Roles) > 0 {
		f.Roles = crit.Roles
	}
	if len(crit.ProvinceCodes) > 0 {
		f.ProvinceCodes = crit.ProvinceCodes
	}
	if len(crit.WardCodes) > 0 {
		f.WardCodes = crit.WardCodes
	}
	for _, code := range crit.PlanCodes {
		if code == PlanNone {
			f.PlanNone = true
			continue
		}
		f.PlanCodes = append(f.PlanCodes, code)
	}
	if crit.RegisteredWithinDays != nil {
		t := now.AddDate(0, 0, -*crit.RegisteredWithinDays)
		f.CreatedFrom = &t
	}
	if crit.RegisteredAtLeastDays != nil {
		t := now.AddDate(0, 0, -*crit.RegisteredAtLeastDays)
		f.CreatedUntil = &t
	}
	if crit.RegisteredBefore != nil {
		t := *crit.RegisteredBefore
		f.CreatedBefore = &t
	}
	if crit.ActiveWithinDays != nil {
		t := now.AddDate(0, 0, -*crit.ActiveWithinDays)
		f.ActiveSince = &t
	}
	if len(crit.UserIDs) > 0 {
		f.UserIDs = slices.Clone(crit.UserIDs)
	}
	return f, nil
}
