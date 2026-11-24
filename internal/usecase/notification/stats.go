package notification

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

// ictZone is the business time zone: the days of the statistics are days there.
var ictZone = time.FixedZone("ICT", 7*3600)

// Statistics is how people reacted to the messages of a sender: totals and days, never who.
type Statistics struct{ *core }

// NewStatistics returns the use case.
func NewStatistics(deps Deps, o Options) *Statistics { return &Statistics{newCore(deps, o)} }

// StatsReport is the answer: the first and last day it covers (day starts in ICT) and the figures.
type StatsReport struct {
	First, Last time.Time
	Stats       domain.Stats
}

// Overview is everything the sender put out: the messages published within the days (and what their recipients did
// afterwards), and the events of each day over all their messages. A landlord sees their own messages, an admin the
// platform's. channel narrows the figures to the notification or to the popup; nil is both.
func (s *Statistics) Overview(ctx context.Context, sender domain.Sender, from, to *time.Time, channel *domain.Channel) (StatsReport, error) {
	q := domain.StatsQuery{Channel: channel}
	switch sender.Kind() {
	case domain.SenderPlatform:
	case domain.SenderLandlord:
		id := sender.UserID
		q.LandlordID = &id
	default:
		return StatsReport{}, domain.ErrScopeNotAllowed
	}
	if err := s.resolve(&q, from, to); err != nil {
		return StatsReport{}, err
	}
	messages, err := s.stats.Messages(ctx, q)
	if err != nil {
		return StatsReport{}, err
	}
	stats := domain.Stats{Messages: &messages}
	if stats.Totals, err = s.stats.Totals(ctx, q, messages.Recipients); err != nil {
		return StatsReport{}, err
	}
	if stats.Series, err = s.stats.Series(ctx, q); err != nil {
		return StatsReport{}, err
	}
	return StatsReport{First: q.First, Last: q.Last, Stats: stats}, nil
}

// Detail is one message: what its recipients did since it went out, and day by day within the days.
func (s *Statistics) Detail(ctx context.Context, sender domain.Sender, id uuid.UUID, from, to *time.Time, channel *domain.Channel) (StatsReport, error) {
	announcement, err := s.announcements.Find(ctx, id)
	if err != nil {
		return StatsReport{}, err
	}
	if !announcement.Owned(sender) {
		return StatsReport{}, shared.ErrNotFound
	}
	q := domain.StatsQuery{AnnouncementID: &id, Channel: channel}
	if err := s.resolve(&q, from, to); err != nil {
		return StatsReport{}, err
	}
	recipients := 0
	if announcement.SendNotification {
		recipients = announcement.RecipientCount
	}
	stats := domain.Stats{}
	if stats.Totals, err = s.stats.Totals(ctx, q, recipients); err != nil {
		return StatsReport{}, err
	}
	if stats.Series, err = s.stats.Series(ctx, q); err != nil {
		return StatsReport{}, err
	}
	return StatsReport{First: q.First, Last: q.Last, Stats: stats}, nil
}

// resolve fills the days of the query from what the request gave (day starts in ICT).
func (s *Statistics) resolve(q *domain.StatsQuery, from, to *time.Time) error {
	now := s.clock.Now().In(ictZone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, ictZone)
	q.First, q.Last = domain.ResolveDays(from, to, today)
	if _, err := domain.Days(q.First, q.Last); err != nil {
		return err
	}
	q.CohortFrom, q.CohortTo = q.First, q.Last.AddDate(0, 0, 1)
	return nil
}
