package contract

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCanMove(t *testing.T) {
	cases := []struct {
		name     string
		from, to Status
		want     bool
	}{
		{"pending to active", StatusPending, StatusActive, true},
		{"pending to rejected", StatusPending, StatusRejected, true},
		{"pending to ended", StatusPending, StatusEnded, false},
		{"active to ended", StatusActive, StatusEnded, true},
		{"active to pending", StatusActive, StatusPending, false},
		{"active to rejected", StatusActive, StatusRejected, false},
		{"ended to active (reopened)", StatusEnded, StatusActive, true},
		{"ended to rejected", StatusEnded, StatusRejected, false},
		{"rejected to pending", StatusRejected, StatusPending, true},
		{"rejected to active", StatusRejected, StatusActive, true},
		{"rejected to ended", StatusRejected, StatusEnded, false},
		{"unknown", Status("x"), StatusActive, false},
	}
	for _, c := range cases {
		if got := CanMove(c.from, c.to); got != c.want {
			t.Errorf("%s: CanMove = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestClosingDate(t *testing.T) {
	today := Date(2026, 6, 10)
	past, future := Date(2026, 6, 1), Date(2026, 7, 1)
	if got := ClosingDate(nil, today); !got.Equal(today) {
		t.Errorf("no end date closes today, got %v", got)
	}
	if got := ClosingDate(&past, today); !got.Equal(past) {
		t.Errorf("a passed end date is kept, got %v", got)
	}
	if got := ClosingDate(&future, today); !got.Equal(today) {
		t.Errorf("a later end date closes today, got %v", got)
	}
}

func TestToday(t *testing.T) {
	ict := time.FixedZone("ICT", 7*3600)
	// 20:00 UTC on the 9th is already the 10th in Vietnam.
	now := time.Date(2026, 6, 9, 20, 0, 0, 0, time.UTC)
	if got := Today(now, ict); !got.Equal(Date(2026, 6, 10)) {
		t.Errorf("Today = %v, want 2026-06-10", got)
	}
}

func TestAddPeriodParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/period-parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Start    string `json:"start"`
		Amount   int    `json:"amount"`
		Unit     string `json:"unit"`
		Expected string `json:"expected"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 80 {
		t.Fatalf("the shared fixture has %d cases, want 80", len(cases))
	}
	for _, c := range cases {
		start, err := ParseDate(c.Start)
		if err != nil {
			t.Fatal(err)
		}
		if got := AddPeriod(start, c.Amount, PeriodUnit(c.Unit)).Format(DateLayout); got != c.Expected {
			t.Errorf("%s + %d %s = %s, want %s", c.Start, c.Amount, c.Unit, got, c.Expected)
		}
	}
}

func TestEarliestEndIfTooSoon(t *testing.T) {
	d := func(s string) *time.Time { v, _ := ParseDate(s); return &v }
	start := *d("2026-01-31")
	if EarliestEndIfTooSoon(start, nil, 3, UnitMonth) != nil {
		t.Error("an open-ended contract always qualifies")
	}
	if EarliestEndIfTooSoon(start, d("2026-04-30"), 3, UnitMonth) != nil {
		t.Error("3 months from 31 Jan end on 30 Apr: fine")
	}
	if got := EarliestEndIfTooSoon(start, d("2026-04-29"), 3, UnitMonth); got == nil || got.Format(DateLayout) != "2026-04-30" {
		t.Errorf("too soon names the earliest date, got %v", got)
	}
	may := *d("2026-05-01")
	if got := EarliestEndIfTooSoon(may, d("2026-05-05"), 10, UnitDay); got == nil || got.Format(DateLayout) != "2026-05-11" {
		t.Errorf("10 days, got %v", got)
	}
	if got := EarliestEndIfTooSoon(may, d("2026-05-11"), 10, UnitDay); got != nil {
		t.Errorf("exactly 10 days is fine, got %v", got)
	}
	var below *BelowMinimumPeriodError
	if err := CheckMinimumPeriod(start, d("2026-02-27"), 1, UnitMonth); !errors.As(err, &below) || below.Earliest.Format(DateLayout) != "2026-02-28" {
		t.Errorf("CheckMinimumPeriod = %v", err)
	}
}

func TestCheckDates(t *testing.T) {
	start, same, later := Date(2026, 1, 1), Date(2026, 1, 1), Date(2026, 1, 2)
	if !errors.Is(CheckDates(start, &same), ErrEndNotAfterStart) {
		t.Error("an end on the start date is refused")
	}
	if CheckDates(start, &later) != nil || CheckDates(start, nil) != nil {
		t.Error("a later or missing end is fine")
	}
}

func TestRoomVacantAndSync(t *testing.T) {
	free := RoomFacts{Live: true, Status: RoomAvailable}
	if !RoomVacant(free, Occupancy{}) {
		t.Error("a live available room with no contract is vacant")
	}
	if RoomVacant(RoomFacts{Live: true, Status: RoomRented}, Occupancy{}) || RoomVacant(RoomFacts{Live: true, Status: "maintenance"}, Occupancy{}) ||
		RoomVacant(RoomFacts{Live: false, Status: RoomAvailable}, Occupancy{}) || RoomVacant(free, Occupancy{Running: 1, WithTenant: 1}) {
		t.Error("rented, repaired, deleted or held rooms are not vacant")
	}
	if s, ok := SyncedRoomStatus(RoomAvailable, Occupancy{Running: 1}); s != RoomRented || !ok {
		t.Errorf("a running contract rents the room, got %s %v", s, ok)
	}
	if s, ok := SyncedRoomStatus(RoomRented, Occupancy{}); s != RoomAvailable || !ok {
		t.Errorf("the last contract gone frees the room, got %s %v", s, ok)
	}
	if s, ok := SyncedRoomStatus("maintenance", Occupancy{}); s != "maintenance" || ok {
		t.Errorf("a room under repair stays, got %s %v", s, ok)
	}
	if _, ok := SyncedRoomStatus(RoomRented, Occupancy{Running: 2}); ok {
		t.Error("nothing to change for a rented room that has a contract")
	}
}

func TestLimitReached(t *testing.T) {
	two := 2
	if LimitReached(nil, 100) || LimitReached(&two, 1) || !LimitReached(&two, 2) || !LimitReached(&two, 3) {
		t.Error("limit rules are wrong")
	}
}

func ptr[T any](v T) *T { return &v }

func TestCancellationRules(t *testing.T) {
	tenant, other := uuid.New(), uuid.New()
	running := Contract{TenantID: &tenant, Status: StatusActive}
	if !running.CanRequestCancellation(tenant) || running.CanRequestCancellation(other) {
		t.Error("only the tenant of a running contract may ask")
	}
	if (Contract{TenantID: &tenant, Status: StatusPending}).CanRequestCancellation(tenant) || (Contract{TenantID: &tenant, Status: StatusEnded}).CanRequestCancellation(tenant) {
		t.Error("a contract that does not run cannot be cancelled")
	}
	now := Date(2026, 6, 10)
	running.RequestCancellation(now, ptr("moving"))
	if running.CanRequestCancellation(tenant) {
		t.Error("an open request cannot be sent again")
	}
	if !running.CanWithdrawCancellation(tenant) || running.CanWithdrawCancellation(other) || !running.CanRespondToCancellation() {
		t.Error("withdraw is the tenant's, response needs an open request")
	}
	running.DeclineCancellation(ptr("no"))
	if *running.Cancellation.Status != CancellationDeclined || *running.Cancellation.ResponseNote != "no" || running.Status != StatusActive {
		t.Errorf("decline keeps it running: %+v", running)
	}
	if !running.CanRequestCancellation(tenant) || running.CanRespondToCancellation() {
		t.Error("a new request is allowed after a decline")
	}
	running.RequestCancellation(now, nil)
	if running.Cancellation.ResponseNote != nil || *running.Cancellation.Status != CancellationRequested {
		t.Errorf("a new request clears the old answer: %+v", running.Cancellation)
	}
	running.WithdrawCancellation()
	if running.Cancellation.Status != nil || running.Cancellation.Reason != nil {
		t.Errorf("withdraw clears everything: %+v", running.Cancellation)
	}
}

func TestCloseAndTerminate(t *testing.T) {
	today := Date(2026, 6, 10)
	past := Date(2026, 6, 1)
	c := Contract{Status: StatusActive, EndsOn: &past}
	c.RequestCancellation(today, nil)
	c.ApproveCancellation(today)
	if c.Status != StatusEnded || !c.EndsOn.Equal(past) || *c.Cancellation.Status != CancellationApproved {
		t.Errorf("approve closes at the earlier end date: %+v", c)
	}
	running := Contract{Status: StatusActive}
	running.RequestCancellation(today, nil)
	running.Terminate(today)
	if running.Status != StatusEnded || !running.EndsOn.Equal(today) || *running.Cancellation.Status != CancellationApproved {
		t.Errorf("terminating answers an open request as approved: %+v", running)
	}
	waiting := Contract{Status: StatusPending}
	waiting.Terminate(today)
	if waiting.Status != StatusRejected || waiting.EndsOn != nil {
		t.Errorf("a waiting registration is rejected: %+v", waiting)
	}
	if !(Contract{Status: StatusPending}).CanTerminate() || (Contract{Status: StatusEnded}).CanTerminate() || (Contract{Status: StatusRejected}).CanTerminate() {
		t.Error("only pending and active contracts can be called off")
	}
}

func TestMoveTo(t *testing.T) {
	today := Date(2026, 6, 10)
	reopened := Contract{Status: StatusEnded, Cancellation: Cancellation{Status: ptr(CancellationApproved), Reason: ptr("x")}}
	reopened.MoveTo(StatusActive, today, false)
	if reopened.Status != StatusActive || reopened.Cancellation.Status != nil || reopened.Cancellation.Reason != nil {
		t.Errorf("reopening clears the old request: %+v", reopened)
	}
	ending := Contract{Status: StatusActive}
	ending.MoveTo(StatusEnded, today, false)
	if !ending.EndsOn.Equal(today) {
		t.Errorf("ending closes today, got %v", ending.EndsOn)
	}
	given := Date(2026, 6, 20)
	explicit := Contract{Status: StatusActive, EndsOn: &given}
	explicit.MoveTo(StatusEnded, today, true)
	if !explicit.EndsOn.Equal(given) {
		t.Errorf("an end date given with the change is kept, got %v", explicit.EndsOn)
	}
	rejected := Contract{Status: StatusPending}
	rejected.MoveTo(StatusRejected, today, false)
	if rejected.EndsOn != nil {
		t.Error("rejecting sets no end date")
	}
}

func TestTenantDelete(t *testing.T) {
	tenant := uuid.New()
	if !(Contract{TenantID: &tenant, Status: StatusPending}).CanTenantDelete(tenant) ||
		(Contract{TenantID: &tenant, Status: StatusActive}).CanTenantDelete(tenant) ||
		(Contract{TenantID: &tenant, Status: StatusPending}).CanTenantDelete(uuid.New()) {
		t.Error("a tenant deletes only their own waiting registration")
	}
}

func TestReorder(t *testing.T) {
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	current := []OrderedID{{a, 1}, {b, 2}, {c, 3}, {d, 4}}
	moves := Reorder(current, []uuid.UUID{c, a})
	// a and c swap slots; b and d stay.
	if len(moves) != 2 || moves[0] != (Position{c, 1}) || moves[1] != (Position{a, 3}) {
		t.Errorf("moves = %v", moves)
	}
	if got := Reorder(current, []uuid.UUID{a, c}); len(got) != 0 {
		t.Errorf("an unchanged order moves nothing, got %v", got)
	}
	// Gaps and duplicates in the stored order are renumbered.
	gappy := []OrderedID{{a, -3}, {b, -3}}
	if got := Reorder(gappy, []uuid.UUID{a}); len(got) != 2 || got[0].Order != 1 || got[1].Order != 2 {
		t.Errorf("renumbering, got %v", got)
	}
	if !AllListed(current, []uuid.UUID{a, d}) || AllListed(current, []uuid.UUID{uuid.New()}) {
		t.Error("AllListed is wrong")
	}
}
