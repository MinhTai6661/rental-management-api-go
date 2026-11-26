package oauthadapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	domain "rental-management/internal/domain/identity"
)

// fakeZalo stands in for the OAuth and Graph endpoints of Zalo.
type fakeZalo struct {
	server      *httptest.Server
	tokenHeader http.Header
	tokenForm   url.Values
	meHeader    http.Header
	meQuery     url.Values
	tokenStatus int
	tokenBody   string
	meStatus    int
	meBody      string
	delay       time.Duration
}

func newFakeZalo(t *testing.T) *fakeZalo {
	t.Helper()
	f := &fakeZalo{
		tokenStatus: http.StatusOK, tokenBody: `{"access_token":"zalo-access","refresh_token":"r","expires_in":"3600"}`,
		meStatus: http.StatusOK, meBody: `{"id":"7001","name":"Bao","picture":{"data":{"url":"https://p/x.png"}}}`,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(f.delay)
		_ = r.ParseForm()
		f.tokenHeader, f.tokenForm = r.Header.Clone(), r.PostForm
		w.WriteHeader(f.tokenStatus)
		_, _ = w.Write([]byte(f.tokenBody))
	})
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		f.meHeader, f.meQuery = r.Header.Clone(), r.URL.Query()
		w.WriteHeader(f.meStatus)
		_, _ = w.Write([]byte(f.meBody))
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeZalo) client(httpClient *http.Client) *ZaloClient {
	return NewZaloClient(ZaloClientConfig{
		AppID: "app-1", AppSecret: "app-secret", AuthorizeURL: f.server.URL + "/permission",
		TokenURL: f.server.URL + "/token", MeURL: f.server.URL + "/me", HTTPClient: httpClient,
	})
}

func (f *fakeZalo) provider() *Zalo {
	return NewZalo(f.client(nil), "https://api.test/v1/auth/zalo/callback")
}

func TestZaloAuthorizationURLUsesPKCEAndNeverTheSecret(t *testing.T) {
	f := newFakeZalo(t)
	raw := f.provider().AuthorizationURL("the-state", testVerifier)
	u, _ := url.Parse(raw)
	q := u.Query()
	if u.Path != "/permission" || q.Get("app_id") != "app-1" || q.Get("state") != "the-state" || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") != domain.PKCEChallenge(testVerifier) || q.Get("redirect_uri") != "https://api.test/v1/auth/zalo/callback" {
		t.Fatalf("unexpected URL: %s", raw)
	}
	if strings.Contains(raw, testVerifier) || strings.Contains(raw, "app-secret") {
		t.Error("neither the verifier nor the app secret may be in the URL")
	}
}

func TestZaloProfileSendsTheSecretKeyHeaderAndTheTokenHeader(t *testing.T) {
	f := newFakeZalo(t)
	got, err := f.provider().Profile(context.Background(), "zalo-code", testVerifier)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "zalo" || got.ID != "7001" || got.Name != "Bao" || got.Email != nil || got.EmailVerified {
		t.Fatalf("profile: %+v", got)
	}
	if f.tokenHeader.Get("secret_key") != "app-secret" || f.tokenForm.Get("app_id") != "app-1" || f.tokenForm.Get("code") != "zalo-code" ||
		f.tokenForm.Get("code_verifier") != testVerifier || f.tokenForm.Get("grant_type") != "authorization_code" {
		t.Errorf("token request: %v %v", f.tokenHeader, f.tokenForm)
	}
	if f.tokenForm.Get("secret_key") != "" || f.tokenForm.Get("app_secret") != "" {
		t.Error("the secret goes in the header only")
	}
	if f.meHeader.Get("access_token") != "zalo-access" || f.meQuery.Get("fields") != "id,name,picture" {
		t.Errorf("me request: %v %v", f.meHeader, f.meQuery)
	}
}

func TestZaloProfileAcceptsANumericId(t *testing.T) {
	f := newFakeZalo(t)
	f.meBody = `{"id":7002,"name":"Chi"}`
	got, err := f.provider().Profile(context.Background(), "c", testVerifier)
	if err != nil || got.ID != "7002" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestZaloFailures(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fakeZalo)
	}{
		{"error in a 200 token answer", func(f *fakeZalo) { f.tokenBody = `{"error":-14014,"error_name":"Invalid authorization code"}` }},
		{"error given as text", func(f *fakeZalo) { f.tokenBody = `{"error":"-14014","error_reason":"bad"}` }},
		{"token HTTP error", func(f *fakeZalo) { f.tokenStatus = http.StatusInternalServerError }},
		{"token answer without a token", func(f *fakeZalo) { f.tokenBody = `{"expires_in":"3600"}` }},
		{"token answer is not JSON", func(f *fakeZalo) { f.tokenBody = `nope` }},
		{"error in a 200 profile answer", func(f *fakeZalo) { f.meBody = `{"error":-216,"message":"Access token is invalid"}` }},
		{"profile HTTP error", func(f *fakeZalo) { f.meStatus = http.StatusBadGateway }},
		{"profile without an id", func(f *fakeZalo) { f.meBody = `{"name":"x"}` }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeZalo(t)
			tc.setup(f)
			if _, err := f.provider().Profile(context.Background(), "c", testVerifier); err == nil {
				t.Fatal("want an error")
			}
		})
	}
	if _, err := newFakeZalo(t).provider().Profile(context.Background(), "", testVerifier); err == nil {
		t.Error("no code is an error")
	}
}

func TestZaloGivesUpOnASlowProviderAndOnACancelledContext(t *testing.T) {
	f := newFakeZalo(t)
	f.delay = 500 * time.Millisecond
	slow := NewZalo(f.client(&http.Client{Timeout: 50 * time.Millisecond}), "https://api.test/cb")
	start := time.Now()
	if _, err := slow.Profile(context.Background(), "c", testVerifier); err == nil || time.Since(start) > 400*time.Millisecond {
		t.Fatalf("a slow provider must time out quickly, got %v after %v", err, time.Since(start))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.provider().Profile(ctx, "c", testVerifier); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled context must stop the call, got %v", err)
	}
}

func TestZaloIdentity(t *testing.T) {
	z := newFakeZalo(t).provider()
	if z.Code() != "zalo" || z.VouchesForEmail() {
		t.Error("zalo gives no email")
	}
}

func TestZaloErrorCode(t *testing.T) {
	cases := map[string]int{"": 0, "null": 0, "0": 0, `"0"`: 0, "-14014": -14014, `"-216"`: -216, `"weird"`: -1}
	for in, want := range cases {
		if got := zaloErrorCode([]byte(in)); got != want {
			t.Errorf("%q: got %d, want %d", in, got, want)
		}
	}
}
