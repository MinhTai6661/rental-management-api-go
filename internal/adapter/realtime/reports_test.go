package realtime

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestReportHubDeliversToTheSocketsOfTheReportOnly(t *testing.T) {
	hub := NewReportHub(2, 3, 4)
	report, other := uuid.New(), uuid.New()
	alice, bob := uuid.New(), uuid.New()
	a, leaveA, _ := hub.Join(report, alice)
	b, leaveB, _ := hub.Join(report, bob)
	o, leaveO, _ := hub.Join(other, alice)
	defer leaveA()
	defer leaveB()
	defer leaveO()

	hub.Deliver(ReportFrame{ReportID: report, Data: []byte(`1`)})
	if string(<-a) != "1" || string(<-b) != "1" || len(o) != 0 {
		t.Fatal("every socket of the report and none of another")
	}
	// The one who is typing does not hear of it.
	hub.Deliver(ReportFrame{ReportID: report, ExceptUser: &alice, Data: []byte(`2`)})
	if len(a) != 0 || string(<-b) != "2" {
		t.Fatal("typing reaches only the other party")
	}
}

func TestReportHubLimitsAndCleansUp(t *testing.T) {
	hub := NewReportHub(2, 3, 1)
	report, alice, bob := uuid.New(), uuid.New(), uuid.New()
	_, leave1, _ := hub.Join(report, alice)
	_, leave2, _ := hub.Join(report, alice)
	if _, _, err := hub.Join(report, alice); !errors.Is(err, ErrTooManyStreams) {
		t.Fatalf("per user: %v", err)
	}
	_, leave3, _ := hub.Join(report, bob)
	if _, _, err := hub.Join(report, uuid.New()); !errors.Is(err, ErrReportFull) {
		t.Fatalf("per report: %v", err)
	}
	if hub.Open() != 3 {
		t.Fatalf("open = %d", hub.Open())
	}
	leave1()
	leave1() // idempotent
	leave2()
	leave3()
	if hub.Open() != 0 || len(hub.perUser) != 0 || len(hub.reports) != 0 {
		t.Fatalf("not empty: %d %v %v", hub.Open(), hub.perUser, hub.reports)
	}
	if _, _, err := hub.Join(report, alice); err != nil {
		t.Fatalf("a freed slot can be used again: %v", err)
	}
}

func TestReportHubDropsASlowSocketAndClosesEverythingOnClose(t *testing.T) {
	hub := NewReportHub(5, 5, 1)
	report := uuid.New()
	slow, _, _ := hub.Join(report, uuid.New())
	hub.Deliver(ReportFrame{ReportID: report, Data: []byte(`1`)})
	hub.Deliver(ReportFrame{ReportID: report, Data: []byte(`2`)}) // the buffer is full: dropped
	<-slow
	if _, open := <-slow; open {
		t.Fatal("a slow socket has its channel closed")
	}
	if hub.Open() != 0 {
		t.Fatal("and is gone from the hub")
	}
	live, _, _ := hub.Join(report, uuid.New())
	hub.Close()
	hub.Close()
	if _, open := <-live; open {
		t.Fatal("close ends every socket")
	}
	if _, _, err := hub.Join(report, uuid.New()); !errors.Is(err, ErrClosed) {
		t.Fatalf("join after close: %v", err)
	}
}
