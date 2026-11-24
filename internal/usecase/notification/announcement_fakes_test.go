package notification

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

// txFake runs the function in place: the fakes keep no state that a rollback would have to undo.
type txFake struct{}

func (txFake) WithinTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type sentNote struct {
	announcement uuid.UUID
	user         uuid.UUID
	params       map[string]string
	audience     domain.Audience
}

// annFake is an in-memory announcement repository. The audience is whatever people holds (a landlord's own tenants when
// the filter has a landlord, everybody else's otherwise); the real definition is tested against Postgres.
type annFake struct {
	items      map[uuid.UUID]*domain.Announcement
	recipients map[uuid.UUID][]uuid.UUID
	people     []uuid.UUID // who any non-landlord audience reaches
	mine       []uuid.UUID // who a landlord's audience reaches
	notes      []sentNote
	filters    []domain.AudienceFilter
	deliverErr map[uuid.UUID]error
	welcome    bool
	inserted   int
}

func newAnnFake() *annFake {
	return &annFake{items: map[uuid.UUID]*domain.Announcement{}, recipients: map[uuid.UUID][]uuid.UUID{}, deliverErr: map[uuid.UUID]error{}}
}

func (r *annFake) audience(f domain.AudienceFilter) []uuid.UUID {
	r.filters = append(r.filters, f)
	pool := r.people
	if f.LandlordID != nil {
		pool = r.mine
	}
	if f.Scope == "picked" {
		var out []uuid.UUID
		for _, id := range pool {
			if slices.Contains(f.Picked, id) {
				out = append(out, id)
			}
		}
		return out
	}
	return pool
}

func (r *annFake) Insert(_ context.Context, a domain.Announcement) (uuid.UUID, error) {
	a.ID = uuid.New()
	a.CreatedAt = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	r.items[a.ID] = &a
	r.inserted++
	return a.ID, nil
}

func (r *annFake) Update(_ context.Context, a domain.Announcement) error {
	cur, ok := r.items[a.ID]
	if !ok {
		return shared.ErrNotFound
	}
	a.PublishedAt, a.SentAt, a.RecalledAt, a.RecipientCount, a.CreatedAt = cur.PublishedAt, cur.SentAt, cur.RecalledAt, cur.RecipientCount, cur.CreatedAt
	*cur = a
	return nil
}

func (r *annFake) Get(_ context.Context, id uuid.UUID) (domain.AnnouncementDetail, error) {
	a, ok := r.items[id]
	if !ok {
		return domain.AnnouncementDetail{}, shared.ErrNotFound
	}
	return domain.AnnouncementDetail{Announcement: *a}, nil
}

func (r *annFake) ForUpdate(_ context.Context, id uuid.UUID) (domain.Announcement, error) {
	a, ok := r.items[id]
	if !ok {
		return domain.Announcement{}, shared.ErrNotFound
	}
	return *a, nil
}

func (r *annFake) List(context.Context, domain.AnnouncementQuery, time.Time) ([]domain.AnnouncementDetail, int, error) {
	var out []domain.AnnouncementDetail
	for _, a := range r.items {
		out = append(out, domain.AnnouncementDetail{Announcement: *a})
	}
	return out, len(out), nil
}

func (r *annFake) RecipientIDs(_ context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	return r.recipients[id], nil
}

func (r *annFake) ReplaceRecipients(_ context.Context, id uuid.UUID, ids []uuid.UUID) error {
	r.recipients[id] = ids
	return nil
}

func (r *annFake) SetPublished(_ context.Context, id uuid.UUID, at *time.Time) error {
	r.items[id].PublishedAt = at
	return nil
}

func (r *annFake) SetRecalled(_ context.Context, id uuid.UUID, at time.Time) (int, error) {
	if r.items[id].RecalledAt != nil {
		return 0, domain.ErrRecalled
	}
	r.items[id].RecalledAt = &at
	before := len(r.notes)
	r.notes = slices.DeleteFunc(r.notes, func(n sentNote) bool { return n.announcement == id })
	return before - len(r.notes), nil
}

func (r *annFake) SetRecipientCount(_ context.Context, id uuid.UUID, n int) error {
	r.items[id].RecipientCount = n
	return nil
}

func (r *annFake) ClaimDelivery(_ context.Context, id uuid.UUID, at time.Time) (bool, error) {
	a := r.items[id]
	if !a.DeliveryDue(at) {
		return false, nil
	}
	a.SentAt = &at
	return true, nil
}

func (r *annFake) DeliverNotifications(_ context.Context, id uuid.UUID, f domain.AudienceFilter, params map[string]string, audience domain.Audience) ([]uuid.UUID, error) {
	if err := r.deliverErr[id]; err != nil {
		return nil, err
	}
	var out []uuid.UUID
	for _, u := range r.audience(f) {
		r.notes = append(r.notes, sentNote{announcement: id, user: u, params: params, audience: audience})
		out = append(out, u)
	}
	return out, nil
}

func (r *annFake) Due(_ context.Context, now time.Time, _ int) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	for id, a := range r.items {
		if a.DeliveryDue(now) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (r *annFake) CountAudience(_ context.Context, f domain.AudienceFilter) (int, error) {
	return len(r.audience(f)), nil
}

func (r *annFake) FilterPeople(_ context.Context, f domain.AudienceFilter, ids []uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for _, u := range r.audience(domain.AudienceFilter{Scope: "my_tenants", LandlordID: f.LandlordID}) {
		if slices.Contains(ids, u) {
			out = append(out, u)
		}
	}
	return out, nil
}

func (r *annFake) People(context.Context, uuid.UUID, string, int, int) ([]domain.Person, int, error) {
	return nil, 0, nil
}

func (r *annFake) WelcomeExists(context.Context) (bool, error) { return r.welcome, nil }

// auditFake remembers the actions recorded.
type auditFake struct{ actions []string }

func (a *auditFake) Record(_ context.Context, e AuditEvent) error {
	a.actions = append(a.actions, e.Action)
	return nil
}

func (r *annFake) Find(_ context.Context, id uuid.UUID) (domain.Announcement, error) {
	a, ok := r.items[id]
	if !ok {
		return domain.Announcement{}, shared.ErrNotFound
	}
	return *a, nil
}

func (r *annFake) PopupCandidates(_ context.Context, now time.Time, limit int) ([]domain.AnnouncementDetail, error) {
	var out []domain.AnnouncementDetail
	for _, a := range r.items {
		if a.ShowPopup && a.StatusAt(now) == domain.StatusActive {
			out = append(out, domain.AnnouncementDetail{Announcement: *a})
		}
	}
	slices.SortFunc(out, func(x, y domain.AnnouncementDetail) int { return y.Priority - x.Priority })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
