package notification

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

// Popups is what a person sees as popups and what they do with them. A popup is pulled: the web asks which ones to show
// when the app opens and tells what the person did.
type Popups struct{ *core }

// NewPopups returns the use case.
func NewPopups(deps Deps, o Options) *Popups { return &Popups{newCore(deps, o)} }

// PopupViewer is the person looking and the login session they are in (nil when the token carries none).
type PopupViewer struct {
	UserID    uuid.UUID
	SessionID *uuid.UUID
}

// Active returns the popups to show the person now: at most domain.PopupsAtOnce, most important first. A popup shows
// when it is out, the person's earlier views allow it (frequency and limits) and the person belongs to its audience.
func (p *Popups) Active(ctx context.Context, viewer PopupViewer) ([]domain.AnnouncementDetail, error) {
	now := p.clock.Now()
	candidates, err := p.announcements.PopupCandidates(ctx, now, domain.PopupCandidates)
	if err != nil || len(candidates) == 0 {
		return nil, err
	}
	ids := make([]uuid.UUID, len(candidates))
	for i, c := range candidates {
		ids[i] = c.ID
	}
	seen, err := p.impressions.Seen(ctx, viewer.UserID, ids)
	if err != nil {
		return nil, err
	}
	var out []domain.AnnouncementDetail
	for _, candidate := range candidates {
		var previous *domain.Seen
		if s, ok := seen[candidate.ID]; ok {
			previous = &s
		}
		// The cheap rules first, so the audience query runs only for popups that could show.
		if !domain.Shows(candidate.PopupRules(), previous, viewer.SessionID, now) {
			continue
		}
		belongs, err := p.inAudience(ctx, candidate.Announcement, viewer.UserID, now)
		if err != nil {
			return nil, err
		}
		if !belongs {
			continue
		}
		candidate.Status = candidate.StatusAt(now)
		out = append(out, candidate)
		if len(out) == domain.PopupsAtOnce {
			break
		}
	}
	return out, nil
}

// Record notes what the person did with a popup: they saw it, closed it or pressed its button. A popup that is not out,
// or is not for the person, is shared.ErrNotFound, so nobody can skew the figures of a popup that is not theirs.
func (p *Popups) Record(ctx context.Context, viewer PopupViewer, id uuid.UUID, action domain.ImpressionAction) error {
	now := p.clock.Now()
	announcement, err := p.announcements.Find(ctx, id)
	if err != nil {
		return err
	}
	if !announcement.ShowPopup || announcement.StatusAt(now) != domain.StatusActive {
		return shared.ErrNotFound
	}
	belongs, err := p.inAudience(ctx, announcement, viewer.UserID, now)
	if err != nil {
		return err
	}
	if !belongs {
		return shared.ErrNotFound
	}
	var previous *domain.Seen
	seen, err := p.impressions.Seen(ctx, viewer.UserID, []uuid.UUID{id})
	if err != nil {
		return err
	}
	if s, ok := seen[id]; ok {
		previous = &s
	}
	next := domain.AfterAction(previous, action, announcement.PopupRules(), viewer.SessionID, now)
	return p.impressions.Upsert(ctx, id, viewer.UserID, next)
}

// inAudience says whether the person belongs to the audience of the stored message, by the definition delivery uses.
func (p *Popups) inAudience(ctx context.Context, announcement domain.Announcement, userID uuid.UUID, now time.Time) (bool, error) {
	audienceFilter, err := p.filterOf(ctx, announcement, now)
	if err != nil {
		// A message whose audience cannot be resolved (it was edited into something invalid) reaches nobody.
		p.log.Warn("resolve the audience of a popup", "error", err, "announcement", announcement.ID)
		return false, nil
	}
	members, err := p.announcements.FilterPeople(ctx, audienceFilter, []uuid.UUID{userID})
	return len(members) > 0, err
}
