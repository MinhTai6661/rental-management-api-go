package identity

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

// Fakes for the ports of social login; like the others they stand in for the adapters, no network or database.

type fakeProvider struct {
	code    string
	vouches bool
	profile domain.SocialProfile
	err     error
	// lastVerifier is the PKCE verifier the use case sent with the code.
	lastVerifier string
}

func (p *fakeProvider) Code() string          { return p.code }
func (p *fakeProvider) VouchesForEmail() bool { return p.vouches }
func (p *fakeProvider) AuthorizationURL(state, verifier string) string {
	return "https://" + p.code + ".test/authorize?state=" + state + "&challenge=" + domain.PKCEChallenge(verifier)
}
func (p *fakeProvider) Profile(_ context.Context, _, verifier string) (domain.SocialProfile, error) {
	p.lastVerifier = verifier
	return p.profile, p.err
}

type fakeStates struct {
	items map[string]domain.SocialState
	n     int
}

func (f *fakeStates) Put(_ context.Context, s domain.SocialState, _ time.Duration) (string, error) {
	f.n++
	key := "state-" + strconv.Itoa(f.n)
	f.items[key] = s
	return key, nil
}

func (f *fakeStates) Take(_ context.Context, key string) (domain.SocialState, error) {
	s, ok := f.items[key]
	if !ok {
		return domain.SocialState{}, shared.ErrNotFound
	}
	delete(f.items, key)
	return s, nil
}

type fakeCodes struct {
	items map[string]domain.SocialLoginGrant
	n     int
}

func (f *fakeCodes) Put(_ context.Context, g domain.SocialLoginGrant, _ time.Duration) (string, error) {
	f.n++
	code := "code-" + strconv.Itoa(f.n)
	f.items[code] = g
	return code, nil
}

func (f *fakeCodes) Take(_ context.Context, code string) (domain.SocialLoginGrant, error) {
	g, ok := f.items[code]
	if !ok {
		return domain.SocialLoginGrant{}, shared.ErrNotFound
	}
	delete(f.items, code)
	return g, nil
}

type fakeOAuthAccounts struct {
	rows []domain.OAuthAccount
}

func (f *fakeOAuthAccounts) FindByProvider(_ context.Context, provider, id string) (domain.OAuthAccount, error) {
	for _, r := range f.rows {
		if r.Provider == provider && r.ProviderUserID == id {
			return r, nil
		}
	}
	return domain.OAuthAccount{}, shared.ErrNotFound
}

func (f *fakeOAuthAccounts) ListOfUser(_ context.Context, userID uuid.UUID) ([]domain.OAuthAccount, error) {
	var out []domain.OAuthAccount
	for _, r := range f.rows {
		if r.UserID == userID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeOAuthAccounts) ListOfUserForUpdate(ctx context.Context, userID uuid.UUID) ([]domain.OAuthAccount, error) {
	return f.ListOfUser(ctx, userID)
}

func (f *fakeOAuthAccounts) Create(_ context.Context, a domain.OAuthAccount, _ time.Time) error {
	f.rows = append(f.rows, a)
	return nil
}

func (f *fakeOAuthAccounts) update(userID uuid.UUID, provider string, fn func(*domain.OAuthAccount)) {
	for i := range f.rows {
		if f.rows[i].UserID == userID && f.rows[i].Provider == provider {
			fn(&f.rows[i])
		}
	}
}

func (f *fakeOAuthAccounts) Relink(_ context.Context, userID uuid.UUID, provider string, _ time.Time) error {
	f.update(userID, provider, func(r *domain.OAuthAccount) { r.UnlinkedAt = nil })
	return nil
}

func (f *fakeOAuthAccounts) Replace(_ context.Context, userID uuid.UUID, provider, id string, _ time.Time) error {
	f.update(userID, provider, func(r *domain.OAuthAccount) { r.ProviderUserID, r.UnlinkedAt = id, nil })
	return nil
}

func (f *fakeOAuthAccounts) Unlink(_ context.Context, userID uuid.UUID, provider string, now time.Time) error {
	f.update(userID, provider, func(r *domain.OAuthAccount) { r.UnlinkedAt = &now })
	return nil
}

// socialAccount is an account of the fake user store.
type socialAccount struct {
	id       uuid.UUID
	email    *string
	password *string
	verified *time.Time
	status   string
	roles    []string
}

type fakeSocialUsers struct {
	SocialUserStore
	accounts  map[uuid.UUID]*socialAccount
	created   []domain.NewSocialUser
	roles     []string
	confirmed []uuid.UUID
	createErr error
}

func (f *fakeSocialUsers) add(a socialAccount) *socialAccount {
	if a.id == uuid.Nil {
		a.id = uuid.New()
	}
	if a.status == "" {
		a.status = authz.StatusActive
	}
	if a.roles == nil {
		a.roles = []string{authz.RoleTenant}
	}
	f.accounts[a.id] = &a
	return &a
}

func (f *fakeSocialUsers) FindByEmail(_ context.Context, email string) (domain.User, error) {
	for _, a := range f.accounts {
		if a.email != nil && *a.email == email {
			return domain.User{ID: a.id, Email: a.email, PasswordHash: a.password, Status: a.status, EmailVerifiedAt: a.verified}, nil
		}
	}
	return domain.User{}, shared.ErrNotFound
}

func (f *fakeSocialUsers) AuthUser(_ context.Context, id uuid.UUID) (domain.AuthUser, error) {
	a, ok := f.accounts[id]
	if !ok {
		return domain.AuthUser{}, shared.ErrNotFound
	}
	return domain.AuthUser{Status: a.status, Roles: a.roles}, nil
}

func (f *fakeSocialUsers) Profile(_ context.Context, id uuid.UUID) (domain.UserProfile, error) {
	a, ok := f.accounts[id]
	if !ok {
		return domain.UserProfile{}, shared.ErrNotFound
	}
	return domain.UserProfile{ID: id, Email: a.email}, nil
}

func (f *fakeSocialUsers) PasswordHash(_ context.Context, id uuid.UUID) (*string, error) {
	return f.accounts[id].password, nil
}

func (f *fakeSocialUsers) AddRole(_ context.Context, _ uuid.UUID, role string, _ time.Time) error {
	f.roles = append(f.roles, role)
	return nil
}

func (f *fakeSocialUsers) CreateSocial(_ context.Context, u domain.NewSocialUser) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, u)
	f.accounts[u.ID] = &socialAccount{id: u.ID, email: u.Email, status: authz.StatusActive, roles: []string{authz.RoleTenant}}
	return nil
}

func (f *fakeSocialUsers) ConfirmEmailFromProvider(_ context.Context, id uuid.UUID, now time.Time) error {
	f.confirmed = append(f.confirmed, id)
	f.accounts[id].verified, f.accounts[id].password = &now, nil
	return nil
}

type socialFixture struct {
	social *Social
	auth   *authFixture
	google *fakeProvider
	zalo   *fakeProvider
	states *fakeStates
	codes  *fakeCodes
	oauth  *fakeOAuthAccounts
	users  *fakeSocialUsers
}

func newSocialFixture(t *testing.T) *socialFixture {
	t.Helper()
	f := &socialFixture{
		auth:   newAuthFixture(t),
		google: &fakeProvider{code: domain.ProviderGoogle, vouches: true},
		zalo:   &fakeProvider{code: domain.ProviderZalo},
		states: &fakeStates{items: map[string]domain.SocialState{}},
		codes:  &fakeCodes{items: map[string]domain.SocialLoginGrant{}},
		oauth:  &fakeOAuthAccounts{},
		users:  &fakeSocialUsers{accounts: map[uuid.UUID]*socialAccount{}},
	}
	f.auth.auth.log = slog.New(slog.DiscardHandler)
	f.social = NewSocial(f.auth.auth, SocialDeps{
		Providers: NewSocialRegistry(f.google, f.zalo), States: f.states, Codes: f.codes, Accounts: f.oauth, Users: f.users,
	}, SocialOptions{StateTTL: 10 * time.Minute, CodeTTL: time.Minute, DefaultReturnPath: "dashboard"})
	return f
}

func strp(s string) *string { return &s }

// roundTrip runs a login from the redirect to the callback with the profile the provider answers.
func (f *socialFixture) roundTrip(t *testing.T, p *fakeProvider, profile domain.SocialProfile) Outcome {
	t.Helper()
	p.profile = profile
	url, err := f.social.BeginLogin(context.Background(), p.code, "my-rooms")
	if err != nil {
		t.Fatal(err)
	}
	return f.social.Complete(context.Background(), Callback{Provider: p.code, State: stateIn(url), Code: "provider-code"})
}

func stateIn(url string) string {
	rest := url[strings.Index(url, "state=")+len("state="):]
	return strings.SplitN(rest, "&", 2)[0]
}

var googleAnn = domain.SocialProfile{ID: "g-1", Name: "Ann", Email: strp("Ann@Example.vn"), EmailVerified: true}

func TestBeginLoginSendsPKCEAndKeepsStateOnTheServer(t *testing.T) {
	f := newSocialFixture(t)
	url, err := f.social.BeginLogin(context.Background(), "google", "//evil.com")
	if err != nil {
		t.Fatal(err)
	}
	state := f.states.items[stateIn(url)]
	if state.Intent != domain.IntentLogin || state.Provider != "google" || state.CodeVerifier == "" {
		t.Fatalf("state: %+v", state)
	}
	if state.ReturnPath != "dashboard" {
		t.Errorf("an unsafe return path must fall back to the default, got %q", state.ReturnPath)
	}
	if !strings.Contains(url, "challenge="+domain.PKCEChallenge(state.CodeVerifier)) {
		t.Errorf("the URL must carry the PKCE challenge of the stored verifier: %s", url)
	}
	if strings.Contains(url, state.CodeVerifier) {
		t.Error("the verifier itself must never leave the server")
	}
	if _, err := f.social.BeginLogin(context.Background(), "facebook", ""); !errors.Is(err, domain.ErrProviderUnavailable) {
		t.Errorf("unknown provider: %v", err)
	}
}

func TestProvidersListsOnlyWhatIsRegistered(t *testing.T) {
	f := newSocialFixture(t)
	f.social.d.Providers = NewSocialRegistry(f.zalo)
	got := f.social.Providers()
	if len(got) != 1 || got[0].Code != "zalo" || got[0].RequiresMatchingEmail {
		t.Fatalf("got %+v", got)
	}
	if _, err := f.social.BeginLogin(context.Background(), "google", ""); !errors.Is(err, domain.ErrProviderUnavailable) {
		t.Errorf("a provider that is not registered must be unreachable: %v", err)
	}
}

func TestNewPersonSignsUpAsTenantAndLogsInWithTheCodeOnce(t *testing.T) {
	f := newSocialFixture(t)
	out := f.roundTrip(t, f.google, googleAnn)
	if out.Err != nil || out.LoginCode == "" || out.ReturnPath != "my-rooms" || out.Intent != domain.IntentLogin {
		t.Fatalf("outcome: %+v", out)
	}
	if len(f.users.created) != 1 {
		t.Fatalf("one account, got %d", len(f.users.created))
	}
	created := f.users.created[0]
	if created.Email == nil || *created.Email != "ann@example.vn" || created.FullName != "Ann" || created.LastActiveRole != authz.RoleTenant {
		t.Errorf("created: %+v", created)
	}
	if len(f.users.roles) != 1 || f.users.roles[0] != authz.RoleTenant {
		t.Errorf("roles: %v", f.users.roles)
	}
	if len(f.oauth.rows) != 1 || f.oauth.rows[0].ProviderUserID != "g-1" || !f.oauth.rows[0].Linked() {
		t.Errorf("link: %+v", f.oauth.rows)
	}
	if f.google.lastVerifier == "" {
		t.Error("the verifier must reach the provider when the code is traded")
	}

	// The code opens a normal login (tokens, session), and works once.
	f.auth.users.auth = domain.AuthUser{Status: authz.StatusActive, Roles: []string{authz.RoleTenant}}
	f.auth.users.created = []domain.NewUser{{Email: "ann@example.vn", FullName: "Ann", TermsVersion: "v"}}
	res, err := f.social.Exchange(context.Background(), out.LoginCode, Client{IP: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Tokens.AccessToken != "access-tenant" || res.Tokens.RefreshToken == "" || len(f.auth.sess.opened) != 1 {
		t.Fatalf("result: %+v", res)
	}
	if _, err := f.social.Exchange(context.Background(), out.LoginCode, Client{}); !errors.Is(err, domain.ErrSocialCodeInvalid) {
		t.Errorf("a code must work once: %v", err)
	}
	if _, err := f.social.Exchange(context.Background(), "never-issued", Client{}); !errors.Is(err, domain.ErrSocialCodeInvalid) {
		t.Errorf("unknown code: %v", err)
	}

	var registered, loggedIn bool
	for _, e := range f.auth.audit.events {
		switch e.Action {
		case AuditRegister:
			registered = e.Meta["via"] == "google"
		case AuditLogin:
			loggedIn = e.Meta["via"] == "google"
		}
	}
	if !registered || !loggedIn {
		t.Errorf("the trail must say the sign-up and the login came through google: %+v", f.auth.audit.events)
	}
}

func TestZaloSignUpHasNoEmailAndNoPassword(t *testing.T) {
	f := newSocialFixture(t)
	out := f.roundTrip(t, f.zalo, domain.SocialProfile{ID: "z-1", Name: "  "})
	if out.Err != nil {
		t.Fatal(out.Err)
	}
	created := f.users.created[0]
	if created.Email != nil || created.FullName != "User" {
		t.Errorf("created: %+v", created)
	}
}

func TestAnEmailTheProviderDoesNotVouchForIsNeverUsed(t *testing.T) {
	f := newSocialFixture(t)
	f.users.add(socialAccount{email: strp("ann@example.vn")})
	out := f.roundTrip(t, f.zalo, domain.SocialProfile{ID: "z-1", Name: "Z", Email: strp("ann@example.vn"), EmailVerified: false})
	if out.Err != nil || len(f.users.created) != 1 || f.users.created[0].Email != nil {
		t.Fatalf("an unverified email must neither merge nor be stored: %+v %+v", out, f.users.created)
	}
}

func TestVouchedEmailMergesIntoTheExistingAccountAndConfirmsIt(t *testing.T) {
	f := newSocialFixture(t)
	existing := f.users.add(socialAccount{email: strp("ann@example.vn"), password: strp("hash")})
	out := f.roundTrip(t, f.google, googleAnn)
	if out.Err != nil || out.LoginCode == "" {
		t.Fatalf("outcome: %+v", out)
	}
	if len(f.users.created) != 0 {
		t.Error("no second account")
	}
	if f.codes.items[out.LoginCode].UserID != existing.id {
		t.Error("the code must open the existing account")
	}
	if len(f.oauth.rows) != 1 || f.oauth.rows[0].UserID != existing.id {
		t.Errorf("the provider must be linked to the existing account: %+v", f.oauth.rows)
	}
	if len(f.users.confirmed) != 1 || existing.password != nil || existing.verified == nil {
		t.Error("the provider proved the address: verified, and the unverified password dropped")
	}
}

func TestAVerifiedAccountKeepsItsPasswordOnMerge(t *testing.T) {
	f := newSocialFixture(t)
	verified := time.Now()
	existing := f.users.add(socialAccount{email: strp("ann@example.vn"), password: strp("hash"), verified: &verified})
	if out := f.roundTrip(t, f.google, googleAnn); out.Err != nil {
		t.Fatal(out.Err)
	}
	if len(f.users.confirmed) != 0 || existing.password == nil {
		t.Error("an already verified account must keep its password")
	}
}

func TestLoginRefusals(t *testing.T) {
	staff := f2("admin", authz.RoleAdmin)
	cases := []struct {
		name  string
		setup func(f *socialFixture)
		want  error
	}{
		{"staff account by email", func(f *socialFixture) { f.users.add(socialAccount{email: strp("ann@example.vn"), roles: staff}) }, domain.ErrLinkStaff},
		{"disabled account by email", func(f *socialFixture) { f.users.add(socialAccount{email: strp("ann@example.vn"), status: "inactive"}) }, domain.ErrAccountInactive},
		{"disabled linked account", func(f *socialFixture) {
			a := f.users.add(socialAccount{status: "inactive"})
			f.oauth.rows = append(f.oauth.rows, domain.OAuthAccount{UserID: a.id, Provider: "google", ProviderUserID: "g-1"})
		}, domain.ErrAccountInactive},
		{"deleted linked account", func(f *socialFixture) {
			f.oauth.rows = append(f.oauth.rows, domain.OAuthAccount{UserID: uuid.New(), Provider: "google", ProviderUserID: "g-1"})
		}, domain.ErrAccountInactive},
		{"unlinked provider account", func(f *socialFixture) {
			a, now := f.users.add(socialAccount{}), time.Now()
			f.oauth.rows = append(f.oauth.rows, domain.OAuthAccount{UserID: a.id, Provider: "google", ProviderUserID: "g-1", UnlinkedAt: &now})
		}, domain.ErrLinkUnlinked},
		{"unlinked provider reached through the email", func(f *socialFixture) {
			a, now := f.users.add(socialAccount{email: strp("ann@example.vn")}), time.Now()
			f.oauth.rows = append(f.oauth.rows, domain.OAuthAccount{UserID: a.id, Provider: "google", ProviderUserID: "g-OLD", UnlinkedAt: &now})
		}, domain.ErrLinkUnlinked},
		{"another provider account linked through the email", func(f *socialFixture) {
			a := f.users.add(socialAccount{email: strp("ann@example.vn")})
			f.oauth.rows = append(f.oauth.rows, domain.OAuthAccount{UserID: a.id, Provider: "google", ProviderUserID: "g-OTHER"})
		}, domain.ErrLinkProviderTaken},
		{"deleted account that kept its email", func(f *socialFixture) { f.users.createErr = &shared.ConstraintError{Kind: shared.Unique} }, domain.ErrAccountInactive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSocialFixture(t)
			tc.setup(f)
			out := f.roundTrip(t, f.google, googleAnn)
			if !errors.Is(out.Err, tc.want) || out.LoginCode != "" {
				t.Fatalf("want %v and no code, got %+v", tc.want, out)
			}
		})
	}
}

func f2(_ string, roles ...string) []string { return roles }

func TestCallbackWithoutAValidRoundTripFails(t *testing.T) {
	f := newSocialFixture(t)
	f.google.profile = googleAnn
	ctx := context.Background()

	if out := f.social.Complete(ctx, Callback{Provider: "google", State: "forged", Code: "c"}); !errors.Is(out.Err, domain.ErrSocialFailed) {
		t.Errorf("unknown state: %+v", out)
	}
	url, _ := f.social.BeginLogin(ctx, "google", "")
	key := stateIn(url)
	if out := f.social.Complete(ctx, Callback{Provider: "google", State: key, Code: "c"}); out.Err != nil {
		t.Fatalf("first use: %+v", out)
	}
	if out := f.social.Complete(ctx, Callback{Provider: "google", State: key, Code: "c"}); !errors.Is(out.Err, domain.ErrSocialFailed) {
		t.Errorf("a state works once: %+v", out)
	}

	url, _ = f.social.BeginLogin(ctx, "google", "")
	if out := f.social.Complete(ctx, Callback{Provider: "zalo", State: stateIn(url), Code: "c"}); !errors.Is(out.Err, domain.ErrSocialFailed) {
		t.Errorf("a state of another provider: %+v", out)
	}
	url, _ = f.social.BeginLogin(ctx, "google", "")
	if out := f.social.Complete(ctx, Callback{Provider: "google", State: stateIn(url)}); !errors.Is(out.Err, domain.ErrSocialFailed) {
		t.Errorf("the person cancelled (no code): %+v", out)
	}
	url, _ = f.social.BeginLogin(ctx, "google", "")
	f.google.err = errors.New("boom")
	if out := f.social.Complete(ctx, Callback{Provider: "google", State: stateIn(url), Code: "c"}); !errors.Is(out.Err, domain.ErrSocialFailed) {
		t.Errorf("provider error: %+v", out)
	}
}

func TestExchangeRefusesAnAccountDisabledSinceTheCallback(t *testing.T) {
	f := newSocialFixture(t)
	acc := f.users.add(socialAccount{})
	code, _ := f.codes.Put(context.Background(), domain.SocialLoginGrant{UserID: acc.id, Via: "google"}, time.Minute)
	acc.status = "inactive"
	if _, err := f.social.Exchange(context.Background(), code, Client{}); !errors.Is(err, domain.ErrAccountInactive) {
		t.Fatalf("got %v", err)
	}
	if len(f.auth.sess.opened) != 0 {
		t.Error("no session for a disabled account")
	}
}

// link starts a link round trip for the account and completes it with the profile.
func (f *socialFixture) link(t *testing.T, acc *socialAccount, p *fakeProvider, profile domain.SocialProfile) Outcome {
	t.Helper()
	p.profile = profile
	principal := authz.Principal{UserID: acc.id.String(), Role: authz.RoleTenant}
	url, err := f.social.BeginLink(context.Background(), principal, p.code)
	if err != nil {
		t.Fatal(err)
	}
	return f.social.Complete(context.Background(), Callback{Provider: p.code, State: stateIn(url), Code: "c"})
}

func TestLinkRules(t *testing.T) {
	annMail := strp("ann@example.vn")
	cases := []struct {
		name     string
		account  socialAccount
		provider func(f *socialFixture) *fakeProvider
		profile  domain.SocialProfile
		setup    func(f *socialFixture, acc *socialAccount)
		want     error
	}{
		{"same email links", socialAccount{email: annMail}, google, googleAnn, nil, nil},
		{"zalo links freely, even without email", socialAccount{}, zalo, domain.SocialProfile{ID: "z-1"}, nil, nil},
		{"email mismatch", socialAccount{email: strp("other@example.vn")}, google, googleAnn, nil, domain.ErrLinkEmailMismatch},
		{"account without email", socialAccount{}, google, googleAnn, nil, domain.ErrLinkNoEmail},
		{"staff", socialAccount{email: annMail, roles: []string{authz.RoleSuperAdmin}}, google, googleAnn, nil, domain.ErrLinkStaff},
		{"linked to another account", socialAccount{email: annMail}, google, googleAnn, func(f *socialFixture, _ *socialAccount) {
			f.oauth.rows = append(f.oauth.rows, domain.OAuthAccount{UserID: uuid.New(), Provider: "google", ProviderUserID: "g-1"})
		}, domain.ErrLinkElsewhere},
		{"another account of the provider already linked", socialAccount{email: annMail}, google, googleAnn, func(f *socialFixture, acc *socialAccount) {
			f.oauth.rows = append(f.oauth.rows, domain.OAuthAccount{UserID: acc.id, Provider: "google", ProviderUserID: "g-OTHER"})
		}, domain.ErrLinkProviderTaken},
		{"linking the same account again is fine", socialAccount{email: annMail}, google, googleAnn, func(f *socialFixture, acc *socialAccount) {
			f.oauth.rows = append(f.oauth.rows, domain.OAuthAccount{UserID: acc.id, Provider: "google", ProviderUserID: "g-1"})
		}, nil},
		{"linking again after unlinking", socialAccount{email: annMail}, google, googleAnn, func(f *socialFixture, acc *socialAccount) {
			now := time.Now()
			f.oauth.rows = append(f.oauth.rows, domain.OAuthAccount{UserID: acc.id, Provider: "google", ProviderUserID: "g-1", UnlinkedAt: &now})
		}, nil},
		{"replacing an unlinked provider account", socialAccount{email: annMail}, google, googleAnn, func(f *socialFixture, acc *socialAccount) {
			now := time.Now()
			f.oauth.rows = append(f.oauth.rows, domain.OAuthAccount{UserID: acc.id, Provider: "google", ProviderUserID: "g-OLD", UnlinkedAt: &now})
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSocialFixture(t)
			acc := f.users.add(tc.account)
			if tc.setup != nil {
				tc.setup(f, acc)
			}
			out := f.link(t, acc, tc.provider(f), tc.profile)
			if !errors.Is(out.Err, tc.want) || out.Intent != domain.IntentLink || out.LoginCode != "" {
				t.Fatalf("want %v, got %+v", tc.want, out)
			}
			if tc.want == nil {
				row := findProvider(f.oauth.rows, tc.provider(f).Code())
				if row == nil || !row.Linked() || row.ProviderUserID != tc.profile.ID {
					t.Errorf("rows: %+v", f.oauth.rows)
				}
			}
			linked := len(f.auth.audit.actions()) == 1 && f.auth.audit.actions()[0] == AuditOAuthLinked
			already := tc.name == "linking the same account again is fine"
			if (tc.want == nil && !already) != linked {
				t.Errorf("audit: %v", f.auth.audit.actions())
			}
			if len(f.users.created) != 0 || len(f.users.confirmed) != 0 {
				t.Error("linking never creates an account nor changes the email")
			}
		})
	}
}

func google(f *socialFixture) *fakeProvider { return f.google }
func zalo(f *socialFixture) *fakeProvider   { return f.zalo }

func TestLinkNeedsTheAccountThatAsked(t *testing.T) {
	f := newSocialFixture(t)
	f.google.profile = googleAnn
	ghost := authz.Principal{UserID: uuid.NewString(), Role: authz.RoleTenant}
	url, _ := f.social.BeginLink(context.Background(), ghost, "google")
	out := f.social.Complete(context.Background(), Callback{Provider: "google", State: stateIn(url), Code: "c"})
	if !errors.Is(out.Err, domain.ErrSocialFailed) {
		t.Fatalf("got %+v", out)
	}
}

func TestUnlink(t *testing.T) {
	ctx := context.Background()
	f := newSocialFixture(t)
	acc := f.users.add(socialAccount{email: strp("ann@example.vn"), password: strp("hash")})
	p := authz.Principal{UserID: acc.id.String(), Role: authz.RoleTenant}
	f.oauth.rows = append(f.oauth.rows, domain.OAuthAccount{UserID: acc.id, Provider: "google", ProviderUserID: "g-1", LinkedAt: time.Now()})

	if err := f.social.Unlink(ctx, p, "zalo"); !errors.Is(err, domain.ErrNotLinked) {
		t.Errorf("not linked: %v", err)
	}
	if err := f.social.Unlink(ctx, p, "facebook"); !errors.Is(err, domain.ErrProviderUnavailable) {
		t.Errorf("unknown: %v", err)
	}
	if err := f.social.Unlink(ctx, p, "google"); err != nil {
		t.Fatal(err)
	}
	if row := findProvider(f.oauth.rows, "google"); row == nil || row.Linked() {
		t.Fatalf("the row must stay, marked unlinked: %+v", f.oauth.rows)
	}
	if got := f.auth.audit.actions(); len(got) != 1 || got[0] != AuditOAuthUnlinked || f.auth.audit.events[0].Meta["provider"] != "google" {
		t.Errorf("audit: %+v", f.auth.audit.events)
	}
	// Unlinked means it no longer signs in, not even through the email.
	if out := f.roundTrip(t, f.google, googleAnn); !errors.Is(out.Err, domain.ErrLinkUnlinked) {
		t.Errorf("sign-in after unlinking: %+v", out)
	}
	// Signed in, the person can link it again.
	if out := f.link(t, acc, f.google, googleAnn); out.Err != nil {
		t.Errorf("link again: %+v", out)
	}
	if row := findProvider(f.oauth.rows, "google"); row == nil || !row.Linked() {
		t.Errorf("rows: %+v", f.oauth.rows)
	}
}

func TestTheLastWayToSignInStays(t *testing.T) {
	ctx := context.Background()
	f := newSocialFixture(t)
	acc := f.users.add(socialAccount{}) // no password
	p := authz.Principal{UserID: acc.id.String(), Role: authz.RoleTenant}
	now := time.Now()
	f.oauth.rows = append(f.oauth.rows,
		domain.OAuthAccount{UserID: acc.id, Provider: "google", ProviderUserID: "g-1", LinkedAt: now},
		domain.OAuthAccount{UserID: acc.id, Provider: "zalo", ProviderUserID: "z-1", LinkedAt: now})
	if err := f.social.Unlink(ctx, p, "google"); err != nil {
		t.Fatalf("one of two can go: %v", err)
	}
	if err := f.social.Unlink(ctx, p, "zalo"); !errors.Is(err, domain.ErrLastSignInMethod) {
		t.Fatalf("the last one must stay: %v", err)
	}
	if row := findProvider(f.oauth.rows, "zalo"); !row.Linked() {
		t.Error("nothing may change when refused")
	}
}

func TestAccountsDescribesEachEnabledProvider(t *testing.T) {
	f := newSocialFixture(t)
	acc := f.users.add(socialAccount{password: strp("hash")})
	now := time.Now()
	f.oauth.rows = append(f.oauth.rows, domain.OAuthAccount{UserID: acc.id, Provider: "zalo", ProviderUserID: "z-1", LinkedAt: now, UnlinkedAt: &now})
	got, err := f.social.Accounts(context.Background(), authz.Principal{UserID: acc.id.String()})
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasPassword || len(got.Providers) != 2 {
		t.Fatalf("got %+v", got)
	}
	g, z := got.Providers[0], got.Providers[1]
	if g.Provider != "google" || g.Linked || g.Unlinked || g.CanUnlink || !g.RequiresMatchingEmail {
		t.Errorf("google: %+v", g)
	}
	if z.Provider != "zalo" || z.Linked || !z.Unlinked || z.CanUnlink || z.LinkedAt == nil {
		t.Errorf("zalo: %+v", z)
	}
}
