//go:build integration

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/testenv"
)

// Popups, their pictures and their statistics are tested through the real composition code, like the announcements.

type popupRow struct {
	ID     uuid.UUID `json:"id"`
	Images []struct {
		ID      uuid.UUID `json:"id"`
		URL     string    `json:"url"`
		LinkURL *string   `json:"link_url"`
	} `json:"images"`
	From struct {
		Role string `json:"role"`
	} `json:"from"`
}

func (e *bridgeEnv) activePopups(bearer string) []popupRow {
	e.t.Helper()
	w := e.call(http.MethodGet, "/v1/popups/active", nil, bearer)
	testenv.ExpectStatus(e.t, w, http.StatusOK)
	return testenv.DecodeData[[]popupRow](e.t, w)
}

func hasPopup(rows []popupRow, id uuid.UUID) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

// tinyPNG is a valid 8x8 picture; the seed makes each one different so two uploads never share bytes.
func tinyPNG(t *testing.T, shade int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := range 8 {
		for y := range 8 {
			img.Set(x, y, color.RGBA{R: uint8(shade & 0xff), G: uint8(x * 30 & 0xff), B: uint8(y * 30 & 0xff), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (e *bridgeEnv) upload(bearer, path string, data []byte) *httptest.ResponseRecorder {
	e.t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "slide.png")
	if err != nil {
		e.t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = form.Close()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

type imageRow struct {
	ID  uuid.UUID `json:"id"`
	URL string    `json:"url"`
}

func popupMessage(audience string) map[string]any {
	return message(audience, map[string]any{"send_notification": false, "show_popup": true})
}

func TestPopupIsShownToItsAudienceOnlyAndFollowsWhatPeopleDo(t *testing.T) {
	e := newBridgeEnv(t)
	a := e.send("admin", "/v1/admin/announcements", popupMessage("tenants"))
	if a.Status != "active" || a.RecipientCount != 3 {
		t.Fatalf("published: %+v", a)
	}
	if !hasPopup(e.activePopups("tenant"), a.ID) || !hasPopup(e.activePopups("landlord-as-tenant"), a.ID) {
		t.Fatal("the tenants get it")
	}
	if len(e.activePopups("admin")) != 0 || len(e.activePopups("super")) != 0 {
		t.Error("staff are not in a tenants audience")
	}
	if p := e.activePopups("tenant")[0]; p.From.Role != "admin" {
		t.Errorf("who it is from: %+v", p.From)
	}

	path := "/v1/popups/" + a.ID.String()
	testenv.ExpectStatus(t, e.call(http.MethodPost, path+"/seen", nil, "tenant"), http.StatusNoContent)
	if !hasPopup(e.activePopups("tenant"), a.ID) {
		t.Error("seen is not closed: it stays")
	}
	testenv.ExpectStatus(t, e.call(http.MethodPost, path+"/dismiss", nil, "tenant"), http.StatusNoContent)
	if hasPopup(e.activePopups("tenant"), a.ID) || !hasPopup(e.activePopups("landlord-as-tenant"), a.ID) {
		t.Error("closing is per person")
	}

	// Somebody outside the audience cannot record anything about it.
	testenv.ExpectError(t, e.call(http.MethodPost, path+"/seen", nil, "admin"), http.StatusNotFound, apperr.CodeResourceNotFound)
	testenv.ExpectError(t, e.call(http.MethodPost, path+"/seen", nil, "super"), http.StatusNotFound, apperr.CodeResourceNotFound)

	// "Show again" brings it back to the person who closed it.
	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/admin/announcements/"+a.ID.String(), mergeMaps(popupMessage("tenants"), map[string]any{"reshow": true}), "admin"), http.StatusOK)
	if !hasPopup(e.activePopups("tenant"), a.ID) {
		t.Error("reshow")
	}

	// The statistics count the people, per channel.
	testenv.ExpectStatus(t, e.call(http.MethodPost, path+"/click", nil, "landlord-as-tenant"), http.StatusNoContent)
	w := e.call(http.MethodGet, "/v1/admin/announcements/"+a.ID.String()+"/stats?channel=popup", nil, "admin")
	testenv.ExpectStatus(t, w, http.StatusOK)
	stats := testenv.DecodeData[statsRow](t, w)
	if stats.Totals.Viewers == nil || *stats.Totals.Viewers != 2 || *stats.Totals.Clicked != 1 || stats.Totals.Read != nil || len(stats.Series) != 30 {
		t.Errorf("stats: %+v", stats)
	}
	if stats.Totals.ClickRate == nil || *stats.Totals.ClickRate != 50 {
		t.Errorf("click rate: %v", stats.Totals.ClickRate)
	}
	today := stats.Series[len(stats.Series)-1]
	if today.Viewers == nil || *today.Viewers != 2 {
		t.Errorf("today's viewers: %+v", today)
	}

	// A range of a year is refused, an unknown channel too, and a landlord cannot read an admin's message.
	testenv.ExpectError(t, e.call(http.MethodGet, "/v1/admin/announcements/stats?from=2026-01-01&to=2026-12-31", nil, "admin"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	testenv.ExpectError(t, e.call(http.MethodGet, "/v1/admin/announcements/stats?channel=sms", nil, "admin"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	testenv.ExpectError(t, e.call(http.MethodGet, "/v1/my/announcements/"+a.ID.String()+"/stats", nil, "landlord"), http.StatusNotFound, apperr.CodeResourceNotFound)
	w = e.call(http.MethodGet, "/v1/admin/announcements/stats", nil, "admin")
	testenv.ExpectStatus(t, w, http.StatusOK)
	overview := testenv.DecodeData[statsRow](t, w)
	if overview.Messages == nil || overview.Messages.WithPopup != 1 || overview.Totals.Read == nil || overview.Totals.ReadRate != nil {
		t.Errorf("overview with both channels and no recipients: %+v", overview)
	}
}

type statsRow struct {
	Messages *struct {
		WithPopup int `json:"with_popup"`
	} `json:"messages"`
	Totals struct {
		Read      *int     `json:"read"`
		ReadRate  *float64 `json:"read_rate"`
		Viewers   *int     `json:"viewers"`
		Clicked   *int     `json:"clicked"`
		ClickRate *float64 `json:"click_rate"`
	} `json:"totals"`
	Series []struct {
		Viewers *int `json:"viewers"`
	} `json:"series"`
}

func mergeMaps(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func TestPopupPicturesUploadArrangeAndServe(t *testing.T) {
	e := newBridgeEnv(t)
	a := e.send("admin", "/v1/admin/announcements", popupMessage("tenants"))
	path := "/v1/admin/announcements/" + a.ID.String() + "/images"

	var images []imageRow
	for shade := range 2 {
		w := e.upload("admin", path, tinyPNG(t, shade*100))
		testenv.ExpectStatus(t, w, http.StatusCreated)
		images = append(images, testenv.DecodeData[imageRow](t, w))
	}
	link := "/find-rooms"
	w := e.call(http.MethodPut, path, map[string]any{"items": []map[string]any{{"id": images[1].ID, "link_url": link}, {"id": images[0].ID}}}, "admin")
	testenv.ExpectStatus(t, w, http.StatusOK)

	popup := e.activePopups("tenant")[0]
	if len(popup.Images) != 2 || popup.Images[0].ID != images[1].ID || popup.Images[0].LinkURL == nil || *popup.Images[0].LinkURL != link || popup.Images[1].LinkURL != nil {
		t.Fatalf("the popup shows the arranged pictures: %+v", popup.Images)
	}

	// The picture is served by the shared public route, cached for ever, without a bearer.
	served := get(e, "/v1/media/"+storedKeyOf(popup.Images[0].URL))
	testenv.ExpectStatus(t, served, http.StatusOK)
	if cc := served.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" || served.Header().Get("ETag") == "" {
		t.Errorf("headers: %v", served.Header())
	}
	if served.Body.Len() == 0 || !strings.HasPrefix(served.Header().Get("Content-Type"), "image/") {
		t.Errorf("body %d, type %s", served.Body.Len(), served.Header().Get("Content-Type"))
	}
	testenv.ExpectStatus(t, get(e, "/v1/media/announcements/2026/11/00000000000000000000000000000000.webp"), http.StatusNotFound)

	// Property pictures still go through the same route.
	room := e.newRoom("P1", "available")
	m := e.upload("landlord", "/v1/rooms/"+room.ID.String()+"/media", tinyPNG(t, 7))
	testenv.ExpectStatus(t, m, http.StatusCreated)
	created := testenv.DecodeData[imageRow](t, m)
	testenv.ExpectStatus(t, get(e, "/v1/media/"+storedKeyOf(created.URL)), http.StatusOK)

	// Removing a picture takes its file with it.
	testenv.ExpectStatus(t, e.call(http.MethodDelete, path+"/"+images[1].ID.String(), nil, "admin"), http.StatusNoContent)
	testenv.ExpectStatus(t, get(e, "/v1/media/"+storedKeyOf(popup.Images[0].URL)), http.StatusNotFound)
	testenv.ExpectError(t, e.call(http.MethodDelete, path+"/"+images[1].ID.String(), nil, "admin"), http.StatusNotFound, apperr.CodeResourceNotFound)
}

func TestLandlordPopupPicturesAreLimitedAndNeverLinked(t *testing.T) {
	e := newBridgeEnv(t)
	e.withTenantOfLandlord()
	a := e.send("landlord", "/v1/my/announcements", popupMessage("my_tenants"))
	path := "/v1/my/announcements/" + a.ID.String() + "/images"
	var first imageRow
	for i := range 3 {
		w := e.upload("landlord", path, tinyPNG(t, i*50))
		testenv.ExpectStatus(t, w, http.StatusCreated)
		if i == 0 {
			first = testenv.DecodeData[imageRow](t, w)
		}
	}
	body := testenv.ExpectError(t, e.upload("landlord", path, tinyPNG(t, 200)), http.StatusConflict, apperr.CodeAnnouncementImageMax)
	if body.Error.Message == "" {
		t.Error("a message")
	}
	w := e.call(http.MethodPut, path, map[string]any{"items": []map[string]any{{"id": first.ID, "link_url": "/find-rooms"}}}, "landlord")
	testenv.ExpectStatus(t, w, http.StatusUnprocessableEntity)
	// Another sender's message is invisible; a stranger's picture upload is a 404.
	testenv.ExpectError(t, e.upload("landlord2", path, tinyPNG(t, 9)), http.StatusNotFound, apperr.CodeResourceNotFound)
	testenv.ExpectError(t, e.upload("admin", path, tinyPNG(t, 9)), http.StatusForbidden, apperr.CodeForbidden)
	testenv.ExpectError(t, e.upload("landlord", path, []byte("not a picture")), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
}

func get(e *bridgeEnv, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

// storedKeyOf is the key of a picture URL made by the memory store of the test environment.
func storedKeyOf(url string) string {
	const prefix = "http://files.test/v1/media/"
	if len(url) > len(prefix) {
		return url[len(prefix):]
	}
	return url
}
