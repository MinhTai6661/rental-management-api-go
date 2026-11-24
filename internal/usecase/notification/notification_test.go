package notification

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

var ctx = context.Background()

func fixedClock() Clock {
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	return ClockFunc(func() time.Time { return at })
}

func TestSendValidatesDropsDuplicatesAndPushesAfterInsert(t *testing.T) {
	repo, push := &repoFake{}, &pushFake{}
	d := NewDeliveries(Deps{Notifications: repo, Pusher: push}, Options{})
	a, b := uuid.New(), uuid.New()

	err := d.Send(ctx, Message{UserIDs: []uuid.UUID{a, uuid.Nil, b, a}, Kind: "invoice_sent", Params: map[string]string{"room": "P1"}, URL: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.rows) != 2 {
		t.Fatalf("want one row per distinct user, got %d", len(repo.rows))
	}
	if repo.rows[0].Audience != domain.AudienceTenant {
		t.Errorf("the default audience of the kind is expected, got %q", repo.rows[0].Audience)
	}
	if len(push.calls) != 1 || len(push.calls[0]) != 2 || push.last.Kind != domain.InvoiceSent {
		t.Errorf("one push to both users expected: %+v", push)
	}
}

func TestSendExplicitAudienceWinsAndNilParamsBecomeEmpty(t *testing.T) {
	repo := &repoFake{}
	d := NewDeliveries(Deps{Notifications: repo}, Options{})
	if err := d.Send(ctx, Message{UserIDs: []uuid.UUID{uuid.New()}, Kind: "invoice_sent", Audience: "manager"}); err != nil {
		t.Fatal(err)
	}
	if repo.rows[0].Audience != domain.AudienceManager || repo.rows[0].Params == nil {
		t.Errorf("explicit audience and an empty params map expected: %+v", repo.rows[0])
	}
}

func TestSendRefusesUnknownKindAudienceAndLongURL(t *testing.T) {
	repo := &repoFake{}
	d := NewDeliveries(Deps{Notifications: repo}, Options{})
	user := []uuid.UUID{uuid.New()}
	if err := d.Send(ctx, Message{UserIDs: user, Kind: "nope"}); !errors.Is(err, domain.ErrUnknownKind) {
		t.Errorf("unknown kind: %v", err)
	}
	if err := d.Send(ctx, Message{UserIDs: user, Kind: "invoice_sent", Audience: "admin"}); !errors.Is(err, domain.ErrUnknownAudience) {
		t.Errorf("unknown audience: %v", err)
	}
	long := make([]byte, domain.MaxURLChars+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := d.Send(ctx, Message{UserIDs: user, Kind: "invoice_sent", URL: string(long)}); !errors.Is(err, ErrURLTooLong) {
		t.Errorf("long url: %v", err)
	}
	if len(repo.rows) != 0 {
		t.Error("nothing may be stored for a refused message")
	}
}

func TestSendNobodyDoesNothingAndAPushErrorDoesNotFail(t *testing.T) {
	repo, push := &repoFake{}, &pushFake{err: errBoom}
	d := NewDeliveries(Deps{Notifications: repo, Pusher: push}, Options{})
	if err := d.Send(ctx, Message{UserIDs: []uuid.UUID{uuid.Nil}, Kind: "invoice_sent"}); err != nil || len(push.calls) != 0 {
		t.Errorf("nobody to tell: %v %v", err, push.calls)
	}
	if err := d.Send(ctx, Message{UserIDs: []uuid.UUID{uuid.New()}, Kind: "invoice_sent"}); err != nil {
		t.Errorf("a push error must not fail the delivery: %v", err)
	}
	repo.insertErr = errBoom
	if err := d.Send(ctx, Message{UserIDs: []uuid.UUID{uuid.New()}, Kind: "invoice_sent"}); !errors.Is(err, errBoom) {
		t.Errorf("an insert error must be returned: %v", err)
	}
}

func TestSendToAdminsIgnoresGivenUsers(t *testing.T) {
	admin, other := uuid.New(), uuid.New()
	repo := &repoFake{admins: []uuid.UUID{admin}}
	d := NewDeliveries(Deps{Notifications: repo}, Options{})
	if err := d.SendToAdmins(ctx, Message{UserIDs: []uuid.UUID{other}, Kind: "listing_submitted"}); err != nil {
		t.Fatal(err)
	}
	if len(repo.rows) != 1 || repo.rows[0].UserID != admin {
		t.Errorf("only the admin must be told: %+v", repo.rows)
	}
	repo.admins = nil
	if err := d.SendToAdmins(ctx, Message{Kind: "listing_submitted"}); err != nil || len(repo.rows) != 1 {
		t.Errorf("no admins is not an error: %v", err)
	}
}

func inboxWith(t *testing.T) (*Inbox, *repoFake, uuid.UUID) {
	t.Helper()
	repo := &repoFake{}
	user := uuid.New()
	d := NewDeliveries(Deps{Notifications: repo}, Options{})
	for _, k := range []string{"invoice_sent", "payment_submitted", "contract_ended", "invoice_overdue"} {
		if err := d.Send(ctx, Message{UserIDs: []uuid.UUID{user}, Kind: k}); err != nil {
			t.Fatal(err)
		}
	}
	return NewInbox(Deps{Notifications: repo, Clock: fixedClock()}, Options{}), repo, user
}

func TestInboxSplitsByRole(t *testing.T) {
	inbox, _, user := inboxWith(t)
	tenant, _ := inbox.UnreadCount(ctx, Viewer{UserID: user, Role: authz.RoleTenant})
	landlord, _ := inbox.UnreadCount(ctx, Viewer{UserID: user, Role: authz.RoleLandlord})
	admin, _ := inbox.UnreadCount(ctx, Viewer{UserID: user, Role: authz.RoleAdmin})
	// tenant: invoice_sent, invoice_overdue, contract_ended (no audience); landlord: payment_submitted, contract_ended.
	if tenant != 3 || landlord != 2 || admin != 4 {
		t.Errorf("tenant %d landlord %d admin %d", tenant, landlord, admin)
	}
}

func TestInboxListIsAFeedWithHasMore(t *testing.T) {
	inbox, _, user := inboxWith(t)
	v := Viewer{UserID: user, Role: authz.RoleAdmin}
	page, err := inbox.List(ctx, v, false, nil, 3, 0)
	if err != nil || len(page.Items) != 3 || !page.HasMore || page.UnreadCount != 4 {
		t.Fatalf("%+v %v", page, err)
	}
	page, _ = inbox.List(ctx, v, false, nil, 3, 3)
	if len(page.Items) != 1 || page.HasMore {
		t.Errorf("last page: %+v", page)
	}
	kind := domain.ContractEnded
	page, _ = inbox.List(ctx, v, false, &kind, 10, 0)
	if len(page.Items) != 1 || page.Items[0].Kind != domain.ContractEnded {
		t.Errorf("kind filter: %+v", page)
	}
	if first, _ := inbox.List(ctx, v, false, nil, 10, 0); first.Items[0].Kind != domain.InvoiceOverdue {
		t.Errorf("newest first expected, got %s", first.Items[0].Kind)
	}
}

func TestLatestClampsTheLimit(t *testing.T) {
	inbox, repo, user := inboxWith(t)
	d := NewDeliveries(Deps{Notifications: repo}, Options{})
	for i := 0; i < 30; i++ {
		_ = d.Send(ctx, Message{UserIDs: []uuid.UUID{user}, Kind: "contract_ended"})
	}
	v := Viewer{UserID: user, Role: authz.RoleAdmin}
	if page, _ := inbox.Latest(ctx, v, 50); len(page.Items) != domain.MaxLatest {
		t.Errorf("max %d expected, got %d", domain.MaxLatest, len(page.Items))
	}
	if page, _ := inbox.Latest(ctx, v, 0); len(page.Items) != domain.DefaultLatest {
		t.Errorf("default %d expected, got %d", domain.DefaultLatest, len(page.Items))
	}
}

func TestMarkReadKeepsTheFirstReadTimeAndIsScoped(t *testing.T) {
	inbox, repo, user := inboxWith(t)
	id := repo.rows[0].ID
	v := Viewer{UserID: user, Role: authz.RoleTenant}
	first, err := inbox.MarkRead(ctx, v, id)
	if err != nil || first.ReadAt == nil {
		t.Fatal(first, err)
	}
	inbox.clock = ClockFunc(func() time.Time { return first.ReadAt.Add(time.Hour) })
	again, _ := inbox.MarkRead(ctx, v, id)
	if !again.ReadAt.Equal(*first.ReadAt) {
		t.Error("a second read must keep the first read time")
	}
	if _, err := inbox.MarkRead(ctx, Viewer{UserID: uuid.New(), Role: authz.RoleTenant}, id); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("somebody else's message: %v", err)
	}
}

func TestMarkAllReadOnlyTouchesTheRolesInbox(t *testing.T) {
	inbox, _, user := inboxWith(t)
	n, err := inbox.MarkAllRead(ctx, Viewer{UserID: user, Role: authz.RoleLandlord})
	if err != nil || n != 2 {
		t.Fatalf("landlord side has 2 unread: %d %v", n, err)
	}
	if left, _ := inbox.UnreadCount(ctx, Viewer{UserID: user, Role: authz.RoleTenant}); left != 2 {
		t.Errorf("the tenant side keeps invoice_sent and invoice_overdue unread, got %d", left)
	}
}

func TestBulkLimitsIgnoresOthersAndDeletes(t *testing.T) {
	inbox, repo, user := inboxWith(t)
	v := Viewer{UserID: user, Role: authz.RoleAdmin}
	stranger := NewDeliveries(Deps{Notifications: repo}, Options{})
	other := uuid.New()
	_ = stranger.Send(ctx, Message{UserIDs: []uuid.UUID{other}, Kind: "contract_ended"})
	foreign := repo.rows[len(repo.rows)-1].ID

	ids := []uuid.UUID{repo.rows[0].ID, repo.rows[1].ID, foreign}
	if n, err := inbox.Bulk(ctx, v, domain.BulkRead, ids); err != nil || n != 2 {
		t.Errorf("only the viewer's own are changed: %d %v", n, err)
	}
	if n, err := inbox.Bulk(ctx, v, domain.BulkDelete, ids); err != nil || n != 2 {
		t.Errorf("delete: %d %v", n, err)
	}
	if left, _ := inbox.UnreadCount(ctx, Viewer{UserID: other, Role: authz.RoleAdmin}); left != 1 {
		t.Error("the other person's message must survive")
	}
	tooMany := make([]uuid.UUID, domain.MaxBulkIDs+1)
	for i := range tooMany {
		tooMany[i] = uuid.New()
	}
	if _, err := inbox.Bulk(ctx, v, domain.BulkRead, tooMany); !errors.Is(err, domain.ErrTooManyIDs) {
		t.Errorf("more than %d ids: %v", domain.MaxBulkIDs, err)
	}
	if n, err := inbox.Bulk(ctx, v, domain.BulkRead, nil); err != nil || n != 0 {
		t.Errorf("no ids: %d %v", n, err)
	}
}

func TestDeleteAndClear(t *testing.T) {
	inbox, repo, user := inboxWith(t)
	v := Viewer{UserID: user, Role: authz.RoleTenant}
	id := repo.rows[0].ID
	if err := inbox.Delete(ctx, v, id); err != nil {
		t.Fatal(err)
	}
	if err := inbox.Delete(ctx, v, id); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("a deleted message is gone: %v", err)
	}
	if err := inbox.Delete(ctx, v, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	_, _ = inbox.MarkAllRead(ctx, Viewer{UserID: user, Role: authz.RoleAdmin})
	if n, _ := inbox.DeleteMany(ctx, v, domain.DeleteRead); n != 3 {
		t.Errorf("the three remaining (read) messages go, got %d", n)
	}
	if n, _ := inbox.DeleteMany(ctx, v, domain.DeleteAll); n != 0 {
		t.Errorf("nothing left, got %d", n)
	}
}

func TestPruneLoopsUntilABatchIsShort(t *testing.T) {
	repo := &repoFake{pruned: []int{pruneBatch, pruneBatch, 7}}
	n, err := NewPruner(Deps{Notifications: repo, Clock: fixedClock()}, Options{RetentionDays: 30}).Prune(ctx)
	if err != nil || n != 2*pruneBatch+7 || repo.pruneCall != 3 {
		t.Errorf("%d %v calls %d", n, err, repo.pruneCall)
	}
}

func TestSeedDemoIsIdempotentAndMarksOneRead(t *testing.T) {
	repo := &repoFake{}
	ids := map[string]uuid.UUID{
		"tenant@rental.test": uuid.New(), "landlord@rental.test": uuid.New(), "admin@rental.test": uuid.New(),
	}
	s := NewSeeder(Deps{Notifications: repo, Clock: fixedClock()}, Options{})
	first, err := s.SeedDemo(ctx, ids)
	if err != nil || first != 5 {
		t.Fatalf("%d %v", first, err)
	}
	read := 0
	for _, n := range repo.rows {
		if n.ReadAt != nil {
			read++
		}
	}
	if read != 1 {
		t.Errorf("one read message expected, got %d", read)
	}
	if again, _ := s.SeedDemo(ctx, ids); again != 0 {
		t.Errorf("second run must write nothing, wrote %d", again)
	}
	if missing, _ := s.SeedDemo(ctx, map[string]uuid.UUID{}); missing != 0 {
		t.Errorf("an unknown account is skipped, wrote %d", missing)
	}
}
