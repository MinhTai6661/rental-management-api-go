//go:build integration

package httpadapter_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	usecase "rental-management/internal/usecase/property"
	"rental-management/pkg/apperr"
)

// ---- fixtures ----

// tinyWebP is an 8x8 lossless WebP.
func tinyWebP(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("UklGRi4AAABXRUJQVlA4TCIAAAAvB8ABALkyRPQ/dhHR/wBhtlFJzh90ryMYjwGMCaB6oP8A")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// riffWebP assembles a WebP from (id, payload) chunks.
func riffWebP(chunks ...[2]string) []byte {
	body := []byte("WEBP")
	for _, c := range chunks {
		body = append(body, c[0]...)
		body = binary.LittleEndian.AppendUint32(body, uint32(len(c[1]))) //nolint:gosec // test fixture
		body = append(body, c[1]...)
		if len(c[1])%2 == 1 {
			body = append(body, 0)
		}
	}
	out := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body))) //nolint:gosec // test fixture
	return append(out, body...)
}

// webpWithEXIF is the tiny image inside a VP8X container that carries an EXIF chunk.
func webpWithEXIF(t *testing.T) []byte {
	t.Helper()
	tiny := tinyWebP(t)
	// The tiny file is "RIFF" size "WEBP" then one chunk; reuse that chunk.
	image := string(tiny[12:])
	vp8x := string([]byte{0x08, 0, 0, 0, 7, 0, 0, 7, 0, 0})
	body := []byte("WEBP")
	body = append(body, "VP8X"...)
	body = binary.LittleEndian.AppendUint32(body, uint32(len(vp8x))) //nolint:gosec // test fixture
	body = append(body, vp8x...)
	body = append(body, image...)
	exif := "Exif\x00\x00gps-secret!!"
	body = append(body, "EXIF"...)
	body = binary.LittleEndian.AppendUint32(body, uint32(len(exif))) //nolint:gosec // test fixture
	body = append(body, exif...)
	out := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body))) //nolint:gosec // test fixture
	return append(out, body...)
}

func animatedWebP(t *testing.T) []byte {
	t.Helper()
	vp8x := string([]byte{0x02, 0, 0, 0, 7, 0, 0, 7, 0, 0})
	return riffWebP([2]string{"VP8X", vp8x}, [2]string{"ANIM", "\x00\x00\x00\x00\x00\x00"}, [2]string{"ANMF", strings.Repeat("\x00", 16)})
}

func pngImage(t *testing.T, w, h int, alpha uint8) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			img.Set(x, y, color.NRGBA{R: uint8(x * 7), G: uint8(y * 5), B: 90, A: alpha})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// jpegWithEXIF is a JPEG with an APP1 Exif segment (as a phone adds, GPS included).
func jpegWithEXIF(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for x := range 32 {
		for y := range 24 {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 10), B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	exif := []byte("Exif\x00\x00GPS-LATITUDE-SECRET")
	seg := []byte{0xFF, 0xE1, 0, byte(len(exif) + 2)} //nolint:gosec // test fixture, short segment
	seg = append(seg, exif...)
	return append(append(append([]byte{}, raw[:2]...), seg...), raw[2:]...)
}

// bombPNG is a valid PNG header claiming 10000x10000 pixels with no pixel data: refused from the header alone.
func bombPNG() []byte {
	chunk := func(kind string, data []byte) []byte {
		out := binary.BigEndian.AppendUint32(nil, uint32(len(data))) //nolint:gosec // test fixture
		out = append(out, kind...)
		out = append(out, data...)
		return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(kind), data...)))
	}
	ihdr := binary.BigEndian.AppendUint32(nil, 10000)
	ihdr = binary.BigEndian.AppendUint32(ihdr, 10000)
	ihdr = append(ihdr, 8, 6, 0, 0, 0)
	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, chunk("IHDR", ihdr)...)
	return append(out, chunk("IEND", nil)...)
}

// upload sends a multipart picture (kind "" sends no kind part).
func (e *propertyEnv) upload(method, path string, data []byte, kind, bearer string) *httptest.ResponseRecorder {
	e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if kind != "" {
		if err := mw.WriteField("kind", kind); err != nil {
			e.t.Fatal(err)
		}
	}
	part, err := mw.CreateFormFile("file", "front.img")
	if err != nil {
		e.t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(method, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept-Language", "en")
	req.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

func (e *propertyEnv) addMedia(path string, data []byte, kind, bearer string) MediaView {
	e.t.Helper()
	w := e.upload(http.MethodPost, path, data, kind, bearer)
	expectStatus(e.t, w, http.StatusCreated)
	return decodeData[MediaView](e.t, w)
}

// storedKey is the storage key behind a public URL of the test store.
func storedKey(url string) string { return strings.TrimPrefix(url, "http://files.test/v1/media/") }

func (e *propertyEnv) storedBytes(url string) []byte {
	e.t.Helper()
	rc, _, err := e.files.Open(context.Background(), storedKey(url))
	if err != nil {
		e.t.Fatalf("open %s: %v", url, err)
	}
	defer func() { _ = rc.Close() }()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rc)
	return buf.Bytes()
}

func (e *propertyEnv) roomMediaPath(id uuid.UUID) string {
	return "/v1/rooms/" + id.String() + "/media"
}
func (e *propertyEnv) buildingMediaPath(id uuid.UUID) string {
	return "/v1/buildings/" + id.String() + "/media"
}

// ---- tests ----

func TestUploadWebPStripsMetadataAndMakesAThumbnail(t *testing.T) {
	e := newPropertyEnv(t, nil)
	room := e.roomAPI("landlord-a", map[string]any{"name": "A1", "address_detail": "x", "price_vnd": 1})
	m := e.addMedia(e.roomMediaPath(room.ID), webpWithEXIF(t), "", "landlord-a")
	if m.ContentType != "image/webp" || m.Kind != "photo" || m.Width != 8 || m.Height != 8 || m.DisplayOrder != 1 {
		t.Fatalf("media: %+v", m)
	}
	if m.OriginalName == nil || *m.OriginalName != "front.img" {
		t.Errorf("original name: %v", m.OriginalName)
	}
	stored := e.storedBytes(m.URL)
	if bytes.Contains(stored, []byte("gps-secret")) || bytes.Contains(stored, []byte("EXIF")) {
		t.Error("EXIF still in the stored WebP")
	}
	thumb := e.storedBytes(m.ThumbURL)
	if _, err := jpeg.Decode(bytes.NewReader(thumb)); err != nil {
		t.Errorf("thumbnail is not a JPEG: %v", err)
	}
	if got := e.getRoom("landlord-a", room.ID); got.Cover == nil || got.Cover.URL != m.URL || len(got.Media) != 1 {
		t.Errorf("first picture becomes the cover: %+v", got.Cover)
	}
}

func TestUploadJPEGAndPNGAreStoredAsJPEGWithoutMetadata(t *testing.T) {
	e := newPropertyEnv(t, nil)
	room := e.roomAPI("landlord-a", map[string]any{"name": "A1", "address_detail": "x", "price_vnd": 1})
	m := e.addMedia(e.roomMediaPath(room.ID), jpegWithEXIF(t), "", "landlord-a")
	if m.ContentType != "image/jpeg" || !strings.HasSuffix(storedKey(m.URL), ".jpg") {
		t.Fatalf("jpeg: %+v", m)
	}
	if b := e.storedBytes(m.URL); bytes.Contains(b, []byte("Exif")) || bytes.Contains(b, []byte("GPS-LATITUDE")) {
		t.Error("EXIF still in the stored JPEG")
	}
	p := e.addMedia(e.roomMediaPath(room.ID), pngImage(t, 40, 30, 128), "", "landlord-a")
	if p.ContentType != "image/jpeg" || p.Width != 40 || p.DisplayOrder != 2 {
		t.Errorf("png with alpha is stored as JPEG: %+v", p)
	}
	if _, err := jpeg.Decode(bytes.NewReader(e.storedBytes(p.URL))); err != nil {
		t.Errorf("stored PNG is not a JPEG: %v", err)
	}
}

func TestUploadRefusals(t *testing.T) {
	e := newPropertyEnv(t, nil)
	room := e.roomAPI("landlord-a", map[string]any{"name": "A1", "address_detail": "x", "price_vnd": 1})
	path := e.roomMediaPath(room.ID)
	cases := []struct {
		name string
		data []byte
		rule string
	}{
		{"text", []byte("hello, this is not a picture"), "image_type"},
		{"gif", []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"), "image_type"},
		{"animated webp", animatedWebP(t), "image_type"},
		{"huge dimensions", bombPNG(), "image_dimensions"},
		{"too many pixels", pngImage(t, 2100, 4, 255), "image_dimensions"},
		{"corrupt", append([]byte("\x89PNG\r\n\x1a\n"), []byte("garbage-after-signature")...), "image_corrupt"},
		{"too large", bytes.Repeat([]byte{1}, 1<<20+10), "file_too_large"},
	}
	for _, c := range cases {
		w := e.upload(http.MethodPost, path, c.data, "", "landlord-a")
		body := expectError(t, w, http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
		if len(body.Error.Fields["file"]) == 0 {
			t.Errorf("%s: no error on file: %s", c.name, w.Body.String())
		}
	}
	// Each reason has its own message: check the rule through the translated text.
	w := e.upload(http.MethodPost, path, bombPNG(), "", "landlord-a")
	if !strings.Contains(w.Body.String(), "2048") {
		t.Errorf("dimension message names the limit: %s", w.Body.String())
	}
	if keys := e.files.Keys(); len(keys) != 0 {
		t.Errorf("a refused upload leaves nothing in storage: %v", keys)
	}
	// A bad kind and a missing file.
	expectError(t, e.upload(http.MethodPost, path, tinyWebP(t), "banner", "landlord-a"), http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer landlord-a")
	w = httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	expectError(t, w, http.StatusBadRequest, apperr.CodeRequestMalformed)
}

func TestMediaLimitAndPlanLimit(t *testing.T) {
	e := newPropertyEnv(t, nil, withMaxMedia(2))
	room := e.roomAPI("landlord-a", map[string]any{"name": "A1", "address_detail": "x", "price_vnd": 1})
	e.addMedia(e.roomMediaPath(room.ID), tinyWebP(t), "", "landlord-a")
	e.addMedia(e.roomMediaPath(room.ID), tinyWebP(t), "", "landlord-a")
	w := e.upload(http.MethodPost, e.roomMediaPath(room.ID), tinyWebP(t), "", "landlord-a")
	body := expectError(t, w, http.StatusConflict, apperr.CodeMediaLimitReached)
	if !strings.Contains(body.Error.Message, "2") {
		t.Errorf("message names the maximum: %s", body.Error.Message)
	}
	if len(e.files.Keys()) != 4 {
		t.Errorf("the refused upload's files are removed again: %v", e.files.Keys())
	}
	// The plan's limit wins over the configured default.
	three := 3
	e.plans.set(mediaPlan{max: &three})
	e.addMedia(e.roomMediaPath(room.ID), tinyWebP(t), "", "landlord-a")
	expectError(t, e.upload(http.MethodPost, e.roomMediaPath(room.ID), tinyWebP(t), "", "landlord-a"), http.StatusConflict, apperr.CodeMediaLimitReached)
}

type mediaPlan struct {
	usecase.UnlimitedPlans
	max *int
}

func (p mediaPlan) MaxMediaPerOwner(context.Context, uuid.UUID) (*int, error) { return p.max, nil }

func TestCoverKindArrangeReplaceDelete(t *testing.T) {
	e := newPropertyEnv(t, nil)
	room := e.roomAPI("landlord-a", map[string]any{"name": "A1", "address_detail": "x", "price_vnd": 1})
	path := e.roomMediaPath(room.ID)
	a := e.addMedia(path, tinyWebP(t), "", "landlord-a")
	b := e.addMedia(path, pngImage(t, 20, 20, 255), "", "landlord-a")
	c := e.addMedia(path, pngImage(t, 21, 20, 255), "cover", "landlord-a")
	if got := e.getRoom("landlord-a", room.ID); got.Cover.URL != c.URL {
		t.Fatalf("an uploaded cover is the cover: %+v", got.Cover)
	}
	d := e.addMedia(path, pngImage(t, 22, 20, 255), "cover", "landlord-a")
	got := e.getRoom("landlord-a", room.ID)
	kinds := map[uuid.UUID]string{}
	for _, m := range got.Media {
		kinds[m.ID] = m.Kind
	}
	if kinds[c.ID] != "photo" || kinds[d.ID] != "cover" || got.Cover.URL != d.URL {
		t.Errorf("a new cover demotes the old one: %v", kinds)
	}

	// Arrange: wrong sets and two covers are refused.
	item := func(id uuid.UUID, kind string) map[string]any { return map[string]any{"id": id, "kind": kind} }
	w := e.call(http.MethodPut, path, map[string]any{"items": []any{item(a.ID, "photo"), item(b.ID, "photo")}}, "landlord-a")
	if body := expectError(t, w, http.StatusUnprocessableEntity, apperr.CodeValidationFailed); len(body.Error.Fields["items"]) == 0 {
		t.Errorf("missing picture: %s", w.Body.String())
	}
	w = e.call(http.MethodPut, path, map[string]any{"items": []any{item(a.ID, "cover"), item(b.ID, "cover"), item(c.ID, "photo"), item(d.ID, "photo")}}, "landlord-a")
	expectError(t, w, http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	w = e.call(http.MethodPut, path, map[string]any{"items": []any{item(a.ID, "photo"), item(a.ID, "photo"), item(c.ID, "photo"), item(d.ID, "photo")}}, "landlord-a")
	expectError(t, w, http.StatusUnprocessableEntity, apperr.CodeValidationFailed)

	w = e.call(http.MethodPut, path, map[string]any{"items": []any{item(d.ID, "photo"), item(c.ID, "photo"), item(b.ID, "cover"), item(a.ID, "photo")}}, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	list := decodeData[[]MediaView](t, w)
	order := []uuid.UUID{list[0].ID, list[1].ID, list[2].ID, list[3].ID}
	if !slices.Equal(order, []uuid.UUID{d.ID, c.ID, b.ID, a.ID}) || list[2].Kind != "cover" || list[2].DisplayOrder != 3 {
		t.Errorf("arrangement applied: %+v", list)
	}
	if got := e.getRoom("landlord-a", room.ID); got.Cover.URL != b.URL {
		t.Errorf("the arranged cover is the room's cover: %+v", got.Cover)
	}
	// No cover marked: the first picture shows.
	w = e.call(http.MethodPut, path, map[string]any{"items": []any{item(a.ID, "photo"), item(b.ID, "photo"), item(c.ID, "photo"), item(d.ID, "photo")}}, "landlord-a")
	expectStatus(t, w, http.StatusOK)
	if got := e.getRoom("landlord-a", room.ID); got.Cover.URL != a.URL {
		t.Errorf("first picture when none is the cover: %+v", got.Cover)
	}

	// Replace the file of a: new keys, old files gone, place kept.
	oldURL, oldThumb := a.URL, a.ThumbURL
	w = e.upload(http.MethodPut, path+"/"+a.ID.String()+"/file", pngImage(t, 50, 40, 255), "", "landlord-a")
	expectStatus(t, w, http.StatusOK)
	r := decodeData[MediaView](t, w)
	if r.ID != a.ID || r.URL == oldURL || r.Width != 50 || r.DisplayOrder != 1 || r.ContentType != "image/jpeg" {
		t.Errorf("replaced: %+v", r)
	}
	keys := e.files.Keys()
	if slices.Contains(keys, storedKey(oldURL)) || slices.Contains(keys, storedKey(oldThumb)) || !slices.Contains(keys, storedKey(r.URL)) {
		t.Errorf("replace swaps the files: %v", keys)
	}
	if got := e.getRoom("landlord-a", room.ID); got.Cover.URL != r.URL {
		t.Errorf("the cover follows the replaced file: %+v", got.Cover)
	}

	// Delete the cover picture: the next one shows, positions close up, files go.
	before := len(e.files.Keys())
	expectStatus(t, e.call(http.MethodDelete, path+"/"+a.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	got = e.getRoom("landlord-a", room.ID)
	if len(got.Media) != 3 || got.Media[0].DisplayOrder != 1 || got.Media[2].DisplayOrder != 3 || got.Cover.URL != got.Media[0].URL {
		t.Errorf("after delete: %+v", got.Media)
	}
	if len(e.files.Keys()) != before-2 {
		t.Errorf("delete removes the picture and its thumbnail: %d -> %d", before, len(e.files.Keys()))
	}
	expectError(t, e.call(http.MethodDelete, path+"/"+a.ID.String(), nil, "landlord-a"), http.StatusNotFound, apperr.CodeResourceNotFound)
	// Deleting the last ones empties the cover.
	for _, m := range got.Media {
		expectStatus(t, e.call(http.MethodDelete, path+"/"+m.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	}
	if got = e.getRoom("landlord-a", room.ID); got.Cover != nil || len(got.Media) != 0 || len(e.files.Keys()) != 0 {
		t.Errorf("no pictures left: %+v, files %v", got.Cover, e.files.Keys())
	}
}

func TestMediaIsOwnerOnly(t *testing.T) {
	e := newPropertyEnv(t, nil)
	room := e.roomAPI("landlord-a", map[string]any{"name": "A1", "address_detail": "x", "price_vnd": 1})
	b := e.building("landlord-a", "B", map[string]any{"price_vnd": 1})
	mine := e.addMedia(e.roomMediaPath(room.ID), tinyWebP(t), "", "landlord-a")
	otherRoom := e.roomAPI("landlord-b", map[string]any{"name": "A1", "address_detail": "x", "price_vnd": 1})
	theirs := e.addMedia(e.roomMediaPath(otherRoom.ID), tinyWebP(t), "", "landlord-b")
	files := len(e.files.Keys())

	// Landlord B cannot attach to, replace, arrange or delete landlord A's pictures; every refusal is a 404.
	for _, path := range []string{e.roomMediaPath(room.ID), e.buildingMediaPath(b.ID)} {
		expectError(t, e.upload(http.MethodPost, path, tinyWebP(t), "", "landlord-b"), http.StatusNotFound, apperr.CodeResourceNotFound)
		w := e.call(http.MethodPut, path, map[string]any{"items": []any{map[string]any{"id": mine.ID, "kind": "photo"}}}, "landlord-b")
		expectError(t, w, http.StatusNotFound, apperr.CodeResourceNotFound)
	}
	expectError(t, e.upload(http.MethodPut, e.roomMediaPath(room.ID)+"/"+mine.ID.String()+"/file", tinyWebP(t), "", "landlord-b"), http.StatusNotFound, apperr.CodeResourceNotFound)
	expectError(t, e.call(http.MethodDelete, e.roomMediaPath(room.ID)+"/"+mine.ID.String(), nil, "landlord-b"), http.StatusNotFound, apperr.CodeResourceNotFound)
	// And B cannot reach A's picture by naming it under B's own room.
	expectError(t, e.call(http.MethodDelete, e.roomMediaPath(otherRoom.ID)+"/"+mine.ID.String(), nil, "landlord-b"), http.StatusNotFound, apperr.CodeResourceNotFound)
	expectError(t, e.upload(http.MethodPut, e.roomMediaPath(otherRoom.ID)+"/"+mine.ID.String()+"/file", tinyWebP(t), "", "landlord-b"), http.StatusNotFound, apperr.CodeResourceNotFound)
	if len(e.files.Keys()) != files {
		t.Errorf("refused requests leave no file behind: %d -> %d", files, len(e.files.Keys()))
	}
	if got := e.getRoom("landlord-a", room.ID); len(got.Media) != 1 || got.Media[0].ID != mine.ID {
		t.Errorf("A's pictures are untouched: %+v", got.Media)
	}
	if got := e.getRoom("landlord-b", otherRoom.ID); len(got.Media) != 1 || got.Media[0].ID != theirs.ID {
		t.Errorf("B's pictures are untouched: %+v", got.Media)
	}
	// Staff and tenants may not change pictures at all.
	for _, who := range []string{"tenant", "admin"} {
		expectError(t, e.upload(http.MethodPost, e.roomMediaPath(room.ID), tinyWebP(t), "", who), http.StatusForbidden, apperr.CodeForbidden)
	}
	expectError(t, e.upload(http.MethodPost, e.roomMediaPath(room.ID), tinyWebP(t), "", ""), http.StatusUnauthorized, apperr.CodeUnauthenticated)
}

func TestRoomFollowingTheBuildingShowsItsPicturesAndLeavesOnItsOwnUpload(t *testing.T) {
	e := newPropertyEnv(t, nil)
	rec := &recorder{}
	e.observer.set(rec)
	b := e.building("landlord-a", "B", map[string]any{"price_vnd": 1000})
	follower := e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "P1"})
	other := e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "P2", "inherit": []string{"price"}})

	m := e.addMedia(e.buildingMediaPath(b.ID), tinyWebP(t), "", "landlord-a")
	got := e.getRoom("landlord-a", follower.ID)
	if got.Cover == nil || got.Cover.URL != m.URL || len(got.Media) != 1 || got.Media[0].ID != m.ID {
		t.Fatalf("the follower shows the building's picture: %+v", got)
	}
	if o := e.getRoom("landlord-a", other.ID); o.Cover != nil || len(o.Media) != 0 {
		t.Errorf("a room with its own pictures does not: %+v", o.Cover)
	}
	if len(rec.content) == 0 || !slices.Contains(rec.content[len(rec.content)-1], follower.ID) || slices.Contains(rec.content[len(rec.content)-1], other.ID) {
		t.Errorf("only the followers' content changed: %v", rec.content)
	}
	// A cover upload changes what the followers show; so does arranging and deleting.
	c := e.addMedia(e.buildingMediaPath(b.ID), pngImage(t, 30, 30, 255), "cover", "landlord-a")
	if got = e.getRoom("landlord-a", follower.ID); got.Cover.URL != c.URL || len(got.Media) != 2 {
		t.Errorf("follower cover follows the building's cover: %+v", got.Cover)
	}
	if bd := decodeData[BuildingDetail](t, e.call(http.MethodGet, "/v1/buildings/"+b.ID.String(), nil, "landlord-a")); bd.Cover == nil || bd.Cover.URL != c.URL || len(bd.Media) != 2 {
		t.Errorf("building cover: %+v", bd.Cover)
	}
	expectStatus(t, e.call(http.MethodDelete, e.buildingMediaPath(b.ID)+"/"+c.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	if got = e.getRoom("landlord-a", follower.ID); got.Cover.URL != m.URL {
		t.Errorf("deleting the building cover moves it on: %+v", got.Cover)
	}

	// The follower uploads its own picture: it stops following, and shows only its own.
	own := e.addMedia(e.roomMediaPath(follower.ID), pngImage(t, 31, 31, 255), "", "landlord-a")
	got = e.getRoom("landlord-a", follower.ID)
	if slices.Contains(got.Inherits, "media") || got.Cover.URL != own.URL || len(got.Media) != 1 || got.Media[0].ID != own.ID {
		t.Errorf("own upload ends the following: inherits %v cover %+v media %+v", got.Inherits, got.Cover, got.Media)
	}
	// A later building picture does not reach it any more.
	e.addMedia(e.buildingMediaPath(b.ID), pngImage(t, 33, 33, 255), "cover", "landlord-a")
	if got = e.getRoom("landlord-a", follower.ID); got.Cover.URL != own.URL {
		t.Errorf("no longer following: %+v", got.Cover)
	}
	// A building's picture is not reachable through a room's path, and the other way round.
	expectError(t, e.call(http.MethodDelete, e.roomMediaPath(follower.ID)+"/"+m.ID.String(), nil, "landlord-a"), http.StatusNotFound, apperr.CodeResourceNotFound)
}

func TestCopyBuildingDuplicatesPictures(t *testing.T) {
	e := newPropertyEnv(t, nil)
	b := e.fullBuilding("landlord-a", "B")
	e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "F1"})
	own := e.roomAPI("landlord-a", map[string]any{"building_id": b.ID, "name": "O1", "inherit": []string{"price"}})
	bm := e.addMedia(e.buildingMediaPath(b.ID), tinyWebP(t), "cover", "landlord-a")
	rm := e.addMedia(e.roomMediaPath(own.ID), pngImage(t, 25, 25, 255), "", "landlord-a")
	e.addMedia(e.roomMediaPath(own.ID), pngImage(t, 26, 25, 255), "", "landlord-a")
	sourceKeys := len(e.files.Keys())

	w := e.call(http.MethodPost, "/v1/buildings/"+b.ID.String()+"/copy", map[string]any{"name": "B copy", "include_rooms": true}, "landlord-a")
	expectStatus(t, w, http.StatusCreated)
	res := decodeData[CopyResult](t, w)
	if res.RoomsCopied != 2 || len(res.Building.Media) != 1 || res.Building.Cover == nil {
		t.Fatalf("copy: %+v", res)
	}
	copied := res.Building.Media[0]
	if copied.URL == bm.URL || copied.Kind != "cover" || res.Building.Cover.URL != copied.URL {
		t.Errorf("the building's picture is a duplicate with its own files: %+v vs %+v", copied, bm)
	}
	if !bytes.Equal(e.storedBytes(copied.URL), e.storedBytes(bm.URL)) {
		t.Error("the duplicated file has the same content")
	}
	if len(e.files.Keys()) != sourceKeys*2 {
		t.Errorf("every file is duplicated exactly once: %d -> %d", sourceKeys, len(e.files.Keys()))
	}

	rooms := e.listRooms("landlord-a", "?building_id="+res.Building.ID.String())
	if len(rooms.Data) != 2 {
		t.Fatalf("rooms: %+v", rooms.Data)
	}
	for _, r := range rooms.Data {
		detail := e.getRoom("landlord-a", r.ID)
		switch detail.Name {
		case "F1":
			if detail.Cover == nil || detail.Cover.URL != copied.URL || len(detail.Media) != 1 || detail.Media[0].URL != copied.URL {
				t.Errorf("a room following the pictures shows the new building's: %+v", detail.Cover)
			}
		case "O1":
			if len(detail.Media) != 2 || detail.Media[0].URL == rm.URL || detail.Cover == nil || detail.Cover.URL != detail.Media[0].URL {
				t.Errorf("a room with its own pictures gets duplicates: %+v", detail.Media)
			}
		}
	}

	// The copy is independent: deleting a picture of the copy leaves the source's files.
	expectStatus(t, e.call(http.MethodDelete, e.buildingMediaPath(res.Building.ID)+"/"+copied.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	if len(e.storedBytes(bm.URL)) == 0 {
		t.Error("source file intact")
	}
}

func TestCopyBuildingFailureLeavesNoFiles(t *testing.T) {
	e := newPropertyEnv(t, nil)
	b := e.building("landlord-a", "B", map[string]any{"price_vnd": 1})
	e.addMedia(e.buildingMediaPath(b.ID), tinyWebP(t), "", "landlord-a")
	before := e.files.Keys()
	// The name "B" is taken: the copy fails inside the transaction, after the files were duplicated.
	w := e.call(http.MethodPost, "/v1/buildings/"+b.ID.String()+"/copy", map[string]any{"name": "b"}, "landlord-a")
	expectError(t, w, http.StatusUnprocessableEntity, apperr.CodeValidationFailed)
	if got := e.files.Keys(); !slices.Equal(got, before) {
		t.Errorf("the failed copy's files are removed: %v -> %v", before, got)
	}
	// Another landlord's building is a 404 and copies nothing.
	expectError(t, e.call(http.MethodPost, "/v1/buildings/"+b.ID.String()+"/copy", map[string]any{"name": "Mine"}, "landlord-b"), http.StatusNotFound, apperr.CodeResourceNotFound)
	if got := e.files.Keys(); !slices.Equal(got, before) {
		t.Errorf("nothing copied for a stranger: %v", got)
	}
}

func TestDeletingABuildingRemovesItsPictureFiles(t *testing.T) {
	e := newPropertyEnv(t, nil)
	b := e.building("landlord-a", "B", map[string]any{"price_vnd": 1})
	e.addMedia(e.buildingMediaPath(b.ID), tinyWebP(t), "", "landlord-a")
	if len(e.files.Keys()) != 2 {
		t.Fatalf("files: %v", e.files.Keys())
	}
	expectStatus(t, e.call(http.MethodDelete, "/v1/buildings/"+b.ID.String(), nil, "landlord-a"), http.StatusNoContent)
	if keys := e.files.Keys(); len(keys) != 0 {
		t.Errorf("files outlive their building: %v", keys)
	}
}

func TestMediaFailureAfterStorageLeavesNoFiles(t *testing.T) {
	e := newPropertyEnv(t, nil)
	room := e.roomAPI("landlord-a", map[string]any{"name": "A1", "address_detail": "x", "price_vnd": 1})
	e.plans.set(failingPlan{})
	w := e.upload(http.MethodPost, e.roomMediaPath(room.ID), tinyWebP(t), "", "landlord-a")
	expectStatus(t, w, http.StatusInternalServerError)
	if keys := e.files.Keys(); len(keys) != 0 {
		t.Errorf("a transaction that fails deletes the files it staged: %v", keys)
	}
}

type failingPlan struct{ usecase.UnlimitedPlans }

func (failingPlan) MaxMediaPerOwner(context.Context, uuid.UUID) (*int, error) {
	return nil, errors.New("plan lookup failed")
}

func TestServeMedia(t *testing.T) {
	e := newPropertyEnv(t, nil)
	room := e.roomAPI("landlord-a", map[string]any{"name": "A1", "address_detail": "x", "price_vnd": 1})
	m := e.addMedia(e.roomMediaPath(room.ID), tinyWebP(t), "", "landlord-a")
	key := storedKey(m.URL)

	get := func(path string, header map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for k, v := range header {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		e.engine.ServeHTTP(w, req)
		return w
	}
	w := get("/v1/media/"+key, nil) // no bearer: the route is public
	expectStatus(t, w, http.StatusOK)
	h := w.Header()
	if h.Get("Content-Type") != "image/webp" || h.Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		h.Get("ETag") != `"`+key[strings.LastIndex(key, "/")+1:]+`"` || h.Get("Cross-Origin-Resource-Policy") != "cross-origin" ||
		h.Get("Content-Security-Policy") != "default-src 'none'" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers: %v", h)
	}
	if !bytes.Equal(w.Body.Bytes(), e.storedBytes(m.URL)) || h.Get("Content-Length") == "" {
		t.Error("body is the stored file")
	}
	etag := h.Get("ETag")
	w = get("/v1/media/"+key, map[string]string{"If-None-Match": etag})
	expectStatus(t, w, http.StatusNotModified)
	if w.Body.Len() != 0 || w.Header().Get("ETag") != etag || w.Header().Get("Cache-Control") == "" {
		t.Errorf("304 keeps the validators and has no body: %q %v", w.Body.String(), w.Header())
	}
	expectStatus(t, get("/v1/media/"+key, map[string]string{"If-None-Match": `"other"`}), http.StatusOK)
	thumb := get("/v1/media/"+storedKey(m.ThumbURL), nil)
	expectStatus(t, thumb, http.StatusOK)
	if thumb.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("thumbnail type: %s", thumb.Header().Get("Content-Type"))
	}
	// HEAD works the same.
	req := httptest.NewRequest(http.MethodHead, "/v1/media/"+key, nil)
	hw := httptest.NewRecorder()
	e.engine.ServeHTTP(hw, req)
	expectStatus(t, hw, http.StatusOK)

	for _, bad := range []string{
		"/v1/media/property/2026/11/" + strings.Repeat("0", 32) + ".webp", // unknown
		"/v1/media/property/../etc/passwd",
		"/v1/media/property/2026/11/ABC.webp",
		"/v1/media/other/2026/11/abc.webp",
		"/v1/media/" + strings.TrimSuffix(key, ".webp") + ".png",
		"/v1/media/",
	} {
		if w := get(bad, nil); w.Code != http.StatusNotFound {
			t.Errorf("%s: want 404, got %d", bad, w.Code)
		}
	}
}
