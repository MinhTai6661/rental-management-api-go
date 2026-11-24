package audit

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEveryActionHasAUniqueNameAndATier(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Actions() {
		if seen[string(a)] {
			t.Errorf("%s is listed twice", a)
		}
		seen[string(a)] = true
		if got, ok := ParseAction(string(a)); !ok || got != a {
			t.Errorf("%s does not parse back", a)
		}
		if a.Tier() != Important && a.Tier() != Routine {
			t.Errorf("%s has no tier", a)
		}
	}
	if _, ok := ParseAction("room.exploded"); ok {
		t.Error("an unknown action must not parse")
	}
}

func TestOnlyTheNoiseIsRoutine(t *testing.T) {
	routine := map[Action]bool{AuthLogin: true, AuthLoginFailed: true, AuthLogout: true, InvitationLookup: true}
	for _, a := range Actions() {
		if (a.Tier() == Routine) != routine[a] {
			t.Errorf("%s: tier %s", a, a.Tier())
		}
	}
}

func TestSanitizeRemovesSecretsAndMasksAccountNumbers(t *testing.T) {
	in := map[string]any{
		"Password": "hunter2", "note": "ok",
		"nested":         map[string]any{"Refresh_Token": "abc", "list": []any{map[string]any{"secret": "s"}}},
		"account_number": "0123456789", "bank_account": map[string]any{"from": "111122223333", "to": nil},
		"accounts": map[string]any{"ACCOUNT_NUMBER": []any{"9999888877"}},
	}
	out := Sanitize(in)
	if out["Password"] != redacted || out["note"] != "ok" {
		t.Fatalf("%v", out)
	}
	nested := out["nested"].(map[string]any)
	if nested["Refresh_Token"] != redacted || nested["list"].([]any)[0].(map[string]any)["secret"] != redacted {
		t.Fatalf("nested: %v", nested)
	}
	if out["account_number"] != "****6789" {
		t.Errorf("scalar: %v", out["account_number"])
	}
	pair := out["bank_account"].(map[string]any)
	if pair["from"] != "****3333" || pair["to"] != nil {
		t.Errorf("pair: %v", pair)
	}
	if got := out["accounts"].(map[string]any)["ACCOUNT_NUMBER"].([]any)[0]; got != "****8877" {
		t.Errorf("array: %v", got)
	}
	if in["Password"] != "hunter2" {
		t.Error("the input must not change")
	}
}

func TestSanitizeCutsABigPayloadAndDropsAnEmptyOne(t *testing.T) {
	if Sanitize(nil) != nil || Sanitize(map[string]any{}) != nil {
		t.Error("nothing to keep gives nil")
	}
	big := Sanitize(map[string]any{"text": strings.Repeat("x", MaxPayloadBytes)})
	if big["truncated"] != true {
		t.Fatalf("%v", big)
	}
	if n, _ := big["bytes"].(int); n <= MaxPayloadBytes {
		t.Errorf("bytes: %v", big["bytes"])
	}
	raw, _ := json.Marshal(Sanitize(map[string]any{"text": strings.Repeat("x", MaxPayloadBytes-100)}))
	if len(raw) > MaxPayloadBytes {
		t.Error("a payload under the limit is kept")
	}
	type typed struct {
		Amount int `json:"amount"`
	}
	if got := Sanitize(typed{Amount: 5}); got["amount"] != float64(5) {
		t.Errorf("typed values are normalized: %v", got)
	}
}

func TestRedactMoney(t *testing.T) {
	in := map[string]any{
		"price": map[string]any{"from": 100.0, "to": 200.0}, "Amount": 5.0, "deposit_amount": nil,
		"room": "P1", "lines": []any{map[string]any{"unit_price": 3.0, "name": "power"}}, "paid_vnd": 7.0, "total_amount_vnd": 9.0,
	}
	out := RedactMoney(InvoiceUpdated, in)
	pair := out["price"].(map[string]any)
	if pair["from"] != MoneyMask || pair["to"] != MoneyMask {
		t.Errorf("a pair keeps its shape: %v", pair)
	}
	if out["Amount"] != MoneyMask || out["deposit_amount"] != nil || out["room"] != "P1" || out["paid_vnd"] != MoneyMask || out["total_amount_vnd"] != MoneyMask {
		t.Errorf("%v", out)
	}
	if line := out["lines"].([]any)[0].(map[string]any); line["unit_price"] != MoneyMask || line["name"] != "power" {
		t.Errorf("deep: %v", line)
	}
	if !reflect.DeepEqual(in["price"], map[string]any{"from": 100.0, "to": 200.0}) {
		t.Error("the input must not change")
	}
	for _, a := range []Action{PlanSaved, SubscriptionAssigned} {
		if got := RedactMoney(a, in); got["Amount"] != 5.0 {
			t.Errorf("%s keeps the platform's amounts", a)
		}
	}
	if RedactMoney(InvoiceSent, nil) != nil {
		t.Error("nil stays nil")
	}
}

func TestScopeOf(t *testing.T) {
	if s := ScopeOf("super_admin"); !s.IncludeStaff || !s.IncludeSettings {
		t.Errorf("%+v", s)
	}
	if s := ScopeOf("admin"); s.IncludeStaff || s.IncludeSettings {
		t.Errorf("%+v", s)
	}
}

func TestForSubjectHidesWhatOthersDid(t *testing.T) {
	me, other := uuid.New(), uuid.New()
	own := Record{Tier: Important, Entry: Entry{Action: AuthLogin, ActorID: &me, ActorRole: "tenant", IP: "1.1.1.1", Meta: map[string]any{"via": "password"}}}
	if got := ForSubject(own, me); got.IP != "1.1.1.1" || got.ActorID == nil {
		t.Errorf("my own entry stays whole: %+v", got)
	}
	theirs := Record{ID: 3, Tier: Important, Entry: Entry{
		Action: UserRolesChanged, ActorID: &other, ActorRole: "admin", IP: "2.2.2.2", UserAgent: "ua",
		Refs: References{TargetUserID: &me}, Changes: map[string]any{"roles": 1}, Meta: map[string]any{"k": "v"},
	}}
	got := ForSubject(theirs, me)
	if got.ActorID != nil || got.ActorRole != "admin" || got.Changes != nil || got.Meta != nil || got.IP != "" || got.UserAgent != "" || got.Refs.TargetUserID != nil || got.ID != 3 {
		t.Errorf("only the role of somebody else's action remains: %+v", got)
	}
}

func TestCollapseMergesABurst(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	at := time.Date(2026, 11, 24, 10, 0, 5, 0, time.UTC)
	rec := func(action Action, actor *uuid.UUID, t time.Time) Record {
		return Record{Tier: Important, Entry: Entry{Action: action, ActorID: actor, ActorRole: "landlord", OccurredAt: t}}
	}
	got := Collapse([]Record{
		rec(RoomDeleted, &a, at.Add(900*time.Millisecond)), rec(RoomDeleted, &a, at), rec(RoomDeleted, &a, at),
		rec(RoomDeleted, &b, at), rec(RoomDeleted, &a, at.Add(-time.Second)), rec(RoomUpdated, &a, at.Add(-time.Second)),
	})
	counts := make([]int, len(got))
	for i, c := range got {
		counts[i] = c.Count
	}
	if !reflect.DeepEqual(counts, []int{3, 1, 1, 1}) {
		t.Errorf("counts %v", counts)
	}
	if Collapse(nil) == nil {
		t.Error("an empty list is an empty slice")
	}
}

func TestPartitionNamesAndRetention(t *testing.T) {
	now := time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	if got := PartitionName(TableImportant, now); got != "audit_logs_2026_11" {
		t.Errorf("%s", got)
	}
	months := MonthsAhead(now, 3)
	if len(months) != 4 || months[0] != time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) || months[3] != time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("%v", months)
	}
	for name, want := range map[string]bool{"audit_logs_2026_11": true, "audit_logs_default": false, "audit_logs_2026_13": false, "audit_activity_2026_11": false, "audit_logs_2026_11_x": false} {
		if _, ok := ParsePartition(TableImportant, name); ok != want {
			t.Errorf("%s: %v", name, ok)
		}
	}
	cutoff := Cutoff(Important, now, 24, 90)
	if !cutoff.Equal(time.Date(2024, 11, 24, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("%v", cutoff)
	}
	if got := Cutoff(Routine, now, 24, 90); !got.Equal(now.AddDate(0, 0, -90)) {
		t.Errorf("%v", got)
	}
	// A month goes only when its last instant is past the cutoff.
	oct := time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC)
	nov := time.Date(2024, 11, 1, 0, 0, 0, 0, time.UTC)
	if !Expired(oct, cutoff) || Expired(nov, cutoff) {
		t.Errorf("oct %v nov %v", Expired(oct, cutoff), Expired(nov, cutoff))
	}
}

func TestFilterBounds(t *testing.T) {
	day := func(d int) *time.Time { v := time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC); return &v }
	from, before, err := Filter{From: day(10), To: day(12)}.Bounds()
	if err != nil || from == nil || before == nil || before.Sub(*from) != 72*time.Hour {
		t.Fatalf("%v %v %v", from, before, err)
	}
	if _, _, err := (Filter{From: day(12), To: day(10)}).Bounds(); !errors.Is(err, ErrRangeBackwards) {
		t.Errorf("%v", err)
	}
	if f, b, err := (Filter{}).Bounds(); f != nil || b != nil || err != nil {
		t.Error("an open range has no bounds")
	}
	if _, ok := ParseTier("weird"); ok {
		t.Error("an unknown tier is refused")
	}
	if tier, ok := ParseTier(""); !ok || tier != "" {
		t.Error("no tier is no filter")
	}
}
