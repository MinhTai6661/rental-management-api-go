package contracthttp

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/contract"
)

func decodeUpdate(t *testing.T, body string) updateRequest {
	t.Helper()
	var r updateRequest
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestUpdateRequestTellsAbsentFromNull(t *testing.T) {
	in, err := decodeUpdate(t, `{}`).input()
	if err != nil || in.EndsOn.Set || in.Notes.Set || in.DepositVND.Set || in.TenantName.Set || in.TenantPhone.Set || in.Status != nil || in.StartsOn != nil {
		t.Fatalf("empty body = %+v, %v", in, err)
	}
	in, err = decodeUpdate(t, `{"ends_on": null, "notes": null, "deposit_vnd": null, "tenant_name": "A", "status": "active", "starts_on": "2026-11-23", "ends_on_x": 1}`).input()
	if err != nil {
		t.Fatal(err)
	}
	if !in.EndsOn.Set || in.EndsOn.Value != nil || !in.Notes.Set || in.Notes.Value != nil || !in.DepositVND.Set || in.DepositVND.Value != nil {
		t.Errorf("null must clear: %+v", in)
	}
	if !in.TenantName.Set || *in.TenantName.Value != "A" || *in.Status != domain.StatusActive || in.StartsOn.Format(domain.DateLayout) != "2026-11-23" {
		t.Errorf("values = %+v", in)
	}
}

func TestUpdateRequestReportsEveryBadField(t *testing.T) {
	_, err := decodeUpdate(t, `{"status": "x", "price_vnd": -1, "deposit_vnd": 2000000000000, "occupant_count": 0, "starts_on": "soon", "ends_on": "2026-02-30"}`).input()
	e, ok := apperr.As(err)
	if !ok || e.Kind != apperr.KindValidation {
		t.Fatalf("%v", err)
	}
	got := map[string]string{}
	for _, f := range e.Fields {
		got[f.Name] = f.Rule
	}
	want := map[string]string{
		"status": apperr.RuleOneOf, "price_vnd": apperr.RuleMinNumber, "deposit_vnd": apperr.RuleMaxNumber,
		"occupant_count": apperr.RuleMinNumber, "starts_on": apperr.RuleDate, "ends_on": apperr.RuleDate,
	}
	for name, rule := range want {
		if got[name] != rule {
			t.Errorf("%s: rule %q, want %q (all: %v)", name, got[name], rule, got)
		}
	}
}

func TestRegisterRequestInput(t *testing.T) {
	room := uuid.New()
	end := "2027-01-01"
	in, err := registerRequest{RoomID: &room, StartsOn: "2026-11-23", EndsOn: &end}.input()
	if err != nil || in.RoomID != room || in.OccupantCount != 0 || in.EndsOn == nil || in.EndsOn.Format(domain.DateLayout) != end {
		t.Fatalf("input = %+v, %v", in, err)
	}
	bad := "2027-1-1"
	if _, err := (registerRequest{RoomID: &room, StartsOn: "x", EndsOn: &bad}).input(); err == nil {
		t.Fatal("bad dates must be refused")
	} else if e, _ := apperr.As(err); len(e.Fields) != 2 {
		t.Errorf("both dates are reported: %+v", e.Fields)
	}
}
