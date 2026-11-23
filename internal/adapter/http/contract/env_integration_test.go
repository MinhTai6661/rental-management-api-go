//go:build integration

package contracthttp_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	contracthttp "rental-management/internal/adapter/http/contract"
	"rental-management/internal/adapter/http/httpx"
	"rental-management/internal/adapter/http/testenv"
	"rental-management/internal/adapter/persistence/postgres"
	contractpg "rental-management/internal/adapter/persistence/postgres/contract"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/identity"
	"rental-management/internal/domain/invitation"
	usecase "rental-management/internal/usecase/contract"
	"rental-management/pkg/authz"
	"rental-management/pkg/i18n"
	"rental-management/pkg/ratelimit"
	"rental-management/pkg/secretbox"
)

// The response bodies under the names the contract tests use.
type (
	ContractView = contracthttp.ContractView
	RentalView   = contracthttp.RentalView
	RentalDetail = contracthttp.RentalDetailView
	BulkView     = contracthttp.BulkView
)

var (
	expectError  = testenv.ExpectError
	expectStatus = testenv.ExpectStatus
)

func decodeData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	return testenv.DecodeData[T](t, w)
}

// fakeAuth maps a bearer string to a caller, so the tests need no login.
type fakeAuth map[string]authz.Principal

func (f fakeAuth) Authenticate(_ context.Context, bearer string) (authz.Principal, error) {
	if p, ok := f[bearer]; ok {
		return p, nil
	}
	return authz.Principal{}, fmt.Errorf("unknown bearer %q", bearer)
}

// keyLimiter never limits and remembers the keys it was asked to count.
type keyLimiter struct {
	mu   sync.Mutex
	keys []string
}

func (k *keyLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (ratelimit.Result, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.keys = append(k.keys, key)
	return ratelimit.Result{Allowed: true}, nil
}

func (k *keyLimiter) seen(key string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return strings.Contains(strings.Join(k.keys, "\n"), key)
}

// testInvitees stands in for the invitation context: the tenants it knows are invitable, anything else is not.
type testInvitees struct {
	byID    map[uuid.UUID]invitation.Person
	byEmail map[string]uuid.UUID
	ownMail string
	blocked map[string]bool
}

func (f *testInvitees) Resolve(_ context.Context, _ uuid.UUID, id *uuid.UUID, email *string) (invitation.Addressee, error) {
	var byID, byEmail *invitation.Person
	if id != nil {
		p, ok := f.byID[*id]
		if !ok {
			return invitation.Addressee{}, invitation.ErrRecipientInvalid
		}
		byID = &p
	}
	var normalized *string
	if email != nil && strings.TrimSpace(*email) != "" {
		n := invitation.NormalizeEmail(*email)
		normalized = &n
		if n == f.ownMail {
			return invitation.Addressee{}, invitation.ErrOwnEmail
		}
		if uid, ok := f.byEmail[n]; ok {
			p := f.byID[uid]
			byEmail = &p
		}
	}
	if byID != nil && byEmail != nil && byID.ID != byEmail.ID {
		return invitation.Addressee{}, invitation.ErrRecipientMismatch
	}
	if byID == nil {
		byID = byEmail
	}
	if byID != nil {
		return invitation.Addressee{Recipient: byID}, nil
	}
	return invitation.Addressee{Email: normalized}, nil
}

func (f *testInvitees) Person(_ context.Context, id uuid.UUID) (invitation.Person, error) {
	return f.byID[id], nil
}

func (f *testInvitees) MayMail(_ context.Context, email string) (bool, error) {
	return !f.blocked[invitation.NormalizeEmail(email)], nil
}

// randomTokens makes real link secrets: 64 base64url characters and the SHA-256 of them.
type randomTokens struct{}

func (randomTokens) New() (string, string, error) {
	b := make([]byte, 48)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, randomTokens{}.Hash(token), nil
}

func (randomTokens) Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// mails records the invitation emails.
type mails struct{ sent []usecase.InvitationMail }

func (m *mails) SendInvitation(_ context.Context, mail usecase.InvitationMail) error {
	m.sent = append(m.sent, mail)
	return nil
}

// sqlRooms stands in for the property context: it changes a room's status straight in the table.
type sqlRooms struct{ pool *pgxpool.Pool }

func (s sqlRooms) SetStatus(ctx context.Context, landlordID, roomID uuid.UUID, status string) error {
	return postgres.Queries(ctx, s.pool).SetRoomStatus(ctx, sqlcgen.SetRoomStatusParams{Status: status, LandlordID: landlordID, Ids: []uuid.UUID{roomID}})
}

type notices struct{ sent []usecase.Notification }

func (n *notices) Notify(_ context.Context, m usecase.Notification) error {
	n.sent = append(n.sent, m)
	return nil
}

// movableClock is the real time plus an offset the test can move forward (to let an invitation expire).
type movableClock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *movableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.offset)
}

func (c *movableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offset += d
}

type fakeURLs struct{}

func (fakeURLs) URL(key string) string { return "http://files.test/" + key }

// contractEnv is one test's stack: a fresh database, two landlords, two tenants and an admin, the real repositories
// and use cases, and the HTTP router with the contract routes mounted.
type contractEnv struct {
	t        *testing.T
	pool     *pgxpool.Pool
	engine   *gin.Engine
	notices  *notices
	limiter  *keyLimiter
	invitees *testInvitees
	mails    *mails
	clock    *movableClock
	landlA   uuid.UUID
	landlB   uuid.UUID
	tenantT  uuid.UUID
	tenantU  uuid.UUID
}

func principal(id uuid.UUID, role string) authz.Principal {
	perms := map[string]struct{}{}
	for _, code := range identity.RolePermissions()[role] {
		perms[code] = struct{}{}
	}
	return authz.Principal{UserID: id.String(), Role: role, Permissions: perms}
}

func newContractEnv(t *testing.T, plans usecase.PlanLimits) *contractEnv {
	t.Helper()
	ctx := context.Background()
	e := &contractEnv{t: t, pool: pg.NewDB(t), notices: &notices{}, limiter: &keyLimiter{}, mails: &mails{}, clock: &movableClock{}}
	for _, q := range []string{
		`INSERT INTO provinces (code, name, administrative_level, slug) VALUES (1, 'Ha Noi', 'city', 'ha-noi')`,
		`INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (4, 1, 'Ba Dinh', 'ward', 'ba-dinh')`,
	} {
		if _, err := e.pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	user := func(email, name string) uuid.UUID {
		var id uuid.UUID
		if err := e.pool.QueryRow(ctx, `INSERT INTO users (email, full_name, phone_number) VALUES ($1, $2, '0901234567') RETURNING id`, email, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	e.landlA, e.landlB = user("a@example.vn", "Landlord A"), user("b@example.vn", "Landlord B")
	e.tenantT, e.tenantU = user("t@example.vn", "Tenant T"), user("u@example.vn", "Tenant U")
	admin := user("m@example.vn", "Admin")

	auth := fakeAuth{
		"landlord-a": principal(e.landlA, authz.RoleLandlord),
		"landlord-b": principal(e.landlB, authz.RoleLandlord),
		"tenant-t":   principal(e.tenantT, authz.RoleTenant),
		"tenant-u":   principal(e.tenantU, authz.RoleTenant),
		// Landlord A acting as a tenant (a landlord also holds the tenant role).
		"landlord-a-as-tenant": principal(e.landlA, authz.RoleTenant),
		"admin":                principal(admin, authz.RoleAdmin),
		// A tenant whose role grants no permission at all.
		"tenant-nothing": {UserID: e.tenantT.String(), Role: authz.RoleTenant, Permissions: map[string]struct{}{}},
	}
	if plans == nil {
		plans = usecase.UnlimitedPlans{}
	}
	person := func(id uuid.UUID, name, email string) invitation.Person {
		return invitation.Person{ID: id, FullName: name, Email: &email, EmailVerified: true, Active: true, AcceptsInvitations: true, Roles: []string{authz.RoleTenant}}
	}
	e.invitees = &testInvitees{
		byID:    map[uuid.UUID]invitation.Person{e.tenantT: person(e.tenantT, "Tenant T", "t@example.vn"), e.tenantU: person(e.tenantU, "Tenant U", "u@example.vn")},
		byEmail: map[string]uuid.UUID{"t@example.vn": e.tenantT, "u@example.vn": e.tenantU},
		ownMail: "a@example.vn", blocked: map[string]bool{},
	}
	key := make([]byte, 32)
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatal(err)
	}
	deps := usecase.Deps{
		Tx: postgres.NewTxManager(e.pool), Contracts: contractpg.NewContractRepository(e.pool), Rooms: contractpg.NewRoomReader(e.pool),
		RoomState: sqlRooms{pool: e.pool}, Notifier: e.notices, Plans: plans, Clock: e.clock,
		Invitations: contractpg.NewInvitationRepository(e.pool), Invitees: e.invitees, Tokens: randomTokens{}, Sealer: box, Mailer: e.mails,
	}
	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	e.engine = gin.New()
	e.engine.Use(httpx.RequestID(slog.New(slog.DiscardHandler)), httpx.Locale(catalog), httpx.Recover())
	binder := httpx.NewBinder()
	contracthttp.MountContracts(e.engine.Group("/v1"),
		contracthttp.NewContractHandler(usecase.NewContracts(deps, usecase.Options{}), fakeURLs{}, binder),
		contracthttp.NewRentalHandler(usecase.NewRentals(deps, usecase.Options{}), fakeURLs{}),
		contracthttp.NewInvitationHandler(usecase.NewInvitations(deps, usecase.Options{WebURL: "http://web.test"}),
			usecase.NewInvitationAnswers(deps, usecase.Options{WebURL: "http://web.test"}), e.clock, fakeURLs{}, binder),
		auth, e.limiter)
	return e
}

// call sends a JSON request (body nil for none) with an optional bearer token and language.
func (e *contractEnv) call(method, path string, body any, bearer string) *httptest.ResponseRecorder {
	return e.callIn("en", method, path, body, bearer)
}

func (e *contractEnv) callIn(lang, method, path string, body any, bearer string) *httptest.ResponseRecorder {
	e.t.Helper()
	var raw []byte
	switch b := body.(type) {
	case nil:
	case string:
		raw = []byte(b)
	default:
		var err error
		if raw, err = json.Marshal(body); err != nil {
			e.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", lang)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

// room inserts a free room of the landlord, with an approved listing, straight into the tables (only a room that is
// on offer can be registered for).
func (e *contractEnv) room(landlord uuid.UUID, name string) uuid.UUID {
	e.t.Helper()
	id := e.unlistedRoom(landlord, name)
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO listings (room_id, landlord_id, title, status, submitted_at, published_at)
		VALUES ($1, $2, 'For rent', 'approved', now(), now())`, id, landlord); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// unlistedRoom inserts a free room of the landlord that has no listing.
func (e *contractEnv) unlistedRoom(landlord uuid.UUID, name string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(context.Background(), `
		INSERT INTO rooms (landlord_id, name, ward_code, address_detail, price_vnd, display_order, cover_key, cover_thumb_key)
		VALUES ($1, $2, 4, '12 Le Loi', 3000000, (SELECT coalesce(max(display_order), 0) + 1 FROM rooms), 'c.webp', 'c_t.webp')
		RETURNING id`, landlord, name).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *contractEnv) roomStatus(id uuid.UUID) string {
	e.t.Helper()
	var s string
	if err := e.pool.QueryRow(context.Background(), `SELECT status FROM rooms WHERE id = $1`, id).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func today() time.Time { return domain.Today(time.Now(), time.FixedZone("ICT", 7*3600)) }

func dateStr(t time.Time) string { return t.Format(domain.DateLayout) }

// register sends a registration for the room as the tenant and returns the contract.
func (e *contractEnv) register(bearer string, room uuid.UUID, extra map[string]any) ContractView {
	e.t.Helper()
	body := map[string]any{"room_id": room, "starts_on": dateStr(today())}
	for k, v := range extra {
		body[k] = v
	}
	w := e.call(http.MethodPost, "/v1/contracts/register", body, bearer)
	expectStatus(e.t, w, http.StatusCreated)
	return decodeData[ContractView](e.t, w)
}

func (e *contractEnv) put(bearer string, id uuid.UUID, body any) *testResponse {
	return &testResponse{e.call(http.MethodPut, "/v1/contracts/"+id.String(), body, bearer)}
}

// running makes the room's contract running through the API (as the landlord approving a registration).
func (e *contractEnv) approve(bearer string, id uuid.UUID) ContractView {
	e.t.Helper()
	w := e.put(bearer, id, map[string]any{"status": "active"})
	expectStatus(e.t, w.ResponseRecorder, http.StatusOK)
	return decodeData[ContractView](e.t, w.ResponseRecorder)
}

type contractList struct {
	Data []ContractView `json:"data"`
	Meta struct {
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
		Total   int `json:"total"`
	} `json:"meta"`
}

func (e *contractEnv) list(bearer, query string) contractList {
	e.t.Helper()
	w := e.call(http.MethodGet, "/v1/contracts"+query, nil, bearer)
	expectStatus(e.t, w, http.StatusOK)
	var out contractList
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func idsOf(items []ContractView) []uuid.UUID {
	out := make([]uuid.UUID, len(items))
	for i, c := range items {
		out[i] = c.ID
	}
	return out
}

// testResponse adds the error fields to a recorded response.
type testResponse struct{ *httptest.ResponseRecorder }

func (r *testResponse) fields() map[string][]string {
	var body testenv.ErrorBody
	_ = json.Unmarshal(r.Body.Bytes(), &body)
	return body.Error.Fields
}

func decodeError(t *testing.T, w *httptest.ResponseRecorder) testenv.ErrorBody {
	t.Helper()
	var body testenv.ErrorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// fixedPlans overrides the limits of the default plan.
type fixedPlans struct {
	usecase.UnlimitedPlans
	contracts *int
	pending   *int
}

func (p fixedPlans) MaxActiveContracts(context.Context, uuid.UUID) (*int, error) {
	return p.contracts, nil
}
func (p fixedPlans) MaxPendingRegistrations(context.Context, uuid.UUID) (*int, error) {
	return p.pending, nil
}
