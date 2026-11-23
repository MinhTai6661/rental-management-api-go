//go:build integration

package invitationhttp_test

import (
	"bytes"
	"context"
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

	"rental-management/internal/adapter/http/httpx"
	invitationhttp "rental-management/internal/adapter/http/invitation"
	"rental-management/internal/adapter/http/testenv"
	invitationpg "rental-management/internal/adapter/persistence/postgres/invitation"
	"rental-management/internal/adapter/security"
	"rental-management/internal/domain/identity"
	usecase "rental-management/internal/usecase/invitation"
	"rental-management/pkg/authz"
	"rental-management/pkg/i18n"
	"rental-management/pkg/ratelimit"
)

var (
	expectError  = testenv.ExpectError
	expectStatus = testenv.ExpectStatus
)

// fakeAuth maps a bearer string to a caller, so the tests need no login.
type fakeAuth map[string]authz.Principal

func (f fakeAuth) Authenticate(_ context.Context, bearer string) (authz.Principal, error) {
	if p, ok := f[bearer]; ok {
		return p, nil
	}
	return authz.Principal{}, fmt.Errorf("unknown bearer %q", bearer)
}

// countingLimiter allows everything and remembers the keys it was asked about.
type countingLimiter struct {
	mu   sync.Mutex
	keys []string
}

func (l *countingLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (ratelimit.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keys = append(l.keys, key)
	return ratelimit.Result{Allowed: true}, nil
}

func (l *countingLimiter) seen(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range l.keys {
		if k == key {
			return true
		}
	}
	return false
}

type auditLog struct {
	mu     sync.Mutex
	events []usecase.AuditEvent
}

func (a *auditLog) Record(_ context.Context, e usecase.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return nil
}

type env struct {
	t        *testing.T
	pool     *pgxpool.Pool
	engine   *gin.Engine
	uc       *usecase.Invitees
	signer   *security.OptOutSigner
	limit    *countingLimiter
	audit    *auditLog
	landlord uuid.UUID
	tenant   uuid.UUID
	admin    uuid.UUID
}

func principal(id uuid.UUID, role string) authz.Principal {
	perms := map[string]struct{}{}
	for _, code := range identity.RolePermissions()[role] {
		perms[code] = struct{}{}
	}
	return authz.Principal{UserID: id.String(), Role: role, Permissions: perms}
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, pool: pg.NewDB(t), limit: &countingLimiter{}, audit: &auditLog{}}
	e.exec(`INSERT INTO roles (code, seniority, is_staff) VALUES ('admin', 2, true), ('landlord', 3, false), ('tenant', 5, false)`)
	e.landlord = e.user("landlord@example.vn", "Chu Hoa", "0911222333", true, "landlord", "tenant")
	e.tenant = e.user("renter@example.com", "Renter One", "0900000001", true, "tenant")
	e.admin = e.user("admin@example.vn", "Admin", "0900000002", true, "admin")

	key := bytes.Repeat([]byte{7}, 32)
	signer, err := security.NewOptOutSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	e.signer = signer
	e.uc = usecase.NewInvitees(usecase.Deps{
		People: invitationpg.NewPeopleRepository(e.pool), Blocks: invitationpg.NewEmailBlockRepository(e.pool), Signer: signer,
		Audit: e.audit, Log: slog.New(slog.DiscardHandler),
	}, usecase.Options{WebURL: "http://web.test"})

	auth := fakeAuth{
		"landlord": principal(e.landlord, authz.RoleLandlord),
		"tenant":   principal(e.tenant, authz.RoleTenant),
		"admin":    principal(e.admin, authz.RoleAdmin),
		// The landlord acting as a tenant has no right to look anybody up.
		"landlord-as-tenant": principal(e.landlord, authz.RoleTenant),
	}
	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	e.engine = gin.New()
	e.engine.Use(httpx.RequestID(slog.New(slog.DiscardHandler)), httpx.Locale(catalog), httpx.Recover())
	invitationhttp.MountInvitees(e.engine.Group("/v1"), invitationhttp.NewInviteeHandler(e.uc, httpx.NewBinder()), auth, e.limit)
	return e
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) user(email, name, phone string, accepts bool, roles ...string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(context.Background(), `
		INSERT INTO users (email, full_name, phone_number, address_detail, accepts_invitations, email_verified_at)
		VALUES ($1, $2, $3, '12 Secret Street', $4, now()) RETURNING id`, email, name, phone, accepts).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, r := range roles {
		e.exec(`INSERT INTO user_roles (user_id, role_code) VALUES ($1, $2)`, id, r)
	}
	return id
}

func (e *env) call(method, path string, body any, bearer string) *httptest.ResponseRecorder {
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
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

type lookupAnswer struct {
	Data invitationhttp.LookupView `json:"data"`
}

func (e *env) lookup(by, value, bearer string) *httptest.ResponseRecorder {
	return e.call(http.MethodPost, "/v1/invitee-lookup", map[string]any{"by": by, "value": value}, bearer)
}

func (e *env) found(by, value string) invitationhttp.LookupView {
	e.t.Helper()
	w := e.lookup(by, value, "landlord")
	expectStatus(e.t, w, http.StatusOK)
	var a lookupAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		e.t.Fatal(err)
	}
	return a.Data
}

func TestLookupFindsARenterOnlyByExactEmailOrIDAndShowsJustEnough(t *testing.T) {
	e := newEnv(t)

	byEmail := e.lookup("email", " Renter@Example.com ", "landlord")
	expectStatus(t, byEmail, http.StatusOK)
	var a lookupAnswer
	_ = json.Unmarshal(byEmail.Body.Bytes(), &a)
	if !a.Data.Found || a.Data.User == nil || a.Data.User.ID != e.tenant.String() || a.Data.User.FullName != "Renter One" ||
		a.Data.User.EmailMasked == nil || *a.Data.User.EmailMasked != "r*****@example.com" {
		t.Fatalf("answer %+v", a.Data)
	}
	// Nothing but id, full_name and the masked email: no phone, no address, not the whole email.
	var raw struct {
		Data struct {
			User map[string]any `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(byEmail.Body.Bytes(), &raw)
	if len(raw.Data.User) != 3 {
		t.Errorf("the user has %d keys: %v", len(raw.Data.User), raw.Data.User)
	}
	for _, secret := range []string{"0900000001", "Secret Street", "renter@example.com"} {
		if strings.Contains(byEmail.Body.String(), secret) {
			t.Errorf("the answer leaks %q: %s", secret, byEmail.Body.String())
		}
	}

	if v := e.found("id", e.tenant.String()); !v.Found || v.User.ID != e.tenant.String() {
		t.Errorf("by id: %+v", v)
	}

	nobody := []struct{ by, value string }{
		{"email", "renter@"}, {"email", "Renter One"}, {"email", "nobody@example.com"},
		{"id", e.admin.String()}, {"email", "admin@example.vn"}, // staff
		{"id", e.landlord.String()}, {"email", "landlord@example.vn"}, // the landlord themself
		{"id", "not-a-uuid"}, {"id", uuid.NewString()},
	}
	for _, n := range nobody {
		w := e.lookup(n.by, n.value, "landlord")
		expectStatus(t, w, http.StatusOK)
		if body := strings.TrimSpace(w.Body.String()); body != `{"data":{"found":false,"user":null}}` {
			t.Errorf("%s %q must read as nobody, got %s", n.by, n.value, body)
		}
	}
}

func TestLookupRulesAndAccess(t *testing.T) {
	e := newEnv(t)
	expectError(t, e.lookup("email", "x@example.vn", ""), http.StatusUnauthorized, "auth.unauthenticated")
	expectError(t, e.lookup("email", "renter@example.com", "tenant"), http.StatusForbidden, "auth.forbidden")
	expectError(t, e.lookup("email", "renter@example.com", "landlord-as-tenant"), http.StatusForbidden, "auth.forbidden")
	expectError(t, e.lookup("email", "renter@example.com", "admin"), http.StatusForbidden, "auth.forbidden")

	body := expectError(t, e.lookup("phone", "0900000001", "landlord"), http.StatusUnprocessableEntity, "validation.failed")
	if len(body.Error.Fields["by"]) == 0 {
		t.Errorf("by is named: %v", body.Error.Fields)
	}
	body = expectError(t, e.call(http.MethodPost, "/v1/invitee-lookup", map[string]any{"by": "email"}, "landlord"), http.StatusUnprocessableEntity, "validation.failed")
	if len(body.Error.Fields["value"]) == 0 {
		t.Errorf("value is required: %v", body.Error.Fields)
	}
	expectError(t, e.lookup("email", strings.Repeat("a", 191), "landlord"), http.StatusUnprocessableEntity, "validation.failed")
}

func TestLookupIsCountedPerLandlordAndAuditedWithoutTheTypedValue(t *testing.T) {
	e := newEnv(t)
	e.found("email", "renter@example.com")
	e.found("email", "nobody@example.com")
	if !e.limit.seen("invitee-lookup:u:" + e.landlord.String()) {
		t.Errorf("the per-account key was not used: %v", e.limit.keys)
	}
	if len(e.audit.events) != 2 {
		t.Fatalf("two lookups, %d events", len(e.audit.events))
	}
	for _, ev := range e.audit.events {
		if ev.Action != usecase.AuditLookup || ev.ActorID != e.landlord {
			t.Errorf("event %+v", ev)
		}
		for _, v := range ev.Meta {
			if s, ok := v.(string); ok && strings.Contains(s, "example.com") {
				t.Errorf("the typed value is in the audit meta: %v", ev.Meta)
			}
		}
	}
}

func TestAnAccountThatSwitchedInvitationsOffIsNotFound(t *testing.T) {
	e := newEnv(t)
	quiet := e.user("quiet@example.com", "Quiet", "0900000009", false, "tenant")
	for _, tc := range []struct{ by, value string }{{"email", "quiet@example.com"}, {"id", quiet.String()}} {
		if v := e.found(tc.by, tc.value); v.Found {
			t.Errorf("%s: an account that does not accept invitations must read as nobody", tc.by)
		}
	}
}

func TestOptOutNeedsTheTokenOfTheLinkAndStoresOnlyAHash(t *testing.T) {
	e := newEnv(t)
	post := func(email, token string) *httptest.ResponseRecorder {
		return e.call(http.MethodPost, "/v1/invitation-emails/opt-out", map[string]any{"email": email, "token": token}, "")
	}

	body := expectError(t, post("New.Person@Example.vn", strings.Repeat("a", 64)), http.StatusUnprocessableEntity, "validation.failed")
	if len(body.Error.Fields["token"]) == 0 {
		t.Errorf("token is named: %v", body.Error.Fields)
	}
	expectError(t, post("new.person@example.vn", "short"), http.StatusUnprocessableEntity, "validation.failed")
	expectError(t, e.call(http.MethodPost, "/v1/invitation-emails/opt-out", map[string]any{"token": "x"}, ""), http.StatusUnprocessableEntity, "validation.failed")
	if may, _ := e.uc.MayMail(context.Background(), "new.person@example.vn"); !may {
		t.Fatal("nothing stored yet: the address may be mailed")
	}

	// The link carries the token of the normalized address; the upper-case form of the address works.
	token := e.signer.Sign("new.person@example.vn")
	expectStatus(t, post("New.Person@Example.vn", token), http.StatusNoContent)
	expectStatus(t, post("new.person@example.vn", token), http.StatusNoContent) // repeating is harmless

	var hashes []string
	rows, err := e.pool.Query(context.Background(), `SELECT email_hash FROM invitation_email_blocks`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}
	if len(hashes) != 1 || len(hashes[0]) != 64 || strings.Contains(hashes[0], "person") {
		t.Errorf("one row with only a hash: %v", hashes)
	}
	var dump string
	if err := e.pool.QueryRow(context.Background(), `SELECT coalesce(json_agg(t)::text, '') FROM invitation_email_blocks t`).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump, "person") {
		t.Errorf("the table holds the address: %s", dump)
	}
	if may, _ := e.uc.MayMail(context.Background(), "NEW.person@example.vn"); may {
		t.Error("an address that opted out is not mailed")
	}
	// The token of one address does not work for another.
	expectError(t, post("other@example.vn", token), http.StatusUnprocessableEntity, "validation.failed")
}
