//go:build integration

package listinghttp_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
)

func TestAnAdminApprovesAListingAndTheLandlordIsTold(t *testing.T) {
	e := newListingEnv(t)
	room := e.room(e.landlA, "P101", 1)
	id := e.listed(room, e.landlA, "Bright room", "pending")
	path := "/v1/admin/listings/" + id.String()

	w := e.call(http.MethodPost, path+"/approve", nil, "admin")
	expectStatus(t, w, http.StatusOK)
	d := decodeData[ListingDetailView](t, w)
	if d.Status != "approved" || d.PublishedAt == nil || d.ReviewedAt == nil || d.Reviewer == nil || d.Reviewer.FullName != "Admin" ||
		d.Landlord == nil || d.Landlord.Email == nil || *d.Landlord.Email != secretEmail {
		t.Fatalf("approved = %+v", d)
	}
	if kinds := e.notices.kinds(); len(kinds) != 1 || kinds[0] != "listing_approved" || e.notices.sent[0].UserID != e.landlA || e.notices.sent[0].URL != "/my-listings" {
		t.Errorf("notices = %v", e.notices.sent)
	}
	// Once approved, the room is on find rooms.
	p := listOf[OfferView](t, e.call(http.MethodGet, "/v1/vacant-rooms", nil, "tenant-t"))
	if len(p.Data) != 1 || p.Data[0].Listing.Title != "Bright room" {
		t.Errorf("find rooms = %+v", p.Data)
	}
	// An approved listing cannot be approved again.
	expectError(t, e.call(http.MethodPost, path+"/approve", nil, "admin"), http.StatusConflict, apperr.CodeListingTransition)
	expectError(t, e.call(http.MethodPost, "/v1/admin/listings/"+uuid.NewString()+"/approve", nil, "admin"), http.StatusNotFound, apperr.CodeResourceNotFound)
}

func TestAnAdminNeedsAReasonToRejectOrTakeDown(t *testing.T) {
	e := newListingEnv(t)
	pending := e.listed(e.room(e.landlA, "P101", 1), e.landlA, "T", "pending")
	live := e.listed(e.room(e.landlA, "P102", 1), e.landlA, "T", "approved")

	for _, c := range []struct {
		id     uuid.UUID
		action string
	}{{pending, "reject"}, {live, "unpublish"}} {
		path := "/v1/admin/listings/" + c.id.String() + "/" + c.action
		body := expectError(t, e.call(http.MethodPost, path, nil, "admin"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
		if len(body.Error.Fields["note"]) != 1 {
			t.Errorf("%s: fields = %v", c.action, body.Error.Fields)
		}
		expectError(t, e.call(http.MethodPost, path, map[string]any{"note": "   "}, "admin"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
		expectError(t, e.call(http.MethodPost, path, map[string]any{"note": strings.Repeat("a", 1001)}, "admin"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	}
	w := e.call(http.MethodPost, "/v1/admin/listings/"+pending.String()+"/reject", map[string]any{"note": "Thiếu ảnh"}, "admin")
	expectStatus(t, w, http.StatusOK)
	if d := decodeData[ListingDetailView](t, w); d.Status != "rejected" || *d.ReviewNote != "Thiếu ảnh" {
		t.Errorf("rejected = %+v", d)
	}
	w = e.call(http.MethodPost, "/v1/admin/listings/"+live.String()+"/unpublish", map[string]any{"note": "Ảnh không đúng thực tế"}, "admin")
	expectStatus(t, w, http.StatusOK)
	if d := decodeData[ListingDetailView](t, w); d.Status != "unpublished" || *d.ReviewNote != "Ảnh không đúng thực tế" || d.UnpublishedAt == nil {
		t.Errorf("taken down = %+v", d)
	}
	kinds := e.notices.kinds()
	if len(kinds) != 2 || kinds[0] != "listing_rejected" || kinds[1] != "listing_taken_down" || e.notices.sent[1].Params["reason"] != "Ảnh không đúng thực tế" {
		t.Errorf("notices = %v", e.notices.sent)
	}
	// Only a live listing is taken down; a refused listing is not live.
	expectError(t, e.call(http.MethodPost, "/v1/admin/listings/"+pending.String()+"/unpublish", map[string]any{"note": "x"}, "admin"), http.StatusConflict, apperr.CodeListingTransition)
	// The landlord fixes it and sends it again.
	path := "/v1/my/listings/" + live.String()
	expectStatus(t, e.call(http.MethodPost, path+"/submit", nil, "landlord-a"), http.StatusOK)
}

func TestTheReviewListNeverShowsADraft(t *testing.T) {
	e := newListingEnv(t)
	draft := e.listed(e.room(e.landlA, "P101", 1), e.landlA, "Draft", "draft")
	old := e.listed(e.room(e.landlA, "P102", 1), e.landlA, "Old", "pending")
	young := e.listed(e.room(e.landlB, "X1", 1), e.landlB, "Young", "pending")
	e.listed(e.room(e.landlB, "X2", 1), e.landlB, "Live", "approved")

	// The waiting ones, oldest sent first, with the counts (drafts are not counted).
	p := listOf[ListingView](t, e.call(http.MethodGet, "/v1/admin/listings", nil, "admin"))
	if len(p.Data) != 2 || p.Data[0].ID != old || p.Data[1].ID != young || p.Meta.Total != 2 {
		t.Fatalf("queue = %+v", p)
	}
	if p.Meta.Counts["pending"] != 2 || p.Meta.Counts["approved"] != 1 || p.Meta.Counts["draft"] != 0 || len(p.Meta.Counts) != 2 {
		t.Errorf("counts = %v", p.Meta.Counts)
	}
	if p.Data[0].Landlord == nil || p.Data[0].Landlord.FullName != "Chu Hoa" || p.Data[1].Landlord.FullName != "Chu Lan" {
		t.Errorf("the admins see who wrote it: %+v", p.Data[0].Landlord)
	}
	// Asking for drafts shows the waiting ones, never a draft.
	p = listOf[ListingView](t, e.call(http.MethodGet, "/v1/admin/listings?status=draft", nil, "admin"))
	for _, l := range p.Data {
		if l.Status == "draft" || l.ID == draft {
			t.Errorf("a draft was listed: %+v", l)
		}
	}
	if len(p.Data) != 2 {
		t.Errorf("status=draft lists the waiting ones: %d", len(p.Data))
	}
	expectError(t, e.call(http.MethodGet, "/v1/admin/listings/"+draft.String(), nil, "admin"), http.StatusNotFound, apperr.CodeResourceNotFound)
	if d := decodeData[ListingDetailView](t, e.call(http.MethodGet, "/v1/admin/listings/"+old.String(), nil, "super-admin")); d.Room.Charges == nil || d.Room.Pictures == nil {
		t.Errorf("the detail carries the room's costs and pictures: %+v", d.Room)
	}
	p = listOf[ListingView](t, e.call(http.MethodGet, "/v1/admin/listings?status=approved&per_page=1&page=1", nil, "admin"))
	if len(p.Data) != 1 || p.Meta.Total != 1 || p.Meta.PerPage != 1 {
		t.Errorf("a status with a page size: %+v", p.Meta)
	}
}

func TestOnlyAdminsReachTheReviewRoutes(t *testing.T) {
	e := newListingEnv(t)
	id := e.listed(e.room(e.landlA, "P101", 1), e.landlA, "T", "pending")
	for _, bearer := range []string{"landlord-a", "tenant-t", "landlord-a-as-tenant", "tenant-nothing"} {
		expectError(t, e.call(http.MethodGet, "/v1/admin/listings", nil, bearer), http.StatusForbidden, apperr.CodeForbidden)
		expectError(t, e.call(http.MethodGet, "/v1/admin/listings/"+id.String(), nil, bearer), http.StatusForbidden, apperr.CodeForbidden)
		expectError(t, e.call(http.MethodPost, "/v1/admin/listings/"+id.String()+"/approve", nil, bearer), http.StatusForbidden, apperr.CodeForbidden)
		expectError(t, e.call(http.MethodPost, "/v1/admin/listings/bulk", map[string]any{"action": "approve", "ids": []uuid.UUID{id}}, bearer), http.StatusForbidden, apperr.CodeForbidden)
	}
	expectError(t, e.call(http.MethodGet, "/v1/admin/listings", nil, ""), http.StatusUnauthorized, apperr.CodeUnauthenticated)
	if e.status(id) != "pending" {
		t.Error("nothing happened")
	}
	// A super admin holds the permission too.
	expectStatus(t, e.call(http.MethodPost, "/v1/admin/listings/"+id.String()+"/approve", nil, "super-admin"), http.StatusOK)
}

func TestBulkReviewReportsWhatItCouldNotDo(t *testing.T) {
	e := newListingEnv(t)
	a := e.listed(e.room(e.landlA, "P101", 1), e.landlA, "A", "pending")
	b := e.listed(e.room(e.landlA, "P102", 1), e.landlA, "B", "pending")
	draft := e.listed(e.room(e.landlA, "P103", 1), e.landlA, "A draft", "draft")

	w := e.call(http.MethodPost, "/v1/admin/listings/bulk", map[string]any{"action": "approve", "ids": []uuid.UUID{a, b, draft}}, "admin")
	expectStatus(t, w, http.StatusOK)
	r := decodeData[BulkView](t, w)
	if len(r.Done) != 2 || len(r.Failed) != 1 || r.Failed[0].ID != draft || r.Failed[0].Name != "A draft" || r.Failed[0].Code != apperr.CodeListingTransition || r.Failed[0].Message == "" {
		t.Fatalf("bulk = %+v", r)
	}
	if e.status(a) != "approved" || e.status(b) != "approved" || e.status(draft) != "draft" {
		t.Error("the ones that could be approved were")
	}
	// The refusal is worded in the language of the request.
	vi := e.callIn("vi", http.MethodPost, "/v1/admin/listings/bulk", map[string]any{"action": "approve", "ids": []uuid.UUID{draft}}, "admin")
	if got := decodeData[BulkView](t, vi); got.Failed[0].Message == r.Failed[0].Message {
		t.Errorf("the message follows Accept-Language: %q", got.Failed[0].Message)
	}

	// A rejection carries one reason for all.
	c := e.listed(e.room(e.landlA, "P104", 1), e.landlA, "C", "pending")
	body := expectError(t, e.call(http.MethodPost, "/v1/admin/listings/bulk", map[string]any{"action": "reject", "ids": []uuid.UUID{c}}, "admin"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if len(body.Error.Fields["note"]) != 1 {
		t.Errorf("fields = %v", body.Error.Fields)
	}
	expectStatus(t, e.call(http.MethodPost, "/v1/admin/listings/bulk", map[string]any{"action": "reject", "ids": []uuid.UUID{c}, "note": "Không hợp lệ"}, "admin"), http.StatusOK)
	if e.status(c) != "rejected" {
		t.Error("rejected")
	}
	// Bad bodies and unknown ids.
	for name, body := range map[string]map[string]any{
		"no ids":      {"action": "approve", "ids": []uuid.UUID{}},
		"bad action":  {"action": "delete", "ids": []uuid.UUID{a}},
		"repeated id": {"action": "approve", "ids": []uuid.UUID{a, a}},
		"unknown id":  {"action": "approve", "ids": []uuid.UUID{a, uuid.New()}},
	} {
		expectError(t, e.call(http.MethodPost, "/v1/admin/listings/bulk", body, "admin"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
		_ = name
	}
}
