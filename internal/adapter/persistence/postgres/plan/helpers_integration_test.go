//go:build integration

package planpg

import (
	"github.com/google/uuid"

	domain "rental-management/internal/domain/plan"
)

func usecaseQuery(a *domain.Audience, activeOnly bool, limit, offset int) domain.PlanQuery {
	return domain.PlanQuery{Audience: a, ActiveOnly: activeOnly, Limit: limit, Offset: offset}
}

func subQuery(status *domain.Status, a *domain.Audience, user *uuid.UUID, limit, offset int) domain.SubscriptionQuery {
	return domain.SubscriptionQuery{Status: status, Audience: a, UserID: user, Limit: limit, Offset: offset}
}
