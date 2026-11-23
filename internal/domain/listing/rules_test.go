package listing

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCanMoveFollowsTheReviewFlow(t *testing.T) {
	cases := []struct {
		name     string
		from, to Status
		want     bool
	}{
		{"a live listing goes back to a draft", StatusApproved, StatusDraft, true},
		{"a waiting listing goes back to a draft", StatusPending, StatusDraft, true},
		{"a refused listing is not a draft again", StatusRejected, StatusDraft, false},
		{"send a draft", StatusDraft, StatusPending, true},
		{"a draft cannot skip review", StatusDraft, StatusApproved, false},
		{"approve", StatusPending, StatusApproved, true},
		{"reject", StatusPending, StatusRejected, true},
		{"take down", StatusApproved, StatusUnpublished, true},
		{"edit an approved one", StatusApproved, StatusPending, true},
		{"send again after a refusal", StatusRejected, StatusPending, true},
		{"a refusal cannot be approved without a new review", StatusRejected, StatusApproved, false},
		{"put a taken-down one back", StatusUnpublished, StatusPending, true},
		{"a taken-down one cannot go live by itself", StatusUnpublished, StatusApproved, false},
		{"a draft cannot be taken down", StatusDraft, StatusUnpublished, false},
		{"no status moves to itself", StatusPending, StatusPending, false},
		{"an unknown status goes nowhere", Status("bogus"), StatusPending, false},
	}
	for _, c := range cases {
		if got := CanMove(c.from, c.to); got != c.want {
			t.Errorf("%s: CanMove(%s, %s) = %v, want %v", c.name, c.from, c.to, got, c.want)
		}
	}
}

func TestEditingReturnsToDraftOnlyWhenOpen(t *testing.T) {
	want := map[Status]bool{
		StatusApproved: true, StatusPending: true, StatusDraft: false, StatusRejected: false, StatusUnpublished: false,
	}
	for s, w := range want {
		if got := EditingReturnsToDraft(s); got != w {
			t.Errorf("EditingReturnsToDraft(%s) = %v, want %v", s, got, w)
		}
		if s.IsOpen() != w {
			t.Errorf("%s.IsOpen() = %v, want %v", s, s.IsOpen(), w)
		}
	}
}

func TestParseStatus(t *testing.T) {
	for _, s := range Statuses() {
		if got, ok := ParseStatus(string(s)); !ok || got != s {
			t.Errorf("ParseStatus(%q) = %q, %v", s, got, ok)
		}
	}
	if _, ok := ParseStatus("nope"); ok {
		t.Error("an unknown status must not parse")
	}
}

var now = time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)

func str(s string) *string { return &s }

func TestEdit(t *testing.T) {
	t.Run("a waiting or live listing becomes a draft when its text changes", func(t *testing.T) {
		for _, st := range []Status{StatusPending, StatusApproved} {
			l := Listing{Title: "Old", Status: st, SubmittedAt: &now, ReviewNote: str("x")}
			changed, back, err := l.Edit(str("New"), nil, false)
			if err != nil || !changed || !back || l.Status != StatusDraft || l.SubmittedAt != nil || l.ReviewNote != nil || l.Title != "New" {
				t.Errorf("%s: changed=%v back=%v err=%v listing=%+v", st, changed, back, err, l)
			}
		}
	})
	t.Run("a draft, a refused and a taken-down listing keep their status", func(t *testing.T) {
		for _, st := range []Status{StatusDraft, StatusRejected, StatusUnpublished} {
			l := Listing{Title: "Old", Status: st}
			changed, back, err := l.Edit(str("New"), nil, false)
			if err != nil || !changed || back || l.Status != st {
				t.Errorf("%s: changed=%v back=%v err=%v status=%s", st, changed, back, err, l.Status)
			}
		}
	})
	t.Run("saving without a change leaves an approved listing alone", func(t *testing.T) {
		l := Listing{Title: "Same", Description: str("Text"), Status: StatusApproved}
		changed, back, err := l.Edit(str("Same"), str("Text"), true)
		if err != nil || changed || back || l.Status != StatusApproved {
			t.Errorf("changed=%v back=%v err=%v status=%s", changed, back, err, l.Status)
		}
	})
	t.Run("the description can be changed and cleared", func(t *testing.T) {
		l := Listing{Title: "T", Description: str("a"), Status: StatusDraft}
		if changed, _, _ := l.Edit(nil, nil, true); !changed || l.Description != nil {
			t.Errorf("clearing the description: changed=%v description=%v", changed, l.Description)
		}
		if changed, _, _ := l.Edit(nil, nil, true); changed {
			t.Error("clearing an empty description is no change")
		}
	})
}

func TestTransitionsSetTheirFields(t *testing.T) {
	admin := uuid.New()
	t.Run("submit", func(t *testing.T) {
		l := Listing{Status: StatusRejected, ReviewNote: str("old")}
		if err := l.Submit(now); err != nil || l.Status != StatusPending || l.SubmittedAt == nil || l.ReviewNote != nil {
			t.Errorf("%v %+v", err, l)
		}
		if err := l.Submit(now); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("sending a waiting listing again: %v", err)
		}
	})
	t.Run("approve", func(t *testing.T) {
		l := Listing{Status: StatusPending, UnpublishedAt: &now}
		if err := l.Approve(admin, now, nil); err != nil || l.Status != StatusApproved || l.PublishedAt == nil ||
			l.UnpublishedAt != nil || l.ReviewedByID == nil || *l.ReviewedByID != admin {
			t.Errorf("%v %+v", err, l)
		}
	})
	t.Run("reject needs a reason", func(t *testing.T) {
		l := Listing{Status: StatusPending}
		if err := l.Reject(admin, now, "  "); !errors.Is(err, ErrNoteRequired) || l.Status != StatusPending {
			t.Errorf("blank reason: %v %s", err, l.Status)
		}
		if err := l.Reject(admin, now, "no photos"); err != nil || l.Status != StatusRejected || *l.ReviewNote != "no photos" {
			t.Errorf("%v %+v", err, l)
		}
	})
	t.Run("take down needs a reason and a live listing", func(t *testing.T) {
		l := Listing{Status: StatusApproved}
		if err := l.TakeDown(admin, now, ""); !errors.Is(err, ErrNoteRequired) {
			t.Errorf("no reason: %v", err)
		}
		if err := l.TakeDown(admin, now, "wrong pictures"); err != nil || l.Status != StatusUnpublished ||
			l.UnpublishedAt == nil || *l.ReviewNote != "wrong pictures" {
			t.Errorf("%v %+v", err, l)
		}
		pending := Listing{Status: StatusPending}
		if err := pending.TakeDown(admin, now, "x"); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("taking down a waiting listing: %v", err)
		}
	})
	t.Run("unpublish clears the note", func(t *testing.T) {
		l := Listing{Status: StatusApproved, ReviewNote: str("n")}
		if err := l.Unpublish(now); err != nil || l.Status != StatusUnpublished || l.ReviewNote != nil {
			t.Errorf("%v %+v", err, l)
		}
	})
}

func TestCheckDelete(t *testing.T) {
	for st, wantErr := range map[Status]bool{
		StatusApproved: true, StatusPending: false, StatusDraft: false, StatusRejected: false, StatusUnpublished: false,
	} {
		err := Listing{Status: st}.CheckDelete()
		if wantErr != errors.Is(err, ErrUnpublishFirst) {
			t.Errorf("CheckDelete(%s) = %v", st, err)
		}
	}
}

func TestReviewStatus(t *testing.T) {
	draft, approved := StatusDraft, StatusApproved
	if ReviewStatus(nil) != StatusPending || ReviewStatus(&draft) != StatusPending || ReviewStatus(&approved) != StatusApproved {
		t.Error("an admin sees the waiting listings by default and never the drafts")
	}
	if DefaultSort(StatusPending) != "submitted_at" || DefaultSort(StatusApproved) != "-created_at" {
		t.Error("the waiting queue goes oldest first, everything else newest first")
	}
}

func TestSearchPattern(t *testing.T) {
	if SearchPattern("   ") != nil || SearchPattern("") != nil {
		t.Error("a blank search is no search")
	}
	if got := SearchPattern(` Sao_Mai 50% \x `); got == nil || *got != `sao\_mai 50\% \\x` {
		t.Errorf("got %v", got)
	}
}

func TestOfferedNeedsEveryCondition(t *testing.T) {
	ok := RoomState{Status: "available", Live: true, LandlordHolds: true, ApprovedListing: true}
	if !ok.Offered() {
		t.Fatal("a live available room of a landlord with an approved listing is offered")
	}
	for name, mutate := range map[string]func(*RoomState){
		"rented":              func(s *RoomState) { s.Status = "rented" },
		"under repair":        func(s *RoomState) { s.Status = "maintenance" },
		"deleted":             func(s *RoomState) { s.Live = false },
		"a tenant holds it":   func(s *RoomState) { s.TenantContractRunning = true },
		"landlord role gone":  func(s *RoomState) { s.LandlordHolds = false },
		"no approved listing": func(s *RoomState) { s.ApprovedListing = false },
	} {
		s := ok
		mutate(&s)
		if s.Offered() {
			t.Errorf("%s: the room must not be offered", name)
		}
	}
}
