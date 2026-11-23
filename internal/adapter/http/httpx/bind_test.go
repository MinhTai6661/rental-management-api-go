package httpx

import (
	"testing"

	"rental-management/internal/adapter/http/apperr"
)

type chargeReq struct {
	Name string `json:"name" validate:"required,max=5"`
}

type nestedReq struct {
	Name    string      `json:"name" validate:"required"`
	Charges []chargeReq `json:"charges" validate:"max=2,dive"`
	IDs     []string    `json:"ids" validate:"min=1,unique"`
	Inherit *[]string   `json:"inherit" validate:"omitempty,dive,oneof=a b"`
}

func fieldsOf(t *testing.T, err error) map[string]apperr.Field {
	t.Helper()
	ae, ok := apperr.As(err)
	if !ok || ae.Kind != apperr.KindValidation {
		t.Fatalf("want a validation error, got %v", err)
	}
	out := map[string]apperr.Field{}
	for _, f := range ae.Fields {
		out[f.Name] = f
	}
	return out
}

func TestBinderReportsNestedFieldPaths(t *testing.T) {
	b := NewBinder()
	bad := []string{"x"}
	req := nestedReq{
		Charges: []chargeReq{{Name: ""}, {Name: "toolong"}},
		IDs:     []string{"x", "x"},
		Inherit: &bad,
	}
	got := fieldsOf(t, b.Struct(&req))
	for name, rule := range map[string]string{
		"name":           apperr.RuleRequired,
		"charges.1.name": apperr.RuleMaxString,
		"charges.0.name": apperr.RuleRequired,
		"ids":            apperr.RuleDistinct,
		"inherit.0":      apperr.RuleOneOf,
	} {
		if got[name].Rule != rule {
			t.Errorf("%s: rule %q, want %q (all: %v)", name, got[name].Rule, rule, got)
		}
	}
}

func TestBinderMinItems(t *testing.T) {
	got := fieldsOf(t, NewBinder().Struct(&nestedReq{Name: "n"}))
	if got["ids"].Rule != apperr.RuleMinItems || got["ids"].Params["min"] != "1" {
		t.Errorf("ids: %+v", got["ids"])
	}
}

func TestBinderMaxItems(t *testing.T) {
	req := nestedReq{Name: "n", IDs: []string{"a"}, Charges: []chargeReq{{Name: "a"}, {Name: "b"}, {Name: "c"}}}
	got := fieldsOf(t, NewBinder().Struct(&req))
	if got["charges"].Rule != apperr.RuleMaxItems || got["charges"].Params["max"] != "2" {
		t.Errorf("charges: %+v", got["charges"])
	}
}
