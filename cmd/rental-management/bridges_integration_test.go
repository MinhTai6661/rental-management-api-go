//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/config"
	adminhttp "rental-management/internal/adapter/http/admin"
	"rental-management/internal/adapter/http/apperr"
	audithttp "rental-management/internal/adapter/http/audit"
	contracthttp "rental-management/internal/adapter/http/contract"
	"rental-management/internal/adapter/http/httpx"
	listinghttp "rental-management/internal/adapter/http/listing"
	notificationhttp "rental-management/internal/adapter/http/notification"
	propertyhttp "rental-management/internal/adapter/http/property"
	"rental-management/internal/adapter/http/testenv"
	"rental-management/internal/domain/identity"
	contractuc "rental-management/internal/usecase/contract"
	"rental-management/pkg/authz"
	"rental-management/pkg/i18n"
	"rental-management/pkg/ratelimit"
	"rental-management/pkg/storage"
)

// The bridges are tested through the real composition code (newContracts, newProperty) and the HTTP routes of both
// contexts: what a landlord does to a room through the property API must reach the contracts, and what a contract
// does must reach the room.

type fakeAuth map[string]authz.Principal

func (f fakeAuth) Authenticate(_ context.Context, bearer string) (authz.Principal, error) {
	if p, ok := f[bearer]; ok {
		return p, nil
	}
	return authz.Principal{}, fmt.Errorf("unknown bearer %q", bearer)
}

type allowAll struct{}

func (allowAll) Allow(context.Context, string, int, time.Duration) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

type bridgeEnv struct {
	t         *testing.T
	pool      *pgxpool.Pool
	engine    *gin.Engine
	inbox     notifications
	plans     plans
	offers    listings
	rentals   contracts
	props     property
	staff     admin
	trail     auditing
	auth      fakeAuth
	landlord  uuid.UUID
	landlord2 uuid.UUID
	tenant    uuid.UUID
	admin     uuid.UUID
	super     uuid.UUID
}

func principalOf(id uuid.UUID, role string) authz.Principal {
	perms := map[string]struct{}{}
	for _, code := range identity.RolePermissions()[role] {
		perms[code] = struct{}{}
	}
	return authz.Principal{UserID: id.String(), Role: role, Permissions: perms}
}

func newBridgeEnv(t *testing.T) *bridgeEnv {
	t.Helper()
	ctx := context.Background()
	e := &bridgeEnv{t: t, pool: pg.NewDB(t)}
	for _, q := range []string{
		`INSERT INTO provinces (code, name, administrative_level, slug) VALUES (1, 'Ha Noi', 'city', 'ha-noi')`,
		`INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (4, 1, 'Ba Dinh', 'ward', 'ba-dinh')`,
	} {
		if _, err := e.pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO roles (code, seniority, is_staff) VALUES ('super_admin', 1, true), ('admin', 2, true), ('landlord', 3, false), ('tenant', 5, false)`); err != nil {
		t.Fatal(err)
	}
	user := func(email string, roles ...string) uuid.UUID {
		var id uuid.UUID
		if err := e.pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ($1, 'N') RETURNING id`, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, r := range roles {
			if _, err := e.pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_code) VALUES ($1, $2)`, id, r); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	e.landlord, e.tenant = user("l@example.vn", "landlord", "tenant"), user("t@example.vn", "tenant")
	e.landlord2, e.admin = user("l2@example.vn", "landlord", "tenant"), user("a@example.vn", "admin")
	e.super = user("s@example.vn", "super_admin")

	cfg := config.Config{
		MediaMaxUploadBytes: 1 << 20, MediaMaxDimension: 2048, AnnouncementImageMaxPlatform: 10, AnnouncementImageMaxLandlord: 3, MediaThumbnailMaxSize: 480, ImageProcessingConcurrency: 2, MediaMaxPerOwner: 10,
	}
	log := slog.New(slog.DiscardHandler)
	files := storage.NewMemory("http://files.test/v1/media")
	e.inbox = newNotifications(cfg, log, e.pool, files, nil)
	e.plans = newPlans(log, e.pool, e.inbox.deliveries)
	e.offers = newListings(log, e.pool, e.plans, e.inbox.deliveries)
	e.rentals = newContracts(cfg, log, e.pool, e.plans, invitationPorts{}, e.inbox.deliveries)
	e.props = newProperty(cfg, log, e.pool, files, e.rentals, e.offers, e.plans)
	e.trail = newAudit(cfg, log, e.pool)
	e.staff = newAdmin(cfg, log, e.pool, adminPorts{notify: e.inbox.deliveries, buildings: e.props.buildings})

	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	auth := fakeAuth{
		"landlord":  principalOf(e.landlord, authz.RoleLandlord),
		"landlord2": principalOf(e.landlord2, authz.RoleLandlord),
		"tenant":    principalOf(e.tenant, authz.RoleTenant),
		// the landlord account acting in its other role: the same person, the other inbox
		"landlord-as-tenant": principalOf(e.landlord, authz.RoleTenant),
		"admin":              principalOf(e.admin, authz.RoleAdmin),
		"super":              principalOf(e.super, authz.RoleSuperAdmin),
	}
	e.auth = auth
	e.engine = gin.New()
	e.engine.Use(httpx.RequestID(log), httpx.Locale(catalog), httpx.Recover())
	binder := httpx.NewBinder()
	v1 := e.engine.Group("/v1")
	media := propertyhttp.NewMediaHandler(e.props.media, files, binder, cfg.MediaMaxUploadBytes)
	propertyhttp.MountProperty(v1,
		propertyhttp.NewBuildingHandler(e.props.buildings, files, binder), propertyhttp.NewRoomHandler(e.props.rooms, files, binder), media, auth, allowAll{})
	images := notificationhttp.NewImageHandler(e.inbox.images, files, binder, cfg.MediaMaxUploadBytes)
	mountMedia(v1, media.Serve, images.Serve)
	contracthttp.MountContracts(v1,
		contracthttp.NewContractHandler(e.rentals.contracts, files, binder), contracthttp.NewRentalHandler(e.rentals.rentals, files),
		contracthttp.NewInvitationHandler(e.rentals.invitations, e.rentals.answers, contractuc.ClockFunc(time.Now), files, binder), auth, allowAll{})
	listinghttp.MountListings(v1,
		listinghttp.NewListingHandler(e.offers.listings, files, binder), listinghttp.NewReviewHandler(e.offers.reviews, files, binder), auth, allowAll{})
	notificationhttp.MountNotifications(v1, notificationhttp.NewInboxHandler(e.inbox.inbox, binder), auth)
	notificationhttp.MountPopups(v1, notificationhttp.NewPopupHandler(e.inbox.popups, files), auth, allowAll{})
	notificationhttp.MountAnnouncements(v1, notificationhttp.NewAnnouncementHandler(e.inbox.announcements, files, binder), images,
		notificationhttp.NewStatsHandler(e.inbox.stats), auth, allowAll{})
	listinghttp.MountSearch(v1, listinghttp.NewOfferHandler(e.offers.offers, files), auth, allowAll{})
	apps := adminhttp.NewApplicationHandler(e.staff.applications, binder)
	legal := adminhttp.NewLegalHandler(e.staff.legal, binder)
	adminhttp.MountAdmin(v1, adminhttp.NewUserHandler(e.staff.users, binder), apps, adminhttp.NewSettingsHandler(e.staff.settings, binder), legal, auth, allowAll{})
	audithttp.Mount(v1, audithttp.NewHandler(e.trail.reader), auth)
	adminhttp.MountSelf(v1, apps, adminhttp.NewProfileHandler(e.staff.profiles, binder), legal, adminhttp.NewPersonalDataHandler(e.staff.personal, binder), auth, allowAll{})
	return e
}

func (e *bridgeEnv) call(method, path string, body any, bearer string) *httptest.ResponseRecorder {
	e.t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			e.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en")
	req.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

type room struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Status string    `json:"status"`
}

func (e *bridgeEnv) newRoom(name, status string) room { return e.newRoomFor("landlord", name, status) }

// newRoomFor creates a room of the landlord whose bearer is given.
func (e *bridgeEnv) newRoomFor(bearer, name, status string) room {
	e.t.Helper()
	w := e.call(http.MethodPost, "/v1/rooms", map[string]any{"name": name, "address_detail": "12 Le Loi", "price_vnd": 2_000_000, "status": status}, bearer)
	testenv.ExpectStatus(e.t, w, http.StatusCreated)
	return testenv.DecodeData[room](e.t, w)
}

type listingRow struct {
	ID     uuid.UUID `json:"id"`
	Status string    `json:"status"`
}

// publish writes a listing for the room, sends it and has an admin approve it, all through the API: the room is then
// on find rooms (while it is vacant) and a tenant may register for it.
func (e *bridgeEnv) publish(bearer string, roomID uuid.UUID) listingRow {
	e.t.Helper()
	created := e.call(http.MethodPost, "/v1/my/listings", map[string]any{"room_id": roomID, "title": "Room for rent"}, bearer)
	testenv.ExpectStatus(e.t, created, http.StatusCreated)
	l := testenv.DecodeData[listingRow](e.t, created)
	testenv.ExpectStatus(e.t, e.call(http.MethodPost, "/v1/my/listings/"+l.ID.String()+"/submit", nil, bearer), http.StatusOK)
	approved := e.call(http.MethodPost, "/v1/admin/listings/"+l.ID.String()+"/approve", nil, "admin")
	testenv.ExpectStatus(e.t, approved, http.StatusOK)
	return testenv.DecodeData[listingRow](e.t, approved)
}

func (e *bridgeEnv) room(id uuid.UUID) room {
	e.t.Helper()
	w := e.call(http.MethodGet, "/v1/rooms/"+id.String(), nil, "landlord")
	testenv.ExpectStatus(e.t, w, http.StatusOK)
	return testenv.DecodeData[room](e.t, w)
}

// setStatus edits the room through the API (a full replacement of its fields) with a new status.
func (e *bridgeEnv) setStatus(r room, status string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.call(http.MethodPut, "/v1/rooms/"+r.ID.String(), map[string]any{"name": r.Name, "address_detail": "12 Le Loi", "price_vnd": 2_000_000, "status": status}, "landlord")
}

type contractRow struct {
	ID         uuid.UUID `json:"id"`
	Status     string    `json:"status"`
	PriceVND   int64     `json:"price_vnd"`
	Unassigned bool      `json:"unassigned"`
	EndsOn     *string   `json:"ends_on"`
	Tenant     *struct {
		ID uuid.UUID `json:"id"`
	} `json:"tenant"`
	Room struct {
		ID uuid.UUID `json:"id"`
	} `json:"room"`
}

// contractsOf lists the landlord's contracts of one room through the API.
func (e *bridgeEnv) contractsOf(roomID uuid.UUID) []contractRow {
	e.t.Helper()
	w := e.call(http.MethodGet, "/v1/contracts?room_id="+roomID.String(), nil, "landlord")
	testenv.ExpectStatus(e.t, w, http.StatusOK)
	return testenv.DecodeData[[]contractRow](e.t, w)
}

func TestMarkingARoomRentedOpensABlankContractAndReleasingItClosesIt(t *testing.T) {
	e := newBridgeEnv(t)

	// A room created rented gets its blank contract at once.
	born := e.newRoom("R1", "rented")
	rows := e.contractsOf(born.ID)
	if len(rows) != 1 || !rows[0].Unassigned || rows[0].Tenant != nil || rows[0].Status != "active" || rows[0].PriceVND != 2_000_000 {
		t.Fatalf("a room born rented: %+v", rows)
	}

	// An available room marked rented through an edit.
	room := e.newRoom("R2", "available")
	if len(e.contractsOf(room.ID)) != 0 {
		t.Fatal("an available room has no contract")
	}
	testenv.ExpectStatus(t, e.setStatus(room, "rented"), http.StatusOK)
	rows = e.contractsOf(room.ID)
	if len(rows) != 1 || !rows[0].Unassigned || rows[0].Status != "active" {
		t.Fatalf("after marking it rented: %+v", rows)
	}
	// Saving it rented again does not open a second one.
	testenv.ExpectStatus(t, e.setStatus(room, "rented"), http.StatusOK)
	if got := e.contractsOf(room.ID); len(got) != 1 {
		t.Errorf("still one blank contract, got %d", len(got))
	}
	// A blank contract does not lock the room: it can leave rented, and the contract ends today.
	testenv.ExpectStatus(t, e.setStatus(room, "available"), http.StatusOK)
	rows = e.contractsOf(room.ID)
	if len(rows) != 1 || rows[0].Status != "ended" || rows[0].EndsOn == nil {
		t.Fatalf("after releasing it: %+v", rows)
	}
	// Rented again: a new blank contract (the ended one is history).
	testenv.ExpectStatus(t, e.setStatus(room, "rented"), http.StatusOK)
	if got := e.contractsOf(room.ID); len(got) != 2 {
		t.Errorf("a second blank contract, got %d", len(got))
	}

	// Bulk status: rooms marked rented together each get one; moving them away closes them.
	a, b := e.newRoom("B1", "available"), e.newRoom("B2", "available")
	w := e.call(http.MethodPost, "/v1/rooms/bulk-status", map[string]any{"ids": []uuid.UUID{a.ID, b.ID}, "status": "rented"}, "landlord")
	testenv.ExpectStatus(t, w, http.StatusOK)
	if len(e.contractsOf(a.ID)) != 1 || len(e.contractsOf(b.ID)) != 1 {
		t.Error("each room gets its blank contract")
	}
	e.call(http.MethodPost, "/v1/rooms/bulk-status", map[string]any{"ids": []uuid.UUID{a.ID, b.ID}, "status": "maintenance"}, "landlord")
	if got := e.contractsOf(a.ID); got[0].Status != "ended" {
		t.Errorf("released = %+v", got)
	}

	// Deleting a rented room (only a blank contract on it) works and ends the blank contract.
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/rooms/"+born.ID.String(), nil, "landlord"), http.StatusNoContent)
	var status string
	if err := e.pool.QueryRow(t.Context(), `SELECT status FROM contracts WHERE room_id = $1`, born.ID).Scan(&status); err != nil || status != "ended" {
		t.Errorf("the blank contract of a deleted room ends: %q, %v", status, err)
	}
}

func TestARoomWithATenantContractIsLockedAndFollowsItsContract(t *testing.T) {
	e := newBridgeEnv(t)
	room := e.newRoom("R1", "available")
	listing := e.publish("landlord", room.ID)
	reg := e.call(http.MethodPost, "/v1/contracts/register", map[string]any{"room_id": room.ID, "starts_on": time.Now().In(time.FixedZone("ICT", 7*3600)).Format("2006-01-02")}, "tenant")
	testenv.ExpectStatus(t, reg, http.StatusCreated)
	c := testenv.DecodeData[contractRow](t, reg)
	if e.room(room.ID).Status != "available" {
		t.Fatal("a waiting registration leaves the room available")
	}

	// Approving rents the room through the property use case.
	approved := e.call(http.MethodPut, "/v1/contracts/"+c.ID.String(), map[string]any{"status": "active"}, "landlord")
	testenv.ExpectStatus(t, approved, http.StatusOK)
	if e.room(room.ID).Status != "rented" {
		t.Fatalf("the room must be rented, got %s", e.room(room.ID).Status)
	}
	if got := e.contractsOf(room.ID); len(got) != 1 {
		t.Errorf("no blank contract beside a tenant's: %+v", got)
	}

	// The running contract locks the room: no status change away from rented, no delete.
	testenv.ExpectError(t, e.setStatus(room, "available"), http.StatusConflict, apperr.CodeRoomStatusLocked)
	testenv.ExpectError(t, e.setStatus(room, "maintenance"), http.StatusConflict, apperr.CodeRoomStatusLocked)
	testenv.ExpectStatus(t, e.setStatus(room, "rented"), http.StatusOK)
	testenv.ExpectError(t, e.call(http.MethodDelete, "/v1/rooms/"+room.ID.String(), nil, "landlord"), http.StatusConflict, apperr.CodeRoomHasContract)

	// The bulk routes report the room as refused, with its code.
	w := e.call(http.MethodPost, "/v1/rooms/bulk-delete", map[string]any{"ids": []uuid.UUID{room.ID}}, "landlord")
	var del struct {
		Data propertyhttp.BulkView `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &del)
	if len(del.Data.Done) != 0 || len(del.Data.Failed) != 1 || del.Data.Failed[0].Code != apperr.CodeRoomHasContract {
		t.Errorf("bulk delete = %s", w.Body.String())
	}
	w = e.call(http.MethodPost, "/v1/rooms/bulk-status", map[string]any{"ids": []uuid.UUID{room.ID}, "status": "available"}, "landlord")
	_ = json.Unmarshal(w.Body.Bytes(), &del)
	if len(del.Data.Failed) != 1 || del.Data.Failed[0].Code != apperr.CodeRoomStatusLocked {
		t.Errorf("bulk status = %s", w.Body.String())
	}

	// Ending the contract frees the room, and then it can be changed and (once its listing is taken down) deleted.
	ended := e.call(http.MethodPost, "/v1/contracts/"+c.ID.String()+"/terminate", nil, "landlord")
	testenv.ExpectStatus(t, ended, http.StatusOK)
	if e.room(room.ID).Status != "available" {
		t.Fatalf("the room must be free again, got %s", e.room(room.ID).Status)
	}
	testenv.ExpectError(t, e.call(http.MethodDelete, "/v1/rooms/"+room.ID.String(), nil, "landlord"), http.StatusConflict, apperr.CodeRoomHasListing)
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/my/listings/"+listing.ID.String()+"/unpublish", nil, "landlord"), http.StatusOK)
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/rooms/"+room.ID.String(), nil, "landlord"), http.StatusNoContent)
}

func TestACancellationRequestApprovedFreesTheRoomThroughTheBridge(t *testing.T) {
	e := newBridgeEnv(t)
	room := e.newRoom("R1", "available")
	e.publish("landlord", room.ID)
	reg := e.call(http.MethodPost, "/v1/contracts/register", map[string]any{"room_id": room.ID, "starts_on": time.Now().In(time.FixedZone("ICT", 7*3600)).Format("2006-01-02")}, "tenant")
	c := testenv.DecodeData[contractRow](t, reg)
	e.call(http.MethodPut, "/v1/contracts/"+c.ID.String(), map[string]any{"status": "active"}, "landlord")
	path := "/v1/contracts/" + c.ID.String() + "/cancellation-request"
	testenv.ExpectStatus(t, e.call(http.MethodPost, path, nil, "tenant"), http.StatusOK)
	testenv.ExpectStatus(t, e.call(http.MethodPost, path+"/approve", nil, "landlord"), http.StatusOK)
	if got := e.room(room.ID).Status; got != "available" {
		t.Errorf("room = %s", got)
	}
}

func TestATenantContractDisplacesTheBlankOneAndTheRoomStaysRented(t *testing.T) {
	e := newBridgeEnv(t)
	room := e.newRoom("R1", "rented")
	// A registration of the past (the room is rented now, so nobody can register for it any more).
	var pending uuid.UUID
	err := e.pool.QueryRow(t.Context(), `
		INSERT INTO contracts (room_id, landlord_id, tenant_id, status, price_vnd, starts_on, confirmed_at, display_order)
		VALUES ($1, $2, $3, 'pending', 2000000, current_date, now(), -5) RETURNING id`, room.ID, e.landlord, e.tenant).Scan(&pending)
	if err != nil {
		t.Fatal(err)
	}
	testenv.ExpectStatus(t, e.call(http.MethodPut, "/v1/contracts/"+pending.String(), map[string]any{"status": "active"}, "landlord"), http.StatusOK)

	byStatus := map[string]int{}
	var unassignedEnded bool
	for _, r := range e.contractsOf(room.ID) {
		byStatus[r.Status]++
		unassignedEnded = unassignedEnded || (r.Unassigned && r.Status == "ended")
	}
	if byStatus["active"] != 1 || byStatus["ended"] != 1 || !unassignedEnded {
		t.Errorf("contracts = %v (blank ended: %v)", byStatus, unassignedEnded)
	}
	if e.room(room.ID).Status != "rented" {
		t.Error("the room stays rented")
	}
	// Now the tenant's contract locks it.
	testenv.ExpectError(t, e.setStatus(room, "available"), http.StatusConflict, apperr.CodeRoomStatusLocked)
}

func TestSeedDemoGivesTheDemoTenantContractsAndIsSafeToRepeat(t *testing.T) {
	e := newBridgeEnv(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := e.props.seeder.SeedDemo(ctx, e.landlord); err != nil {
			t.Fatal(err)
		}
		if _, err := e.offers.seeder.SeedDemo(ctx, e.landlord, &e.admin); err != nil {
			t.Fatal(err)
		}
		if err := e.rentals.seeder.SeedDemo(ctx, e.landlord, e.tenant); err != nil {
			t.Fatal(err)
		}
	}
	w := e.call(http.MethodGet, "/v1/me/contracts", nil, "tenant")
	testenv.ExpectStatus(t, w, http.StatusOK)
	var body struct {
		Data []struct {
			Status string `json:"status"`
			Room   struct {
				Name string `json:"name"`
			} `json:"room"`
		} `json:"data"`
		Meta struct {
			Total  int            `json:"total"`
			Counts map[string]int `json:"counts"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Meta.Total != 3 || body.Meta.Counts["active"] != 1 || body.Meta.Counts["pending"] != 1 || body.Meta.Counts["history"] != 1 {
		t.Fatalf("demo contracts = %s", w.Body.String())
	}
	// The demo room of the running contract is rented, and the property API now refuses to free it.
	var rented string
	if err := e.pool.QueryRow(ctx, `SELECT status FROM rooms WHERE name = 'P101'`).Scan(&rented); err != nil || rented != "rented" {
		t.Errorf("P101 = %q, %v", rented, err)
	}
	var blank int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM contracts WHERE tenant_id IS NULL`).Scan(&blank)
	if blank != 0 {
		t.Errorf("no blank contract beside the demo contract, got %d", blank)
	}
}

// Deleting a room through the property API withdraws the invitations still waiting on it, and leaves the answered ones.
func TestDeletingARoomWithdrawsItsOpenInvitations(t *testing.T) {
	e := newBridgeEnv(t)
	ctx := context.Background()
	r := e.newRoom("P1", "available")
	insert := func(status string, hash string) uuid.UUID {
		var id uuid.UUID
		err := e.pool.QueryRow(ctx, `
			INSERT INTO contract_invitations (room_id, landlord_id, status, token_hash, link_token_sealed, expires_at, price_vnd, starts_on, answered_at)
			VALUES ($1, $2, $3, $4, CASE WHEN $3 = 'pending' THEN 'sealed' END, now() + interval '7 days', 2000000, current_date,
			        CASE WHEN $3 = 'pending' THEN NULL ELSE now() END)
			RETURNING id`, r.ID, e.landlord, status, strings.Repeat(hash, 64)).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	open, declined := insert("pending", "a"), insert("declined", "b")

	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/rooms/"+r.ID.String(), nil, "landlord"), http.StatusNoContent)

	state := func(id uuid.UUID) (status string, sealed *string) {
		if err := e.pool.QueryRow(ctx, `SELECT status, link_token_sealed FROM contract_invitations WHERE id = $1`, id).Scan(&status, &sealed); err != nil {
			t.Fatal(err)
		}
		return status, sealed
	}
	if st, sealed := state(open); st != "revoked" || sealed != nil {
		t.Errorf("the waiting offer is withdrawn: %s %v", st, sealed)
	}
	if st, _ := state(declined); st != "declined" {
		t.Errorf("an answered offer stays as it was: %s", st)
	}
}
