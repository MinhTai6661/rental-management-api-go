package notification

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

// The audit actions of the announcement use cases.
const (
	AuditAnnouncementCreated     = "announcement.created"
	AuditAnnouncementUpdated     = "announcement.updated"
	AuditAnnouncementSent        = "announcement.sent"
	AuditAnnouncementUnpublished = "announcement.unpublished"
	AuditAnnouncementRecalled    = "announcement.recalled"
	AuditAnnouncementImages      = "announcement.images_changed"
)

const (
	// dueBatch is how many due messages one DispatchDue run delivers at most.
	dueBatch = 100
	// pushChunk is how many people one realtime push names.
	pushChunk = 100
	// BellURL is the page the notification of an announcement leads to.
	BellURL = "/notifications"
)

// Announcements is what the platform's admins and the landlords do with their messages: write, put out, take back and
// recall them, and count who they reach. A message that is not the sender's is "not found".
type Announcements struct{ *core }

// NewAnnouncements returns the use case.
func NewAnnouncements(deps Deps, o Options) *Announcements { return &Announcements{newCore(deps, o)} }

// Filters narrow a list of messages.
type Filters struct {
	Status   *domain.Status
	From, To *time.Time
	Type     *domain.Type
	Channel  *domain.Channel
}

// List returns a page of the sender's messages, newest first, and how many there are. The platform's admins see the
// messages of the platform; a landlord sees their own.
func (announcements *Announcements) List(ctx context.Context, sender domain.Sender, filters Filters, limit, offset int) ([]domain.AnnouncementDetail, int, error) {
	q := domain.AnnouncementQuery{Status: filters.Status, From: filters.From, To: filters.To, Type: filters.Type, Channel: filters.Channel, Limit: limit, Offset: offset}
	switch sender.Kind() {
	case domain.SenderPlatform:
	case domain.SenderLandlord:
		id := sender.UserID
		q.LandlordID = &id
	default:
		return nil, 0, domain.ErrScopeNotAllowed
	}
	now := announcements.clock.Now()
	rows, total, err := announcements.announcements.List(ctx, q, now)
	if err != nil {
		return nil, 0, err
	}
	for i := range rows {
		rows[i].Status = rows[i].StatusAt(now)
	}
	return rows, total, nil
}

// Get returns one of the sender's messages with what happened to it.
func (announcements *Announcements) Get(ctx context.Context, sender domain.Sender, id uuid.UUID) (domain.AnnouncementDetail, error) {
	detail, err := announcements.announcements.Get(ctx, id)
	if err != nil {
		return domain.AnnouncementDetail{}, err
	}
	if !detail.Owned(sender) {
		return domain.AnnouncementDetail{}, shared.ErrNotFound
	}
	detail.Status = detail.StatusAt(announcements.clock.Now())
	return detail, nil
}

// Create writes a message and, when publish is true, puts it out: its notifications go to the audience now (or when it
// starts) and its popup shows from its start.
func (announcements *Announcements) Create(ctx context.Context, sender domain.Sender, content domain.Content, publish bool) (domain.AnnouncementDetail, error) {
	if err := domain.ValidateContent(content, sender); err != nil {
		return domain.AnnouncementDetail{}, err
	}
	now := announcements.clock.Now()
	_, picks, err := announcements.prepareAudience(ctx, sender, content, now)
	if err != nil {
		return domain.AnnouncementDetail{}, err
	}
	announcement := announcementOf(content, sender.Role)
	announcement.CreatedByID = &sender.UserID
	if sender.Kind() == domain.SenderLandlord {
		announcement.LandlordID = &sender.UserID
	}
	var id uuid.UUID
	err = announcements.tx.WithinTx(ctx, func(ctx context.Context) error {
		var err error
		if id, err = announcements.announcements.Insert(ctx, announcement); err != nil {
			return err
		}
		if content.Scope.IsPicked() {
			if err := announcements.announcements.ReplaceRecipients(ctx, id, picks); err != nil {
				return err
			}
		}
		return announcements.audit.Record(ctx, auditOf(AuditAnnouncementCreated, sender, id, announcement, map[string]any{"audience": string(content.Scope), "popup": content.ShowPopup, "notification": content.SendNotification}))
	})
	if err != nil {
		return domain.AnnouncementDetail{}, err
	}
	if publish {
		return announcements.Publish(ctx, sender, id)
	}
	return announcements.Get(ctx, sender, id)
}

// Update replaces the content of a message (it is a full replacement, so what is not sent goes back to its default).
// Notifications already sent keep their words; the popup follows the edit at once. reshow raises the revision, so the
// people who closed the popup see it again. publish puts a draft out. A recalled message cannot be changed.
func (announcements *Announcements) Update(ctx context.Context, sender domain.Sender, id uuid.UUID, content domain.Content, publish, reshow bool) (domain.AnnouncementDetail, error) {
	var published bool
	err := announcements.tx.WithinTx(ctx, func(ctx context.Context) error {
		announcement, err := announcements.locked(ctx, sender, id)
		if err != nil {
			return err
		}
		// An admin editing a message written by announcements more senior sender takes it over at their own seniority, so the
		// criteria are re-checked against what the editor may reach and a message aimed at staff above them cannot stay.
		editor := editorOf(sender, announcement)
		if err := domain.ValidateContent(content, editor); err != nil {
			return err
		}
		now := announcements.clock.Now()
		_, picks, err := announcements.prepareAudience(ctx, editor, content, now)
		if err != nil {
			return err
		}
		next := announcementOf(content, editor.Role)
		next.ID, next.CreatedByID, next.LandlordID, next.Revision = announcement.ID, announcement.CreatedByID, announcement.LandlordID, announcement.Revision
		if reshow {
			next.Revision++
		}
		if err := announcements.announcements.Update(ctx, next); err != nil {
			return err
		}
		recipients := []uuid.UUID{}
		if content.Scope.IsPicked() {
			recipients = picks
		}
		if err := announcements.announcements.ReplaceRecipients(ctx, id, recipients); err != nil {
			return err
		}
		published = announcement.PublishedAt != nil
		return announcements.audit.Record(ctx, auditOf(AuditAnnouncementUpdated, sender, id, announcement, map[string]any{"audience": string(content.Scope), "popup": content.ShowPopup, "reshow": reshow}))
	})
	if err != nil {
		return domain.AnnouncementDetail{}, err
	}
	if publish && !published {
		return announcements.Publish(ctx, sender, id)
	}
	return announcements.Get(ctx, sender, id)
}

// Publish puts a written message out. It is safe to call again: a message that is out stays as it is. A message whose
// start has not come waits for DispatchDue; a failed delivery is retried by it too.
func (announcements *Announcements) Publish(ctx context.Context, sender domain.Sender, id uuid.UUID) (domain.AnnouncementDetail, error) {
	deliver := false
	err := announcements.tx.WithinTx(ctx, func(ctx context.Context) error {
		announcement, err := announcements.locked(ctx, sender, id)
		if err != nil || announcement.PublishedAt != nil {
			return err
		}
		now := announcements.clock.Now()
		if err := announcements.announcements.SetPublished(ctx, id, &now); err != nil {
			return err
		}
		picked := 0
		if !announcement.SendNotification {
			// Nothing is sent: the figure shown is the people the popup is for.
			audienceFilter, err := announcements.filterOf(ctx, announcement, now)
			if err != nil {
				return err
			}
			n, err := announcements.announcements.CountAudience(ctx, audienceFilter)
			if err != nil {
				return err
			}
			if err := announcements.announcements.SetRecipientCount(ctx, id, n); err != nil {
				return err
			}
		}
		if announcement.Scope.IsPicked() {
			ids, err := announcements.announcements.RecipientIDs(ctx, id)
			if err != nil {
				return err
			}
			picked = len(ids)
		}
		deliver = announcement.SendNotification && (announcement.StartsAt == nil || !announcement.StartsAt.After(now))
		return announcements.audit.Record(ctx, auditOf(AuditAnnouncementSent, sender, id, announcement, map[string]any{
			"audience": string(announcement.Scope), "popup": announcement.ShowPopup, "notification": announcement.SendNotification, "picked": picked,
		}))
	})
	if err != nil {
		return domain.AnnouncementDetail{}, err
	}
	if deliver {
		if _, err := announcements.Deliver(ctx, id); err != nil {
			announcements.log.Warn("deliver an announcement; the dispatch will retry", "error", err, "announcement", id)
		}
	}
	return announcements.Get(ctx, sender, id)
}

// Unpublish takes a message back to a draft: its popup stops and it can be edited and put out again. Notifications
// already delivered stay in the inboxes (use Recall to remove them) and putting it out again does not send them twice.
func (announcements *Announcements) Unpublish(ctx context.Context, sender domain.Sender, id uuid.UUID) (domain.AnnouncementDetail, error) {
	err := announcements.tx.WithinTx(ctx, func(ctx context.Context) error {
		announcement, err := announcements.locked(ctx, sender, id)
		if err != nil || announcement.PublishedAt == nil {
			return err
		}
		if err := announcements.announcements.SetPublished(ctx, id, nil); err != nil {
			return err
		}
		return announcements.audit.Record(ctx, auditOf(AuditAnnouncementUnpublished, sender, id, announcement, map[string]any{"audience": string(announcement.Scope)}))
	})
	if err != nil {
		return domain.AnnouncementDetail{}, err
	}
	return announcements.Get(ctx, sender, id)
}

// Recall takes a message back for good: it disappears from the inbox of everybody who got it and its popup stops. A
// message that is already recalled is domain.ErrRecalled.
func (announcements *Announcements) Recall(ctx context.Context, sender domain.Sender, id uuid.UUID) error {
	return announcements.tx.WithinTx(ctx, func(ctx context.Context) error {
		announcement, err := announcements.locked(ctx, sender, id)
		if err != nil {
			return err
		}
		deleted, err := announcements.announcements.SetRecalled(ctx, id, announcements.clock.Now())
		if err != nil {
			return err
		}
		return announcements.audit.Record(ctx, auditOf(AuditAnnouncementRecalled, sender, id, announcement, map[string]any{"audience": string(announcement.Scope), "deleted": deleted}))
	})
}

// ReachInput is an audience to count, without saving anything.
type ReachInput struct {
	Scope     domain.Scope
	Criteria  domain.Criteria
	PeopleIDs []uuid.UUID
}

// Reach says how many people an audience would reach now (shown while writing). It counts with the very definition
// delivery uses.
func (announcements *Announcements) Reach(ctx context.Context, sender domain.Sender, input ReachInput) (int, error) {
	audienceFilter, err := domain.ResolveFilter(input.Scope, sender, input.Criteria, input.PeopleIDs, announcements.clock.Now())
	if err != nil {
		return 0, err
	}
	return announcements.announcements.CountAudience(ctx, audienceFilter)
}

// People searches the people a landlord can write to (their running tenants) by name or email, with how many match.
func (announcements *Announcements) People(ctx context.Context, sender domain.Sender, search string, limit, offset int) ([]domain.Person, int, error) {
	if sender.Kind() != domain.SenderLandlord {
		return nil, 0, domain.ErrScopeNotAllowed
	}
	return announcements.announcements.People(ctx, sender.UserID, search, limit, offset)
}

// Deliver sends the notifications of a published message to its audience, once: concurrent and repeated calls deliver
// exactly one set. It returns how many people got it (0 when somebody else delivered, or it is not due).
func (announcements *Announcements) Deliver(ctx context.Context, id uuid.UUID) (int, error) {
	var delivered []uuid.UUID
	var audience domain.Audience
	err := announcements.tx.WithinTx(ctx, func(ctx context.Context) error {
		now := announcements.clock.Now()
		claimed, err := announcements.announcements.ClaimDelivery(ctx, id, now)
		if err != nil || !claimed {
			return err
		}
		announcement, err := announcements.announcements.ForUpdate(ctx, id)
		if err != nil {
			return err
		}
		audienceFilter, err := announcements.filterOf(ctx, announcement, now)
		if err != nil {
			return err
		}
		audience = announcement.Scope.NotificationAudience()
		if delivered, err = announcements.announcements.DeliverNotifications(ctx, id, audienceFilter, paramsOf(announcement), audience); err != nil {
			return err
		}
		return announcements.announcements.SetRecipientCount(ctx, id, len(delivered))
	})
	if err != nil {
		return 0, err
	}
	for start := 0; start < len(delivered); start += pushChunk {
		end := min(start+pushChunk, len(delivered))
		e := Event{Kind: domain.KindAnnouncement, URL: BellURL, Audience: audience}
		if err := announcements.pusher.Push(ctx, delivered[start:end], e); err != nil {
			announcements.log.Warn("push an announcement", "error", err, "announcement", id)
		}
	}
	return len(delivered), nil
}

// DispatchDue delivers the notifications of the published messages whose start has come (a scheduled message waits for
// it) and of the ones whose delivery failed earlier. One failing message is logged and the others still go. It returns
// how many messages it delivered. It is safe to run again and to run twice at once.
func (announcements *Announcements) DispatchDue(ctx context.Context) (int, error) {
	ids, err := announcements.announcements.Due(ctx, announcements.clock.Now(), dueBatch)
	if err != nil {
		return 0, err
	}
	messages := 0
	for _, id := range ids {
		n, err := announcements.Deliver(ctx, id)
		if err != nil {
			announcements.log.Warn("deliver announcements due announcement", "error", err, "announcement", id)
			continue
		}
		if n > 0 {
			messages++
		}
	}
	return messages, nil
}

// locked loads the message with its row locked and checks it is the sender's and not recalled.
func (c *core) locked(ctx context.Context, sender domain.Sender, id uuid.UUID) (domain.Announcement, error) {
	announcement, err := c.announcements.ForUpdate(ctx, id)
	if err != nil {
		return domain.Announcement{}, err
	}
	if !announcement.Owned(sender) {
		return domain.Announcement{}, shared.ErrNotFound
	}
	if announcement.RecalledAt != nil {
		return domain.Announcement{}, domain.ErrRecalled
	}
	return announcement, nil
}

// prepareAudience resolves the audience of what a sender wrote and, for a landlord, makes sure somebody is reached: the
// people picked are cut down to the ones that are really their tenants. It returns the filter and the picks to store.
func (announcements *Announcements) prepareAudience(ctx context.Context, sender domain.Sender, content domain.Content, now time.Time) (domain.AudienceFilter, []uuid.UUID, error) {
	audienceFilter, err := domain.ResolveFilter(content.Scope, sender, content.Criteria, content.PeopleIDs, now)
	if err != nil {
		return audienceFilter, nil, err
	}
	if sender.Kind() != domain.SenderLandlord {
		return audienceFilter, nil, nil
	}
	if content.Scope.IsPicked() {
		allowed, err := announcements.announcements.FilterPeople(ctx, audienceFilter, content.PeopleIDs)
		if err != nil {
			return audienceFilter, nil, err
		}
		if len(allowed) == 0 {
			return audienceFilter, nil, domain.ErrNoPeople
		}
		audienceFilter.Picked = allowed
		return audienceFilter, allowed, nil
	}
	n, err := announcements.announcements.CountAudience(ctx, audienceFilter)
	if err != nil {
		return audienceFilter, nil, err
	}
	if n == 0 {
		return audienceFilter, nil, domain.ErrNoPeople
	}
	return audienceFilter, nil, nil
}

// filterOf is the audience of a stored message: its stored criteria, picks and sender (never the creator's current
// roles).
func (c *core) filterOf(ctx context.Context, announcement domain.Announcement, now time.Time) (domain.AudienceFilter, error) {
	var picked []uuid.UUID
	if announcement.Scope.IsPicked() {
		var err error
		if picked, err = c.announcements.RecipientIDs(ctx, announcement.ID); err != nil {
			return domain.AudienceFilter{}, err
		}
	}
	return domain.ResolveFilter(announcement.Scope, storedSender(announcement), announcement.Criteria, picked, now)
}

// storedSender is the sender a stored message was written by: the role it was written in, and the landlord it is for.
func storedSender(announcement domain.Announcement) domain.Sender {
	s := domain.Sender{Role: announcement.SenderRole}
	switch {
	case announcement.LandlordID != nil:
		s.UserID = *announcement.LandlordID
	case announcement.CreatedByID != nil:
		s.UserID = *announcement.CreatedByID
	}
	return s
}

// editorOf is the sender as the stored message sees them when they edit it: a landlord stays a landlord; a staff editor
// keeps the stored role unless they are less senior than it, content which case they take over at their own role.
func editorOf(sender domain.Sender, announcement domain.Announcement) domain.Sender {
	if announcement.SenderKind() == domain.SenderLandlord {
		return sender
	}
	role := announcement.SenderRole
	if authz.Seniority(sender.Role) > authz.Seniority(role) {
		role = sender.Role
	}
	return domain.Sender{UserID: sender.UserID, Role: role}
}

// announcementOf is the stored form of what a sender wrote. Criteria are kept for the scopes they apply to.
func announcementOf(content domain.Content, senderRole string) domain.Announcement {
	announcement := domain.Announcement{
		SenderRole: senderRole, Scope: content.Scope, Type: content.Type, Title: content.Title, Body: content.Body,
		SendNotification: content.SendNotification, ShowPopup: content.ShowPopup, StartsAt: content.StartsAt, EndsAt: content.EndsAt,
		Frequency: content.Frequency, FrequencyDays: content.FrequencyDays, MaxPerPeriod: content.MaxPerPeriod, MaxTotal: content.MaxTotal,
		Priority: content.Priority, PopupSize: content.PopupSize, CTAURL: content.CTAURL, Revision: 1,
	}
	if content.CTAURL != "" {
		announcement.CTALabel = content.CTALabel
	}
	if content.Scope == domain.ScopeEveryone || content.Scope == domain.ScopeCustom {
		announcement.Criteria = content.Criteria.Normalize()
	}
	return announcement
}

// paramsOf are the values the web puts in the text of an announcement notification: its id and the texts of every
// language it was written in (the web picks the language of the person who looks).
func paramsOf(announcement domain.Announcement) map[string]string {
	p := map[string]string{"announcement_id": announcement.ID.String()}
	for _, l := range domain.Locales() {
		if announcement.Title.Has(l) {
			p["title_"+l] = announcement.Title[l]
		}
		if announcement.Body.Has(l) {
			p["body_"+l] = announcement.Body[l]
		}
	}
	return p
}

func auditOf(action string, sender domain.Sender, id uuid.UUID, announcement domain.Announcement, meta map[string]any) AuditEvent {
	return AuditEvent{Action: action, ActorID: sender.UserID, AnnouncementID: id, OwnerID: announcement.LandlordID, Meta: meta}
}
