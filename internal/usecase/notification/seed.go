package notification

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
)

// Seeder writes the demo inbox and the welcome popup draft. It is safe to run again: an account that already has a
// message of a kind, and a platform that already has a welcome message, are left alone.
type Seeder struct{ *core }

// NewSeeder returns the use case.
func NewSeeder(d Deps, o Options) *Seeder { return &Seeder{newCore(d, o)} }

type demoMessage struct {
	account  string
	kind     domain.Kind
	params   map[string]string
	url      string
	audience domain.Audience
	read     bool
}

// demoMessages are a few messages of the kinds the demo accounts would really get. They point at pages, not at rows,
// so they stay valid whatever else is seeded.
func demoMessages() []demoMessage {
	return []demoMessage{
		{account: "tenant@rental.test", kind: domain.InvoiceSent, url: "/my-rooms", params: map[string]string{
			"invoice_number": "HD-2026-11-0001", "room": "P101", "amount_vnd": "3500000", "due_date": "2026-11-30",
		}},
		{account: "tenant@rental.test", kind: domain.RegistrationApproved, url: "/my-rooms", read: true, params: map[string]string{"room": "P101"}},
		{account: "landlord@rental.test", kind: domain.PaymentSubmitted, url: "/payments", audience: domain.AudienceLandlord, params: map[string]string{
			"room": "P101", "tenant": "Demo Tenant", "invoice_number": "HD-2026-11-0001", "amount_vnd": "3500000", "remaining_vnd": "0",
		}},
		{account: "landlord@rental.test", kind: domain.RegistrationConfirmed, url: "/contracts", audience: domain.AudienceLandlord, params: map[string]string{
			"room": "P102", "tenant": "Demo Tenant",
		}},
		{account: "admin@rental.test", kind: domain.ListingSubmitted, url: "/admin/listings", params: map[string]string{"room": "P101"}},
	}
}

// SeedDemo writes the demo messages for the accounts in ids (keyed by email) and returns how many it wrote.
func (s *Seeder) SeedDemo(ctx context.Context, ids map[string]uuid.UUID) (int, error) {
	written := 0
	for _, m := range demoMessages() {
		user, ok := ids[m.account]
		if !ok {
			continue
		}
		has, err := s.notifications.HasKind(ctx, user, m.kind)
		if err != nil {
			return written, err
		}
		if has {
			continue
		}
		rows, err := s.notifications.Insert(ctx, []uuid.UUID{user}, m.kind, m.params, m.url, domain.ResolveAudience(m.audience, m.kind))
		if err != nil {
			return written, err
		}
		written++
		if m.read && len(rows) == 1 {
			if _, err := s.notifications.MarkRead(ctx, user, rows[0], s.clock.Now()); err != nil {
				return written, err
			}
		}
	}
	welcome, err := s.seedWelcomeDraft(ctx, ids)
	return written + welcome, err
}

// seedWelcomeDraft writes one draft welcome popup by the demo admin, for accounts registered in the last 14 days. It
// is a draft: nobody sees it until an admin publishes it.
func (s *Seeder) seedWelcomeDraft(ctx context.Context, ids map[string]uuid.UUID) (int, error) {
	admin, ok := ids["admin@rental.test"]
	if !ok || s.announcements == nil {
		return 0, nil
	}
	exists, err := s.announcements.WelcomeExists(ctx)
	if err != nil || exists {
		return 0, err
	}
	days := 14
	_, err = s.announcements.Insert(ctx, domain.Announcement{
		CreatedByID: &admin, SenderRole: "admin", Scope: domain.ScopeCustom, Criteria: domain.Criteria{RegisteredWithinDays: &days},
		Type:  domain.TypeWelcome,
		Title: domain.Texts{"vi": "Chào mừng bạn đến với nền tảng", "en": "Welcome to the platform"},
		Body: domain.Texts{
			"vi": "Tìm phòng trọ phù hợp, đăng ký thuê và theo dõi hóa đơn ngay trên một nơi.",
			"en": "Find a room that suits you, register to rent it and follow your bills in one place.",
		},
		ShowPopup: true, Frequency: domain.FrequencyOnce, PopupSize: domain.SizeMedium, Revision: 1,
		CTALabel: domain.Texts{"vi": "Tìm phòng ngay", "en": "Find a room"}, CTAURL: "/find-rooms",
	})
	if err != nil {
		return 0, err
	}
	return 1, nil
}
