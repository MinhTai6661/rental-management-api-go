//go:build integration

package main

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"rental-management/internal/adapter/http/testenv"
	"rental-management/internal/domain/identity"
	statsdomain "rental-management/internal/domain/stats"
)

type moneyFigures struct {
	BilledVND      int64    `json:"billed_vnd"`
	CollectedVND   int64    `json:"collected_vnd"`
	CollectionRate *float64 `json:"collection_rate"`
}

type overviewBody struct {
	Rooms struct {
		Total  int `json:"total"`
		Rented int `json:"rented"`
	} `json:"rooms"`
	OccupancyRate        *float64 `json:"occupancy_rate"`
	ActiveContracts      int      `json:"active_contracts"`
	PendingRegistrations int      `json:"pending_registrations"`
	Money                struct {
		OutstandingVND int64        `json:"outstanding_vnd"`
		OverdueVND     int64        `json:"overdue_vnd"`
		OverdueCount   int          `json:"overdue_count"`
		ThisMonth      moneyFigures `json:"this_month"`
		PreviousMonth  moneyFigures `json:"previous_month"`
	} `json:"money"`
}

type revenueBody struct {
	Months []struct {
		Month          string `json:"month"`
		BilledVND      int64  `json:"billed_vnd"`
		CollectedVND   int64  `json:"collected_vnd"`
		OutstandingVND int64  `json:"outstanding_vnd"`
	} `json:"months"`
	Totals struct {
		BilledVND int64 `json:"billed_vnd"`
	} `json:"totals"`
}

type dashboardBody struct {
	Overview  overviewBody `json:"overview"`
	Revenue   revenueBody  `json:"revenue"`
	Occupancy struct {
		Months []struct {
			Month         string   `json:"month"`
			OccupiedDays  int      `json:"occupied_days"`
			AvailableDays int      `json:"available_days"`
			Rate          *float64 `json:"rate"`
		} `json:"months"`
	} `json:"occupancy"`
	Rooms struct {
		Rooms []struct {
			Name      string `json:"name"`
			BilledVND int64  `json:"billed_vnd"`
		} `json:"rooms"`
	} `json:"rooms"`
	Utilities struct {
		Months []struct {
			Month string `json:"month"`
		} `json:"months"`
	} `json:"utilities"`
}

func stat[T any](e *bridgeEnv, bearer, path string) T {
	e.t.Helper()
	w := e.call(http.MethodGet, path, nil, bearer)
	testenv.ExpectStatus(e.t, w, http.StatusOK)
	return decodeFull[struct {
		Data T `json:"data"`
	}](e.t, w).Data
}

// billedRoom is a rented room of the landlord with a 3M invoice of this month, 1M of it paid, and a due date that has
// passed.
func (e *bridgeEnv) billedRoom() {
	e.t.Helper()
	ctx, month := context.Background(), statsdomain.MonthOf(statsdomain.Day(time.Now())).First()
	var roomID, contractID, invoiceID string
	must := func(err error) {
		e.t.Helper()
		if err != nil {
			e.t.Fatal(err)
		}
	}
	must(e.pool.QueryRow(ctx, `INSERT INTO rooms (landlord_id, name, address_detail, price_vnd, status, display_order, created_at)
		VALUES ($1, 'S1', 'x', 1000000, 'rented', 1, $2) RETURNING id`, e.landlord, month.AddDate(0, -2, 0)).Scan(&roomID))
	must(e.pool.QueryRow(ctx, `INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, display_order, confirmed_at)
		VALUES ($1, $2, $3, 'active', 1000000, $4, 1, now()) RETURNING id`, roomID, e.landlord, e.tenant, month.AddDate(0, -2, 0)).Scan(&contractID))
	must(e.pool.QueryRow(ctx, `INSERT INTO invoices (contract_id, invoice_number, name, payment_label, payment_code, period_start, period_end, total_amount_vnd, due_at, status, sent_at, sent_via)
		VALUES ($1, 'INV-S1', 'inv', 'L', 'CODE-S1', $2, $3, 3000000, now() - interval '1 day', 'sent', now(), 'manual') RETURNING id`,
		contractID, month, month.AddDate(0, 0, 27)).Scan(&invoiceID))
	_, err := e.pool.Exec(ctx, `INSERT INTO payments (invoice_id, method, amount_vnd, status, paid_at, reviewed_at) VALUES ($1, 'manual_proof', 1000000, 'confirmed', now(), now())`, invoiceID)
	must(err)
}

func TestLandlordFigures(t *testing.T) {
	e := newBridgeEnv(t)
	e.billedRoom()

	o := stat[overviewBody](e, "landlord", "/v1/stats/property/overview")
	if o.Rooms.Total != 1 || o.Rooms.Rented != 1 || o.OccupancyRate == nil || *o.OccupancyRate != 100 || o.ActiveContracts != 1 {
		t.Errorf("%+v", o)
	}
	m := o.Money
	// 3M billed, 1M paid, 2M outstanding and past due; this month is 1M of 3M = 33.3 % collected; last month nothing.
	if m.OutstandingVND != 2_000_000 || m.OverdueVND != 2_000_000 || m.OverdueCount != 1 {
		t.Errorf("owed %+v", m)
	}
	if m.ThisMonth.BilledVND != 3_000_000 || m.ThisMonth.CollectedVND != 1_000_000 || m.ThisMonth.CollectionRate == nil || *m.ThisMonth.CollectionRate != 33.3 {
		t.Errorf("this month %+v", m.ThisMonth)
	}
	if m.PreviousMonth.BilledVND != 0 || m.PreviousMonth.CollectionRate != nil {
		t.Errorf("previous month %+v", m.PreviousMonth)
	}

	d := stat[dashboardBody](e, "landlord", "/v1/stats/property/dashboard")
	if !reflect.DeepEqual(d.Overview, o) {
		t.Errorf("the dashboard's overview is the overview's:\n%+v\n%+v", d.Overview, o)
	}
	if len(d.Revenue.Months) != 12 || d.Revenue.Totals.BilledVND != 3_000_000 || d.Revenue.Months[11].OutstandingVND != 2_000_000 {
		t.Errorf("revenue %+v", d.Revenue)
	}
	if len(d.Occupancy.Months) != 12 || len(d.Utilities.Months) != 12 || len(d.Rooms.Rooms) != 1 || d.Rooms.Rooms[0].BilledVND != 3_000_000 {
		t.Errorf("%+v", d)
	}
	for _, path := range []string{"/revenue", "/rooms", "/occupancy", "/utilities", "/buildings"} {
		stat[map[string]any](e, "landlord", "/v1/stats/property"+path)
	}

	// Another landlord sees none of it, and nothing divides by nothing.
	other := stat[overviewBody](e, "landlord2", "/v1/stats/property/overview")
	if other.Rooms.Total != 0 || other.OccupancyRate != nil || other.Money.ThisMonth.BilledVND != 0 || other.Money.ThisMonth.CollectionRate != nil {
		t.Errorf("%+v", other)
	}
}

func TestStatsParametersAreChecked(t *testing.T) {
	e := newBridgeEnv(t)
	cases := []struct{ query, field string }{
		{"?from=2020-01-01&to=2026-01-01", "from"}, // more than 36 months
		{"?from=2026-05-01&to=2026-01-01", "to"},   // backwards
		{"?from=yesterday", "from"},
		{"?sort=name", "sort"},
		{"?limit=0", "limit"},
		{"?limit=101", "limit"},
		{"?building_id=nope", "building_id"},
	}
	for _, c := range cases {
		w := e.call(http.MethodGet, "/v1/stats/property/rooms"+c.query, nil, "landlord")
		testenv.ExpectStatus(t, w, http.StatusUnprocessableEntity)
		fields := errorFieldsOf(t, w.Body.String())
		if _, ok := fields[c.field]; !ok {
			t.Errorf("%s: want a problem with %s, got %v", c.query, c.field, fields)
		}
	}
}

func TestStatsPermissions(t *testing.T) {
	e := newBridgeEnv(t)
	for _, tc := range []struct {
		bearer, path string
		want         int
	}{
		{"landlord", "/v1/stats/property/overview", http.StatusOK},
		{"tenant", "/v1/stats/property/overview", http.StatusForbidden},
		{"landlord-as-tenant", "/v1/stats/property/dashboard", http.StatusForbidden},
		{"admin", "/v1/stats/property/overview", http.StatusForbidden},
		{"super", "/v1/stats/property/rooms", http.StatusForbidden},
		{"landlord", "/v1/stats/admin/overview", http.StatusForbidden},
		{"landlord", "/v1/stats/admin/growth", http.StatusForbidden},
		{"tenant", "/v1/stats/admin/overview", http.StatusForbidden},
		{"admin", "/v1/stats/admin/overview", http.StatusOK},
		{"super", "/v1/stats/admin/growth", http.StatusOK},
		{"admin", "/v1/stats/system/overview", http.StatusForbidden},
		{"landlord", "/v1/stats/system/overview", http.StatusForbidden},
		{"super", "/v1/stats/system/overview", http.StatusOK},
	} {
		testenv.ExpectStatus(t, e.call(http.MethodGet, tc.path, nil, tc.bearer), tc.want)
	}
	// The catalog gives the three permissions to the right roles only.
	perms := identity.RolePermissions()
	has := func(role, code string) bool {
		for _, c := range perms[role] {
			if c == code {
				return true
			}
		}
		return false
	}
	if !has("landlord", "stats.view") || has("admin", "stats.view") || !has("admin", "stats.admin") || has("admin", "stats.system") || !has("super_admin", "stats.system") || has("tenant", "stats.view") {
		t.Error("stats permissions of the roles")
	}
}

func TestPlatformFigures(t *testing.T) {
	e := newBridgeEnv(t)
	o := stat[struct {
		Users struct {
			Total  int            `json:"total"`
			ByRole map[string]int `json:"by_role"`
		} `json:"users"`
		Applications struct {
			ByStatus map[string]int `json:"by_status"`
			Average  *float64       `json:"average_review_hours"`
			Oldest   *int           `json:"oldest_waiting_days"`
		} `json:"applications"`
	}](e, "admin", "/v1/stats/admin/overview")
	// The environment holds five accounts: the landlord and landlord2 also hold tenant, the tenant, one admin, one super admin.
	if o.Users.Total != 5 || o.Users.ByRole["tenant"] != 3 || o.Users.ByRole["landlord"] != 2 || o.Users.ByRole["admin"] != 1 || o.Users.ByRole["super_admin"] != 1 || o.Users.ByRole["manager"] != 0 {
		t.Errorf("%+v", o.Users)
	}
	if o.Applications.Average != nil || o.Applications.Oldest != nil || o.Applications.ByStatus["pending_review"] != 0 {
		t.Errorf("%+v", o.Applications)
	}
	g := stat[struct {
		Months []struct {
			Month string `json:"month"`
			Users int    `json:"users"`
		} `json:"months"`
	}](e, "admin", "/v1/stats/admin/growth")
	if len(g.Months) != 12 || g.Months[11].Users != 5 {
		t.Errorf("%+v", g)
	}
	s := stat[struct {
		Staff struct {
			Admins      int `json:"admins"`
			SuperAdmins int `json:"super_admins"`
		} `json:"staff"`
		Plans []any `json:"plans"`
	}](e, "super", "/v1/stats/system/overview")
	if s.Staff.Admins != 1 || s.Staff.SuperAdmins != 1 || s.Plans == nil {
		t.Errorf("%+v", s)
	}
}

// errorFieldsOf is the fields of the error of a response body.
func errorFieldsOf(t *testing.T, body string) map[string]any {
	t.Helper()
	fields, _ := errorOf(t, body)["fields"].(map[string]any)
	return fields
}
