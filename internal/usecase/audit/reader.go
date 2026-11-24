package audit

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/audit"
	"rental-management/internal/domain/shared"
)

// maxOffset bounds how deep into the trail a client may page: nobody reads that far, and the query cost grows with it.
const maxOffset = 100_000

// Page is the page of a feed a client asked for.
type Page struct {
	Page    int
	PerPage int
}

func (p Page) offset() int { return (p.Page - 1) * p.PerPage }

// Feed is a page of the trail and whether another page follows (the trail is never counted).
type Feed struct {
	Items   []domain.Record
	HasMore bool
}

// Viewer is the signed in person reading the trail and the role they act in.
type Viewer struct {
	UserID uuid.UUID
	Role   string
}

// Reader reads the trail for the people who may see it.
type Reader struct {
	repo    Repository
	objects Objects
}

// NewReader returns a reader.
func NewReader(repo Repository, objects Objects) *Reader {
	return &Reader{repo: repo, objects: objects}
}

// Admin is the staff's view (route permission audit.view): the whole trail, filtered. An admin does not see what staff
// did or what was done to staff, nor the setting changes; nobody on the staff sees an amount of money.
func (r *Reader) Admin(ctx context.Context, viewer Viewer, f domain.Filter, page Page) (Feed, error) {
	from, before, err := f.Bounds()
	if err != nil {
		return Feed{}, err
	}
	scope := domain.ScopeOf(viewer.Role)
	q := domain.Query{
		Filter: f, From: from, Before: before,
		Important: f.Tier != domain.Routine, Routine: f.Tier != domain.Important,
		ExcludeStaff: !scope.IncludeStaff, ExcludeSettings: !scope.IncludeSettings,
	}
	feed, err := r.page(ctx, q, page)
	if err != nil {
		return Feed{}, err
	}
	for i, rec := range feed.Items {
		feed.Items[i].Changes = domain.RedactMoney(rec.Action, rec.Changes)
		feed.Items[i].Meta = domain.RedactMoney(rec.Action, rec.Meta)
	}
	return feed, nil
}

// Mine is the person's own activity: what they did and what was done to their account, both tiers, with what others
// did shown only as a role, and bursts of the same action merged.
func (r *Reader) Mine(ctx context.Context, userID uuid.UUID, page Page) ([]domain.Counted, bool, error) {
	feed, err := r.page(ctx, domain.Query{Involving: &userID, Important: true, Routine: true}, page)
	if err != nil {
		return nil, false, err
	}
	shown := make([]domain.Record, len(feed.Items))
	for i, rec := range feed.Items {
		shown[i] = domain.ForSubject(rec, userID)
	}
	return domain.Collapse(shown), feed.HasMore, nil
}

// History is what happened to a room, a contract or an invoice, for its landlord. The history of a room includes its
// contracts and invoices (their entries carry the room too). Anybody else gets shared.ErrNotFound, whether or not the
// thing exists.
func (r *Reader) History(ctx context.Context, viewer Viewer, subject domain.Subject, id uuid.UUID, page Page) (Feed, error) {
	owner, err := r.objects.OwnerOf(ctx, subject, id)
	if err != nil {
		return Feed{}, err
	}
	if owner != viewer.UserID {
		return Feed{}, shared.ErrNotFound
	}
	q := domain.Query{Important: true}
	switch subject {
	case domain.SubjectRoom:
		q.Filter.RoomID = &id
	case domain.SubjectContract:
		q.Filter.ContractID = &id
	case domain.SubjectInvoice:
		q.Filter.InvoiceID = &id
	default:
		return Feed{}, shared.ErrNotFound
	}
	return r.page(ctx, q, page)
}

// SettingChanges lists the changes of the system settings, newest first: the history of the settings page.
func (r *Reader) SettingChanges(ctx context.Context, page Page) (Feed, error) {
	action := domain.SettingChanged
	return r.page(ctx, domain.Query{Filter: domain.Filter{Action: &action}, Important: true}, page)
}

// page runs the query for a page: each tier contributes at most page*per_page+1 rows before they are merged.
func (r *Reader) page(ctx context.Context, q domain.Query, p Page) (Feed, error) {
	if p.Page < 1 || p.PerPage < 1 || p.offset() > maxOffset {
		return Feed{Items: []domain.Record{}}, nil
	}
	q.Limit, q.Offset = p.PerPage+1, p.offset()
	q.BranchLimit = p.Page*p.PerPage + 1
	rows, err := r.repo.Search(ctx, q)
	if err != nil {
		return Feed{}, err
	}
	hasMore := len(rows) > p.PerPage
	if hasMore {
		rows = rows[:p.PerPage]
	}
	if rows == nil {
		rows = []domain.Record{}
	}
	return Feed{Items: rows, HasMore: hasMore}, nil
}
