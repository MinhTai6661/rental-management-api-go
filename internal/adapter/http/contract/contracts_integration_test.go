//go:build integration

package contracthttp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/contract"
	usecase "rental-management/internal/usecase/contract"
)

func TestRegisterCreatesAWaitingContractTheLandlordSeesAtOnce(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.room(e.landlA, "P101")

	c := e.register("tenant-t", room, map[string]any{"occupant_count": 2, "notes": "Hello"})
	if c.Status != "pending" || c.PriceVND != 3_000_000 || c.OccupantCount != 2 || !c.Confirmed || *c.Notes != "Hello" ||
		c.StartsOn != dateStr(today()) || c.EndsOn != nil || c.Unassigned || c.DepositVND != nil {
		t.Fatalf("contract = %+v", c)
	}
	if c.Tenant == nil || c.Tenant.FullName != "Tenant T" || c.Landlord.FullName != "Landlord A" || c.Room.Name != "P101" ||
		c.Room.Cover == nil || c.Room.Cover.ThumbURL != "http://files.test/c_t.webp" {
		t.Errorf("parties = %+v", c)
	}
	if c.Abilities.Review || c.Abilities.Update || c.Abilities.Delete == false {
		t.Errorf("a tenant can only withdraw a waiting registration: %+v", c.Abilities)
	}
	if e.roomStatus(room) != "available" {
		t.Error("the room stays on the market")
	}
	if len(e.notices.sent) != 1 || e.notices.sent[0].Kind != usecase.NotifyRegistrationConfirmed || e.notices.sent[0].UserID != e.landlA {
		t.Errorf("the landlord is told: %+v", e.notices.sent)
	}

	// The landlord sees it, with the right to answer it.
	w := e.call(http.MethodGet, "/v1/contracts/"+c.ID.String(), nil, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	seen := decodeData[ContractView](t, w)
	if !seen.Abilities.Review || !seen.Abilities.Update || !seen.Abilities.Delete || seen.ID != c.ID {
		t.Errorf("landlord view = %+v", seen)
	}
}

func TestRegisterValidationAndRefusals(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.room(e.landlA, "P101")
	rented := e.room(e.landlA, "P102")
	repair := e.room(e.landlA, "P103")
	for id, status := range map[uuid.UUID]string{rented: "rented", repair: "maintenance"} {
		if _, err := e.pool.Exec(t.Context(), `UPDATE rooms SET status = $1 WHERE id = $2`, status, id); err != nil {
			t.Fatal(err)
		}
	}
	post := func(bearer string, body any) *testResponse {
		return &testResponse{e.call(http.MethodPost, "/v1/contracts/register", body, bearer)}
	}
	ok := map[string]any{"room_id": room, "starts_on": dateStr(today())}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for kk, vv := range ok {
			m[kk] = vv
		}
		m[k] = v
		return m
	}

	// Authentication and roles.
	expectError(t, post("", ok).ResponseRecorder, http.StatusUnauthorized, apperr.CodeUnauthenticated)
	expectError(t, post("landlord-a", ok).ResponseRecorder, http.StatusForbidden, apperr.CodeForbidden)
	expectError(t, post("admin", ok).ResponseRecorder, http.StatusForbidden, apperr.CodeForbidden)
	expectError(t, post("tenant-nothing", ok).ResponseRecorder, http.StatusForbidden, apperr.CodeForbidden)

	// Fields.
	body := expectError(t, post("tenant-t", map[string]any{}).ResponseRecorder, http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if len(body.Error.Fields["room_id"]) == 0 || len(body.Error.Fields["starts_on"]) == 0 {
		t.Errorf("fields = %v", body.Error.Fields)
	}
	for name, bad := range map[string]map[string]any{
		"bad start date":  with("starts_on", "23/11/2026"),
		"bad end date":    with("ends_on", "tomorrow"),
		"no occupants":    with("occupant_count", 0),
		"too many":        with("occupant_count", 51),
		"long notes":      with("notes", string(make([]byte, 1001))),
		"unknown room":    {"room_id": uuid.New(), "starts_on": dateStr(today())},
		"start in past":   with("starts_on", dateStr(today().AddDate(0, 0, -1))),
		"end not after":   with("ends_on", dateStr(today())),
		"room id invalid": with("room_id", "nope"),
	} {
		if w := post("tenant-t", bad); w.Code != http.StatusUnprocessableEntity && w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	w := post("tenant-t", with("starts_on", "23/11/2026"))
	if got := expectError(t, w.ResponseRecorder, http.StatusUnprocessableEntity, apperr.CodeValidationFailed).Error.Fields["starts_on"]; len(got) != 1 {
		t.Errorf("a bad date is reported on its field: %v", got)
	}
	past := post("tenant-t", with("starts_on", dateStr(today().AddDate(0, 0, -1))))
	expectError(t, past.ResponseRecorder, http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if got := past.fields()["starts_on"]; len(got) != 1 {
		t.Errorf("past start: %v", got)
	}
	if got := post("tenant-t", with("ends_on", dateStr(today()))).fields()["ends_on"]; len(got) != 1 {
		t.Errorf("end must follow start: %v", got)
	}
	if got := post("tenant-t", map[string]any{"room_id": uuid.New(), "starts_on": dateStr(today())}).fields()["room_id"]; len(got) != 1 {
		t.Errorf("unknown room: %v", got)
	}

	// The room's state and the tenant's.
	expectError(t, post("landlord-a-as-tenant", ok).ResponseRecorder, http.StatusConflict, apperr.CodeContractOwnRoom)
	expectError(t, post("tenant-t", map[string]any{"room_id": rented, "starts_on": dateStr(today())}).ResponseRecorder, http.StatusConflict, apperr.CodeContractNotVacant)
	expectError(t, post("tenant-t", map[string]any{"room_id": repair, "starts_on": dateStr(today())}).ResponseRecorder, http.StatusConflict, apperr.CodeContractNotVacant)
	expectStatus(t, post("tenant-t", ok).ResponseRecorder, http.StatusCreated)
	expectError(t, post("tenant-t", ok).ResponseRecorder, http.StatusConflict, apperr.CodeContractRegistered)
	// Another tenant may still wait for the same room.
	expectStatus(t, post("tenant-u", ok).ResponseRecorder, http.StatusCreated)
}

func TestRegisterChecksTheMinimumRentalPeriodAndThePlan(t *testing.T) {
	two := 1
	e := newContractEnv(t, fixedPlans{pending: &two})
	room := e.room(e.landlA, "P101")
	if _, err := e.pool.Exec(t.Context(), `UPDATE rooms SET minimum_rental_period = 3, rental_period_unit = 'month' WHERE id = $1`, room); err != nil {
		t.Fatal(err)
	}
	start := today()
	end := func(t0 string) map[string]any {
		return map[string]any{"room_id": room, "starts_on": dateStr(start), "ends_on": t0}
	}
	soon := domain.AddPeriod(start, 3, domain.UnitMonth).AddDate(0, 0, -1)
	w := e.call(http.MethodPost, "/v1/contracts/register", end(dateStr(soon)), "tenant-t")
	body := expectError(t, w, http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if msgs := body.Error.Fields["ends_on"]; len(msgs) != 1 || msgs[0] == "" {
		t.Fatalf("fields = %v", body.Error.Fields)
	}
	vi := e.callIn("vi", http.MethodPost, "/v1/contracts/register", end(dateStr(soon)), "tenant-t")
	var vb struct {
		Error struct {
			Fields map[string][]string `json:"fields"`
		} `json:"error"`
	}
	_ = json.Unmarshal(vi.Body.Bytes(), &vb)
	if len(vb.Error.Fields["ends_on"]) != 1 || vb.Error.Fields["ends_on"][0] == body.Error.Fields["ends_on"][0] {
		t.Errorf("the message follows Accept-Language: %v", vb.Error.Fields)
	}
	exact := domain.AddPeriod(start, 3, domain.UnitMonth)
	expectStatus(t, e.call(http.MethodPost, "/v1/contracts/register", end(dateStr(exact)), "tenant-t"), http.StatusCreated)

	// One waiting registration is all the plan allows.
	second := e.room(e.landlA, "P102")
	w = e.call(http.MethodPost, "/v1/contracts/register", map[string]any{"room_id": second, "starts_on": dateStr(start)}, "tenant-t")
	expectError(t, w, http.StatusConflict, apperr.CodeRegistrationLimit)
	if p := decodeError(t, w); p.Error.Message == "" {
		t.Error("the limit message is translated")
	}
}

func TestListScopingPaginationAndFilters(t *testing.T) {
	e := newContractEnv(t, nil)
	rooms := []uuid.UUID{e.room(e.landlA, "P101"), e.room(e.landlA, "P102"), e.room(e.landlA, "P103")}
	foreign := e.room(e.landlB, "Z1")
	a := e.register("tenant-t", rooms[0], nil)
	b := e.register("tenant-u", rooms[1], nil)
	c := e.register("tenant-t", rooms[2], nil)
	other := e.register("tenant-t", foreign, nil)
	e.approve("landlord-a", b.ID)

	all := e.list("landlord-a", "")
	if all.Meta.Total != 3 || all.Meta.Page != 1 || all.Meta.PerPage != 20 || slices.Contains(idsOf(all.Data), other.ID) {
		t.Errorf("landlord list = %+v", all.Meta)
	}
	// The newest registration comes first, then the landlord's order.
	if got := idsOf(all.Data); !slices.Equal(got, []uuid.UUID{c.ID, b.ID, a.ID}) {
		t.Errorf("order = %v", got)
	}
	page2 := e.list("landlord-a", "?per_page=2&page=2")
	if len(page2.Data) != 1 || page2.Meta.Total != 3 || page2.Meta.PerPage != 2 || page2.Meta.Page != 2 {
		t.Errorf("page 2 = %+v", page2)
	}
	if got := idsOf(e.list("landlord-a", "?status=active").Data); !slices.Equal(got, []uuid.UUID{b.ID}) {
		t.Errorf("status = %v", got)
	}
	if got := idsOf(e.list("landlord-a", "?search=tenant%20u").Data); !slices.Equal(got, []uuid.UUID{b.ID}) {
		t.Errorf("search = %v", got)
	}
	if got := idsOf(e.list("landlord-a", "?room_id="+rooms[0].String()).Data); !slices.Equal(got, []uuid.UUID{a.ID}) {
		t.Errorf("room = %v", got)
	}
	if got := idsOf(e.list("landlord-a", "?sort=created_at").Data); !slices.Equal(got, []uuid.UUID{a.ID, b.ID, c.ID}) {
		t.Errorf("sort = %v", got)
	}
	// A tenant sees their own contracts, across landlords, and cannot use the landlord's filters on others.
	mine := e.list("tenant-t", "")
	if mine.Meta.Total != 3 || !slices.Contains(idsOf(mine.Data), other.ID) || slices.Contains(idsOf(mine.Data), b.ID) {
		t.Errorf("tenant list = %v", idsOf(mine.Data))
	}
	if mine.Data[0].Abilities.Update || mine.Data[0].Abilities.Review {
		t.Errorf("a tenant cannot edit: %+v", mine.Data[0].Abilities)
	}
	// Bad filters and pages.
	for _, q := range []string{"?status=nope", "?cancellation_status=x", "?room_id=zzz", "?no_building=maybe", "?sort=price", "?page=0", "?per_page=101", "?per_page=abc"} {
		expectError(t, e.call(http.MethodGet, "/v1/contracts"+q, nil, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	}
	expectError(t, e.call(http.MethodGet, "/v1/contracts", nil, "admin"), http.StatusForbidden, apperr.CodeForbidden)
	expectError(t, e.call(http.MethodGet, "/v1/contracts", nil, "tenant-nothing"), http.StatusForbidden, apperr.CodeForbidden)
	expectError(t, e.call(http.MethodGet, "/v1/contracts", nil, ""), http.StatusUnauthorized, apperr.CodeUnauthenticated)
	// The same account sees the other side when it acts in the other role.
	if got := e.list("landlord-a-as-tenant", ""); got.Meta.Total != 0 {
		t.Errorf("landlord A as a tenant holds no contract: %d", got.Meta.Total)
	}
}

func TestAnotherLandlordsContractIsNotFound(t *testing.T) {
	e := newContractEnv(t, nil)
	c := e.register("tenant-t", e.room(e.landlA, "P101"), nil)
	id := c.ID.String()
	for name, w := range map[string]*testResponse{
		"get":        {e.call(http.MethodGet, "/v1/contracts/"+id, nil, "landlord-b")},
		"put":        e.put("landlord-b", c.ID, map[string]any{"notes": "x"}),
		"patch":      {e.call(http.MethodPatch, "/v1/contracts/"+id, map[string]any{"notes": "x"}, "landlord-b")},
		"delete":     {e.call(http.MethodDelete, "/v1/contracts/"+id, nil, "landlord-b")},
		"terminate":  {e.call(http.MethodPost, "/v1/contracts/"+id+"/terminate", nil, "landlord-b")},
		"approve":    {e.call(http.MethodPost, "/v1/contracts/"+id+"/cancellation-request/approve", nil, "landlord-b")},
		"decline":    {e.call(http.MethodPost, "/v1/contracts/"+id+"/cancellation-request/decline", nil, "landlord-b")},
		"tenant u":   {e.call(http.MethodGet, "/v1/contracts/"+id, nil, "tenant-u")},
		"tenant del": {e.call(http.MethodDelete, "/v1/contracts/"+id, nil, "tenant-u")},
		"tenant ask": {e.call(http.MethodPost, "/v1/contracts/"+id+"/cancellation-request", nil, "tenant-u")},
		"bad id":     {e.call(http.MethodGet, "/v1/contracts/not-a-uuid", nil, "landlord-a")},
	} {
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// Tenants cannot use the landlord's routes at all.
	for name, w := range map[string]*testResponse{
		"put":       e.put("tenant-t", c.ID, map[string]any{"notes": "x"}),
		"terminate": {e.call(http.MethodPost, "/v1/contracts/"+id+"/terminate", nil, "tenant-t")},
		"bulk":      {e.call(http.MethodPost, "/v1/contracts/bulk", map[string]any{"action": "approve", "ids": []string{id}}, "tenant-t")},
		"order":     {e.call(http.MethodPut, "/v1/contracts/order", map[string]any{"ids": []string{id}}, "tenant-t")},
		"approve":   {e.call(http.MethodPost, "/v1/contracts/"+id+"/cancellation-request/approve", nil, "tenant-t")},
	} {
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// And landlords cannot use the tenant's.
	expectError(t, e.call(http.MethodPost, "/v1/contracts/"+id+"/cancellation-request", nil, "landlord-a"), http.StatusForbidden, apperr.CodeForbidden)
	expectError(t, e.call(http.MethodGet, "/v1/me/contracts", nil, "landlord-a"), http.StatusForbidden, apperr.CodeForbidden)
}

func TestApprovingARegistrationRentsTheRoomAndRejectsTheRivals(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.room(e.landlA, "P101")
	winner := e.register("tenant-t", room, nil)
	rival := e.register("tenant-u", room, nil)

	d := e.approve("landlord-a", winner.ID)
	if d.Status != "active" || e.roomStatus(room) != "rented" {
		t.Fatalf("approved = %+v, room %s", d, e.roomStatus(room))
	}
	w := e.call(http.MethodGet, "/v1/contracts/"+rival.ID.String(), nil, "landlord-a")
	if got := decodeData[ContractView](t, w); got.Status != "rejected" {
		t.Errorf("rival = %s", got.Status)
	}
	if kinds := noticeKinds(e); !slices.Contains(kinds, usecase.NotifyRegistrationApproved) {
		t.Errorf("the tenant hears the answer: %v", kinds)
	}
	// A rejected registration can be reopened only when the room is free: here it is not.
	expectError(t, e.put("landlord-a", rival.ID, map[string]any{"status": "active"}).ResponseRecorder, http.StatusConflict, apperr.CodeContractRoomOccupied)
	// A status the life cycle does not allow.
	expectError(t, e.put("landlord-a", winner.ID, map[string]any{"status": "pending"}).ResponseRecorder, http.StatusConflict, apperr.CodeContractTransition)
	// Ending frees the room again, with an end date of today.
	ended := e.put("landlord-a", winner.ID, map[string]any{"status": "ended"})
	expectStatus(t, ended.ResponseRecorder, http.StatusOK)
	if got := decodeData[ContractView](t, ended.ResponseRecorder); got.Status != "ended" || got.EndsOn == nil || *got.EndsOn != dateStr(today()) || e.roomStatus(room) != "available" {
		t.Errorf("ended = %+v, room %s", got, e.roomStatus(room))
	}
	// Reopening works while the room is free; only one contract runs in a room.
	if got := e.approve("landlord-a", rival.ID); got.Status != "active" {
		t.Errorf("reopened = %+v", got)
	}
	expectError(t, e.put("landlord-a", winner.ID, map[string]any{"status": "active"}).ResponseRecorder, http.StatusConflict, apperr.CodeContractRoomOccupied)
}

func noticeKinds(e *contractEnv) []string {
	var out []string
	for _, n := range e.notices.sent {
		out = append(out, n.Kind)
	}
	return out
}

func TestPartialUpdateKeepsWhatIsAbsentAndClearsNull(t *testing.T) {
	e := newContractEnv(t, nil)
	c := e.register("tenant-t", e.room(e.landlA, "P101"), map[string]any{"notes": "first"})
	end := dateStr(today().AddDate(1, 0, 0))

	w := e.put("landlord-a", c.ID, map[string]any{"deposit_vnd": 1_000_000, "ends_on": end, "occupant_count": 3, "price_vnd": 3_500_000})
	expectStatus(t, w.ResponseRecorder, http.StatusOK)
	got := decodeData[ContractView](t, w.ResponseRecorder)
	if *got.DepositVND != 1_000_000 || *got.EndsOn != end || got.OccupantCount != 3 || got.PriceVND != 3_500_000 || *got.Notes != "first" {
		t.Fatalf("after the first update: %+v", got)
	}
	// PATCH is the same partial update; null clears, absent keeps.
	w = &testResponse{e.call(http.MethodPatch, "/v1/contracts/"+c.ID.String(), `{"ends_on": null, "notes": null}`, "landlord-a")}
	expectStatus(t, w.ResponseRecorder, http.StatusOK)
	got = decodeData[ContractView](t, w.ResponseRecorder)
	if got.EndsOn != nil || got.Notes != nil || *got.DepositVND != 1_000_000 || got.OccupantCount != 3 {
		t.Errorf("after null: %+v", got)
	}
	// The name and phone of the tenant are for blank contracts only: an account's own are never overwritten.
	w = e.put("landlord-a", c.ID, map[string]any{"tenant_name": "Somebody"})
	if got = decodeData[ContractView](t, w.ResponseRecorder); got.TenantName != nil || got.Tenant.FullName != "Tenant T" {
		t.Errorf("tenant name = %+v", got)
	}

	for name, body := range map[string]any{
		"status":         map[string]any{"status": "done"},
		"price_vnd":      map[string]any{"price_vnd": -1},
		"price_vnd max":  map[string]any{"price_vnd": 2_000_000_000_000},
		"deposit_vnd":    map[string]any{"deposit_vnd": -5},
		"occupant_count": map[string]any{"occupant_count": 51},
		"starts_on":      map[string]any{"starts_on": "soon"},
		"ends_on":        map[string]any{"ends_on": "2026-13-45"},
		"notes":          map[string]any{"notes": string(make([]byte, 1001))},
		"tenant_name":    map[string]any{"tenant_name": string(make([]byte, 101))},
	} {
		w := e.put("landlord-a", c.ID, body)
		eb := expectError(t, w.ResponseRecorder, http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
		field := name
		if i := indexByte(name, ' '); i > 0 {
			field = name[:i]
		}
		if len(eb.Error.Fields[field]) != 1 {
			t.Errorf("%s: fields = %v", name, eb.Error.Fields)
		}
	}
	// Domain rules: the end must follow the start, the room's minimum period holds.
	same := dateStr(today())
	if f := e.put("landlord-a", c.ID, map[string]any{"ends_on": same}).fields(); len(f["ends_on"]) != 1 {
		t.Errorf("end must follow start: %v", f)
	}
	expectError(t, e.call(http.MethodPut, "/v1/contracts/"+c.ID.String(), `{"status": 3}`, "landlord-a"), http.StatusBadRequest, apperr.CodeRequestMalformed)
}

func indexByte(s string, b byte) int {
	for i := range s {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func TestBulkAnswerReportsEachContract(t *testing.T) {
	e := newContractEnv(t, nil)
	p1, p2, p3 := e.room(e.landlA, "P101"), e.room(e.landlA, "P102"), e.room(e.landlA, "P103")
	a := e.register("tenant-t", p1, nil)
	b := e.register("tenant-u", p2, nil)
	e.approve("landlord-a", b.ID) // no longer waiting
	c := e.register("tenant-t", p3, nil)
	foreign := e.register("tenant-t", e.room(e.landlB, "Z1"), nil)

	w := e.call(http.MethodPost, "/v1/contracts/bulk", map[string]any{"action": "approve", "ids": []uuid.UUID{a.ID, b.ID, c.ID}}, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	res := decodeData[BulkView](t, w)
	if !slices.Equal(res.Done, []uuid.UUID{a.ID, c.ID}) || len(res.Failed) != 1 || res.Failed[0].ID != b.ID || res.Failed[0].Name != "P102" ||
		res.Failed[0].Code != apperr.CodeContractTransition || res.Failed[0].Message == "" {
		t.Fatalf("result = %+v", res)
	}
	if e.roomStatus(p1) != "rented" || e.roomStatus(p3) != "rented" {
		t.Error("approved rooms are rented")
	}
	// Reject a waiting one.
	d := e.register("tenant-u", e.room(e.landlA, "P104"), nil)
	w = e.call(http.MethodPost, "/v1/contracts/bulk", map[string]any{"action": "reject", "ids": []uuid.UUID{d.ID}}, "landlord-a")
	if res = decodeData[BulkView](t, w); len(res.Done) != 1 || len(res.Failed) != 0 {
		t.Errorf("reject = %+v", res)
	}
	// A refusal that is in the language of the request.
	w = e.callIn("vi", http.MethodPost, "/v1/contracts/bulk", map[string]any{"action": "approve", "ids": []uuid.UUID{a.ID}}, "landlord-a")
	if res = decodeData[BulkView](t, w); len(res.Failed) != 1 || res.Failed[0].Message == "" {
		t.Errorf("vi = %+v", res)
	}
	// Somebody else's contract refuses the whole request; so do bad bodies.
	w = e.call(http.MethodPost, "/v1/contracts/bulk", map[string]any{"action": "approve", "ids": []uuid.UUID{foreign.ID}}, "landlord-a")
	if f := (&testResponse{w}).fields(); len(f["ids"]) != 1 {
		t.Errorf("ids = %v", f)
	}
	for _, body := range []any{
		map[string]any{"action": "delete", "ids": []uuid.UUID{a.ID}},
		map[string]any{"action": "approve", "ids": []uuid.UUID{}},
		map[string]any{"action": "approve", "ids": []uuid.UUID{a.ID, a.ID}},
		map[string]any{"ids": []uuid.UUID{a.ID}},
	} {
		expectError(t, e.call(http.MethodPost, "/v1/contracts/bulk", body, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	}
}

func TestTerminate(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.room(e.landlA, "P101")
	c := e.register("tenant-t", room, nil)
	e.approve("landlord-a", c.ID)
	w := e.call(http.MethodPost, "/v1/contracts/"+c.ID.String()+"/terminate", map[string]any{"reason": "Broken rules"}, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	got := decodeData[ContractView](t, w)
	if got.Status != "ended" || *got.EndsOn != dateStr(today()) || e.roomStatus(room) != "available" {
		t.Errorf("terminated = %+v", got)
	}
	last := e.notices.sent[len(e.notices.sent)-2:]
	if last[0].Kind != usecase.NotifyContractEnded || last[0].Params["reason"] != "Broken rules" || last[0].UserID != e.tenantT || last[1].UserID != e.landlA {
		t.Errorf("both sides are told: %+v", last)
	}
	// An ended contract cannot be called off again.
	expectError(t, e.call(http.MethodPost, "/v1/contracts/"+c.ID.String()+"/terminate", nil, "landlord-a"), http.StatusConflict, apperr.CodeContractTransition)
	// A waiting registration is rejected.
	waiting := e.register("tenant-u", room, nil)
	w = e.call(http.MethodPost, "/v1/contracts/"+waiting.ID.String()+"/terminate", nil, "landlord-a")
	if got = decodeData[ContractView](t, w); got.Status != "rejected" {
		t.Errorf("waiting = %+v", got)
	}
	expectError(t, e.call(http.MethodPost, "/v1/contracts/"+waiting.ID.String()+"/terminate", map[string]any{"reason": string(make([]byte, 501))}, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
}

func TestCancellationRequestFlow(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.room(e.landlA, "P101")
	c := e.register("tenant-t", room, nil)
	e.approve("landlord-a", c.ID)
	path := "/v1/contracts/" + c.ID.String() + "/cancellation-request"

	// Nothing to answer or withdraw yet.
	expectError(t, e.call(http.MethodPost, path+"/approve", nil, "landlord-a"), http.StatusConflict, apperr.CodeContractCancelDenied)
	expectError(t, e.call(http.MethodPost, path+"/decline", nil, "landlord-a"), http.StatusConflict, apperr.CodeContractCancelDenied)
	expectError(t, e.call(http.MethodDelete, path, nil, "tenant-t"), http.StatusConflict, apperr.CodeContractCancelDenied)

	w := e.call(http.MethodPost, path, map[string]any{"reason": "Moving"}, "tenant-t")
	expectStatus(t, w, http.StatusOK)
	got := decodeData[ContractView](t, w)
	if got.Status != "active" || *got.Cancellation.Status != "requested" || *got.Cancellation.Reason != "Moving" || got.Cancellation.RequestedAt == nil {
		t.Fatalf("requested = %+v", got)
	}
	expectError(t, e.call(http.MethodPost, path, nil, "tenant-t"), http.StatusConflict, apperr.CodeContractCancelDenied)
	filtered := e.list("landlord-a", "?cancellation_status=requested")
	if got := idsOf(filtered.Data); !slices.Equal(got, []uuid.UUID{c.ID}) {
		t.Errorf("filter = %v", got)
	}
	if k := noticeKinds(e); k[len(k)-1] != usecase.NotifyCancellationRequested {
		t.Errorf("the landlord is told: %v", k)
	}

	// Decline: the contract keeps running and the tenant sees why; a new request is allowed.
	w = e.call(http.MethodPost, path+"/decline", map[string]any{"note": "Deposit is not settled"}, "landlord-a")
	got = decodeData[ContractView](t, w)
	if got.Status != "active" || *got.Cancellation.Status != "declined" || *got.Cancellation.ResponseNote != "Deposit is not settled" || e.roomStatus(room) != "rented" {
		t.Errorf("declined = %+v", got)
	}
	w = e.call(http.MethodPost, path, map[string]any{"reason": "Really"}, "tenant-t")
	if got = decodeData[ContractView](t, w); got.Cancellation.ResponseNote != nil {
		t.Errorf("a new request clears the answer: %+v", got)
	}
	// Withdraw.
	w = e.call(http.MethodDelete, path, nil, "tenant-t")
	if got = decodeData[ContractView](t, w); got.Cancellation.Status != nil {
		t.Errorf("withdrawn = %+v", got)
	}
	// Request again and approve: the contract ends and the room is free.
	e.call(http.MethodPost, path, nil, "tenant-t")
	w = e.call(http.MethodPost, path+"/approve", nil, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	got = decodeData[ContractView](t, w)
	if got.Status != "ended" || *got.Cancellation.Status != "approved" || *got.EndsOn != dateStr(today()) || e.roomStatus(room) != "available" {
		t.Errorf("approved = %+v", got)
	}
	// An ended contract cannot be cancelled again.
	expectError(t, e.call(http.MethodPost, path, nil, "tenant-t"), http.StatusConflict, apperr.CodeContractCancelDenied)
	expectError(t, e.call(http.MethodPost, path, map[string]any{"reason": string(make([]byte, 1001))}, "tenant-t"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
}

func TestDelete(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.room(e.landlA, "P101")
	pending := e.register("tenant-t", room, nil)
	running := e.register("tenant-u", e.room(e.landlA, "P102"), nil)
	e.approve("landlord-a", running.ID)

	// A tenant only withdraws a registration that waits.
	expectError(t, e.call(http.MethodDelete, "/v1/contracts/"+running.ID.String(), nil, "tenant-u"), http.StatusConflict, apperr.CodeContractNotDeletable)
	expectStatus(t, e.call(http.MethodDelete, "/v1/contracts/"+pending.ID.String(), nil, "tenant-t"), http.StatusNoContent)
	expectError(t, e.call(http.MethodGet, "/v1/contracts/"+pending.ID.String(), nil, "tenant-t"), http.StatusNotFound, apperr.CodeResourceNotFound)
	// The landlord removes a running one: the room is free again, and the contract is gone from every list.
	expectStatus(t, e.call(http.MethodDelete, "/v1/contracts/"+running.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	if e.list("landlord-a", "").Meta.Total != 0 || e.list("tenant-u", "").Meta.Total != 0 {
		t.Error("deleted contracts are gone")
	}
	var status string
	_ = e.pool.QueryRow(t.Context(), `SELECT status FROM rooms WHERE name = 'P102'`).Scan(&status)
	if status != "available" {
		t.Errorf("room = %s", status)
	}
	expectError(t, e.call(http.MethodDelete, "/v1/contracts/"+running.ID.String(), nil, "landlord-a"), http.StatusNotFound, apperr.CodeResourceNotFound)
}

func TestReorder(t *testing.T) {
	e := newContractEnv(t, nil)
	var ids []uuid.UUID
	for _, name := range []string{"P101", "P102", "P103"} {
		ids = append(ids, e.register("tenant-t", e.room(e.landlA, name), nil).ID)
	}
	// New contracts go on top: the list is P103, P102, P101.
	if got := idsOf(e.list("landlord-a", "").Data); !slices.Equal(got, []uuid.UUID{ids[2], ids[1], ids[0]}) {
		t.Fatalf("initial order = %v", got)
	}
	expectStatus(t, e.call(http.MethodPut, "/v1/contracts/order", map[string]any{"ids": []uuid.UUID{ids[0], ids[2]}}, "landlord-a"), http.StatusNoContent)
	if got := idsOf(e.list("landlord-a", "").Data); !slices.Equal(got, []uuid.UUID{ids[0], ids[1], ids[2]}) {
		t.Errorf("order = %v", got)
	}
	foreign := e.register("tenant-t", e.room(e.landlB, "Z1"), nil)
	for _, body := range []any{
		map[string]any{"ids": []uuid.UUID{foreign.ID}},
		map[string]any{"ids": []uuid.UUID{uuid.New()}},
	} {
		w := &testResponse{e.call(http.MethodPut, "/v1/contracts/order", body, "landlord-a")}
		if f := w.fields(); w.Code != http.StatusUnprocessableEntity || len(f["ids"]) != 1 {
			t.Errorf("%d %s", w.Code, w.Body.String())
		}
	}
	for _, body := range []any{map[string]any{"ids": []uuid.UUID{}}, map[string]any{"ids": []uuid.UUID{ids[0], ids[0]}}, map[string]any{}} {
		expectError(t, e.call(http.MethodPut, "/v1/contracts/order", body, "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	}
	// The other landlord's order was never touched.
	if got := e.list("landlord-b", ""); len(got.Data) != 1 || got.Data[0].DisplayOrder != foreign.DisplayOrder {
		t.Errorf("landlord B = %+v", got.Data)
	}
}

func TestContractLimitOfThePlan(t *testing.T) {
	one := 1
	e := newContractEnv(t, fixedPlans{contracts: &one})
	a := e.register("tenant-t", e.room(e.landlA, "P101"), nil)
	b := e.register("tenant-u", e.room(e.landlA, "P102"), nil)
	e.approve("landlord-a", a.ID)
	w := e.put("landlord-a", b.ID, map[string]any{"status": "active"})
	expectError(t, w.ResponseRecorder, http.StatusConflict, apperr.CodeContractLimit)
	if decodeError(t, w.ResponseRecorder).Error.Message == "" {
		t.Error("translated")
	}
}

func TestMyContractsAndTheirDetail(t *testing.T) {
	e := newContractEnv(t, nil)
	p1, p2, p3 := e.room(e.landlA, "P101"), e.room(e.landlA, "P102"), e.room(e.landlB, "Z1")
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO charges (room_id, name, kind, amount_vnd, unit, display_order) VALUES ($1, 'Dien', 'electricity', 3500, 'per_kwh', 1)`, p1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO media (room_id, file_key, thumb_key, content_type, size_bytes, width, height, display_order) VALUES ($1, 'r/1.webp', 'r/1_t.webp', 'image/webp', 10, 100, 80, 1)`, p1); err != nil {
		t.Fatal(err)
	}
	active := e.register("tenant-t", p1, nil)
	e.approve("landlord-a", active.ID)
	waiting := e.register("tenant-t", p2, nil)
	ended := e.register("tenant-t", p3, nil)
	e.approve("landlord-b", ended.ID)
	e.call(http.MethodPost, "/v1/contracts/"+ended.ID.String()+"/terminate", nil, "landlord-b")
	e.register("tenant-u", e.room(e.landlA, "P104"), nil)

	type listBody struct {
		Data []RentalView `json:"data"`
		Meta struct {
			Total   int            `json:"total"`
			PerPage int            `json:"per_page"`
			Counts  map[string]int `json:"counts"`
		} `json:"meta"`
	}
	get := func(q string) listBody {
		t.Helper()
		w := e.call(http.MethodGet, "/v1/me/contracts"+q, nil, "tenant-t")
		expectStatus(t, w, http.StatusOK)
		var b listBody
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	all := get("")
	if all.Meta.Total != 3 || all.Meta.Counts["active"] != 1 || all.Meta.Counts["pending"] != 1 || all.Meta.Counts["history"] != 1 {
		t.Errorf("all = %+v", all.Meta)
	}
	if got := get("?group=pending"); len(got.Data) != 1 || got.Data[0].ID != waiting.ID || got.Meta.Counts["active"] != 1 {
		t.Errorf("pending = %+v", got)
	}
	if got := get("?group=history"); len(got.Data) != 1 || got.Data[0].ID != ended.ID || got.Data[0].Status != "ended" {
		t.Errorf("history = %+v", got)
	}
	if got := get("?per_page=2"); len(got.Data) != 2 || got.Meta.Total != 3 || got.Meta.PerPage != 2 {
		t.Errorf("paged = %+v", got.Meta)
	}
	expectError(t, e.call(http.MethodGet, "/v1/me/contracts?group=old", nil, "tenant-t"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	expectError(t, e.call(http.MethodGet, "/v1/me/contracts?page=0", nil, "tenant-t"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)

	w := e.call(http.MethodGet, "/v1/me/contracts/"+active.ID.String(), nil, "tenant-t")
	expectStatus(t, w, http.StatusOK)
	d := decodeData[RentalDetail](t, w)
	if d.Room.Name != "P101" || d.Room.Ward == nil || d.Room.Ward.Province.Name != "Ha Noi" || d.Landlord.FullName != "Landlord A" ||
		len(d.Room.Charges) != 1 || d.Room.Charges[0].AmountVND != 3500 || len(d.Room.Pictures) != 1 || d.Room.Pictures[0].URL != "http://files.test/r/1.webp" {
		t.Errorf("detail = %+v", d)
	}
	// Somebody else's contract, a landlord's contract and a bad id do not exist for the tenant.
	for _, id := range []string{e.register("tenant-u", e.room(e.landlA, "P105"), nil).ID.String(), "nope"} {
		expectError(t, e.call(http.MethodGet, "/v1/me/contracts/"+id, nil, "tenant-t"), http.StatusNotFound, apperr.CodeResourceNotFound)
	}
	expectError(t, e.call(http.MethodGet, "/v1/me/contracts/"+active.ID.String(), nil, "tenant-u"), http.StatusNotFound, apperr.CodeResourceNotFound)
	expectError(t, e.call(http.MethodGet, "/v1/me/contracts", nil, "tenant-nothing"), http.StatusForbidden, apperr.CodeForbidden)
}

func TestRegisterNeedsAnApprovedListing(t *testing.T) {
	e := newContractEnv(t, nil)
	room := e.unlistedRoom(e.landlA, "P101")
	register := func() *httptest.ResponseRecorder {
		return e.call(http.MethodPost, "/v1/contracts/register", map[string]any{"room_id": room, "starts_on": dateStr(today())}, "tenant-t")
	}

	// No listing at all, and a listing that only waits for review, are both refused with the same code.
	body := expectError(t, register(), http.StatusConflict, apperr.CodeContractNotListed)
	if body.Error.Message == "" {
		t.Error("the message is translated")
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO listings (room_id, landlord_id, title, status, submitted_at) VALUES ($1, $2, 'T', 'pending', now())`, room, e.landlA); err != nil {
		t.Fatal(err)
	}
	expectError(t, register(), http.StatusConflict, apperr.CodeContractNotListed)

	// Approved: it goes through.
	if _, err := e.pool.Exec(t.Context(), `UPDATE listings SET status = 'approved', published_at = now() WHERE room_id = $1`, room); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, register(), http.StatusCreated)
}
