package listing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/listing"
	"rental-management/internal/domain/shared"
)

type world struct {
	store    *store
	notices  *notices
	audits   *audits
	landlord uuid.UUID
	other    uuid.UUID
	admin    uuid.UUID
	room     *domain.RoomFacts
	deps     Deps
	mine     *Listings
	review   *Reviews
	links    *RoomLinks
}

func newWorld(limit *int) *world {
	st := newStore(time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC))
	w := &world{store: st, notices: &notices{}, audits: &audits{}, admin: uuid.New()}
	w.landlord, w.other = st.landlord("Chu Hoa"), st.landlord("Chu Lan")
	w.room = st.room(w.landlord, "P101")
	w.deps = Deps{
		Tx: fakeTx{}, Listings: st, Rooms: st, Offers: offerRepo{st}, Seed: st, Plans: fixedLimit{limit},
		Notifier: w.notices, Audit: w.audits, Clock: ClockFunc(func() time.Time { return st.now }),
	}
	w.mine, w.review, w.links = NewListings(w.deps), NewReviews(w.deps), NewRoomLinks(w.deps)
	return w
}

func (w *world) status(id uuid.UUID) domain.Status { return w.store.listings[id].Status }

func kinds(n *notices) []string {
	out := make([]string, len(n.sent))
	for i, m := range n.sent {
		out[i] = m.Kind
	}
	return out
}

func TestCreateWritesADraftOfOwnLiveRoom(t *testing.T) {
	ctx := context.Background()
	w := newWorld(nil)
	d, err := w.mine.Create(ctx, w.landlord, CreateInput{RoomID: w.room.ID, Title: "Phòng gần chợ"})
	if err != nil || d.Status != domain.StatusDraft || d.RoomID != w.room.ID || d.LandlordID != w.landlord {
		t.Fatalf("create: %+v %v", d, err)
	}
	if got := w.audits.actions(); len(got) != 1 || got[0] != AuditCreated {
		t.Errorf("audit: %v", got)
	}
	if len(w.notices.sent) != 0 {
		t.Error("nobody is told about a draft")
	}

	t.Run("another landlord's room, a deleted room and an unknown room are not found", func(t *testing.T) {
		gone := w.store.room(w.landlord, "P102")
		gone.Live = false
		for name, in := range map[string]struct {
			by   uuid.UUID
			room uuid.UUID
		}{"not mine": {w.other, w.room.ID}, "deleted": {w.landlord, gone.ID}, "unknown": {w.landlord, uuid.New()}} {
			if _, err := w.mine.Create(ctx, in.by, CreateInput{RoomID: in.room, Title: "x"}); !errors.Is(err, domain.ErrRoomNotFound) {
				t.Errorf("%s: %v", name, err)
			}
		}
	})
}

func TestOneOpenListingPerRoom(t *testing.T) {
	ctx := context.Background()
	for _, open := range []domain.Status{domain.StatusPending, domain.StatusApproved} {
		w := newWorld(nil)
		w.store.add(w.room, open)
		_, err := w.mine.Create(ctx, w.landlord, CreateInput{RoomID: w.room.ID, Title: "Second"})
		var taken *domain.RoomAlreadyListedError
		if !errors.As(err, &taken) || taken.Room != "P101" {
			t.Errorf("%s: %v", open, err)
		}
	}
	t.Run("a draft, a refused and a taken-down listing do not hold the room", func(t *testing.T) {
		w := newWorld(nil)
		w.store.add(w.room, domain.StatusRejected)
		if _, err := w.mine.Create(ctx, w.landlord, CreateInput{RoomID: w.room.ID, Title: "Again"}); err != nil {
			t.Errorf("create: %v", err)
		}
	})
	t.Run("sending a second listing for a room that has one waiting is refused", func(t *testing.T) {
		w := newWorld(nil)
		w.store.add(w.room, domain.StatusPending)
		draft := w.store.add(w.room, domain.StatusDraft)
		_, err := w.mine.Submit(ctx, w.landlord, draft.ID)
		var taken *domain.RoomAlreadyListedError
		if !errors.As(err, &taken) {
			t.Fatalf("submit: %v", err)
		}
		if w.status(draft.ID) != domain.StatusDraft {
			t.Error("a refused submit leaves the draft as it was")
		}
	})
}

func TestSubmitTellsTheAdmins(t *testing.T) {
	ctx := context.Background()
	w := newWorld(nil)
	l := w.store.add(w.room, domain.StatusDraft)
	d, err := w.mine.Submit(ctx, w.landlord, l.ID)
	if err != nil || d.Status != domain.StatusPending || d.SubmittedAt == nil {
		t.Fatalf("submit: %+v %v", d, err)
	}
	if len(w.notices.sent) != 1 {
		t.Fatalf("notices: %v", kinds(w.notices))
	}
	n := w.notices.sent[0]
	if n.Kind != NotifySubmitted || !n.ToAdmins || n.Params["landlord"] != "Chu Hoa" || n.Params["room"] != "P101" ||
		n.URL != "/admin/listings/"+l.ID.String() {
		t.Errorf("notification: %+v", n)
	}
	if _, err := w.mine.Submit(ctx, w.landlord, l.ID); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Errorf("sending a waiting listing again: %v", err)
	}
	if _, err := w.mine.Submit(ctx, w.other, l.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord's listing: %v", err)
	}
}

func TestSubmitHonoursThePlanLimit(t *testing.T) {
	ctx := context.Background()
	one := 1
	w := newWorld(&one)
	w.store.add(w.store.room(w.landlord, "P102"), domain.StatusApproved)
	l := w.store.add(w.room, domain.StatusDraft)
	_, err := w.mine.Submit(ctx, w.landlord, l.ID)
	var limit *domain.LimitError
	if !errors.As(err, &limit) || limit.Limit != 1 {
		t.Fatalf("submit past the limit: %v", err)
	}
	two := 2
	w2 := newWorld(&two)
	w2.store.add(w2.store.room(w2.landlord, "P102"), domain.StatusApproved)
	if _, err := w2.mine.Submit(ctx, w2.landlord, w2.store.add(w2.room, domain.StatusDraft).ID); err != nil {
		t.Errorf("under the limit: %v", err)
	}
}

func TestEditingReturnsAnOpenListingToDraft(t *testing.T) {
	ctx := context.Background()
	for _, open := range []domain.Status{domain.StatusPending, domain.StatusApproved} {
		w := newWorld(nil)
		l := w.store.add(w.room, open)
		title := "Tiêu đề mới"
		d, err := w.mine.Update(ctx, w.landlord, l.ID, UpdateInput{Title: &title})
		if err != nil || d.Status != domain.StatusDraft || d.Title != title {
			t.Fatalf("%s: %+v %v", open, d, err)
		}
		if len(w.notices.sent) != 0 {
			t.Errorf("%s: the admins are not told when a listing goes back to a draft: %v", open, kinds(w.notices))
		}
	}
	t.Run("saving without a change leaves it alone", func(t *testing.T) {
		w := newWorld(nil)
		l := w.store.add(w.room, domain.StatusApproved)
		same := l.Title
		d, err := w.mine.Update(ctx, w.landlord, l.ID, UpdateInput{Title: &same})
		if err != nil || d.Status != domain.StatusApproved || len(w.audits.events) != 0 {
			t.Errorf("%+v %v %v", d, err, w.audits.actions())
		}
	})
	t.Run("a refused listing keeps its status when edited", func(t *testing.T) {
		w := newWorld(nil)
		l := w.store.add(w.room, domain.StatusRejected)
		title := "Có ảnh"
		if d, err := w.mine.Update(ctx, w.landlord, l.ID, UpdateInput{Title: &title}); err != nil || d.Status != domain.StatusRejected {
			t.Errorf("%+v %v", d, err)
		}
	})
	t.Run("somebody else's listing is not found", func(t *testing.T) {
		w := newWorld(nil)
		l := w.store.add(w.room, domain.StatusDraft)
		title := "x"
		if _, err := w.mine.Update(ctx, w.other, l.ID, UpdateInput{Title: &title}); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%v", err)
		}
	})
}

func TestDeleteAndUnpublish(t *testing.T) {
	ctx := context.Background()
	w := newWorld(nil)
	live := w.store.add(w.room, domain.StatusApproved)
	if err := w.mine.Delete(ctx, w.landlord, live.ID); !errors.Is(err, domain.ErrUnpublishFirst) {
		t.Fatalf("deleting a live listing: %v", err)
	}
	if d, err := w.mine.Unpublish(ctx, w.landlord, live.ID); err != nil || d.Status != domain.StatusUnpublished || d.UnpublishedAt == nil {
		t.Fatalf("unpublish: %+v %v", d, err)
	}
	if err := w.mine.Delete(ctx, w.landlord, live.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := w.store.listings[live.ID]; ok {
		t.Error("the listing must be gone")
	}
	if err := w.mine.Delete(ctx, w.landlord, live.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deleting it twice: %v", err)
	}
	pending := w.store.add(w.room, domain.StatusPending)
	if _, err := w.mine.Unpublish(ctx, w.landlord, pending.ID); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Errorf("unpublishing a waiting listing: %v", err)
	}
	if err := w.mine.Delete(ctx, w.other, pending.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord deleting it: %v", err)
	}
}

func TestListIsScopedAndCounted(t *testing.T) {
	ctx := context.Background()
	w := newWorld(nil)
	w.store.add(w.room, domain.StatusApproved)
	w.store.add(w.store.room(w.landlord, "P102"), domain.StatusDraft)
	w.store.add(w.store.room(w.other, "X1"), domain.StatusPending)

	page, err := w.mine.List(ctx, w.landlord, ListFilter{}, 20, 0)
	if err != nil || page.Total != 2 || len(page.Items) != 2 || page.Counts[domain.StatusDraft] != 1 || page.Counts[domain.StatusPending] != 0 {
		t.Fatalf("own list: %+v %v", page, err)
	}
	if _, err := w.mine.Get(ctx, w.landlord, page.Items[0].ID); err != nil {
		t.Errorf("get: %v", err)
	}
	if _, err := w.mine.Get(ctx, w.other, page.Items[0].ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another landlord's get: %v", err)
	}
	if w.store.extras != 1 {
		t.Errorf("the detail reads the room's costs and pictures once, read %d", w.store.extras)
	}
}

func TestAdminDecisions(t *testing.T) {
	ctx := context.Background()
	t.Run("approve tells the landlord", func(t *testing.T) {
		w := newWorld(nil)
		l := w.store.add(w.room, domain.StatusPending)
		note := "Đẹp"
		d, err := w.review.Approve(ctx, w.admin, l.ID, &note)
		if err != nil || d.Status != domain.StatusApproved || d.PublishedAt == nil || d.ReviewedByID == nil || *d.ReviewedByID != w.admin {
			t.Fatalf("approve: %+v %v", d, err)
		}
		if got := kinds(w.notices); len(got) != 1 || got[0] != NotifyApproved || w.notices.sent[0].UserID != w.landlord {
			t.Errorf("notices: %v", got)
		}
	})
	t.Run("a refusal needs a reason and the landlord can send the listing again", func(t *testing.T) {
		w := newWorld(nil)
		l := w.store.add(w.room, domain.StatusPending)
		if _, err := w.review.Reject(ctx, w.admin, l.ID, " "); !errors.Is(err, domain.ErrNoteRequired) {
			t.Fatalf("blank reason: %v", err)
		}
		if w.status(l.ID) != domain.StatusPending || len(w.notices.sent) != 0 {
			t.Error("a refused decision changes and tells nothing")
		}
		d, err := w.review.Reject(ctx, w.admin, l.ID, "Thiếu ảnh")
		if err != nil || d.Status != domain.StatusRejected || w.notices.sent[0].Kind != NotifyRejected || w.notices.sent[0].Params["reason"] != "Thiếu ảnh" {
			t.Fatalf("reject: %+v %v %+v", d, err, w.notices.sent)
		}
		if d, err := w.mine.Submit(ctx, w.landlord, l.ID); err != nil || d.Status != domain.StatusPending || d.ReviewNote != nil {
			t.Errorf("send again: %+v %v", d, err)
		}
	})
	t.Run("take down needs a reason and a live listing, and tells the landlord", func(t *testing.T) {
		w := newWorld(nil)
		live := w.store.add(w.room, domain.StatusApproved)
		if _, err := w.review.TakeDown(ctx, w.admin, live.ID, ""); !errors.Is(err, domain.ErrNoteRequired) {
			t.Fatalf("no reason: %v", err)
		}
		d, err := w.review.TakeDown(ctx, w.admin, live.ID, "Ảnh không đúng thực tế")
		if err != nil || d.Status != domain.StatusUnpublished || *d.ReviewNote != "Ảnh không đúng thực tế" {
			t.Fatalf("take down: %+v %v", d, err)
		}
		if got := kinds(w.notices); len(got) != 1 || got[0] != NotifyTakenDown {
			t.Errorf("notices: %v", got)
		}
		pending := w.store.add(w.store.room(w.landlord, "P2"), domain.StatusPending)
		if _, err := w.review.TakeDown(ctx, w.admin, pending.ID, "x"); !errors.Is(err, domain.ErrInvalidTransition) {
			t.Errorf("taking down a waiting listing: %v", err)
		}
	})
	t.Run("an unknown listing is not found", func(t *testing.T) {
		w := newWorld(nil)
		if _, err := w.review.Approve(ctx, w.admin, uuid.New(), nil); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%v", err)
		}
	})
}

func TestAdminNeverSeesADraft(t *testing.T) {
	ctx := context.Background()
	w := newWorld(nil)
	draft := w.store.add(w.room, domain.StatusDraft)
	w.store.add(w.store.room(w.other, "X"), domain.StatusPending)

	if _, err := w.review.Get(ctx, draft.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get a draft: %v", err)
	}
	asked := domain.StatusDraft
	page, err := w.review.List(ctx, ListFilter{Status: &asked}, 20, 0)
	if err != nil || page.Total != 1 || page.Items[0].Status != domain.StatusPending {
		t.Errorf("asking for drafts lists the waiting listings: %+v %v", page, err)
	}
	if _, has := page.Counts[domain.StatusDraft]; has {
		t.Error("the counts leave drafts out")
	}
	page, err = w.review.List(ctx, ListFilter{}, 20, 0)
	if err != nil || page.Total != 1 {
		t.Errorf("default list: %+v %v", page, err)
	}
}

func TestBulkReportsWhatItCouldNotDo(t *testing.T) {
	ctx := context.Background()
	w := newWorld(nil)
	a := w.store.add(w.room, domain.StatusPending)
	b := w.store.add(w.store.room(w.landlord, "P2"), domain.StatusPending)
	draft := w.store.add(w.store.room(w.landlord, "P3"), domain.StatusDraft)

	res, err := w.review.Bulk(ctx, w.admin, ActionApprove, []uuid.UUID{a.ID, b.ID, draft.ID, a.ID}, "")
	if err != nil || len(res.Done) != 2 || len(res.Failed) != 1 || res.Failed[0].ID != draft.ID ||
		!errors.Is(res.Failed[0].Err, domain.ErrInvalidTransition) || res.Failed[0].Name != draft.Title {
		t.Fatalf("bulk approve: %+v %v", res, err)
	}
	if w.status(a.ID) != domain.StatusApproved || w.status(b.ID) != domain.StatusApproved || w.status(draft.ID) != domain.StatusDraft {
		t.Error("the listings that could be approved were")
	}
	if n := len(kinds(w.notices)); n != 2 {
		t.Errorf("each approved landlord is told once, got %d", n)
	}

	c := w.store.add(w.store.room(w.landlord, "P4"), domain.StatusPending)
	res, err = w.review.Bulk(ctx, w.admin, ActionReject, []uuid.UUID{c.ID}, "Không hợp lệ")
	if err != nil || len(res.Done) != 1 || *w.store.listings[c.ID].ReviewNote != "Không hợp lệ" {
		t.Errorf("bulk reject: %+v %v", res, err)
	}
	if _, err := w.review.Bulk(ctx, w.admin, ActionApprove, []uuid.UUID{a.ID, uuid.New()}, ""); !errors.Is(err, domain.ErrItemsNotFound) {
		t.Errorf("an unknown id is refused up front: %v", err)
	}
}

func TestRoomLinks(t *testing.T) {
	ctx := context.Background()
	w := newWorld(nil)
	open := w.store.add(w.room, domain.StatusApproved)
	free := w.store.room(w.landlord, "P2")
	w.store.add(free, domain.StatusRejected)

	got, err := w.links.Open(ctx, []uuid.UUID{w.room.ID, free.ID})
	if err != nil || !got[w.room.ID] || got[free.ID] {
		t.Errorf("open rooms: %v %v", got, err)
	}
	if err := w.links.ReturnToDraft(ctx, []uuid.UUID{w.room.ID, free.ID}); err != nil {
		t.Fatalf("return to draft: %v", err)
	}
	if w.status(open.ID) != domain.StatusDraft || w.status(open.ID) == domain.StatusApproved {
		t.Error("the live listing goes back to a draft")
	}
	if len(w.notices.sent) != 0 {
		t.Error("a room edit tells nobody")
	}
	last := w.audits.events[len(w.audits.events)-1]
	if last.Action != AuditUpdated || last.Meta["because"] != "room_changed" || last.ActorID != uuid.Nil {
		t.Errorf("audit: %+v", last)
	}
	// Nothing open any more: a second call changes nothing.
	if err := w.links.ReturnToDraft(ctx, []uuid.UUID{w.room.ID}); err != nil {
		t.Errorf("second call: %v", err)
	}
	if err := w.links.ReturnToDraft(ctx, nil); err != nil {
		t.Errorf("no rooms: %v", err)
	}
	// A pending one goes back to a draft too.
	pend := w.store.add(w.room, domain.StatusPending)
	if err := w.links.ReturnToDraft(ctx, []uuid.UUID{w.room.ID}); err != nil || w.status(pend.ID) != domain.StatusDraft {
		t.Errorf("a waiting listing goes back to a draft: %v", err)
	}
}

func TestSeedDemoWritesOneListingPerRoomAndRepeats(t *testing.T) {
	ctx := context.Background()
	w := newWorld(nil)
	for _, n := range []string{"P2", "P3", "P4", "P5", "P6"} {
		w.store.room(w.landlord, n)
	}
	seeder := NewSeeder(w.deps)
	n, err := seeder.SeedDemo(ctx, w.landlord, &w.admin)
	if err != nil || n != 6 {
		t.Fatalf("seed: %d %v", n, err)
	}
	counts, _ := w.store.Counts(ctx, &w.landlord, false)
	if counts[domain.StatusApproved] != 3 || counts[domain.StatusPending] != 1 || counts[domain.StatusRejected] != 1 || counts[domain.StatusDraft] != 1 {
		t.Errorf("every status is there: %v", counts)
	}
	if n, err := seeder.SeedDemo(ctx, w.landlord, &w.admin); err != nil || n != 0 {
		t.Errorf("a second run writes nothing: %d %v", n, err)
	}
	for _, l := range w.store.listings {
		if l.Status == domain.StatusApproved && (l.PublishedAt == nil || l.ReviewedByID == nil) {
			t.Errorf("an approved demo listing is published and reviewed: %+v", l)
		}
		if l.Status == domain.StatusDraft && l.SubmittedAt != nil {
			t.Errorf("a draft was never sent: %+v", l)
		}
	}
}

func TestStoreMissingRoomIsNotFound(t *testing.T) {
	w := newWorld(nil)
	if _, err := w.store.Lock(context.Background(), uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Error("the fake must behave like the repository")
	}
}
