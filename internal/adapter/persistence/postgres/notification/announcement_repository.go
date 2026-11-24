package notificationpg

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

// AnnouncementRepository reads and writes announcements.
type AnnouncementRepository struct{ pool *pgxpool.Pool }

// NewAnnouncementRepository returns a repository on the pool.
func NewAnnouncementRepository(pool *pgxpool.Pool) *AnnouncementRepository {
	return &AnnouncementRepository{pool: pool}
}

func (repository *AnnouncementRepository) queries(ctx context.Context) *sqlcgen.Queries {
	return postgres.Queries(ctx, repository.pool)
}

// content is what Insert and Update write: the columns both share.
type content struct {
	targeting, title, body, ctaLabel []byte
}

func contentOf(announcement domain.Announcement) (content, error) {
	var columns content
	var err error
	if columns.targeting, err = criteriaBytes(announcement.Criteria); err != nil {
		return columns, err
	}
	if columns.title, err = textsBytes(announcement.Title); err != nil {
		return columns, err
	}
	if columns.body, err = textsBytes(announcement.Body); err != nil {
		return columns, err
	}
	columns.ctaLabel, err = optionalTextsBytes(announcement.CTALabel)
	return columns, err
}

func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Insert writes a new message (a draft: it has no publishing date) and returns its id.
func (repository *AnnouncementRepository) Insert(ctx context.Context, announcement domain.Announcement) (uuid.UUID, error) {
	columns, err := contentOf(announcement)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := repository.queries(ctx).InsertAnnouncement(ctx, sqlcgen.InsertAnnouncementParams{
		CreatedByID: announcement.CreatedByID, SenderRole: announcement.SenderRole, LandlordID: announcement.LandlordID, Audience: string(announcement.Scope), Targeting: columns.targeting,
		Type: string(announcement.Type), Title: columns.title, Body: columns.body, SendNotification: announcement.SendNotification, ShowPopup: announcement.ShowPopup,
		StartsAt: announcement.StartsAt, EndsAt: announcement.EndsAt, Frequency: string(announcement.Frequency), FrequencyDays: int16Ptr(announcement.FrequencyDays),
		MaxPerPeriod: int16Ptr(announcement.MaxPerPeriod), MaxTotal: int16Ptr(announcement.MaxTotal), Priority: int16(announcement.Priority), //nolint:gosec // 0..10
		PopupSize: string(announcement.PopupSize), CtaLabel: columns.ctaLabel, CtaUrl: nonEmptyPtr(announcement.CTAURL), Revision: int32(announcement.Revision), //nolint:gosec // small
	})
	return id, postgres.Translate(err)
}

// Update writes the content, the revision and the sender role.
func (repository *AnnouncementRepository) Update(ctx context.Context, announcement domain.Announcement) error {
	columns, err := contentOf(announcement)
	if err != nil {
		return err
	}
	n, err := repository.queries(ctx).UpdateAnnouncement(ctx, sqlcgen.UpdateAnnouncementParams{
		ID: announcement.ID, SenderRole: announcement.SenderRole, Audience: string(announcement.Scope), Targeting: columns.targeting,
		Type: string(announcement.Type), Title: columns.title, Body: columns.body, SendNotification: announcement.SendNotification, ShowPopup: announcement.ShowPopup,
		StartsAt: announcement.StartsAt, EndsAt: announcement.EndsAt, Frequency: string(announcement.Frequency), FrequencyDays: int16Ptr(announcement.FrequencyDays),
		MaxPerPeriod: int16Ptr(announcement.MaxPerPeriod), MaxTotal: int16Ptr(announcement.MaxTotal), Priority: int16(announcement.Priority), //nolint:gosec // 0..10
		PopupSize: string(announcement.PopupSize), CtaLabel: columns.ctaLabel, CtaUrl: nonEmptyPtr(announcement.CTAURL), Revision: int32(announcement.Revision), //nolint:gosec // small
	})
	if err != nil {
		return postgres.Translate(err)
	}
	if n == 0 {
		return shared.ErrNotFound
	}
	return nil
}

// Get returns a message with its creator, the people picked, and what happened to it.
func (repository *AnnouncementRepository) Get(ctx context.Context, id uuid.UUID) (domain.AnnouncementDetail, error) {
	row, err := repository.queries(ctx).GetAnnouncement(ctx, id)
	if err != nil {
		return domain.AnnouncementDetail{}, postgres.Translate(err)
	}
	announcement, err := announcementOf(row)
	if err != nil {
		return domain.AnnouncementDetail{}, err
	}
	detail := domain.AnnouncementDetail{Announcement: announcement}
	if err := repository.fill(ctx, []*domain.AnnouncementDetail{&detail}); err != nil {
		return domain.AnnouncementDetail{}, err
	}
	people, err := repository.queries(ctx).ListAnnouncementRecipientPeople(ctx, id)
	if err != nil {
		return domain.AnnouncementDetail{}, postgres.Translate(err)
	}
	for _, p := range people {
		detail.People = append(detail.People, domain.Person{ID: p.ID, FullName: p.FullName, Email: p.Email})
	}
	return detail, nil
}

// ForUpdate loads the message and locks its row until the transaction ends.
func (repository *AnnouncementRepository) ForUpdate(ctx context.Context, id uuid.UUID) (domain.Announcement, error) {
	row, err := repository.queries(ctx).LockAnnouncement(ctx, id)
	if err != nil {
		return domain.Announcement{}, postgres.Translate(err)
	}
	return announcementOf(row)
}

// List returns a page of messages and how many there are.
func (repository *AnnouncementRepository) List(ctx context.Context, query domain.AnnouncementQuery, now time.Time) ([]domain.AnnouncementDetail, int, error) {
	var status, typ, channel *string
	if query.Status != nil {
		s := string(*query.Status)
		status = &s
	}
	if query.Type != nil {
		s := string(*query.Type)
		typ = &s
	}
	if query.Channel != nil {
		s := string(*query.Channel)
		channel = &s
	}
	total, err := repository.queries(ctx).CountAnnouncements(ctx, sqlcgen.CountAnnouncementsParams{
		LandlordID: query.LandlordID, Status: status, Now: now, FromAt: query.From, ToAt: query.To, Type: typ, Channel: channel,
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	rows, err := repository.queries(ctx).ListAnnouncements(ctx, sqlcgen.ListAnnouncementsParams{
		LandlordID: query.LandlordID, Status: status, Now: now, FromAt: query.From, ToAt: query.To, Type: typ, Channel: channel,
		PageLimit: int32(query.Limit), PageOffset: int32(query.Offset), //nolint:gosec // bounded by the page parser
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.AnnouncementDetail, len(rows))
	refs := make([]*domain.AnnouncementDetail, len(rows))
	for i, row := range rows {
		announcement, err := announcementOf(row)
		if err != nil {
			return nil, 0, err
		}
		out[i] = domain.AnnouncementDetail{Announcement: announcement}
		refs[i] = &out[i]
	}
	if err := repository.fill(ctx, refs); err != nil {
		return nil, 0, err
	}
	return out, int(total), nil
}

// fill adds the creator, the read count and the popup statistics to messages, one query each for all of them.
func (repository *AnnouncementRepository) fill(ctx context.Context, items []*domain.AnnouncementDetail) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(items))
	var creators []uuid.UUID
	for i, detail := range items {
		ids[i] = detail.ID
		if detail.CreatedByID != nil {
			creators = append(creators, *detail.CreatedByID)
		}
	}
	reads, err := repository.queries(ctx).AnnouncementReadCounts(ctx, ids)
	if err != nil {
		return postgres.Translate(err)
	}
	readBy := map[uuid.UUID]int{}
	for _, row := range reads {
		if row.AnnouncementID != nil {
			readBy[*row.AnnouncementID] = int(row.ReadCount)
		}
	}
	impressions, err := repository.queries(ctx).AnnouncementImpressionCounts(ctx, ids)
	if err != nil {
		return postgres.Translate(err)
	}
	statsBy := map[uuid.UUID]domain.PopupStats{}
	for _, row := range impressions {
		statsBy[row.AnnouncementID] = domain.PopupStats{Seen: int(row.Seen), Dismissed: int(row.Dismissed), Clicked: int(row.Clicked)}
	}
	images, err := repository.imagesOf(ctx, ids)
	if err != nil {
		return err
	}
	people := map[uuid.UUID]domain.Person{}
	if len(creators) > 0 {
		rows, err := repository.queries(ctx).ListAnnouncementCreators(ctx, creators)
		if err != nil {
			return postgres.Translate(err)
		}
		for _, row := range rows {
			people[row.ID] = domain.Person{ID: row.ID, FullName: row.FullName, Email: row.Email}
		}
	}
	for _, detail := range items {
		detail.ReadCount = readBy[detail.ID]
		detail.PopupStats = statsBy[detail.ID]
		detail.Images = images[detail.ID]
		if detail.CreatedByID != nil {
			if p, ok := people[*detail.CreatedByID]; ok {
				detail.CreatedBy = &p
			}
		}
	}
	return nil
}

// RecipientIDs are the people a sender picked one by one.
func (repository *AnnouncementRepository) RecipientIDs(ctx context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	ids, err := repository.queries(ctx).ListAnnouncementRecipientIDs(ctx, id)
	return ids, postgres.Translate(err)
}

// ReplaceRecipients makes the people picked exactly userIDs.
func (repository *AnnouncementRepository) ReplaceRecipients(ctx context.Context, id uuid.UUID, userIDs []uuid.UUID) error {
	if err := repository.queries(ctx).DeleteAnnouncementRecipients(ctx, id); err != nil {
		return postgres.Translate(err)
	}
	if len(userIDs) == 0 {
		return nil
	}
	return postgres.Translate(repository.queries(ctx).InsertAnnouncementRecipients(ctx, sqlcgen.InsertAnnouncementRecipientsParams{AnnouncementID: id, UserIds: userIDs}))
}

// SetPublished sets (or, with nil, clears) the publishing time.
func (repository *AnnouncementRepository) SetPublished(ctx context.Context, id uuid.UUID, at *time.Time) error {
	n, err := repository.queries(ctx).SetAnnouncementPublished(ctx, sqlcgen.SetAnnouncementPublishedParams{ID: id, PublishedAt: at})
	if err != nil {
		return postgres.Translate(err)
	}
	if n == 0 {
		return shared.ErrNotFound
	}
	return nil
}

// SetRecalled marks the message recalled and deletes its notifications; it returns how many it deleted. A message that
// is already recalled is domain.ErrRecalled.
func (repository *AnnouncementRepository) SetRecalled(ctx context.Context, id uuid.UUID, at time.Time) (int, error) {
	n, err := repository.queries(ctx).RecallAnnouncement(ctx, sqlcgen.RecallAnnouncementParams{ID: id, RecalledAt: at})
	if err != nil {
		return 0, postgres.Translate(err)
	}
	if n == 0 {
		return 0, domain.ErrRecalled
	}
	deleted, err := repository.queries(ctx).DeleteAnnouncementNotifications(ctx, &id)
	return int(deleted), postgres.Translate(err)
}

// SetRecipientCount stores how many people the message went to.
func (repository *AnnouncementRepository) SetRecipientCount(ctx context.Context, id uuid.UUID, n int) error {
	return postgres.Translate(repository.queries(ctx).SetAnnouncementRecipientCount(ctx, sqlcgen.SetAnnouncementRecipientCountParams{
		ID: id, RecipientCount: int32(n), //nolint:gosec // bounded by the number of accounts
	}))
}

// ClaimDelivery marks the notifications as sent when they are due and not sent yet; false means somebody else did, or
// they are not due. Concurrent callers get exactly one true: the second waits for the first's row lock, then finds the
// message sent.
func (repository *AnnouncementRepository) ClaimDelivery(ctx context.Context, id uuid.UUID, at time.Time) (bool, error) {
	_, err := repository.queries(ctx).ClaimAnnouncementDelivery(ctx, sqlcgen.ClaimAnnouncementDeliveryParams{ID: id, At: at})
	if errors.Is(postgres.Translate(err), shared.ErrNotFound) {
		return false, nil
	}
	return err == nil, postgres.Translate(err)
}

// DeliverNotifications writes one notification per person of the audience (once each) and returns their user ids.
func (repository *AnnouncementRepository) DeliverNotifications(ctx context.Context, id uuid.UUID, filter domain.AudienceFilter, params map[string]string, audience domain.Audience) ([]uuid.UUID, error) {
	raw, err := jsonObject(params)
	if err != nil {
		return nil, err
	}
	ids, err := repository.queries(ctx).DeliverAnnouncementNotifications(ctx, sqlcgen.DeliverAnnouncementNotificationsParams{
		Params: raw, AudienceRole: nonEmptyPtr(string(audience)), AnnouncementID: id,
		Scope: filter.Scope, Landlord: filter.LandlordID, Picked: filter.Picked, Roles: filter.Roles, ExcludedRoles: nonNil(filter.ExcludedRoles),
		ProvinceCodes: int32s(filter.ProvinceCodes), WardCodes: int32s(filter.WardCodes), PlanCodes: filter.PlanCodes, PlanNone: filter.PlanNone,
		CreatedFrom: filter.CreatedFrom, CreatedUntil: filter.CreatedUntil, CreatedBefore: filter.CreatedBefore, ActiveSince: filter.ActiveSince,
		UserIds: filter.UserIDs, Now: filter.Now,
	})
	return ids, postgres.Translate(err)
}

// Due returns up to limit published messages whose notifications are due.
func (repository *AnnouncementRepository) Due(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	ids, err := repository.queries(ctx).ListDueAnnouncementIDs(ctx, sqlcgen.ListDueAnnouncementIDsParams{Now: now, PageLimit: int32(limit)}) //nolint:gosec // announcement small constant
	return ids, postgres.Translate(err)
}

// CountAudience counts the people of an audience.
func (repository *AnnouncementRepository) CountAudience(ctx context.Context, filter domain.AudienceFilter) (int, error) {
	n, err := repository.queries(ctx).CountAnnouncementAudience(ctx, sqlcgen.CountAnnouncementAudienceParams{
		Scope: filter.Scope, Landlord: filter.LandlordID, Picked: filter.Picked, Roles: filter.Roles, ExcludedRoles: nonNil(filter.ExcludedRoles),
		ProvinceCodes: int32s(filter.ProvinceCodes), WardCodes: int32s(filter.WardCodes), PlanCodes: filter.PlanCodes, PlanNone: filter.PlanNone,
		CreatedFrom: filter.CreatedFrom, CreatedUntil: filter.CreatedUntil, CreatedBefore: filter.CreatedBefore, ActiveSince: filter.ActiveSince,
		UserIds: filter.UserIDs, Now: filter.Now,
	})
	return int(n), postgres.Translate(err)
}

// FilterPeople returns the ones of ids that belong to the audience.
func (repository *AnnouncementRepository) FilterPeople(ctx context.Context, filter domain.AudienceFilter, ids []uuid.UUID) ([]uuid.UUID, error) {
	out, err := repository.queries(ctx).FilterAnnouncementAudience(ctx, sqlcgen.FilterAnnouncementAudienceParams{
		Scope: filter.Scope, Landlord: filter.LandlordID, Picked: filter.Picked, Roles: filter.Roles, ExcludedRoles: nonNil(filter.ExcludedRoles),
		ProvinceCodes: int32s(filter.ProvinceCodes), WardCodes: int32s(filter.WardCodes), PlanCodes: filter.PlanCodes, PlanNone: filter.PlanNone,
		CreatedFrom: filter.CreatedFrom, CreatedUntil: filter.CreatedUntil, CreatedBefore: filter.CreatedBefore, ActiveSince: filter.ActiveSince,
		UserIds: filter.UserIDs, Now: filter.Now, Ids: ids,
	})
	return out, postgres.Translate(err)
}

// escapeLike escapes the characters that mean something in a LIKE pattern, and lowercases the text.
func escapeLike(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// People searches the running tenants of a landlord by name or email.
func (repository *AnnouncementRepository) People(ctx context.Context, landlordID uuid.UUID, search string, limit, offset int) ([]domain.Person, int, error) {
	pattern := escapeLike(search)
	total, err := repository.queries(ctx).CountLandlordPeople(ctx, sqlcgen.CountLandlordPeopleParams{LandlordID: landlordID, Search: pattern})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	rows, err := repository.queries(ctx).SearchLandlordPeople(ctx, sqlcgen.SearchLandlordPeopleParams{
		LandlordID: landlordID, Search: pattern, PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by the page parser
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.Person, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.Person{ID: row.ID, FullName: row.FullName, Email: row.Email})
	}
	return out, int(total), nil
}

// WelcomeExists says whether the platform already has a welcome message.
func (repository *AnnouncementRepository) WelcomeExists(ctx context.Context) (bool, error) {
	ok, err := repository.queries(ctx).WelcomeAnnouncementExists(ctx)
	return ok, postgres.Translate(err)
}

// nonNil is an empty list instead of nil, where a NULL array would not mean "none" (the excluded roles).
func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// Find returns the message without locking it or reading what happened to it.
func (repository *AnnouncementRepository) Find(ctx context.Context, id uuid.UUID) (domain.Announcement, error) {
	row, err := repository.queries(ctx).GetAnnouncement(ctx, id)
	if err != nil {
		return domain.Announcement{}, postgres.Translate(err)
	}
	return announcementOf(row)
}

// PopupCandidates returns up to limit popups that are out at now, most important first, with their pictures and who
// wrote them.
func (repository *AnnouncementRepository) PopupCandidates(ctx context.Context, now time.Time, limit int) ([]domain.AnnouncementDetail, error) {
	rows, err := repository.queries(ctx).ListPopupCandidates(ctx, sqlcgen.ListPopupCandidatesParams{Now: now, PageLimit: int32(limit)}) //nolint:gosec // a small constant
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.AnnouncementDetail, len(rows))
	ids := make([]uuid.UUID, len(rows))
	var creators []uuid.UUID
	for i, row := range rows {
		announcement, err := announcementOf(row)
		if err != nil {
			return nil, err
		}
		out[i] = domain.AnnouncementDetail{Announcement: announcement}
		ids[i] = row.ID
		if row.CreatedByID != nil {
			creators = append(creators, *row.CreatedByID)
		}
	}
	if len(rows) == 0 {
		return out, nil
	}
	images, err := repository.imagesOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	people := map[uuid.UUID]domain.Person{}
	if len(creators) > 0 {
		names, err := repository.queries(ctx).ListAnnouncementCreators(ctx, creators)
		if err != nil {
			return nil, postgres.Translate(err)
		}
		for _, row := range names {
			people[row.ID] = domain.Person{ID: row.ID, FullName: row.FullName, Email: row.Email}
		}
	}
	for i := range out {
		out[i].Images = images[out[i].ID]
		if out[i].CreatedByID != nil {
			if p, ok := people[*out[i].CreatedByID]; ok {
				out[i].CreatedBy = &p
			}
		}
	}
	return out, nil
}

// imagesOf reads the pictures of the messages, each message's in order.
func (repository *AnnouncementRepository) imagesOf(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]domain.Image, error) {
	rows, err := repository.queries(ctx).ListAnnouncementImages(ctx, ids)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := map[uuid.UUID][]domain.Image{}
	for _, row := range rows {
		out[row.AnnouncementID] = append(out[row.AnnouncementID], imageOf(row))
	}
	return out, nil
}
