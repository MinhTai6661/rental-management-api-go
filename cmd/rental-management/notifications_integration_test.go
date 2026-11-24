//go:build integration

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/testenv"
	notificationuc "rental-management/internal/usecase/notification"
)

// decodeFull reads the whole body (data and meta), where testenv.DecodeData only reads "data".
func decodeFull[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return out
}

func messageFor(user uuid.UUID, kind string) notificationuc.Message {
	return notificationuc.Message{UserIDs: []uuid.UUID{user}, Kind: kind, Params: map[string]string{"room": "R"}, URL: "/x"}
}

// The inbox is tested through the real composition code: contract and listing events reach the notification use cases
// through the bridges, and the HTTP routes show each person only their own messages, per role.

type inboxRow struct {
	ID     uuid.UUID         `json:"id"`
	Kind   string            `json:"kind"`
	Params map[string]string `json:"params"`
	URL    *string           `json:"url"`
	ReadAt *time.Time        `json:"read_at"`
}

type inboxList struct {
	Data []inboxRow `json:"data"`
	Meta struct {
		UnreadCount int   `json:"unread_count"`
		HasMore     *bool `json:"has_more"`
	} `json:"meta"`
}

func (e *bridgeEnv) inboxOf(bearer, query string) inboxList {
	e.t.Helper()
	w := e.call(http.MethodGet, "/v1/notifications"+query, nil, bearer)
	testenv.ExpectStatus(e.t, w, http.StatusOK)
	return decodeFull[inboxList](e.t, w)
}

func kinds(rows []inboxRow) map[string]int {
	out := map[string]int{}
	for _, r := range rows {
		out[r.Kind]++
	}
	return out
}

func TestRealEventsReachTheRightInboxes(t *testing.T) {
	e := newBridgeEnv(t)
	room := e.newRoom("R1", "available")

	// Submitting a listing tells the admins (not the landlord), approving it tells the landlord.
	e.publish("landlord", room.ID)
	if got := kinds(e.inboxOf("admin", "").Data); got["listing_submitted"] != 1 {
		t.Errorf("the admin is told about the submitted listing: %v", got)
	}
	if got := kinds(e.inboxOf("landlord", "").Data); got["listing_approved"] != 1 || got["listing_submitted"] != 0 {
		t.Errorf("the landlord is told about the approval only: %v", got)
	}

	// A tenant registers: the landlord is told in the landlord inbox; the tenant is told nothing yet.
	reg := e.call(http.MethodPost, "/v1/contracts/register", map[string]any{"room_id": room.ID, "starts_on": time.Now().In(time.FixedZone("ICT", 7*3600)).Format("2006-01-02")}, "tenant")
	testenv.ExpectStatus(t, reg, http.StatusCreated)
	c := testenv.DecodeData[contractRow](t, reg)
	landlordBox := e.inboxOf("landlord", "")
	if got := kinds(landlordBox.Data); got["registration_confirmed"] != 1 {
		t.Fatalf("the landlord is told about the registration: %v", got)
	}
	if len(e.inboxOf("tenant", "").Data) != 0 {
		t.Error("the tenant has nothing yet")
	}

	// The same person acting as a tenant does not see the landlord's messages, and the other way round.
	if got := e.inboxOf("landlord-as-tenant", ""); len(got.Data) != 0 || got.Meta.UnreadCount != 0 {
		t.Errorf("the landlord's own tenant inbox is empty: %+v", got)
	}

	// Approving tells the tenant, in the tenant inbox.
	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/contracts/"+c.ID.String(), map[string]any{"status": "active"}, "landlord"), http.StatusOK)
	tenantBox := e.inboxOf("tenant", "")
	if got := kinds(tenantBox.Data); got["registration_approved"] != 1 || tenantBox.Meta.UnreadCount != 1 {
		t.Fatalf("the tenant is told about the approval: %+v", tenantBox)
	}
	if tenantBox.Data[0].Params["room"] != "R1" || tenantBox.Data[0].URL == nil {
		t.Errorf("params and url travel with the message: %+v", tenantBox.Data[0])
	}

	// Ownership: nobody else can read, delete or even see that somebody's message exists.
	id := tenantBox.Data[0].ID.String()
	for _, bearer := range []string{"landlord", "landlord2", "admin"} {
		testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/notifications/"+id+"/read", nil, bearer), http.StatusNotFound)
		testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/notifications/"+id, nil, bearer), http.StatusNotFound)
	}
	if e.inboxOf("tenant", "").Meta.UnreadCount != 1 {
		t.Error("the strangers changed nothing")
	}
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/notifications/"+id+"/read", nil, "tenant"), http.StatusOK)
	if e.inboxOf("tenant", "?unread=true").Meta.UnreadCount != 0 {
		t.Error("the owner marked it read")
	}
}

func TestInboxRoutes(t *testing.T) {
	e := newBridgeEnv(t)
	ctx := t.Context()
	for i := 0; i < 25; i++ {
		kind := "contract_ended"
		if i%5 == 0 {
			kind = "invoice_sent"
		}
		if err := e.inbox.deliveries.Send(ctx, messageFor(e.tenant, kind)); err != nil {
			t.Fatal(err)
		}
	}
	// A message of another person never shows.
	if err := e.inbox.deliveries.Send(ctx, messageFor(e.landlord2, "contract_ended")); err != nil {
		t.Fatal(err)
	}

	page := e.inboxOf("tenant", "?per_page=10")
	if len(page.Data) != 10 || page.Meta.HasMore == nil || !*page.Meta.HasMore || page.Meta.UnreadCount != 25 {
		t.Fatalf("first page of the feed: %d rows, %+v", len(page.Data), page.Meta)
	}
	if last := e.inboxOf("tenant", "?per_page=10&page=3"); len(last.Data) != 5 || *last.Meta.HasMore {
		t.Errorf("last page: %d rows", len(last.Data))
	}
	if got := e.inboxOf("tenant", "?kind=invoice_sent"); len(got.Data) != 5 {
		t.Errorf("the kind filter: %d", len(got.Data))
	}
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/notifications?kind=nope", nil, "tenant"), http.StatusUnprocessableEntity)
	testenv.ExpectStatus(t, e.call(http.MethodGet, "/v1/notifications?per_page=0", nil, "tenant"), http.StatusUnprocessableEntity)

	// The bell is bounded and says how many are unread.
	w := e.call(http.MethodGet, "/v1/notifications/latest?limit=50", nil, "tenant")
	testenv.ExpectStatus(t, w, http.StatusOK)
	if latest := decodeFull[inboxList](t, w); len(latest.Data) != 20 || latest.Meta.UnreadCount != 25 {
		t.Errorf("latest: %d rows, %+v", len(latest.Data), latest.Meta)
	}
	w = e.call(http.MethodGet, "/v1/notifications/unread-count", nil, "tenant")
	testenv.ExpectStatus(t, w, http.StatusOK)
	if got := testenv.DecodeData[struct {
		UnreadCount int `json:"unread_count"`
	}](t, w); got.UnreadCount != 25 {
		t.Errorf("unread count: %d", got.UnreadCount)
	}

	// Bulk: read three, delete two (one of them somebody else's), then the limits.
	ids := make([]string, 0, 5)
	for _, r := range e.inboxOf("tenant", "?per_page=5").Data {
		ids = append(ids, r.ID.String())
	}
	foreign := e.inboxOf("landlord2", "").Data[0].ID.String()
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/notifications/bulk", map[string]any{"action": "read", "ids": ids[:3]}, "tenant"), http.StatusOK)
	w = e.call(http.MethodPost, "/v1/notifications/bulk", map[string]any{"action": "delete", "ids": []string{ids[3], foreign}}, "tenant")
	testenv.ExpectStatus(t, w, http.StatusOK)
	if got := testenv.DecodeData[struct {
		Done int `json:"done"`
	}](t, w); got.Done != 1 {
		t.Errorf("only the tenant's own message is deleted: %d", got.Done)
	}
	if e.inboxOf("landlord2", "").Meta.UnreadCount != 1 {
		t.Error("the other person's message survived")
	}
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/notifications/bulk", map[string]any{"action": "burn", "ids": ids[:1]}, "tenant"), http.StatusUnprocessableEntity)
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/notifications/bulk", map[string]any{"action": "read", "ids": []string{"x"}}, "tenant"), http.StatusUnprocessableEntity)
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/notifications/bulk", map[string]any{"action": "read", "ids": []string{}}, "tenant"), http.StatusUnprocessableEntity)

	// Read-all, then clearing what was read, then everything.
	w = e.call(http.MethodPost, "/v1/notifications/read-all", nil, "tenant")
	testenv.ExpectStatus(t, w, http.StatusOK)
	if got := testenv.DecodeData[struct {
		Updated int `json:"updated"`
	}](t, w); got.Updated != 21 {
		t.Errorf("24 left, 3 already read: %d", got.Updated)
	}
	w = e.call(http.MethodDelete, "/v1/notifications?scope=read", nil, "tenant")
	testenv.ExpectStatus(t, w, http.StatusOK)
	if got := testenv.DecodeData[struct {
		Deleted int `json:"deleted"`
	}](t, w); got.Deleted != 24 {
		t.Errorf("everything is read now: %d", got.Deleted)
	}
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/notifications?scope=weird", nil, "tenant"), http.StatusUnprocessableEntity)
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/notifications", nil, "tenant"), http.StatusUnprocessableEntity)
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/notifications?scope=all", nil, "tenant"), http.StatusOK)
}
