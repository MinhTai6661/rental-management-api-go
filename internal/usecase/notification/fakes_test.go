package notification

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

// repoFake is an in-memory inbox that follows the rules the real repository does: everything is scoped to the user and
// the audience filter keeps the messages for that side and the ones for every side.
type repoFake struct {
	rows      []domain.Notification
	admins    []uuid.UUID
	insertErr error
	pruned    []int // what each PruneBefore call returns, in order
	pruneCall int
	nextID    int
}

func (r *repoFake) Insert(_ context.Context, userIDs []uuid.UUID, kind domain.Kind, params map[string]string, url string, audience domain.Audience) ([]uuid.UUID, error) {
	if r.insertErr != nil {
		return nil, r.insertErr
	}
	var ids []uuid.UUID
	for _, u := range userIDs {
		r.nextID++
		id := uuid.New()
		r.rows = append(r.rows, domain.Notification{
			ID: id, UserID: u, Kind: kind, Params: params, URL: url, Audience: audience,
			CreatedAt: time.Date(2026, 11, 24, 8, 0, r.nextID, 0, time.UTC),
		})
		ids = append(ids, id)
	}
	return ids, nil
}

func visible(n domain.Notification, only domain.Audience, filtered bool) bool {
	return !filtered || n.Audience == domain.AudienceNone || n.Audience == only
}

func (r *repoFake) List(_ context.Context, userID uuid.UUID, only domain.Audience, filtered, unreadOnly bool, kind *domain.Kind, limit, offset int) ([]domain.Notification, error) {
	var out []domain.Notification
	for i := len(r.rows) - 1; i >= 0; i-- { // newest first
		n := r.rows[i]
		if n.UserID != userID || !visible(n, only, filtered) || (unreadOnly && n.ReadAt != nil) || (kind != nil && n.Kind != *kind) {
			continue
		}
		out = append(out, n)
	}
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

func (r *repoFake) UnreadCount(_ context.Context, userID uuid.UUID, only domain.Audience, filtered bool) (int, error) {
	n := 0
	for _, row := range r.rows {
		if row.UserID == userID && row.ReadAt == nil && visible(row, only, filtered) {
			n++
		}
	}
	return n, nil
}

func (r *repoFake) MarkRead(_ context.Context, userID, id uuid.UUID, at time.Time) (domain.Notification, error) {
	for i := range r.rows {
		if r.rows[i].ID == id && r.rows[i].UserID == userID {
			if r.rows[i].ReadAt == nil {
				r.rows[i].ReadAt = &at
			}
			return r.rows[i], nil
		}
	}
	return domain.Notification{}, shared.ErrNotFound
}

func (r *repoFake) MarkAllRead(_ context.Context, userID uuid.UUID, only domain.Audience, filtered bool, at time.Time) (int, error) {
	n := 0
	for i := range r.rows {
		if r.rows[i].UserID == userID && r.rows[i].ReadAt == nil && visible(r.rows[i], only, filtered) {
			r.rows[i].ReadAt = &at
			n++
		}
	}
	return n, nil
}

func (r *repoFake) MarkManyRead(_ context.Context, userID uuid.UUID, ids []uuid.UUID, at time.Time) (int, error) {
	n := 0
	for i := range r.rows {
		if r.rows[i].UserID == userID && r.rows[i].ReadAt == nil && slices.Contains(ids, r.rows[i].ID) {
			r.rows[i].ReadAt = &at
			n++
		}
	}
	return n, nil
}

func (r *repoFake) Delete(_ context.Context, userID, id uuid.UUID) (bool, error) {
	n, _ := r.DeleteMany(context.Background(), userID, []uuid.UUID{id})
	return n == 1, nil
}

func (r *repoFake) DeleteMany(_ context.Context, userID uuid.UUID, ids []uuid.UUID) (int, error) {
	return r.remove(func(n domain.Notification) bool { return n.UserID == userID && slices.Contains(ids, n.ID) }), nil
}

func (r *repoFake) DeleteByScope(_ context.Context, userID uuid.UUID, readOnly bool) (int, error) {
	return r.remove(func(n domain.Notification) bool { return n.UserID == userID && (!readOnly || n.ReadAt != nil) }), nil
}

func (r *repoFake) remove(match func(domain.Notification) bool) int {
	before := len(r.rows)
	r.rows = slices.DeleteFunc(r.rows, match)
	return before - len(r.rows)
}

func (r *repoFake) PruneBefore(context.Context, time.Time, int) (int, error) {
	if r.pruneCall >= len(r.pruned) {
		return 0, nil
	}
	n := r.pruned[r.pruneCall]
	r.pruneCall++
	return n, nil
}

func (r *repoFake) AdminIDs(context.Context) ([]uuid.UUID, error) { return r.admins, nil }

func (r *repoFake) HasKind(_ context.Context, userID uuid.UUID, kind domain.Kind) (bool, error) {
	for _, n := range r.rows {
		if n.UserID == userID && n.Kind == kind {
			return true, nil
		}
	}
	return false, nil
}

// pushFake records what it was asked to push.
type pushFake struct {
	calls [][]uuid.UUID
	last  Event
	err   error
}

func (p *pushFake) Push(_ context.Context, ids []uuid.UUID, e Event) error {
	p.calls = append(p.calls, ids)
	p.last = e
	return p.err
}

var errBoom = errors.New("boom")
