package notification

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

type popupWorld struct {
	*annWorld
	popups      *Popups
	impressions *impressionFake
	viewer      PopupViewer
}

func newPopupWorld() *popupWorld {
	w := &popupWorld{annWorld: newAnnWorld(), impressions: newImpressionFake()}
	session := uuid.New()
	w.viewer = PopupViewer{UserID: uuid.New(), SessionID: &session}
	w.repo.people = []uuid.UUID{w.viewer.UserID}
	w.popups = NewPopups(Deps{
		Tx: txFake{}, Announcements: w.repo, Impressions: w.impressions, Audit: w.audit, Clock: ClockFunc(func() time.Time { return w.now }),
	}, Options{})
	return w
}

func popupContent(priority int) domain.Content {
	c := good(domain.ScopeTenants)
	c.SendNotification, c.ShowPopup, c.Priority = false, true, priority
	return c
}

func (w *popupWorld) publish(t *testing.T, c domain.Content) uuid.UUID {
	t.Helper()
	d, err := w.uc.Create(ctx, adminS, c, true)
	if err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func TestActiveReturnsAtMostThreeByPriority(t *testing.T) {
	w := newPopupWorld()
	var top uuid.UUID
	for i := range 5 {
		id := w.publish(t, popupContent(i))
		if i == 4 {
			top = id
		}
	}
	got, err := w.popups.Active(ctx, w.viewer)
	if err != nil || len(got) != domain.PopupsAtOnce || got[0].ID != top || got[0].Priority < got[1].Priority {
		t.Fatalf("%d popups, first %v: %v", len(got), got[0].ID, err)
	}
}

func TestActiveSkipsWhatThePersonIsNotAudienceOf(t *testing.T) {
	w := newPopupWorld()
	w.publish(t, popupContent(0))
	stranger := PopupViewer{UserID: uuid.New()}
	if got, err := w.popups.Active(ctx, stranger); err != nil || len(got) != 0 {
		t.Errorf("outsider sees %d: %v", len(got), err)
	}
	if got, _ := w.popups.Active(ctx, w.viewer); len(got) != 1 {
		t.Errorf("member sees %d", len(got))
	}
}

func TestSeenDismissClickAreRecordedAndStopThePopup(t *testing.T) {
	w := newPopupWorld()
	id := w.publish(t, popupContent(0))
	if err := w.popups.Record(ctx, w.viewer, id, domain.ActionSeen); err != nil {
		t.Fatal(err)
	}
	s := w.impressions.rows[[2]uuid.UUID{id, w.viewer.UserID}]
	if s.ViewCount != 1 || s.DismissedAt != nil {
		t.Fatalf("seen: %+v", s)
	}
	if got, _ := w.popups.Active(ctx, w.viewer); len(got) != 1 {
		t.Error("seen but not closed: still active")
	}
	if err := w.popups.Record(ctx, w.viewer, id, domain.ActionClick); err != nil {
		t.Fatal(err)
	}
	s = w.impressions.rows[[2]uuid.UUID{id, w.viewer.UserID}]
	if s.ClickedAt == nil || s.DismissedAt == nil {
		t.Errorf("a click closes: %+v", s)
	}
	if got, _ := w.popups.Active(ctx, w.viewer); len(got) != 0 {
		t.Error("closed: no longer active")
	}
}

func TestReshowBringsAClosedPopupBack(t *testing.T) {
	w := newPopupWorld()
	id := w.publish(t, popupContent(0))
	_ = w.popups.Record(ctx, w.viewer, id, domain.ActionDismiss)
	if _, err := w.uc.Update(ctx, adminS, id, popupContent(0), false, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.popups.Active(ctx, w.viewer); len(got) != 1 {
		t.Errorf("reshow: %d", len(got))
	}
}

func TestMaxTotalLimitsViews(t *testing.T) {
	w := newPopupWorld()
	c := popupContent(0)
	c.Frequency, c.MaxTotal = domain.FrequencyEverySession, intp(2)
	id := w.publish(t, c)
	for range 2 {
		if err := w.popups.Record(ctx, w.viewer, id, domain.ActionSeen); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := w.popups.Active(ctx, w.viewer); len(got) != 0 {
		t.Error("two views of two: gone")
	}
}

func TestRecordRefusesWhatIsNotOutOrNotForThePerson(t *testing.T) {
	w := newPopupWorld()
	id := w.publish(t, popupContent(0))
	if err := w.popups.Record(ctx, PopupViewer{UserID: uuid.New()}, id, domain.ActionSeen); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("outsider: %v", err)
	}
	draft, _ := w.uc.Create(ctx, adminS, popupContent(0), false)
	if err := w.popups.Record(ctx, w.viewer, draft.ID, domain.ActionSeen); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("draft: %v", err)
	}
	if err := w.popups.Record(ctx, w.viewer, uuid.New(), domain.ActionSeen); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if err := w.uc.Recall(ctx, adminS, id); err != nil {
		t.Fatal(err)
	}
	if err := w.popups.Record(ctx, w.viewer, id, domain.ActionSeen); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("recalled: %v", err)
	}
}

func intp(n int) *int { return &n }
