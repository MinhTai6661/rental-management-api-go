// Package planpg is the database side of the plan use cases: the plan and subscription repositories, and the
// read-only views of accounts and usage the plan rules need. It returns domain types and translates driver errors
// (see postgres.Translate).
package planpg

import (
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/plan"
)

// toInt32 and fromInt32 convert an optional limit; the database holds limits as 32-bit integers.
func toInt32(v *int) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v) //nolint:gosec // limits are validated to small numbers
	return &n
}

func fromInt32(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func planOf(r sqlcgen.Plan) domain.Plan {
	features := make([]domain.Feature, 0, len(r.Features))
	for _, f := range r.Features {
		features = append(features, domain.Feature(f))
	}
	return domain.Plan{
		ID: r.ID, Code: r.Code, Name: r.Name, Description: r.Description, Audience: domain.Audience(r.Audience),
		PriceVND: r.PriceVnd, Interval: domain.Interval(r.BillingInterval),
		Limits: domain.Limits{
			MaxRooms: fromInt32(r.MaxRooms), MaxBuildings: fromInt32(r.MaxBuildings), MaxImagesPerRoom: fromInt32(r.MaxImagesPerRoom),
			MaxActiveContracts: fromInt32(r.MaxActiveContracts), MaxPendingRegistrations: fromInt32(r.MaxPendingRegistrations),
		},
		SearchPriority: int(r.SearchPriority), Features: features, IsDefault: r.IsDefault, IsActive: r.IsActive,
		DisplayOrder: int(r.DisplayOrder), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func featureCodes(fs []domain.Feature) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, string(f))
	}
	return out
}

func subscriptionOf(r sqlcgen.Subscription) domain.Subscription {
	return domain.Subscription{
		ID: r.ID, UserID: r.UserID, PlanID: r.PlanID, Audience: domain.Audience(r.Audience), Status: domain.Status(r.Status),
		StartsAt: r.StartsAt, EndsAt: r.EndsAt, CanceledAt: r.CanceledAt, GrantedByID: r.GrantedByID, CreatedAt: r.CreatedAt,
	}
}

func detailOf(s sqlcgen.Subscription, p sqlcgen.Plan, userName string, userEmail *string) domain.Detail {
	return domain.Detail{Subscription: subscriptionOf(s), Plan: planOf(p), UserName: userName, UserEmail: userEmail}
}
