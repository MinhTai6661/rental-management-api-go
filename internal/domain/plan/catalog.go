package plan

// Base returns the plans every installation starts with (the Laravel catalog): landlord Free, Pro and VIP, tenant
// Free and Plus. `seed` writes them by code, so a later edit of another plan survives and the listed ones are
// written again.
func Base() []Plan {
	n := func(v int) *int { return &v }
	text := func(s string) *string { return &s }
	return []Plan{
		{
			Code: "landlord_free", Name: "Free", Audience: AudienceLandlord, PriceVND: 0, Interval: IntervalMonth,
			Description: text("Get started with a few rooms."),
			Limits:      Limits{MaxRooms: n(20), MaxBuildings: n(3), MaxImagesPerRoom: n(5), MaxActiveContracts: n(20)},
			Features:    []Feature{}, IsDefault: true, IsActive: true, DisplayOrder: 1,
		},
		{
			Code: "landlord_pro", Name: "Pro", Audience: AudienceLandlord, PriceVND: 199_000, Interval: IntervalMonth,
			Description:    text("For growing boarding houses: more rooms, better placement and automatic invoices."),
			Limits:         Limits{MaxRooms: n(100), MaxBuildings: n(20), MaxImagesPerRoom: n(10), MaxActiveContracts: n(100)},
			SearchPriority: 10, Features: []Feature{FeatureAutoInvoicing}, IsActive: true, DisplayOrder: 2,
		},
		{
			Code: "landlord_vip", Name: "VIP", Audience: AudienceLandlord, PriceVND: 499_000, Interval: IntervalMonth,
			Description:    text("No limits, top placement in find rooms, VIP badge and priority support."),
			Limits:         Limits{MaxImagesPerRoom: n(20)},
			SearchPriority: 100, Features: []Feature{FeatureAutoInvoicing, FeatureVIPBadge, FeaturePrioritySupport},
			IsActive: true, DisplayOrder: 3,
		},
		{
			Code: "tenant_free", Name: "Free", Audience: AudienceTenant, PriceVND: 0, Interval: IntervalMonth,
			Description: text("Look for a room and send a few rental requests at a time."),
			Limits:      Limits{MaxPendingRegistrations: n(3)},
			Features:    []Feature{}, IsDefault: true, IsActive: true, DisplayOrder: 1,
		},
		{
			Code: "tenant_plus", Name: "Plus", Audience: AudienceTenant, PriceVND: 49_000, Interval: IntervalMonth,
			Description: text("Send many rental requests at once and get priority support."),
			Limits:      Limits{MaxPendingRegistrations: n(10)},
			Features:    []Feature{FeaturePrioritySupport}, IsActive: true, DisplayOrder: 2,
		},
	}
}

// DemoAssignment is a demo account and the plan it is put on, so each tier can be tried out.
type DemoAssignment struct {
	Email    string
	PlanCode string
}

// DemoAssignments are the demo accounts that start on a paid plan (Laravel's DemoSubscriptionSeeder).
func DemoAssignments() []DemoAssignment {
	return []DemoAssignment{
		{Email: "landlord@rental.test", PlanCode: "landlord_vip"},
		{Email: "landlord2@rental.test", PlanCode: "landlord_pro"},
		{Email: "tenant@rental.test", PlanCode: "tenant_plus"},
	}
}
