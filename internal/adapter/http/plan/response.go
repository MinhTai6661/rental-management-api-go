package planhttp

import (
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/plan"
	usecase "rental-management/internal/usecase/plan"
)

// The bodies of the plan responses. The json tags are the contract with the web app. A limit of null is unlimited.

// LimitsView is what a plan caps.
type LimitsView struct {
	MaxRooms                *int `json:"max_rooms"`
	MaxBuildings            *int `json:"max_buildings"`
	MaxImagesPerRoom        *int `json:"max_images_per_room"`
	MaxActiveContracts      *int `json:"max_active_contracts"`
	MaxPendingRegistrations *int `json:"max_pending_registrations"`
}

// PlanView is a plan of the catalog.
type PlanView struct {
	ID              uuid.UUID  `json:"id"`
	Code            string     `json:"code"`
	Name            string     `json:"name"`
	Description     *string    `json:"description"`
	Audience        string     `json:"audience"`
	PriceVND        int64      `json:"price_vnd"`
	BillingInterval string     `json:"billing_interval"`
	Limits          LimitsView `json:"limits"`
	SearchPriority  int        `json:"search_priority"`
	Features        []string   `json:"features"`
	IsDefault       bool       `json:"is_default"`
	IsActive        bool       `json:"is_active"`
	DisplayOrder    int        `json:"display_order"`
}

// UserView names the owner of a subscription in the admin's list.
type UserView struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"full_name"`
	Email    *string   `json:"email"`
}

// SubscriptionView is a subscription with its plan. The user is only there in the admin's list.
type SubscriptionView struct {
	ID          uuid.UUID  `json:"id"`
	Status      string     `json:"status"`
	Audience    string     `json:"audience"`
	StartsAt    time.Time  `json:"starts_at"`
	EndsAt      *time.Time `json:"ends_at"`
	GrantedByID *uuid.UUID `json:"granted_by_id"`
	Plan        PlanView   `json:"plan"`
	User        *UserView  `json:"user,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// MineView is the user's own plan: what they are on, the running subscription, a paid request waiting for an admin and
// what they have used of the plan's limits.
type MineView struct {
	Plan           *PlanView         `json:"plan"`
	Subscription   *SubscriptionView `json:"subscription"`
	PendingRequest *SubscriptionView `json:"pending_request"`
	Usage          map[string]int    `json:"usage"`
}

func planViewOf(p domain.Plan) PlanView {
	features := make([]string, len(p.Features))
	for i, f := range p.Features {
		features[i] = string(f)
	}
	return PlanView{
		ID: p.ID, Code: p.Code, Name: p.Name, Description: p.Description, Audience: string(p.Audience), PriceVND: p.PriceVND,
		BillingInterval: string(p.Interval), SearchPriority: p.SearchPriority, Features: features, IsDefault: p.IsDefault,
		IsActive: p.IsActive, DisplayOrder: p.DisplayOrder,
		Limits: LimitsView{
			MaxRooms: p.Limits.MaxRooms, MaxBuildings: p.Limits.MaxBuildings, MaxImagesPerRoom: p.Limits.MaxImagesPerRoom,
			MaxActiveContracts: p.Limits.MaxActiveContracts, MaxPendingRegistrations: p.Limits.MaxPendingRegistrations,
		},
	}
}

func planViewsOf(ps []domain.Plan) []PlanView {
	out := make([]PlanView, len(ps))
	for i, p := range ps {
		out[i] = planViewOf(p)
	}
	return out
}

func subscriptionViewOf(d domain.Detail, withUser bool) SubscriptionView {
	v := SubscriptionView{
		ID: d.ID, Status: string(d.Status), Audience: string(d.Audience), StartsAt: d.StartsAt, EndsAt: d.EndsAt,
		GrantedByID: d.GrantedByID, Plan: planViewOf(d.Plan), CreatedAt: d.CreatedAt,
	}
	if withUser {
		v.User = &UserView{ID: d.UserID, FullName: d.UserName, Email: d.UserEmail}
	}
	return v
}

func subscriptionViewsOf(ds []domain.Detail) []SubscriptionView {
	out := make([]SubscriptionView, len(ds))
	for i, d := range ds {
		out[i] = subscriptionViewOf(d, true)
	}
	return out
}

func optionalSubscriptionView(d *domain.Detail) *SubscriptionView {
	if d == nil {
		return nil
	}
	v := subscriptionViewOf(*d, false)
	return &v
}

func mineViewOf(m usecase.Mine) MineView {
	v := MineView{Subscription: optionalSubscriptionView(m.Subscription), PendingRequest: optionalSubscriptionView(m.Pending), Usage: m.Usage}
	if m.Plan != nil {
		p := planViewOf(*m.Plan)
		v.Plan = &p
	}
	return v
}
