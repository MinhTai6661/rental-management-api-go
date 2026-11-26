//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/config"
	httpadapter "rental-management/internal/adapter/http"
	adminhttp "rental-management/internal/adapter/http/admin"
	"rental-management/internal/adapter/http/httpx"
	identityhttp "rental-management/internal/adapter/http/identity"
	"rental-management/internal/adapter/http/testenv"
	"rental-management/internal/adapter/security"
	domain "rental-management/internal/domain/identity"
	usecase "rental-management/internal/usecase/identity"
	"rental-management/pkg/i18n"
)

// Social login through the real composition code: the real auth stack (JWT, sessions, audit trail) and the routes of
// the admin context, with fake providers injected into the registry so nothing touches the network.

type scriptedProvider struct {
	code    string
	vouches bool
	people  map[string]domain.SocialProfile
}

func (p *scriptedProvider) Code() string          { return p.code }
func (p *scriptedProvider) VouchesForEmail() bool { return p.vouches }
func (p *scriptedProvider) AuthorizationURL(state, verifier string) string {
	return "https://" + p.code + ".test/authorize?state=" + url.QueryEscape(state) + "&code_challenge=" + domain.PKCEChallenge(verifier)
}
func (p *scriptedProvider) Profile(_ context.Context, code, _ string) (domain.SocialProfile, error) {
	return p.people[code], nil
}

func TestSocialLoginGivesWorkingTokensAndErasureDropsTheLinks(t *testing.T) {
	e := newBridgeEnv(t) // the database with the catalog and the admin use cases
	rdb := rds.Client(t)
	log := slog.New(slog.DiscardHandler)
	cfg := config.Config{
		JWTSecret: "0123456789abcdef0123456789abcdef", JWTIssuer: "rental-management", AccessTokenTTL: 15 * time.Minute,
		JWTLeeway: 5 * time.Second, RefreshTokenTTL: time.Hour, RefreshReuseGrace: 10 * time.Second,
		EmailVerificationTTL: time.Hour, EmailResendCooldown: time.Minute, PermissionCacheTTL: time.Minute,
		TermsVersion: "2026-11-03", WebURL: "http://localhost:2001", RedisKeyPrefix: "test:",
		SocialStateTTL: 10 * time.Minute, SocialLoginCodeTTL: time.Minute,
	}
	auth, err := newIdentity(cfg, log, e.pool, rdb, nil)
	if err != nil {
		t.Fatal(err)
	}
	google := &scriptedProvider{code: "google", vouches: true, people: map[string]domain.SocialProfile{
		"ann": {ID: "g-ann", Name: "Ann", Email: strp("ann@example.vn"), EmailVerified: true},
		"bob": {ID: "g-bob", Name: "Bob", Email: strp("bob@example.vn"), EmailVerified: true},
	}}
	zalo := &scriptedProvider{code: "zalo", people: map[string]domain.SocialProfile{"ann": {ID: "z-ann", Name: "Ann Zalo"}}}
	social := newSocialWith(cfg, auth, e.pool, rdb, []usecase.SocialProvider{google, zalo})

	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	engine.Use(httpx.RequestID(log), httpx.Locale(catalog), httpx.Recover())
	v1 := engine.Group("/v1")
	authenticator := httpadapter.NewAuthenticator(auth)
	binder := httpx.NewBinder()
	identityhttp.MountAuth(v1, identityhttp.NewAuthHandler(auth, binder, allowAll{}), authenticator, allowAll{})
	identityhttp.MountSocial(v1, identityhttp.NewSocialHandler(social, binder, cfg.WebURL), authenticator, allowAll{})
	adminhttp.MountSelf(v1, adminhttp.NewApplicationHandler(e.staff.applications, binder), adminhttp.NewProfileHandler(e.staff.profiles, binder),
		adminhttp.NewLegalHandler(e.staff.legal, binder), adminhttp.NewPersonalDataHandler(e.staff.personal, binder), authenticator, allowAll{})

	call := func(method, path string, body any, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		return serve(t, engine, method, path, body, bearer)
	}
	redirect := func(w *httptest.ResponseRecorder) *url.URL {
		t.Helper()
		if w.Code != http.StatusFound {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		u, _ := url.Parse(w.Header().Get("Location"))
		return u
	}
	signIn := func(provider, person string) identityhttp.LoginResult {
		t.Helper()
		started := redirect(call(http.MethodGet, "/v1/auth/"+provider, nil, ""))
		back := redirect(call(http.MethodGet, "/v1/auth/"+provider+"/callback?code="+person+"&state="+url.QueryEscape(started.Query().Get("state")), nil, ""))
		code := back.Query().Get("code")
		if code == "" {
			t.Fatalf("no code in %s", back)
		}
		w := call(http.MethodPost, "/v1/auth/social/exchange", map[string]any{"code": code}, "")
		testenv.ExpectStatus(t, w, http.StatusOK)
		return testenv.DecodeData[identityhttp.LoginResult](t, w)
	}

	ann := signIn("google", "ann")
	bob := signIn("google", "bob")

	// The tokens are real: an authenticated route of another context accepts them.
	testenv.ExpectStatus(t, call(http.MethodGet, "/v1/auth/me", nil, ann.Tokens.AccessToken), http.StatusOK)
	testenv.ExpectStatus(t, call(http.MethodGet, "/v1/profile/erasure-check", nil, ann.Tokens.AccessToken), http.StatusOK)
	testenv.ExpectStatus(t, call(http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": ann.Tokens.RefreshToken}, ""), http.StatusOK)

	// Ann links Zalo too (which has no email, so it links freely).
	w := call(http.MethodPost, "/v1/me/oauth-accounts/zalo", nil, ann.Tokens.AccessToken)
	testenv.ExpectStatus(t, w, http.StatusOK)
	target, _ := url.Parse(testenv.DecodeData[struct {
		URL string `json:"url"`
	}](t, w).URL)
	landed := redirect(call(http.MethodGet, "/v1/auth/zalo/callback?code=ann&state="+url.QueryEscape(target.Query().Get("state")), nil, ""))
	if landed.Path != "/profile" || landed.Query().Get("linked") != "zalo" {
		t.Fatalf("link landing: %s", landed)
	}

	// The audit trail says how each sign-in happened and records the link.
	var via string
	if err := e.pool.QueryRow(t.Context(), `SELECT meta->>'via' FROM audit_activity WHERE action = 'auth.login' AND actor_id = $1`, ann.User.ID).Scan(&via); err != nil || via != "google" {
		t.Fatalf("login via %q: %v", via, err)
	}
	var linked int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_logs WHERE action = 'auth.oauth_linked' AND actor_id = $1 AND meta->>'provider' = 'zalo'`, ann.User.ID).Scan(&linked)
	if linked != 1 {
		t.Errorf("the link must be in the trail, got %d entries", linked)
	}
	var registered int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_logs WHERE action = 'auth.register' AND meta->>'via' = 'google'`).Scan(&registered)
	if registered != 2 {
		t.Errorf("both sign-ups must be in the trail, got %d", registered)
	}

	// Erasure drops the links of the account being erased (and only those).
	hash, err := security.PasswordHasher{}.Hash("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `UPDATE users SET password_hash = $1 WHERE id = $2`, hash, ann.User.ID); err != nil {
		t.Fatal(err)
	}
	count := func(userID string) int {
		var n int
		_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM oauth_accounts WHERE user_id = $1`, userID).Scan(&n)
		return n
	}
	if count(ann.User.ID) != 2 || count(bob.User.ID) != 1 {
		t.Fatalf("before erasure: ann %d, bob %d", count(ann.User.ID), count(bob.User.ID))
	}
	testenv.ExpectStatus(t, call(http.MethodDelete, "/v1/profile", map[string]any{"password": "correct-horse-battery", "confirm": true}, ann.Tokens.AccessToken), http.StatusNoContent)
	if count(ann.User.ID) != 0 || count(bob.User.ID) != 1 {
		t.Fatalf("after erasure: ann %d, bob %d", count(ann.User.ID), count(bob.User.ID))
	}
	// The erased person's Google account no longer opens anything: signing in again creates a new, empty account.
	again := signIn("google", "ann")
	if again.User.ID == ann.User.ID {
		t.Error("an erased account must never come back")
	}
}

func strp(s string) *string { return &s }

// serve sends a JSON request to the engine.
func serve(t *testing.T, engine *gin.Engine, method, path string, body any, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}
