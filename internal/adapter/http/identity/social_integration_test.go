//go:build integration

package identityhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	httpadapter "rental-management/internal/adapter/http"
	"rental-management/internal/adapter/http/httpx"
	identityhttp "rental-management/internal/adapter/http/identity"
	identitypg "rental-management/internal/adapter/persistence/postgres/identity"
	redisstore "rental-management/internal/adapter/redis"
	domain "rental-management/internal/domain/identity"
	usecase "rental-management/internal/usecase/identity"
	"rental-management/pkg/authz"
	"rental-management/pkg/ratelimit"
)

const webURL = "http://localhost:2001"

// fakeProvider is a provider that needs no network: the "code" the test sends back stands for the person who signed in.
type fakeProvider struct {
	code    string
	vouches bool
	// people maps the callback code to the profile the provider would answer with.
	people map[string]domain.SocialProfile
}

func (p *fakeProvider) Code() string          { return p.code }
func (p *fakeProvider) VouchesForEmail() bool { return p.vouches }
func (p *fakeProvider) AuthorizationURL(state, verifier string) string {
	return "https://" + p.code + ".test/authorize?state=" + url.QueryEscape(state) + "&code_challenge=" + domain.PKCEChallenge(verifier)
}
func (p *fakeProvider) Profile(_ context.Context, code, _ string) (domain.SocialProfile, error) {
	profile, ok := p.people[code]
	if !ok {
		return domain.SocialProfile{}, context.Canceled
	}
	return profile, nil
}

func strPtr(s string) *string { return &s }

// socialEnv is the auth stack of newEnv with the social login routes on top, driven by fake providers.
type socialEnv struct {
	*env
	google *fakeProvider
	zalo   *fakeProvider
}

func newSocialEnv(t *testing.T) *socialEnv {
	t.Helper()
	e := newEnv(t)
	s := &socialEnv{
		env: e,
		google: &fakeProvider{code: "google", vouches: true, people: map[string]domain.SocialProfile{
			"ann": {ID: "g-ann", Name: "Ann Google", Email: strPtr("ann@example.vn"), EmailVerified: true},
			"bob": {ID: "g-bob", Name: "Bob", Email: strPtr("bob@example.vn"), EmailVerified: true},
		}},
		zalo: &fakeProvider{code: "zalo", people: map[string]domain.SocialProfile{"chi": {ID: "z-chi", Name: "Chi Zalo"}}},
	}
	social := usecase.NewSocial(e.uc, usecase.SocialDeps{
		Providers: usecase.NewSocialRegistry(s.google, s.zalo),
		States:    redisstore.NewSocialStateStore(e.rdb, "test:"), Codes: redisstore.NewLoginCodeStore(e.rdb, "test:"),
		Accounts: identitypg.NewOAuthAccountRepository(e.pool), Users: identitypg.NewUserRepository(e.pool),
	}, usecase.SocialOptions{StateTTL: 10 * time.Minute, CodeTTL: time.Minute, DefaultReturnPath: "dashboard"})
	limiter := ratelimit.NewRedis(e.rdb, "test:")
	identityhttp.MountSocial(e.engine.Group("/v1"), identityhttp.NewSocialHandler(social, httpx.NewBinder(), webURL), httpadapter.NewAuthenticator(e.uc), limiter)
	return s
}

// redirectOf reads where a redirect response goes.
func redirectOf(t *testing.T, w *httptest.ResponseRecorder) *url.URL {
	t.Helper()
	if w.Code != http.StatusFound {
		t.Fatalf("status %d, want %d: %s", w.Code, http.StatusFound, w.Body.String())
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// start begins a login (or a link, with a bearer) and returns the state the provider would send back.
func (s *socialEnv) start(provider, returnPath string) string {
	s.t.Helper()
	w := s.call(http.MethodGet, "/v1/auth/"+provider+"?redirect="+url.QueryEscape(returnPath), nil, "")
	u := redirectOf(s.t, w)
	if u.Host != provider+".test" {
		s.t.Fatalf("redirected to %s", u)
	}
	return u.Query().Get("state")
}

func (s *socialEnv) callback(provider, state, code string) *url.URL {
	s.t.Helper()
	w := s.call(http.MethodGet, "/v1/auth/"+provider+"/callback?state="+url.QueryEscape(state)+"&code="+url.QueryEscape(code), nil, "")
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		s.t.Errorf("a callback may carry a code: no caching, no referrer: %v", w.Header())
	}
	return redirectOf(s.t, w)
}

// link runs the whole link round trip for the signed-in account and returns where the browser lands.
func (s *socialEnv) link(bearer, provider, code string) *url.URL {
	s.t.Helper()
	w := s.call(http.MethodPost, "/v1/me/oauth-accounts/"+provider, nil, bearer)
	expectStatus(s.t, w, http.StatusOK)
	target, _ := url.Parse(decodeData[struct {
		URL string `json:"url"`
	}](s.t, w).URL)
	return s.callback(provider, target.Query().Get("state"), code)
}

func (s *socialEnv) exchange(code string) *httptest.ResponseRecorder {
	return s.call(http.MethodPost, "/v1/auth/social/exchange", map[string]any{"code": code}, "")
}

func TestProvidersListsTheEnabledOnes(t *testing.T) {
	s := newSocialEnv(t)
	w := s.call(http.MethodGet, "/v1/auth/providers", nil, "")
	expectStatus(t, w, http.StatusOK)
	got := decodeData[struct {
		Providers []identityhttp.ProviderBody `json:"providers"`
	}](t, w).Providers
	if len(got) != 2 || got[0].Provider != "google" || !got[0].RequiresMatchingEmail || got[1].Provider != "zalo" || got[1].RequiresMatchingEmail {
		t.Fatalf("got %+v", got)
	}
}

func TestSocialSignUpGivesAOneTimeCodeThatOpensATenantSession(t *testing.T) {
	s := newSocialEnv(t)
	state := s.start("google", "my-rooms")
	landing := s.callback("google", state, "ann")
	if landing.String()[:len(webURL)] != webURL || landing.Path != "/my-rooms" || landing.Query().Get("code") == "" {
		t.Fatalf("landing %s", landing)
	}
	if landing.Query().Get("access_token") != "" || landing.Query().Get("refresh_token") != "" {
		t.Error("tokens must never travel in the URL")
	}
	code := landing.Query().Get("code")

	w := s.exchange(code)
	expectStatus(t, w, http.StatusOK)
	result := decodeData[identityhttp.LoginResult](t, w)
	if result.User.ActiveRole != authz.RoleTenant || result.User.Email == nil || *result.User.Email != "ann@example.vn" ||
		result.User.FullName != "Ann Google" || !result.User.Terms.NeedsAcceptance {
		t.Fatalf("user: %+v", result.User)
	}
	expectStatus(t, s.call(http.MethodGet, "/v1/auth/me", nil, result.Tokens.AccessToken), http.StatusOK)
	// The refresh token of that session keeps working like any other.
	expectStatus(t, s.refresh(result.Tokens.RefreshToken), http.StatusOK)

	expectError(t, s.exchange(code), http.StatusBadRequest, "auth.social_code_invalid")
	expectError(t, s.exchange("never-issued"), http.StatusBadRequest, "auth.social_code_invalid")

	// A password was never set: signing in with an email and a password is impossible.
	expectError(t, s.login("ann@example.vn", "anything-at-all"), http.StatusUnauthorized, "auth.invalid_credentials")

	// The second time, the same person lands on the same account.
	again := s.callback("google", s.start("google", ""), "ann")
	if again.Path != "/dashboard" {
		t.Errorf("default return path expected, got %s", again)
	}
	second := decodeData[identityhttp.LoginResult](t, s.exchange(again.Query().Get("code")))
	if second.User.ID != result.User.ID {
		t.Error("the same Google account must open the same account")
	}
}

func TestZaloSignUpHasNoEmail(t *testing.T) {
	s := newSocialEnv(t)
	landing := s.callback("zalo", s.start("zalo", ""), "chi")
	result := decodeData[identityhttp.LoginResult](t, s.exchange(landing.Query().Get("code")))
	if result.User.Email != nil || result.User.FullName != "Chi Zalo" || result.User.ActiveRole != authz.RoleTenant {
		t.Fatalf("user: %+v", result.User)
	}
}

func TestAVouchedEmailMergesIntoThePasswordAccount(t *testing.T) {
	s := newSocialEnv(t)
	id := s.register("ann@example.vn") // never verified
	landing := s.callback("google", s.start("google", ""), "ann")
	result := decodeData[identityhttp.LoginResult](t, s.exchange(landing.Query().Get("code")))
	if result.User.ID != id {
		t.Fatalf("expected the existing account %s, got %s", id, result.User.ID)
	}
	// Google proved the address: the unverified password no longer counts, but the account is verified.
	expectError(t, s.login("ann@example.vn", testPassword), http.StatusUnauthorized, "auth.invalid_credentials")
	var verified bool
	_ = s.pool.QueryRow(context.Background(), `SELECT email_verified_at IS NOT NULL FROM users WHERE id = $1`, id).Scan(&verified)
	if !verified {
		t.Error("the email must be verified")
	}
}

func TestCallbackRefusals(t *testing.T) {
	s := newSocialEnv(t)
	loginError := func(u *url.URL) string {
		t.Helper()
		if u.Path != "/login" {
			t.Fatalf("expected the login page, got %s", u)
		}
		return u.Query().Get("error")
	}
	if got := loginError(s.callback("google", "forged", "ann")); got != "social_failed" {
		t.Errorf("forged state: %q", got)
	}
	state := s.start("google", "")
	_ = s.callback("google", state, "ann")
	if got := loginError(s.callback("google", state, "ann")); got != "social_failed" {
		t.Errorf("reused state: %q", got)
	}
	if got := loginError(s.callback("google", s.start("google", ""), "")); got != "social_failed" {
		t.Errorf("cancelled at the provider: %q", got)
	}
	if got := loginError(s.callback("google", s.start("google", ""), "nobody")); got != "social_failed" {
		t.Errorf("provider failure: %q", got)
	}
	if got := loginError(s.callback("zalo", s.start("google", ""), "chi")); got != "social_failed" {
		t.Errorf("state of another provider: %q", got)
	}

	// A disabled account does not sign in.
	s.exec(`UPDATE users SET status = 'inactive' WHERE email = 'ann@example.vn'`)
	if got := loginError(s.callback("google", s.start("google", ""), "ann")); got != "account_inactive" {
		t.Errorf("disabled account: %q", got)
	}
	// Staff accounts are not opened by social login.
	s.verified("bob@example.vn")
	s.exec(`INSERT INTO user_roles (user_id, role_code) SELECT id, 'admin' FROM users WHERE email = 'bob@example.vn'`)
	if got := loginError(s.callback("google", s.start("google", ""), "bob")); got != "link_staff" {
		t.Errorf("staff account: %q", got)
	}
}

func TestUnknownProviderAndBadExchangeBody(t *testing.T) {
	s := newSocialEnv(t)
	expectStatus(t, s.call(http.MethodGet, "/v1/auth/facebook", nil, ""), http.StatusNotFound)
	expectError(t, s.call(http.MethodPost, "/v1/auth/social/exchange", map[string]any{}, ""), http.StatusUnprocessableEntity, "validation.failed")
	expectStatus(t, s.call(http.MethodGet, "/v1/me/oauth-accounts", nil, ""), http.StatusUnauthorized)
	expectStatus(t, s.call(http.MethodPost, "/v1/me/oauth-accounts/zalo", nil, ""), http.StatusUnauthorized)
}

func TestLinkUnlinkAndRelink(t *testing.T) {
	s := newSocialEnv(t)
	s.verified("ann@example.vn")
	bearer := s.signedIn("ann@example.vn").Tokens.AccessToken

	list := func() (bool, map[string]identityhttp.LinkedProviderBody) {
		t.Helper()
		w := s.call(http.MethodGet, "/v1/me/oauth-accounts", nil, bearer)
		expectStatus(t, w, http.StatusOK)
		body := decodeData[struct {
			HasPassword bool                              `json:"has_password"`
			Providers   []identityhttp.LinkedProviderBody `json:"providers"`
		}](t, w)
		byName := map[string]identityhttp.LinkedProviderBody{}
		for _, p := range body.Providers {
			byName[p.Provider] = p
		}
		return body.HasPassword, byName
	}

	hasPassword, providers := list()
	if !hasPassword || providers["google"].Linked || providers["google"].CanUnlink || !providers["google"].RequiresMatchingEmail {
		t.Fatalf("before linking: %+v", providers)
	}

	if u := s.link(bearer, "google", "ann"); u.Path != "/profile" || u.Query().Get("linked") != "google" {
		t.Fatalf("link landing: %s", u)
	}
	_, providers = list()
	if g := providers["google"]; !g.Linked || g.LinkedAt == nil || !g.CanUnlink || g.Unlinked {
		t.Fatalf("after linking: %+v", g)
	}
	// Linking the same account again changes nothing and is not an error.
	if u := s.link(bearer, "google", "ann"); u.Query().Get("linked") != "google" {
		t.Fatalf("link again: %s", u)
	}

	// Signing in with Google now opens this account, which keeps its password.
	landing := s.callback("google", s.start("google", ""), "ann")
	if decodeData[identityhttp.LoginResult](t, s.exchange(landing.Query().Get("code"))).User.Email == nil {
		t.Fatal("the account has its email")
	}
	expectStatus(t, s.login("ann@example.vn", testPassword), http.StatusOK)

	expectStatus(t, s.call(http.MethodDelete, "/v1/me/oauth-accounts/google", nil, bearer), http.StatusNoContent)
	expectError(t, s.call(http.MethodDelete, "/v1/me/oauth-accounts/google", nil, bearer), http.StatusUnprocessableEntity, "validation.failed")
	_, providers = list()
	if g := providers["google"]; g.Linked || !g.Unlinked || g.LinkedAt != nil {
		t.Fatalf("after unlinking: %+v", g)
	}
	var rows int
	_ = s.pool.QueryRow(context.Background(), `SELECT count(*) FROM oauth_accounts WHERE unlinked_at IS NOT NULL`).Scan(&rows)
	if rows != 1 {
		t.Errorf("the row must stay, marked unlinked: %d", rows)
	}

	// After unlinking Google no longer signs in, not even through the email.
	refused := s.callback("google", s.start("google", ""), "ann")
	if refused.Path != "/login" || refused.Query().Get("error") != "link_unlinked" {
		t.Fatalf("sign-in after unlinking: %s", refused)
	}
	// Only a signed-in person links it again.
	if u := s.link(bearer, "google", "ann"); u.Query().Get("linked") != "google" {
		t.Fatalf("link again after unlinking: %s", u)
	}
	_, providers = list()
	if !providers["google"].Linked {
		t.Fatalf("relinked: %+v", providers["google"])
	}
}

func TestLinkRefusalsArriveAsQueryValues(t *testing.T) {
	s := newSocialEnv(t)
	s.verified("ann@example.vn")
	s.verified("bob@example.vn")
	annBearer := s.signedIn("ann@example.vn").Tokens.AccessToken
	bobBearer := s.signedIn("bob@example.vn").Tokens.AccessToken

	check := func(u *url.URL, want string) {
		t.Helper()
		if u.Path != "/profile" || u.Query().Get("link_error") != want || u.Query().Get("provider") == "" {
			t.Errorf("want link_error=%s, got %s", want, u)
		}
	}
	// Google gives bob's email, not ann's: linking never changes the email.
	check(s.link(annBearer, "google", "bob"), "link_email_mismatch")
	if u := s.link(bobBearer, "google", "bob"); u.Query().Get("linked") != "google" {
		t.Fatalf("bob links his own: %s", u)
	}
	// Zalo links freely, but one Zalo account opens one account.
	if u := s.link(bobBearer, "zalo", "chi"); u.Query().Get("linked") != "zalo" {
		t.Fatalf("bob links zalo: %s", u)
	}
	check(s.link(annBearer, "zalo", "chi"), "link_elsewhere")
	// An account has one Zalo account.
	s.zalo.people["dung"] = domain.SocialProfile{ID: "z-dung", Name: "Dung"}
	check(s.link(bobBearer, "zalo", "dung"), "link_provider_taken")
	// An account without an email cannot link Google.
	zaloLanding := s.callback("zalo", s.start("zalo", ""), "dung")
	zaloUser := decodeData[identityhttp.LoginResult](t, s.exchange(zaloLanding.Query().Get("code")))
	check(s.link(zaloUser.Tokens.AccessToken, "google", "ann"), "link_no_email")
	// A broken round trip.
	w := s.call(http.MethodPost, "/v1/me/oauth-accounts/google", nil, annBearer)
	target, _ := url.Parse(decodeData[struct {
		URL string `json:"url"`
	}](t, w).URL)
	check(s.callback("google", target.Query().Get("state"), ""), "social_failed")
	expectStatus(t, s.call(http.MethodPost, "/v1/me/oauth-accounts/facebook", nil, annBearer), http.StatusNotFound)
	// Staff accounts cannot link.
	s.exec(`INSERT INTO user_roles (user_id, role_code) SELECT id, 'admin' FROM users WHERE email = 'ann@example.vn'`)
	check(s.link(annBearer, "google", "ann"), "link_staff")
}

func TestTheLastWayToSignInCannotBeRemoved(t *testing.T) {
	s := newSocialEnv(t)
	landing := s.callback("zalo", s.start("zalo", ""), "chi")
	result := decodeData[identityhttp.LoginResult](t, s.exchange(landing.Query().Get("code")))
	bearer := result.Tokens.AccessToken

	w := s.call(http.MethodGet, "/v1/me/oauth-accounts", nil, bearer)
	body := decodeData[struct {
		HasPassword bool                              `json:"has_password"`
		Providers   []identityhttp.LinkedProviderBody `json:"providers"`
	}](t, w)
	if body.HasPassword || body.Providers[1].Provider != "zalo" || !body.Providers[1].Linked || body.Providers[1].CanUnlink {
		t.Fatalf("zalo is the only way in: %+v", body)
	}
	w = s.call(http.MethodDelete, "/v1/me/oauth-accounts/zalo", nil, bearer)
	expectError(t, w, http.StatusUnprocessableEntity, "validation.failed")
	if !strings.Contains(w.Body.String(), "last_sign_in_method") && !strings.Contains(w.Body.String(), "last way") {
		t.Errorf("the message must say why: %s", w.Body.String())
	}
	expectStatus(t, s.call(http.MethodGet, "/v1/auth/me", nil, bearer), http.StatusOK)
}

func TestSocialRoutesAreRateLimitedPerAddress(t *testing.T) {
	s := newSocialEnv(t)
	limited := 0
	for i := 0; i < 35; i++ {
		if s.call(http.MethodGet, "/v1/auth/providers", nil, "").Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited != 5 {
		t.Errorf("30 requests a minute per address, got %d refused of 35", limited)
	}
}
