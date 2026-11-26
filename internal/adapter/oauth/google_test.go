package oauthadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	domain "rental-management/internal/domain/identity"
)

const testVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"

// fakeGoogle stands in for the token and userinfo endpoints of Google.
type fakeGoogle struct {
	server      *httptest.Server
	tokenForm   url.Values
	tokenStatus int
	userinfo    string
	userStatus  int
	userAuth    string
	delay       time.Duration
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	f := &fakeGoogle{tokenStatus: http.StatusOK, userStatus: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(f.delay)
		_ = r.ParseForm()
		f.tokenForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.tokenStatus)
		if f.tokenStatus != http.StatusOK {
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"google-access","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		f.userAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.userStatus)
		_, _ = w.Write([]byte(f.userinfo))
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGoogle) provider(client *http.Client) *Google {
	return NewGoogle(GoogleConfig{
		ClientID: "client-id", ClientSecret: "client-secret", RedirectURL: "https://api.test/v1/auth/google/callback",
		Endpoint:    &oauth2.Endpoint{AuthURL: f.server.URL + "/auth", TokenURL: f.server.URL + "/token"},
		UserInfoURL: f.server.URL + "/userinfo", HTTPClient: client,
	})
}

func TestGoogleAuthorizationURLCarriesStateAndOnlyThePKCEChallenge(t *testing.T) {
	f := newFakeGoogle(t)
	raw := f.provider(nil).AuthorizationURL("the-state", testVerifier)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Path != "/auth" || q.Get("state") != "the-state" || q.Get("client_id") != "client-id" || q.Get("response_type") != "code" ||
		q.Get("redirect_uri") != "https://api.test/v1/auth/google/callback" || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") != domain.PKCEChallenge(testVerifier) || q.Get("scope") != "openid email profile" {
		t.Fatalf("unexpected URL: %s", raw)
	}
	if strings.Contains(raw, testVerifier) || strings.Contains(raw, "client-secret") {
		t.Error("neither the verifier nor the client secret may be in the URL")
	}
}

func TestGoogleProfileTradesTheCodeAndMapsTheUserinfo(t *testing.T) {
	f := newFakeGoogle(t)
	f.userinfo = `{"sub":"1234","email":"Ann@Example.vn","email_verified":true,"name":" Ann Nguyen ","picture":"https://x/y.png"}`
	got, err := f.provider(nil).Profile(context.Background(), "auth-code", testVerifier)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "google" || got.ID != "1234" || got.Name != "Ann Nguyen" || got.Email == nil || *got.Email != "Ann@Example.vn" || !got.EmailVerified {
		t.Fatalf("profile: %+v", got)
	}
	if f.tokenForm.Get("code") != "auth-code" || f.tokenForm.Get("code_verifier") != testVerifier || f.tokenForm.Get("grant_type") != "authorization_code" ||
		f.tokenForm.Get("redirect_uri") != "https://api.test/v1/auth/google/callback" {
		t.Errorf("token request: %v", f.tokenForm)
	}
	if f.userAuth != "Bearer google-access" {
		t.Errorf("the userinfo call must carry the access token, got %q", f.userAuth)
	}
}

func TestGoogleProfileKeepsAnUnverifiedEmailUnvouched(t *testing.T) {
	f := newFakeGoogle(t)
	f.userinfo = `{"sub":"1","email":"a@x.vn","email_verified":false,"name":""}`
	got, err := f.provider(nil).Profile(context.Background(), "c", testVerifier)
	if err != nil {
		t.Fatal(err)
	}
	if got.EmailVerified || got.Name != "a@x.vn" {
		t.Fatalf("an email Google did not verify must not be vouched, and the name falls back to it: %+v", got)
	}
}

func TestGoogleProfileFailures(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fakeGoogle)
	}{
		{"token refused", func(f *fakeGoogle) { f.tokenStatus = http.StatusBadRequest }},
		{"userinfo refused", func(f *fakeGoogle) { f.userStatus = http.StatusUnauthorized; f.userinfo = `{}` }},
		{"profile without an id", func(f *fakeGoogle) { f.userinfo = `{"email":"a@x.vn"}` }},
		{"userinfo is not JSON", func(f *fakeGoogle) { f.userinfo = `<html>` }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGoogle(t)
			tc.setup(f)
			if _, err := f.provider(nil).Profile(context.Background(), "c", testVerifier); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestGoogleGivesUpOnASlowProvider(t *testing.T) {
	f := newFakeGoogle(t)
	f.delay = 500 * time.Millisecond
	start := time.Now()
	_, err := f.provider(&http.Client{Timeout: 50 * time.Millisecond}).Profile(context.Background(), "c", testVerifier)
	if err == nil || time.Since(start) > 400*time.Millisecond {
		t.Fatalf("a slow provider must time out quickly, got %v after %v", err, time.Since(start))
	}
}

func TestGoogleIdentity(t *testing.T) {
	g := NewGoogle(GoogleConfig{})
	if g.Code() != "google" || !g.VouchesForEmail() {
		t.Error("google gives a verified email")
	}
}
