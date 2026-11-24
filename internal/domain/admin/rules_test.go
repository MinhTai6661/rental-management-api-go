package admin

import (
	"errors"
	"strings"
	"testing"
	"time"

	"rental-management/pkg/authz"
)

func TestCheckCanApply(t *testing.T) {
	tests := []struct {
		name    string
		acting  string
		held    []string
		pending bool
		want    error
	}{
		{"a tenant may apply", authz.RoleTenant, []string{authz.RoleTenant}, false, nil},
		{"a landlord already is one", authz.RoleLandlord, []string{authz.RoleLandlord, authz.RoleTenant}, false, ErrAlreadyLandlord},
		{"acting as a manager", authz.RoleManager, []string{authz.RoleTenant, authz.RoleManager}, false, ErrNotRenter},
		{"no tenant role", authz.RoleManager, []string{authz.RoleManager}, false, ErrNotRenter},
		{"staff", authz.RoleAdmin, []string{authz.RoleAdmin}, false, ErrNotRenter},
		{"one application waiting", authz.RoleTenant, []string{authz.RoleTenant}, true, ErrAlreadyPending},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckCanApply(tc.acting, tc.held, tc.pending); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestApplicationInputNormalize(t *testing.T) {
	if _, err := (ApplicationInput{BusinessName: "  ", Phone: ""}).Normalize(); err == nil {
		t.Fatal("blank name and phone must be refused")
	}
	long := strings.Repeat("x", MaxBusinessName+1)
	var ve *ValidationError
	if _, err := (ApplicationInput{BusinessName: long, Phone: "0900000000"}).Normalize(); !errors.As(err, &ve) || ve.Violations[0].Rule != ruleMaxString {
		t.Fatalf("long name: %v", err)
	}
	blank := "   "
	in, err := (ApplicationInput{BusinessName: " Nhà trọ A ", Phone: "090", Note: &blank}).Normalize()
	if err != nil || in.BusinessName != "Nhà trọ A" || in.Note != nil {
		t.Fatalf("got %+v, %v", in, err)
	}
}

func TestReview(t *testing.T) {
	if err := (Application{Status: ApplicationApproved}).CheckReviewable(); !errors.Is(err, ErrNotReviewable) {
		t.Fatalf("an answered application cannot be answered again: %v", err)
	}
	if _, err := NormalizeReview("  ", true); err == nil {
		t.Fatal("a rejection needs a reason")
	}
	if _, err := NormalizeReview("", false); err != nil {
		t.Fatal("an approval needs no note")
	}
	if got := FirstBuildingName(strings.Repeat("a", 80)); len(got) != 50 {
		t.Fatalf("building name must be cut to 50: %d", len(got))
	}
}

func TestProfileChangedFields(t *testing.T) {
	phone := "090"
	p := Profile{BusinessName: "A", Phone: &phone}
	got := p.ChangedFields(ProfileInput{BusinessName: "B", Phone: &phone, Description: &phone})
	if len(got) != 2 || got[0] != "business_name" || got[1] != "description" {
		t.Fatalf("got %v", got)
	}
}

func TestSettingCheckValue(t *testing.T) {
	d, _ := FindDefinition("invoices.due_days")
	for value, want := range map[float64]string{0: ruleMinNumber, 91: ruleMaxNumber, 3.5: ruleInteger, 30: ""} {
		if rule, _ := d.CheckValue(value); rule != want {
			t.Errorf("value %v: want %q got %q", value, want, rule)
		}
	}
	q, _ := FindDefinition("uploads.client_quality")
	if rule, _ := q.CheckValue(0.7); rule != "" {
		t.Fatalf("a float setting takes fractions: %q", rule)
	}
	if _, ok := FindDefinition("evil.key"); ok {
		t.Fatal("only listed keys can be set")
	}
}

func TestVersionChangesWithOverrides(t *testing.T) {
	a, b := Version(nil), Version(map[string]float64{"invoices.due_days": 5})
	if a == b || len(a) != 12 || Version(map[string]float64{"invoices.due_days": 5}) != b {
		t.Fatalf("version must be 12 chars, stable and change with the overrides: %s %s", a, b)
	}
}

func TestNeedsAcceptance(t *testing.T) {
	old, cur := "v1", "v2"
	tenant, staff := []string{authz.RoleTenant}, []string{authz.RoleAdmin}
	if !NeedsAcceptance(tenant, &old, cur) || !NeedsAcceptance(tenant, nil, cur) {
		t.Fatal("a tenant on an old version must accept again")
	}
	if NeedsAcceptance(tenant, &cur, cur) {
		t.Fatal("an up-to-date tenant is not asked")
	}
	if NeedsAcceptance(staff, &old, cur) {
		t.Fatal("staff is never asked")
	}
}

func TestParseLegalKindAndVersion(t *testing.T) {
	if _, err := ParseLegalKind("cookies"); !errors.Is(err, ErrUnknownKind) {
		t.Fatal("unknown kind")
	}
	if got := NewVersion(time.Date(2026, 11, 24, 8, 5, 9, 0, time.UTC)); got != "2026-11-24.080509" {
		t.Fatalf("got %s", got)
	}
}

func TestLegalInputNormalize(t *testing.T) {
	good := LegalText{Title: "Terms", Body: []byte(`{"type":"doc","content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"1. Use"}]}]}`)}
	in := LegalInput{Content: LegalContent{VI: good, EN: good}, Summary: ChangeSummary{VI: "đổi", EN: "changed"}}
	if _, err := in.Normalize(); err != nil {
		t.Fatalf("valid input refused: %v", err)
	}
	in.Summary.EN = " "
	in.Content.VI.Body = []byte(`{"type":"doc","content":[]}`)
	var ve *ValidationError
	if _, err := in.Normalize(); !errors.As(err, &ve) || len(ve.Violations) != 2 {
		t.Fatalf("want 2 violations, got %v", err)
	}
}

func TestCheckErasable(t *testing.T) {
	if err := CheckErasable([]string{authz.RoleAdmin}, ErasureFacts{}); !errors.Is(err, ErrStaffCannotErase) {
		t.Fatalf("staff: %v", err)
	}
	var blocked *ErasureBlockedError
	if err := CheckErasable([]string{authz.RoleTenant}, ErasureFacts{AsTenant: true, AsLandlord: true}); !errors.As(err, &blocked) || len(blocked.Blockers) != 2 {
		t.Fatalf("running contracts must block: %v", err)
	}
	if err := CheckErasable([]string{authz.RoleTenant}, ErasureFacts{}); err != nil {
		t.Fatal(err)
	}
}
