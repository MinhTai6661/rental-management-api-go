//go:build integration

package notificationpg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

func (w *audienceWorld) popup(t *testing.T, sender string, landlord *uuid.UUID, published bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	creator := w.admin
	if landlord != nil {
		creator = *landlord
	}
	id, err := w.repo.Insert(ctx, domain.Announcement{
		CreatedByID: &creator, SenderRole: sender, LandlordID: landlord, Scope: domain.ScopeTenants, Type: domain.TypeNotice,
		Title: domain.Texts{"vi": "T"}, Body: domain.Texts{"vi": "B"}, SendNotification: true, ShowPopup: true,
		Frequency: domain.FrequencyOnce, PopupSize: domain.SizeMedium, Revision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if published {
		at := w.now
		if err := w.repo.SetPublished(ctx, id, &at); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestImpressionsRoundTrip(t *testing.T) {
	w := newAudienceWorld(t)
	ctx := context.Background()
	repo := NewImpressionRepository(w.pool)
	w.now = w.now.Truncate(time.Microsecond) // Postgres keeps microseconds
	id := w.popup(t, "admin", nil, true)
	if seen, err := repo.Seen(ctx, w.plain, []uuid.UUID{id}); err != nil || len(seen) != 0 {
		t.Fatalf("never met: %v %v", seen, err)
	}
	session := uuid.New()
	first := domain.AfterAction(nil, domain.ActionSeen, domain.PopupRules{Frequency: domain.FrequencyOnce, Revision: 1}, &session, w.now)
	if err := repo.Upsert(ctx, id, w.plain, first); err != nil {
		t.Fatal(err)
	}
	second := domain.AfterAction(&first, domain.ActionClick, domain.PopupRules{Frequency: domain.FrequencyOnce, Revision: 1}, &session, w.now.Add(time.Minute))
	if err := repo.Upsert(ctx, id, w.plain, second); err != nil {
		t.Fatal(err)
	}
	seen, err := repo.Seen(ctx, w.plain, []uuid.UUID{id, uuid.New()})
	if err != nil || len(seen) != 1 {
		t.Fatalf("%v %v", seen, err)
	}
	got := seen[id]
	if got.ViewCount != 1 || got.PeriodViewCount != 1 || got.ClickedAt == nil || got.DismissedAt == nil || got.SessionID == nil || *got.SessionID != session ||
		got.Revision != 1 || !got.FirstSeenAt.Equal(first.FirstSeenAt) {
		t.Errorf("%+v", got)
	}
	// One row per person: the other person has none.
	if other, _ := repo.Seen(ctx, w.fresh, []uuid.UUID{id}); len(other) != 0 {
		t.Error("another person's row")
	}
}

func TestImagesAndPopupCandidates(t *testing.T) {
	w := newAudienceWorld(t)
	ctx := context.Background()
	images := NewImageRepository(w.pool)
	id := w.popup(t, "admin", nil, true)
	draft := w.popup(t, "admin", nil, false)
	recalled := w.popup(t, "admin", nil, true)
	if _, err := w.repo.SetRecalled(ctx, recalled, w.now); err != nil {
		t.Fatal(err)
	}
	var added []domain.Image
	for i, key := range []string{"announcements/2026/11/a.webp", "announcements/2026/11/b.webp"} {
		img, err := images.Insert(ctx, id, domain.NewImage{FileKey: key, ContentType: "image/webp", Width: 10, Height: 5, SizeBytes: 3}, i)
		if err != nil {
			t.Fatal(err)
		}
		added = append(added, img)
	}
	if err := images.Update(ctx, id, added[1].ID, 0, "/find-rooms"); err != nil {
		t.Fatal(err)
	}
	if err := images.Update(ctx, id, added[0].ID, 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := images.Update(ctx, draft, added[0].ID, 0, ""); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another popup's picture: %v", err)
	}
	list, err := images.List(ctx, id)
	if err != nil || len(list) != 2 || list[0].ID != added[1].ID || list[0].LinkURL != "/find-rooms" || list[1].LinkURL != "" {
		t.Fatalf("ordered: %+v %v", list, err)
	}
	detail, err := w.repo.Get(ctx, id)
	if err != nil || len(detail.Images) != 2 || detail.Images[0].FileKey != "announcements/2026/11/b.webp" {
		t.Fatalf("Get carries the pictures: %+v %v", detail.Images, err)
	}

	candidates, err := w.repo.PopupCandidates(ctx, w.now.Add(time.Second), 20)
	if err != nil || len(candidates) != 1 || candidates[0].ID != id || len(candidates[0].Images) != 2 || candidates[0].CreatedBy == nil || candidates[0].CreatedBy.ID != w.admin {
		t.Fatalf("only the published, unrecalled popup is a candidate: %+v %v", candidates, err)
	}
	// A popup that ended or has not started is not one either.
	past := w.now.Add(-time.Hour)
	if _, err := w.pool.Exec(ctx, `UPDATE announcements SET ends_at = $2 WHERE id = $1`, id, past); err != nil {
		t.Fatal(err)
	}
	if c, _ := w.repo.PopupCandidates(ctx, w.now, 20); len(c) != 0 {
		t.Errorf("ended: %d", len(c))
	}
	if err := images.Delete(ctx, id, added[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := images.Delete(ctx, id, added[0].ID); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("twice: %v", err)
	}
	if _, err := w.repo.Find(ctx, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("find unknown: %v", err)
	}
}

// The first case of Laravel's AnnouncementStatsTest, with hand-computed answers: 4 recipients of whom 2 read it gives
// 50.0; 3 people saw the popup, 1 closed it and 1 pressed the button gives 33.3 and 33.3; and nobody gives null rates.
func TestStatsMatchWhatHappened(t *testing.T) {
	w := newAudienceWorld(t)
	ctx := context.Background()
	stats := NewStatsRepository(w.pool)
	imp := NewImpressionRepository(w.pool)
	ict := time.FixedZone("ICT", 7*3600)
	nowICT := w.now.In(ict)
	today := time.Date(nowICT.Year(), nowICT.Month(), nowICT.Day(), 0, 0, 0, 0, ict)

	id := w.popup(t, "admin", nil, true)
	other := w.popup(t, "admin", nil, true) // another message of the platform, nobody did anything
	mine := w.popup(t, "landlord", &w.landlord, true)
	gone := w.popup(t, "admin", nil, true)
	if err := w.repo.SetRecipientCount(ctx, id, 4); err != nil {
		t.Fatal(err)
	}
	if err := w.repo.SetRecipientCount(ctx, other, 6); err != nil {
		t.Fatal(err)
	}
	if err := w.repo.SetRecipientCount(ctx, mine, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := w.repo.SetRecalled(ctx, gone, w.now); err != nil {
		t.Fatal(err)
	}
	// 4 notifications of `id`, 2 read; one of the landlord's message read (not in the platform's figures).
	recipients := []uuid.UUID{w.renter, w.pro, w.fresh, w.active}
	for i, user := range recipients {
		readAt := "NULL"
		if i < 2 {
			readAt = "now()"
		}
		if _, err := w.pool.Exec(ctx, `INSERT INTO notifications (user_id, kind, announcement_id, read_at) VALUES ($1, 'announcement', $2, `+readAt+`)`, user, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.pool.Exec(ctx, `INSERT INTO notifications (user_id, kind, announcement_id, read_at) VALUES ($1, 'announcement', $2, now())`, w.renter, mine); err != nil {
		t.Fatal(err)
	}
	rules := domain.PopupRules{Frequency: domain.FrequencyOnce, Revision: 1}
	session := uuid.New()
	views := map[uuid.UUID]int{w.renter: 1, w.pro: 2, w.fresh: 3}
	for user, n := range views {
		var s *domain.Seen
		for range n {
			next := domain.AfterAction(s, domain.ActionSeen, rules, &session, w.now)
			s = &next
		}
		if user == w.pro {
			next := domain.AfterAction(s, domain.ActionDismiss, rules, &session, w.now)
			s = &next
		}
		if user == w.fresh {
			next := domain.AfterAction(s, domain.ActionClick, rules, &session, w.now)
			s = &next
		}
		if err := imp.Upsert(ctx, id, user, *s); err != nil {
			t.Fatal(err)
		}
	}

	q := domain.StatsQuery{First: today.AddDate(0, 0, -2), Last: today, CohortFrom: today.AddDate(0, 0, -2), CohortTo: today.AddDate(0, 0, 1)}
	messages, err := stats.Messages(ctx, q)
	if err != nil || messages.Total != 2 || messages.WithNotification != 2 || messages.WithPopup != 2 || messages.Recipients != 10 {
		t.Fatalf("the platform's cohort (not the landlord's, not the recalled): %+v %v", messages, err)
	}
	// One message: 4 recipients, 2 read.
	one := q
	one.AnnouncementID = &id
	totals, err := stats.Totals(ctx, one, 4)
	if err != nil {
		t.Fatal(err)
	}
	if n := totals.Notification; n.Read != 2 || n.ReadRate == nil || *n.ReadRate != 50 {
		t.Errorf("read: %+v", n)
	}
	p := totals.Popup
	if p.Viewers != 3 || p.Views != 6 || p.Closed != 2 || p.Clicked != 1 || *p.ViewsPerViewer != 2 || *p.CloseRate != 66.7 || *p.ClickRate != 33.3 {
		t.Errorf("popup: %+v", p)
	}
	// The cohort of the platform: the same people, over both messages.
	all, err := stats.Totals(ctx, q, messages.Recipients)
	if err != nil || all.Notification.Read != 2 || *all.Notification.ReadRate != 20 || all.Popup.Viewers != 3 {
		t.Errorf("cohort totals: %+v %v", all, err)
	}
	// A message nobody saw: null rates, not zero.
	none := q
	none.AnnouncementID = &other
	empty, _ := stats.Totals(ctx, none, 0)
	if empty.Popup.CloseRate != nil || empty.Popup.ViewsPerViewer != nil || empty.Notification.ReadRate != nil {
		t.Errorf("no viewers, no recipients: %+v %+v", empty.Popup, empty.Notification)
	}
	// Only the channel asked for.
	popup := domain.ChannelPopup
	one.Channel = &popup
	if only, _ := stats.Totals(ctx, one, 4); only.Notification != nil || only.Popup == nil {
		t.Errorf("channel popup: %+v", only)
	}

	series, err := stats.Series(ctx, one)
	if err != nil || len(series) != 3 {
		t.Fatalf("three days: %v %v", series, err)
	}
	last := series[2]
	if series[0].Viewers != 0 || last.Viewers != 3 || last.Closed != 2 || last.Clicked != 1 || last.Read != 0 {
		t.Errorf("the events fall on today: %+v", series)
	}
	one.Channel = nil
	if both, _ := stats.Series(ctx, one); both[2].Read != 2 {
		t.Errorf("reads today: %+v", both[2])
	}
	// The landlord's scope is theirs alone.
	lq := q
	lq.LandlordID = &w.landlord
	if lm, _ := stats.Messages(ctx, lq); lm.Total != 1 || lm.Recipients != 100 {
		t.Errorf("landlord cohort: %+v", lm)
	}
	// A cohort window that ends before the messages were published holds none of them.
	old := q
	old.CohortFrom, old.CohortTo = today.AddDate(0, 0, -10), today.AddDate(0, 0, -5)
	if om, _ := stats.Messages(ctx, old); om.Total != 0 {
		t.Errorf("old window: %+v", om)
	}
}
