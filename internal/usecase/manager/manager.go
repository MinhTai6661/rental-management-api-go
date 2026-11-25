// Package manager holds the use cases of handing rooms over to managers: the landlord invites a person to manage some
// rooms by link, the person accepts or declines, and the landlord gives, changes and takes back what the manager may do
// room by room. It declares the ports it needs (ports.go) and imports no framework, driver or other context: who can
// be invited, the role of the account, the notifications, the audit trail and the offers a manager wrote are ports too.
package manager

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/manager"
	"rental-management/internal/domain/shared"
)

// Deps are the collaborators of the use cases. Nil optional ports get their no-op defaults.
type Deps struct {
	Tx          TxManager
	Invitations InvitationRepository
	Assignments AssignmentRepository
	Rooms       RoomReader
	Roles       Roles
	Invitees    Invitees
	Tokens      LinkTokens
	// Sealer is optional: without it the links are not kept and cannot be copied again.
	Sealer   Sealer
	Mailer   InvitationMailer
	Notifier Notifier
	Audit    AuditRecorder
	// Offers withdraws the offers of rooms a manager wrote when they lose every room; defaults to NoOffers.
	Offers OfferWithdrawer
	Clock  Clock
	Log    *slog.Logger
}

// Options are the settings of the use cases.
type Options struct {
	// InvitationTTL is how long an invitation can be answered. Default 7 days.
	InvitationTTL time.Duration
	// WebURL is the origin of the web app: the links of invitations point there.
	WebURL string
}

// core is what the use case groups share.
type core struct {
	Deps
	opts Options
}

func newCore(d Deps, o Options) *core {
	c := &core{Deps: d, opts: o}
	if c.Mailer == nil {
		c.Mailer = NoInvitationMailer{}
	}
	if c.Notifier == nil {
		c.Notifier = NoNotifier{}
	}
	if c.Audit == nil {
		c.Audit = NoAudit{}
	}
	if c.Offers == nil {
		c.Offers = NoOffers{}
	}
	if c.Clock == nil {
		c.Clock = ClockFunc(time.Now)
	}
	if c.Log == nil {
		c.Log = slog.New(slog.DiscardHandler)
	}
	if c.opts.InvitationTTL <= 0 {
		c.opts.InvitationTTL = 7 * 24 * time.Hour
	}
	return c
}

// notFound turns a repository's "no such row" into the domain's not found.
func notFound(err error) error {
	if errors.Is(err, shared.ErrNotFound) {
		return domain.ErrNotFound
	}
	return err
}

// invalidIfMissing turns "no such row" into the one answer for every unusable invitation.
func invalidIfMissing(err error) error {
	if errors.Is(err, shared.ErrNotFound) || errors.Is(err, domain.ErrNotFound) {
		return domain.ErrInvitationInvalid
	}
	return err
}

// newSecret makes the secret of a link: its hash, and the encrypted copy when the links are kept. The token is stored
// in *token for the caller to put into the link.
func (c *core) newSecret(token *string) (hash string, sealed *string, err error) {
	*token, hash, err = c.Tokens.New()
	if err != nil {
		return "", nil, err
	}
	if c.Sealer == nil {
		return hash, nil, nil
	}
	box, err := c.Sealer.Seal(*token)
	if err != nil {
		return "", nil, err
	}
	return hash, &box, nil
}

// invitationURL is the page of the web app that shows an invitation; key says how the page finds it: "token" carries
// the secret, "invitation" the id of an invitation addressed to the signed-in account.
func (c *core) invitationURL(key, value string) string {
	return strings.TrimRight(c.opts.WebURL, "/") + "/manager-invitation?" + key + "=" + url.QueryEscape(value)
}

// notify tells a user about something; a failure is logged and never undoes what was done.
func (c *core) notify(ctx context.Context, to uuid.UUID, kind, audience, link string, params map[string]string) {
	if err := c.Notifier.Notify(ctx, Notification{UserID: to, Kind: kind, Params: params, URL: link, Audience: audience}); err != nil {
		c.Log.Warn("notify about a manager", "error", err, "kind", kind)
	}
}

// roomNames returns the names of the rooms in the order of ids.
func roomNames(rooms []RoomInfo, ids []uuid.UUID) []string {
	byID := make(map[uuid.UUID]string, len(rooms))
	for _, r := range rooms {
		byID[r.ID] = r.Name
	}
	var out []string
	for _, id := range ids {
		if n, ok := byID[id]; ok {
			out = append(out, n)
		}
	}
	return out
}

// namesOf lists the names of rooms.
func namesOf(rooms []RoomInfo) []string {
	out := make([]string, len(rooms))
	for i, r := range rooms {
		out[i] = r.Name
	}
	return out
}

// distinct removes the repeated ids, keeping the first of each.
func distinct(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
