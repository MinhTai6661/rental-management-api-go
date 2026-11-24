package notification

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
)

// Message is what another context wants somebody to be told. The kind and the audience are plain strings because the
// sender is a different context: the use case checks them against the catalog.
type Message struct {
	UserIDs  []uuid.UUID
	Kind     string
	Params   map[string]string
	URL      string
	Audience string
}

// Deliveries puts messages in people's inboxes. It is the one implementation behind every context's notifier port.
type Deliveries struct{ *core }

// NewDeliveries returns the use case.
func NewDeliveries(d Deps, o Options) *Deliveries { return &Deliveries{newCore(d, o)} }

// ErrURLTooLong is returned for a message that leads to an address longer than the inbox keeps.
var ErrURLTooLong = errors.New("notification url too long")

// Send puts the message in the inbox of each user (once each; the nil id and repeats are dropped), then tells the ones
// who are online. An unknown kind or audience is refused: the catalog has one source of truth. The push is best effort.
func (d *Deliveries) Send(ctx context.Context, m Message) error {
	kind, err := domain.ParseKind(m.Kind)
	if err != nil {
		return err
	}
	explicit, err := domain.ParseAudience(m.Audience)
	if err != nil {
		return err
	}
	if len(m.URL) > domain.MaxURLChars {
		return ErrURLTooLong
	}
	ids := uniqueIDs(m.UserIDs)
	if len(ids) == 0 {
		return nil
	}
	audience := domain.ResolveAudience(explicit, kind)
	params := m.Params
	if params == nil {
		params = map[string]string{}
	}
	if _, err := d.notifications.Insert(ctx, ids, kind, params, m.URL, audience); err != nil {
		return err
	}
	if err := d.pusher.Push(ctx, ids, Event{Kind: kind, Params: params, URL: m.URL, Audience: audience}); err != nil {
		d.log.Warn("push a notification", "error", err, "kind", kind)
	}
	return nil
}

// SendToAdmins sends the message to every active admin (super admins have the admin screens of their own and are not
// told, as in the Laravel API); UserIDs of the message are ignored.
func (d *Deliveries) SendToAdmins(ctx context.Context, m Message) error {
	admins, err := d.notifications.AdminIDs(ctx)
	if err != nil {
		return err
	}
	m.UserIDs = admins
	return d.Send(ctx, m)
}

// uniqueIDs drops the nil id and repeats, keeping the first appearance of each.
func uniqueIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
