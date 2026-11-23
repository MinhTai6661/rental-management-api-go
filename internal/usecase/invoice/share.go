package invoice

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invoice"
	"rental-management/internal/domain/shared"
)

// ShareLink returns the link that shows a bill that was sent without signing in: made on the first ask and the same
// one afterwards, until it runs out or is replaced (a new one is made then). domain.ErrLinkNeedsSent for a draft.
func (s *Invoices) ShareLink(ctx context.Context, landlordID, id uuid.UUID) (Link, error) {
	return s.shareOp(ctx, landlordID, id, false)
}

// RegenerateShareLink makes a new link: whatever was passed on before stops working.
func (s *Invoices) RegenerateShareLink(ctx context.Context, landlordID, id uuid.UUID) (Link, error) {
	return s.shareOp(ctx, landlordID, id, true)
}

func (s *Invoices) shareOp(ctx context.Context, landlordID, id uuid.UUID, fresh bool) (Link, error) {
	var link Link
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		d, err := s.invoices.ForUpdate(ctx, landlordView(landlordID), id)
		if err != nil {
			return notFound(err)
		}
		if d.Status == domain.StatusDraft {
			return domain.ErrLinkNeedsSent
		}
		if fresh {
			link, err = s.issue(ctx, &d, landlordID)
		} else {
			link, err = s.linkFor(ctx, &d, landlordID)
		}
		return err
	})
	return link, err
}

// RevokeShareLink withdraws the link of a bill; what was passed on stops working. Nothing happens for a bill that has
// no link.
func (s *Invoices) RevokeShareLink(ctx context.Context, landlordID, id uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		d, err := s.invoices.ForUpdate(ctx, landlordView(landlordID), id)
		if err != nil {
			return notFound(err)
		}
		if d.Share.TokenHash == "" {
			return nil
		}
		if err := s.invoices.SaveShare(ctx, id, domain.Share{}); err != nil {
			return err
		}
		d.Share = domain.Share{}
		return s.record(ctx, AuditLinkRevoked, landlordID, d.Invoice, d.Contract, nil)
	})
}

// linkFor is the link of the bill: the one it has while it works, else a new one.
func (s *Invoices) linkFor(ctx context.Context, d *domain.Detail, actor uuid.UUID) (Link, error) {
	if d.Share.Live(s.clock.Now()) {
		if token, err := s.sealer.Open(d.Share.Sealed); err == nil {
			return Link{URL: s.shareURL(token), ExpiresAt: *d.Share.ExpiresAt}, nil
		}
		// A copy that cannot be opened (the key changed) is replaced rather than left to break the landlord's copy.
	}
	return s.issue(ctx, d, actor)
}

// issue makes a new link for the bill.
func (s *Invoices) issue(ctx context.Context, d *domain.Detail, actor uuid.UUID) (Link, error) {
	token, hash, err := s.tokens.New()
	if err != nil {
		return Link{}, err
	}
	sealed, err := s.sealer.Seal(token)
	if err != nil {
		return Link{}, err
	}
	expires := s.clock.Now().Add(s.opts.ShareTTL)
	share := domain.Share{TokenHash: hash, Sealed: sealed, ExpiresAt: &expires}
	if err := s.invoices.SaveShare(ctx, d.ID, share); err != nil {
		return Link{}, err
	}
	d.Share = share
	if err := s.record(ctx, AuditLinkCreated, actor, d.Invoice, d.Contract, nil); err != nil {
		return Link{}, err
	}
	return Link{URL: s.shareURL(token), ExpiresAt: expires}, nil
}

// shareURL is the page of the web app that shows the bill of a link.
func (s *Invoices) shareURL(token string) string {
	return strings.TrimRight(s.opts.WebURL, "/") + "/bill?token=" + url.QueryEscape(token)
}

// Public is the bill a link shows to anybody, signed in or not.
type Public struct{ *core }

// NewPublic builds the use case of the public bill.
func NewPublic(d Deps, o Options) *Public { return &Public{newCore(d, o)} }

// View returns the bill a link shows. A link that is wrong, ran out, was withdrawn, or is for a bill still a draft all
// answer domain.ErrLinkInvalid, so the answer tells nothing about which it was. The view has no ids of people, rooms
// or contracts.
func (p *Public) View(ctx context.Context, token string) (domain.PublicView, error) {
	if !domain.ValidShareToken(token) {
		return domain.PublicView{}, domain.ErrLinkInvalid
	}
	d, err := p.invoices.FindByShareHash(ctx, p.tokens.Hash(token))
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return domain.PublicView{}, domain.ErrLinkInvalid
		}
		return domain.PublicView{}, err
	}
	if d.Status == domain.StatusDraft || d.Share.ExpiresAt == nil || !d.Share.ExpiresAt.After(p.clock.Now()) {
		return domain.PublicView{}, domain.ErrLinkInvalid
	}
	totals, err := p.totalsOf(ctx, []uuid.UUID{d.ID})
	if err != nil {
		return domain.PublicView{}, err
	}
	return domain.PublicView{
		Number: d.Number, Name: d.Label(), Status: d.Status, PeriodStart: d.PeriodStart, PeriodEnd: d.PeriodEnd,
		TotalVND: d.TotalVND, DueAt: d.DueAt, PaidAt: d.PaidAt, Note: d.Note, PaymentCode: d.PaymentCode,
		Room: d.Contract.RoomName, Landlord: d.Contract.LandlordName, Recipient: d.Contract.Recipient(),
		Summary: domain.SummaryOf(d.TotalVND, totals[d.ID]), Items: domain.PublicItems(d.Items),
	}, nil
}
