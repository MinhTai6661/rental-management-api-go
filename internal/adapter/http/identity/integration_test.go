//go:build integration

package identityhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	httpadapter "rental-management/internal/adapter/http"
	"rental-management/internal/adapter/http/httpx"
	identityhttp "rental-management/internal/adapter/http/identity"
	mailadapter "rental-management/internal/adapter/mail"
	"rental-management/internal/adapter/persistence/postgres"
	identitypg "rental-management/internal/adapter/persistence/postgres/identity"
	redisstore "rental-management/internal/adapter/redis"
	"rental-management/internal/adapter/security"
	usecase "rental-management/internal/usecase/identity"
	"rental-management/pkg/authz"
	"rental-management/pkg/i18n"
	"rental-management/pkg/mail"
	"rental-management/pkg/ratelimit"
	"rental-management/pkg/testdb"
)

var (
	pg  *testdb.Postgres
	rds *testdb.Redis
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	var err error
	if pg, err = testdb.StartPostgres(ctx); err == nil {
		rds, err = testdb.StartRedis(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	rds.Terminate()
	pg.Terminate()
	os.Exit(code)
}

const testSecret = "0123456789abcdef0123456789abcdef"

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time { return c.t }

const (
	testPassword = "correct-horse-battery"
	clientIP     = "192.0.2.10:5000"
)

// env is one test's whole stack: a fresh database cloned from the migrated template, a flushed Redis, the real
// adapters and use cases with a controllable clock and a recording mailer, and the HTTP routes on top.
type env struct {
	t      *testing.T
	pool   *pgxpool.Pool
	rdb    *redis.Client
	mailer *mail.Recorder
	clock  *testClock
	tokens *security.TokenIssuer
	uc     *usecase.Auth
	seeder *usecase.Seeder
	engine *gin.Engine
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	e := &env{t: t, pool: pg.NewDB(t), rdb: rds.Client(t), mailer: &mail.Recorder{}, clock: &testClock{time.Now().UTC()}}
	tx := postgres.NewTxManager(e.pool)
	perms := identitypg.NewPermissionRepository(e.pool)
	users := identitypg.NewUserRepository(e.pool)
	hasher := security.PasswordHasher{}
	e.seeder = usecase.NewSeeder(tx, perms, users, hasher)
	if err := e.seeder.SeedCatalog(ctx); err != nil {
		t.Fatal(err)
	}

	renderer, err := mail.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	e.tokens = security.NewTokenIssuer(testSecret, "rental-management", 15*time.Minute, 5*time.Second, e.clock.now)
	e.uc, err = usecase.NewAuth(usecase.Deps{
		Tx: tx, Users: users, Sessions: identitypg.NewSessionRepository(e.pool),
		Verifications: identitypg.NewVerificationRepository(e.pool), Permissions: perms,
		Revocations: redisstore.NewRevocationStore(e.rdb, "test:"), Hasher: hasher, Tokens: e.tokens,
		Opaque: security.OpaqueTokens{}, Mailer: mailadapter.NewVerificationMailer(e.mailer, renderer, "http://localhost:2001"),
		Clock: usecase.ClockFunc(e.clock.now), Log: log,
		Dispatch: func(work func()) { work() }, // inline: the test reads the email right after the request
	}, usecase.Options{
		TermsVersion: "2026-11-03", RefreshTTL: 720 * time.Hour,
		ReuseGrace: 10 * time.Second, VerificationTTL: 24 * time.Hour, ResendCooldown: 60 * time.Second,
		RevokedFor: 15*time.Minute + 5*time.Second, PermissionCacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	e.engine = gin.New()
	e.engine.Use(httpx.RequestID(log), httpx.Locale(catalog), httpx.Recover())
	limiter := ratelimit.NewRedis(e.rdb, "test:")
	identityhttp.MountAuth(e.engine.Group("/v1"), identityhttp.NewAuthHandler(e.uc, httpx.NewBinder(), limiter), httpadapter.NewAuthenticator(e.uc), limiter)
	return e
}

// call sends a JSON request (body nil for none) with an optional bearer token.
func (e *env) call(method, path string, body any, bearer string) *httptest.ResponseRecorder {
	e.t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = clientIP
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

var tokenInLink = regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)

// lastVerificationToken reads the token out of the link of the latest email sent to the address.
func (e *env) lastVerificationToken(email string) string {
	e.t.Helper()
	sent := e.mailer.Sent()
	for i := len(sent) - 1; i >= 0; i-- {
		if sent[i].To == email {
			if m := tokenInLink.FindStringSubmatch(sent[i].Text); m != nil {
				return m[1]
			}
		}
	}
	e.t.Fatalf("no verification email was sent to %s", email)
	return ""
}

func (e *env) mailCount(email string) int {
	n := 0
	for _, m := range e.mailer.Sent() {
		if m.To == email {
			n++
		}
	}
	return n
}

// register creates the account through the API and returns its id.
func (e *env) register(email string) string {
	e.t.Helper()
	w := e.call(http.MethodPost, "/v1/auth/register", map[string]any{
		"full_name": "Nguyễn Văn A", "email": email, "password": testPassword, "accept_terms": true,
	}, "")
	expectStatus(e.t, w, http.StatusCreated)
	return decodeData[identityhttp.UserView](e.t, w).ID
}

// verified registers an account and confirms its email.
func (e *env) verified(email string) string {
	e.t.Helper()
	id := e.register(email)
	expectStatus(e.t, e.call(http.MethodPost, "/v1/auth/email/verify", map[string]any{"token": e.lastVerificationToken(email)}, ""), http.StatusNoContent)
	return id
}

func (e *env) login(email, password string) *httptest.ResponseRecorder {
	return e.call(http.MethodPost, "/v1/auth/login", map[string]any{"email": email, "password": password}, "")
}

// signedIn logs an account in and returns what the client receives.
func (e *env) signedIn(email string) identityhttp.LoginResult {
	e.t.Helper()
	w := e.login(email, testPassword)
	expectStatus(e.t, w, http.StatusOK)
	return decodeData[identityhttp.LoginResult](e.t, w)
}

func (e *env) refresh(token string) *httptest.ResponseRecorder {
	return e.call(http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": token}, "")
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

func TestRegisterVerifyLoginMeRefresh(t *testing.T) {
	e := newEnv(t)

	// Register: 201, tenant only, the account is not usable yet.
	w := e.call(http.MethodPost, "/v1/auth/register", map[string]any{
		"full_name": "  Nguyễn Văn A ", "email": "Ann.Lee@Example.VN", "password": testPassword, "accept_terms": true,
		"phone_number": "0901234567", "address_detail": "  ",
	}, "")
	expectStatus(t, w, http.StatusCreated)
	if strings.Contains(strings.ToLower(w.Body.String()), "password") {
		t.Errorf("the response must not mention passwords: %s", w.Body.String())
	}
	user := decodeData[identityhttp.UserView](t, w)
	if user.Email == nil || *user.Email != "ann.lee@example.vn" || user.FullName != "Nguyễn Văn A" ||
		len(user.Roles) != 1 || user.Roles[0] != authz.RoleTenant || user.ActiveRole != authz.RoleTenant ||
		user.AddressDetail != nil || user.Terms.NeedsAcceptance || user.Terms.CurrentVersion != "2026-11-03" {
		t.Fatalf("unexpected user: %+v", user)
	}
	if e.mailCount("ann.lee@example.vn") != 1 {
		t.Fatalf("one verification email expected, got %d", e.mailCount("ann.lee@example.vn"))
	}

	// Wrong password and unknown email are the same answer; a right password on an unverified account is told apart.
	expectError(t, e.login("ann.lee@example.vn", "wrong-password"), http.StatusUnauthorized, "auth.invalid_credentials")
	expectError(t, e.login("nobody@example.vn", testPassword), http.StatusUnauthorized, "auth.invalid_credentials")
	expectError(t, e.login("ANN.LEE@example.vn", testPassword), http.StatusForbidden, "auth.email_unverified")

	// Verify: single use.
	token := e.lastVerificationToken("ann.lee@example.vn")
	expectStatus(t, e.call(http.MethodPost, "/v1/auth/email/verify", map[string]any{"token": token}, ""), http.StatusNoContent)
	expectError(t, e.call(http.MethodPost, "/v1/auth/email/verify", map[string]any{"token": token}, ""), http.StatusBadRequest, "auth.verification_invalid")
	expectError(t, e.call(http.MethodPost, "/v1/auth/email/verify", map[string]any{"token": "made-up"}, ""), http.StatusBadRequest, "auth.verification_invalid")

	// Login (case-insensitive email), then the token works.
	result := e.signedIn("Ann.Lee@example.vn")
	if result.Tokens.TokenType != "Bearer" || result.Tokens.ExpiresIn != 900 || result.Tokens.RefreshExpiresIn != 720*3600 ||
		result.Tokens.AccessToken == "" || result.Tokens.RefreshToken == "" {
		t.Fatalf("unexpected tokens: %+v", result.Tokens)
	}
	claims, err := e.tokens.Verify(result.Tokens.AccessToken)
	if err != nil || claims.Role != authz.RoleTenant || claims.UserID != user.ID {
		t.Fatalf("access token claims: %+v, %v", claims, err)
	}

	// The refresh token is stored as a hash only.
	var raw, hashed int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM refresh_tokens WHERE token_hash = $1`, []byte(result.Tokens.RefreshToken)).Scan(&raw)
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM refresh_tokens WHERE token_hash = $1`, security.HashToken(result.Tokens.RefreshToken)).Scan(&hashed)
	if raw != 0 || hashed != 1 {
		t.Errorf("refresh token storage: %d raw rows, %d hashed rows", raw, hashed)
	}

	// Me: the user with the permissions of the active role.
	w = e.call(http.MethodGet, "/v1/auth/me", nil, result.Tokens.AccessToken)
	expectStatus(t, w, http.StatusOK)
	me := decodeData[identityhttp.UserView](t, w)
	if me.ID != user.ID || me.ActiveRole != authz.RoleTenant || !slices.Contains(me.Permissions, "rooms.view") || slices.Contains(me.Permissions, "rooms.manage") {
		t.Fatalf("unexpected me: %+v", me)
	}

	// Refresh gives a new pair.
	w = e.refresh(result.Tokens.RefreshToken)
	expectStatus(t, w, http.StatusOK)
	next := decodeData[struct {
		Tokens identityhttp.Tokens `json:"tokens"`
	}](t, w).Tokens
	if next.RefreshToken == result.Tokens.RefreshToken || next.AccessToken == "" {
		t.Fatalf("refresh must rotate the token: %+v", next)
	}
	expectStatus(t, e.call(http.MethodGet, "/v1/auth/me", nil, next.AccessToken), http.StatusOK)
}

func TestRegisterValidation(t *testing.T) {
	e := newEnv(t)
	e.register("taken@example.vn")

	cases := map[string]struct {
		body  map[string]any
		field string
	}{
		"duplicate email":    {map[string]any{"full_name": "A", "email": "TAKEN@example.vn", "password": testPassword, "accept_terms": true}, "email"},
		"terms not accepted": {map[string]any{"full_name": "A", "email": "a@example.vn", "password": testPassword, "accept_terms": false}, "accept_terms"},
		"short password":     {map[string]any{"full_name": "A", "email": "a@example.vn", "password": "short", "accept_terms": true}, "password"},
		"bad email":          {map[string]any{"full_name": "A", "email": "not-an-email", "password": testPassword, "accept_terms": true}, "email"},
		"blank name":         {map[string]any{"full_name": "   ", "email": "a@example.vn", "password": testPassword, "accept_terms": true}, "full_name"},
		"unknown ward":       {map[string]any{"full_name": "A", "email": "a@example.vn", "password": testPassword, "accept_terms": true, "ward_code": 987654}, "ward_code"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			body := expectError(t, e.call(http.MethodPost, "/v1/auth/register", tc.body, ""), http.StatusUnprocessableEntity, "validation.failed")
			if len(body.Error.Fields[tc.field]) == 0 {
				t.Errorf("expected an error on %q: %+v", tc.field, body.Error.Fields)
			}
		})
	}
	// A role cannot be asked for: register only ever grants tenant.
	w := e.call(http.MethodPost, "/v1/auth/register", map[string]any{
		"full_name": "A", "email": "sneaky@example.vn", "password": testPassword, "accept_terms": true, "roles": []string{"super_admin"}, "role": "admin",
	}, "")
	expectStatus(t, w, http.StatusCreated)
	if got := decodeData[identityhttp.UserView](t, w).Roles; len(got) != 1 || got[0] != authz.RoleTenant {
		t.Errorf("only tenant may be granted, got %v", got)
	}
	// The body size limit is still enforced.
	big := map[string]any{"full_name": strings.Repeat("a", 2<<20)}
	expectError(t, e.call(http.MethodPost, "/v1/auth/register", big, ""), http.StatusRequestEntityTooLarge, "request.too_large")
}

func TestMeShowsTheWardAndProvince(t *testing.T) {
	e := newEnv(t)
	e.exec(`INSERT INTO provinces (code, name, administrative_level, slug, area_phone_code) VALUES (79, 'Thành phố Hồ Chí Minh', 'city', 'ho_chi_minh', 28)`)
	e.exec(`INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (26734, 79, 'Phường Bến Thành', 'ward', 'ben_thanh')`)

	w := e.call(http.MethodPost, "/v1/auth/register", map[string]any{
		"full_name": "A", "email": "ward@example.vn", "password": testPassword, "accept_terms": true, "ward_code": 26734,
	}, "")
	expectStatus(t, w, http.StatusCreated)
	expectStatus(t, e.call(http.MethodPost, "/v1/auth/email/verify", map[string]any{"token": e.lastVerificationToken("ward@example.vn")}, ""), http.StatusNoContent)

	me := decodeData[identityhttp.UserView](t, e.call(http.MethodGet, "/v1/auth/me", nil, e.signedIn("ward@example.vn").Tokens.AccessToken))
	if me.Ward == nil || me.Ward.Code != 26734 || me.Ward.Name != "Phường Bến Thành" || me.Ward.Province.Code != 79 || me.Ward.Province.Name != "Thành phố Hồ Chí Minh" {
		t.Fatalf("ward and province expected: %+v", me.Ward)
	}
}

func TestRefreshReuseAfterTheGraceWindowClosesTheWholeSession(t *testing.T) {
	e := newEnv(t)
	e.verified("reuse@example.vn")
	login := e.signedIn("reuse@example.vn")

	// The legitimate client rotates its token.
	w := e.refresh(login.Tokens.RefreshToken)
	expectStatus(t, w, http.StatusOK)
	current := decodeData[struct {
		Tokens identityhttp.Tokens `json:"tokens"`
	}](t, w).Tokens

	// Later somebody presents the OLD token: it was copied. The whole session is closed ...
	e.clock.t = e.clock.t.Add(11 * time.Second)
	expectError(t, e.refresh(login.Tokens.RefreshToken), http.StatusUnauthorized, "auth.refresh_invalid")

	// ... including the token the real client holds, and its access token.
	expectError(t, e.refresh(current.RefreshToken), http.StatusUnauthorized, "auth.refresh_invalid")
	expectError(t, e.call(http.MethodGet, "/v1/auth/me", nil, current.AccessToken), http.StatusUnauthorized, "auth.unauthenticated")

	var reason string
	if err := e.pool.QueryRow(context.Background(), `SELECT revoked_reason FROM user_sessions`).Scan(&reason); err != nil || reason != "reuse_detected" {
		t.Errorf("the revocation must be committed, not rolled back: reason %q, %v", reason, err)
	}
}

func TestRefreshTwiceWithinTheGraceWindowIsAllowed(t *testing.T) {
	e := newEnv(t)
	e.verified("tabs@example.vn")
	login := e.signedIn("tabs@example.vn")

	first := e.refresh(login.Tokens.RefreshToken)
	e.clock.t = e.clock.t.Add(3 * time.Second) // a second tab, a moment later
	second := e.refresh(login.Tokens.RefreshToken)
	expectStatus(t, first, http.StatusOK)
	expectStatus(t, second, http.StatusOK)

	a := decodeData[struct {
		Tokens identityhttp.Tokens `json:"tokens"`
	}](t, first).Tokens
	b := decodeData[struct {
		Tokens identityhttp.Tokens `json:"tokens"`
	}](t, second).Tokens
	if a.RefreshToken == b.RefreshToken {
		t.Error("each refresh hands out its own token")
	}
	expectStatus(t, e.call(http.MethodGet, "/v1/auth/me", nil, b.AccessToken), http.StatusOK)
}

func TestRefreshRefusesUnknownAndExpiredTokens(t *testing.T) {
	e := newEnv(t)
	e.verified("old@example.vn")
	login := e.signedIn("old@example.vn")

	expectError(t, e.refresh("not-a-token"), http.StatusUnauthorized, "auth.refresh_invalid")
	expectError(t, e.call(http.MethodPost, "/v1/auth/refresh", map[string]any{}, ""), http.StatusUnprocessableEntity, "validation.failed")

	e.clock.t = e.clock.t.Add(721 * time.Hour)
	expectError(t, e.refresh(login.Tokens.RefreshToken), http.StatusUnauthorized, "auth.refresh_invalid")
}

func TestLogoutEndsTheAccessTokenAndTheRefreshToken(t *testing.T) {
	e := newEnv(t)
	e.verified("out@example.vn")
	login := e.signedIn("out@example.vn")

	expectStatus(t, e.call(http.MethodGet, "/v1/auth/me", nil, login.Tokens.AccessToken), http.StatusOK)
	expectStatus(t, e.call(http.MethodPost, "/v1/auth/logout", map[string]any{"refresh_token": login.Tokens.RefreshToken}, login.Tokens.AccessToken), http.StatusNoContent)

	expectError(t, e.call(http.MethodGet, "/v1/auth/me", nil, login.Tokens.AccessToken), http.StatusUnauthorized, "auth.unauthenticated")
	expectError(t, e.refresh(login.Tokens.RefreshToken), http.StatusUnauthorized, "auth.refresh_invalid")
	// Logging out needs a login.
	expectError(t, e.call(http.MethodPost, "/v1/auth/logout", nil, ""), http.StatusUnauthorized, "auth.unauthenticated")
}

func TestLogoutAllEndsEverySession(t *testing.T) {
	e := newEnv(t)
	e.verified("all@example.vn")
	phone, laptop := e.signedIn("all@example.vn"), e.signedIn("all@example.vn")

	expectStatus(t, e.call(http.MethodPost, "/v1/auth/logout-all", nil, phone.Tokens.AccessToken), http.StatusNoContent)

	for name, tokens := range map[string]identityhttp.Tokens{"phone": phone.Tokens, "laptop": laptop.Tokens} {
		expectError(t, e.call(http.MethodGet, "/v1/auth/me", nil, tokens.AccessToken), http.StatusUnauthorized, "auth.unauthenticated")
		if w := e.refresh(tokens.RefreshToken); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: the refresh token must be dead, got %d", name, w.Code)
		}
	}
}

func TestSwitchRole(t *testing.T) {
	e := newEnv(t)
	id := e.verified("two@example.vn")
	e.exec(`INSERT INTO user_roles (user_id, role_code) VALUES ($1, 'landlord')`, uuid.MustParse(id))
	login := e.signedIn("two@example.vn")
	if login.User.ActiveRole != authz.RoleTenant {
		t.Fatalf("a new login starts in the role used last (tenant), got %s", login.User.ActiveRole)
	}
	oldClaims, _ := e.tokens.Verify(login.Tokens.AccessToken)

	// A role the user does not hold, and a refresh token that is not the caller's, are refused.
	body := expectError(t, e.call(http.MethodPost, "/v1/auth/switch-role", map[string]any{"role": "admin", "refresh_token": login.Tokens.RefreshToken}, login.Tokens.AccessToken), http.StatusUnprocessableEntity, "validation.failed")
	if len(body.Error.Fields["role"]) == 0 {
		t.Errorf("role_not_held expected on role: %+v", body.Error.Fields)
	}
	expectError(t, e.call(http.MethodPost, "/v1/auth/switch-role", map[string]any{"role": "landlord", "refresh_token": "somebody-elses"}, login.Tokens.AccessToken), http.StatusUnauthorized, "auth.refresh_invalid")
	expectError(t, e.call(http.MethodPost, "/v1/auth/switch-role", map[string]any{"role": "wizard", "refresh_token": login.Tokens.RefreshToken}, login.Tokens.AccessToken), http.StatusUnprocessableEntity, "validation.failed")

	// The switch itself.
	w := e.call(http.MethodPost, "/v1/auth/switch-role", map[string]any{"role": "landlord", "refresh_token": login.Tokens.RefreshToken}, login.Tokens.AccessToken)
	expectStatus(t, w, http.StatusOK)
	switched := decodeData[identityhttp.LoginResult](t, w)
	newClaims, err := e.tokens.Verify(switched.Tokens.AccessToken)
	if err != nil || switched.User.ActiveRole != authz.RoleLandlord || newClaims.Role != authz.RoleLandlord || newClaims.SessionID == oldClaims.SessionID {
		t.Fatalf("a new session in the landlord role expected: %+v, %+v, %v", switched.User, newClaims, err)
	}
	if !slices.Contains(switched.User.Permissions, "rooms.manage") {
		t.Errorf("the landlord permissions expected: %v", switched.User.Permissions)
	}

	// The old session is dead, the new one works, and the role is remembered for the next login.
	expectError(t, e.call(http.MethodGet, "/v1/auth/me", nil, login.Tokens.AccessToken), http.StatusUnauthorized, "auth.unauthenticated")
	expectError(t, e.refresh(login.Tokens.RefreshToken), http.StatusUnauthorized, "auth.refresh_invalid")
	expectStatus(t, e.call(http.MethodGet, "/v1/auth/me", nil, switched.Tokens.AccessToken), http.StatusOK)
	var last string
	_ = e.pool.QueryRow(context.Background(), `SELECT last_active_role FROM users WHERE id = $1`, uuid.MustParse(id)).Scan(&last)
	if last != authz.RoleLandlord || e.signedIn("two@example.vn").User.ActiveRole != authz.RoleLandlord {
		t.Errorf("the role used last must persist (db says %q)", last)
	}
}

func TestChangePasswordClosesTheOtherSessions(t *testing.T) {
	e := newEnv(t)
	e.verified("pw@example.vn")
	here, elsewhere := e.signedIn("pw@example.vn"), e.signedIn("pw@example.vn")

	change := func(current, next string) *httptest.ResponseRecorder {
		return e.call(http.MethodPut, "/v1/auth/password", map[string]any{"current_password": current, "new_password": next}, here.Tokens.AccessToken)
	}
	body := expectError(t, change("not-my-password", "another-password-1"), http.StatusUnprocessableEntity, "validation.failed")
	if len(body.Error.Fields["current_password"]) == 0 {
		t.Errorf("password_incorrect expected: %+v", body.Error.Fields)
	}
	body = expectError(t, change(testPassword, testPassword), http.StatusUnprocessableEntity, "validation.failed")
	if len(body.Error.Fields["new_password"]) == 0 {
		t.Errorf("password_same expected: %+v", body.Error.Fields)
	}
	expectError(t, change(testPassword, "short"), http.StatusUnprocessableEntity, "validation.failed")

	expectStatus(t, change(testPassword, "another-password-1"), http.StatusNoContent)

	// The session that changed it stays; every other one is closed.
	expectStatus(t, e.call(http.MethodGet, "/v1/auth/me", nil, here.Tokens.AccessToken), http.StatusOK)
	expectError(t, e.call(http.MethodGet, "/v1/auth/me", nil, elsewhere.Tokens.AccessToken), http.StatusUnauthorized, "auth.unauthenticated")
	expectError(t, e.refresh(elsewhere.Tokens.RefreshToken), http.StatusUnauthorized, "auth.refresh_invalid")
	expectStatus(t, e.refresh(here.Tokens.RefreshToken), http.StatusOK)

	expectError(t, e.login("pw@example.vn", testPassword), http.StatusUnauthorized, "auth.invalid_credentials")
	expectStatus(t, e.login("pw@example.vn", "another-password-1"), http.StatusOK)
}

func TestDisabledAccount(t *testing.T) {
	e := newEnv(t)
	id := e.verified("off@example.vn")
	login := e.signedIn("off@example.vn")

	e.exec(`UPDATE users SET status = 'inactive' WHERE id = $1`, uuid.MustParse(id))

	expectError(t, e.call(http.MethodGet, "/v1/auth/me", nil, login.Tokens.AccessToken), http.StatusForbidden, "auth.account_inactive")
	expectError(t, e.login("off@example.vn", testPassword), http.StatusForbidden, "auth.account_inactive")
	// Only after the password is right: a wrong one still looks like any wrong password.
	expectError(t, e.login("off@example.vn", "wrong-password"), http.StatusUnauthorized, "auth.invalid_credentials")

	// Refreshing closes the session for good (and the closure is committed).
	expectError(t, e.refresh(login.Tokens.RefreshToken), http.StatusUnauthorized, "auth.refresh_invalid")
	var reason string
	if err := e.pool.QueryRow(context.Background(), `SELECT revoked_reason FROM user_sessions`).Scan(&reason); err != nil || reason != "account_inactive" {
		t.Errorf("session closure: reason %q, %v", reason, err)
	}
}

func TestLoginIsRateLimitedPerAddressAndEmail(t *testing.T) {
	e := newEnv(t)
	e.verified("limit@example.vn")

	const loginEmailLimit = 5 // the per address and email limit of the login handler
	for i := 1; i <= loginEmailLimit; i++ {
		expectError(t, e.login("limit@example.vn", "wrong-password"), http.StatusUnauthorized, "auth.invalid_credentials")
	}
	w := e.login("limit@example.vn", testPassword) // even the right password is refused once the limit is hit
	expectError(t, w, http.StatusTooManyRequests, "request.rate_limited")
	if w.Header().Get("Retry-After") == "" {
		t.Error("a 429 must say when to come back")
	}
	// Another email from the same address is counted on its own.
	expectError(t, e.login("someone-else@example.vn", "wrong-password"), http.StatusUnauthorized, "auth.invalid_credentials")
}

func TestResendVerification(t *testing.T) {
	e := newEnv(t)
	e.register("resend@example.vn")
	first := e.lastVerificationToken("resend@example.vn")
	resend := func(email string) *httptest.ResponseRecorder {
		return e.call(http.MethodPost, "/v1/auth/email/resend", map[string]any{"email": email}, "")
	}

	// The same answer for an unknown address: nothing tells the caller who is registered.
	expectStatus(t, resend("nobody@example.vn"), http.StatusAccepted)
	if e.mailCount("nobody@example.vn") != 0 {
		t.Error("no email goes to an unknown address")
	}

	// Within the cooldown nothing new is sent, and the answer is the same.
	expectStatus(t, resend("resend@example.vn"), http.StatusAccepted)
	if e.mailCount("resend@example.vn") != 1 {
		t.Fatalf("the cooldown must hold: %d emails", e.mailCount("resend@example.vn"))
	}

	// After the cooldown a new link is sent and the older one stops working.
	e.clock.t = e.clock.t.Add(61 * time.Second)
	expectStatus(t, resend("RESEND@example.vn"), http.StatusAccepted)
	if e.mailCount("resend@example.vn") != 2 {
		t.Fatalf("a second email expected after the cooldown: %d", e.mailCount("resend@example.vn"))
	}
	second := e.lastVerificationToken("resend@example.vn")
	if second == first {
		t.Fatal("a new token expected")
	}
	expectError(t, e.call(http.MethodPost, "/v1/auth/email/verify", map[string]any{"token": first}, ""), http.StatusBadRequest, "auth.verification_invalid")
	expectStatus(t, e.call(http.MethodPost, "/v1/auth/email/verify", map[string]any{"token": second}, ""), http.StatusNoContent)

	// A verified address gets nothing more.
	e.clock.t = e.clock.t.Add(time.Hour)
	expectStatus(t, resend("resend@example.vn"), http.StatusAccepted)
	if e.mailCount("resend@example.vn") != 2 {
		t.Error("a verified address must not get another email")
	}
	expectError(t, e.call(http.MethodPost, "/v1/auth/email/resend", map[string]any{"email": "not-an-email"}, ""), http.StatusUnprocessableEntity, "validation.failed")
}

func TestVerificationLinkExpires(t *testing.T) {
	e := newEnv(t)
	e.register("late@example.vn")
	token := e.lastVerificationToken("late@example.vn")

	e.clock.t = e.clock.t.Add(25 * time.Hour)
	expectError(t, e.call(http.MethodPost, "/v1/auth/email/verify", map[string]any{"token": token}, ""), http.StatusBadRequest, "auth.verification_invalid")
	expectError(t, e.login("late@example.vn", testPassword), http.StatusForbidden, "auth.email_unverified")
}

func TestRequireAuthRefusesBadTokens(t *testing.T) {
	e := newEnv(t)
	id := e.verified("auth@example.vn")
	login := e.signedIn("auth@example.vn")

	expectError(t, e.call(http.MethodGet, "/v1/auth/me", nil, ""), http.StatusUnauthorized, "auth.unauthenticated")
	expectError(t, e.call(http.MethodGet, "/v1/auth/me", nil, "garbage"), http.StatusUnauthorized, "auth.unauthenticated")

	// A token of an account that no longer exists.
	e.exec(`DELETE FROM users WHERE id = $1`, uuid.MustParse(id))
	expectError(t, e.call(http.MethodGet, "/v1/auth/me", nil, login.Tokens.AccessToken), http.StatusUnauthorized, "auth.unauthenticated")

	// An expired token.
	e.verified("auth2@example.vn")
	second := e.signedIn("auth2@example.vn")
	e.clock.t = e.clock.t.Add(16 * time.Minute)
	expectError(t, e.call(http.MethodGet, "/v1/auth/me", nil, second.Tokens.AccessToken), http.StatusUnauthorized, "auth.unauthenticated")
}

// When Redis cannot say whether a token was revoked, the request is refused with a 503: it is never let through.
func TestRevocationCheckFailsClosedWhenRedisIsDown(t *testing.T) {
	e := newEnv(t)
	e.verified("down@example.vn")
	login := e.signedIn("down@example.vn")
	expectStatus(t, e.call(http.MethodGet, "/v1/auth/me", nil, login.Tokens.AccessToken), http.StatusOK)

	_ = e.rdb.Close()
	expectError(t, e.call(http.MethodGet, "/v1/auth/me", nil, login.Tokens.AccessToken), http.StatusServiceUnavailable, "server.busy")
}

func TestPermissionsAreCheckedByRole(t *testing.T) {
	e := newEnv(t)
	e.verified("perm@example.vn")
	login := e.signedIn("perm@example.vn")

	engine := gin.New()
	catalog, _ := i18n.New()
	engine.Use(httpx.Locale(catalog))
	guard := engine.Group("/x", httpx.RequireAuth(httpadapter.NewAuthenticator(e.uc)))
	guard.GET("/view", httpx.RequirePermission("rooms.view"), func(c *gin.Context) { httpx.NoContent(c) })
	guard.GET("/manage", httpx.RequirePermission("rooms.manage"), func(c *gin.Context) { httpx.NoContent(c) })
	guard.GET("/admin", httpx.RequireRole(authz.RoleAdmin), func(c *gin.Context) { httpx.NoContent(c) })

	get := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+login.Tokens.AccessToken)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w.Code
	}
	if get("/x/view") != http.StatusNoContent || get("/x/manage") != http.StatusForbidden || get("/x/admin") != http.StatusForbidden {
		t.Errorf("a tenant may view rooms, not manage them, and is no admin: %d %d %d", get("/x/view"), get("/x/manage"), get("/x/admin"))
	}
}

func TestSeedDemoIsIdempotentAndNeverMixesStaff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.exec(`INSERT INTO provinces (code, name, administrative_level, slug, area_phone_code) VALUES (79, 'Thành phố Hồ Chí Minh', 'city', 'ho_chi_minh', 28)`)
	e.exec(`INSERT INTO wards (code, province_code, name, administrative_level, slug) VALUES (26734, 79, 'Phường Bến Thành', 'ward', 'ben_thanh')`)

	for i := 0; i < 2; i++ {
		if _, err := e.seeder.SeedDemo(ctx, "demo-password-123", "2026-11-03", e.clock.now()); err != nil {
			t.Fatalf("seed %d: %v", i+1, err)
		}
	}
	var users, roles int
	_ = e.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM user_roles)`).Scan(&users, &roles)
	if users != 4 || roles != 5 { // landlord has two roles
		t.Fatalf("%d users and %d role grants after seeding twice", users, roles)
	}

	// The accounts can log in at once, with the seeded password, in their most senior role.
	for email, role := range map[string]string{
		"superadmin@rental.test": authz.RoleSuperAdmin, "admin@rental.test": authz.RoleAdmin,
		"landlord@rental.test": authz.RoleLandlord, "tenant@rental.test": authz.RoleTenant,
	} {
		w := e.login(email, "demo-password-123")
		expectStatus(t, w, http.StatusOK)
		got := decodeData[identityhttp.LoginResult](t, w).User
		if got.ActiveRole != role || got.Terms.NeedsAcceptance {
			t.Errorf("%s: role %s, needs acceptance %v", email, got.ActiveRole, got.Terms.NeedsAcceptance)
		}
		if (role == authz.RoleLandlord || role == authz.RoleTenant) && got.Ward == nil {
			t.Errorf("%s should have an address", email)
		}
	}
}

func TestPreferencesSwitchInvitationsOffAndOn(t *testing.T) {
	e := newEnv(t)
	e.verified("pref@example.vn")
	token := e.signedIn("pref@example.vn").Tokens.AccessToken

	me := func() identityhttp.UserView {
		return decodeData[identityhttp.UserView](t, e.call(http.MethodGet, "/v1/auth/me", nil, token))
	}
	if !me().AcceptsInvitations {
		t.Fatal("a new account accepts invitations")
	}

	w := e.call(http.MethodPut, "/v1/me/preferences", map[string]any{"accepts_invitations": false}, token)
	expectStatus(t, w, http.StatusOK)
	if decodeData[identityhttp.UserView](t, w).AcceptsInvitations || me().AcceptsInvitations {
		t.Error("the preference is off in the answer and when read back through /auth/me")
	}

	// A body without the field changes nothing.
	expectStatus(t, e.call(http.MethodPut, "/v1/me/preferences", map[string]any{}, token), http.StatusOK)
	if me().AcceptsInvitations {
		t.Error("an empty change must keep the preference off")
	}

	expectStatus(t, e.call(http.MethodPut, "/v1/me/preferences", map[string]any{"accepts_invitations": true}, token), http.StatusOK)
	if !me().AcceptsInvitations {
		t.Error("the preference is back on")
	}

	expectError(t, e.call(http.MethodPut, "/v1/me/preferences", map[string]any{"accepts_invitations": "no"}, token), http.StatusBadRequest, "request.malformed")
	expectError(t, e.call(http.MethodPut, "/v1/me/preferences", map[string]any{"accepts_invitations": true}, ""), http.StatusUnauthorized, "auth.unauthenticated")
}
