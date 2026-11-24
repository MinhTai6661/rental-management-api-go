package identity

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

// These tests run the auth use cases against hand-written fakes of the ports: no database, no Redis, no SMTP, no
// real cryptography. A fake embeds the interface it stands for, so a method the test does not expect to be called
// panics.

type testClock struct{ t time.Time }

func (c *testClock) Now() time.Time { return c.t }

type fakeTx struct {
	runs int
	errs []error // what fn returned, per run
}

func (f *fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	f.runs++
	err := fn(ctx)
	f.errs = append(f.errs, err)
	return err
}

type fakeUsers struct {
	UserRepository
	byEmail   map[string]domain.User
	auth      domain.AuthUser // what AuthUser answers for any account
	stored    *string         // the stored password hash of any account
	createErr error
	created   []domain.NewUser
	roles     []string
	newHash   string
	lastRole  string
	accepts   bool // the stored accepts_invitations of any account
}

func (f *fakeUsers) FindByEmail(_ context.Context, email string) (domain.User, error) {
	if u, ok := f.byEmail[email]; ok {
		return u, nil
	}
	return domain.User{}, shared.ErrNotFound
}

func (f *fakeUsers) Create(_ context.Context, u domain.NewUser) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, u)
	return nil
}

func (f *fakeUsers) AddRole(_ context.Context, _ uuid.UUID, role string, _ time.Time) error {
	f.roles = append(f.roles, role)
	return nil
}

func (f *fakeUsers) Profile(_ context.Context, id uuid.UUID) (domain.UserProfile, error) {
	return domain.UserProfile{ID: id, Email: &f.created[0].Email, FullName: f.created[0].FullName, Status: authz.StatusActive, TermsVersion: &f.created[0].TermsVersion, AcceptsInvitations: f.accepts}, nil
}

func (f *fakeUsers) SetAcceptsInvitations(_ context.Context, _ uuid.UUID, accepts bool) error {
	f.accepts = accepts
	return nil
}

func (f *fakeUsers) AuthUser(context.Context, uuid.UUID) (domain.AuthUser, error) {
	if f.auth.Status == "" { // the register flow reads the roles it just granted
		return domain.AuthUser{Status: authz.StatusActive, Roles: f.roles}, nil
	}
	return f.auth, nil
}

func (f *fakeUsers) SetLastActiveRole(_ context.Context, _ uuid.UUID, role string, _ time.Time) error {
	f.lastRole = role
	return nil
}

func (f *fakeUsers) PasswordHash(context.Context, uuid.UUID) (*string, error) { return f.stored, nil }

func (f *fakeUsers) UpdatePassword(_ context.Context, _ uuid.UUID, hash string, _ time.Time) error {
	f.newHash = hash
	return nil
}

type fakeVerifications struct {
	VerificationRepository
	inserted []domain.NewVerificationToken
}

func (f *fakeVerifications) Insert(_ context.Context, t domain.NewVerificationToken, _ time.Time) error {
	f.inserted = append(f.inserted, t)
	return nil
}

type revokedSession struct {
	id     uuid.UUID
	reason string
}

type fakeSessions struct {
	SessionRepository
	token       domain.RefreshToken // answered for the hash "hash:known"
	closedByAll []uuid.UUID         // what RevokeAllOfUser reports
	kept        *uuid.UUID
	revoked     []revokedSession
	inserted    []domain.NewRefreshToken
	opened      []domain.NewSession
	rotated     []uuid.UUID
	touched     []domain.SessionTouch
}

func (f *fakeSessions) RefreshTokenForUpdate(_ context.Context, hash []byte) (domain.RefreshToken, error) {
	if string(hash) == "hash:known" {
		return f.token, nil
	}
	return domain.RefreshToken{}, shared.ErrNotFound
}

func (f *fakeSessions) Create(_ context.Context, s domain.NewSession, _ time.Time) error {
	f.opened = append(f.opened, s)
	return nil
}

func (f *fakeSessions) Revoke(_ context.Context, id uuid.UUID, _ time.Time, reason string) error {
	f.revoked = append(f.revoked, revokedSession{id, reason})
	return nil
}

func (f *fakeSessions) RevokeAllOfUser(_ context.Context, _ uuid.UUID, except *uuid.UUID, _ time.Time, _ string) ([]uuid.UUID, error) {
	f.kept = except
	return f.closedByAll, nil
}

func (f *fakeSessions) MarkRefreshTokenRotated(_ context.Context, id uuid.UUID, _ time.Time) error {
	f.rotated = append(f.rotated, id)
	return nil
}

func (f *fakeSessions) InsertRefreshToken(_ context.Context, t domain.NewRefreshToken, _ time.Time) error {
	f.inserted = append(f.inserted, t)
	return nil
}

func (f *fakeSessions) Touch(_ context.Context, t domain.SessionTouch, _ time.Time) error {
	f.touched = append(f.touched, t)
	return nil
}

type fakePermissions struct{ PermissionRepository }

func (fakePermissions) CodesOfRole(context.Context, string) ([]string, error) {
	return []string{"rooms.view"}, nil
}

type fakeRevocations struct {
	sessions   []string
	tokens     []string
	revokedErr error // what IsRevoked answers with
}

func (f *fakeRevocations) RevokeToken(_ context.Context, jti string, _ time.Duration) error {
	f.tokens = append(f.tokens, jti)
	return nil
}

func (f *fakeRevocations) RevokeSessions(_ context.Context, sids []string, _ time.Duration) error {
	f.sessions = append(f.sessions, sids...)
	return nil
}

func (f *fakeRevocations) IsRevoked(context.Context, string, string) (bool, error) {
	return false, f.revokedErr
}

// fakeHasher "hashes" to a readable string and counts the verifications, so a test can tell that an unknown email
// still costs one.
type fakeHasher struct{ verifies int }

func (*fakeHasher) Hash(password string) (string, error) { return "hashed:" + password, nil }

func (f *fakeHasher) Verify(password, encoded string) (bool, error) {
	f.verifies++
	return encoded == "hashed:"+password, nil
}

type fakeTokens struct{}

func (fakeTokens) Sign(userID, sessionID uuid.UUID, role string) (string, domain.AccessClaims, error) {
	return "access-" + role, domain.AccessClaims{UserID: userID.String(), SessionID: sessionID.String(), Role: role}, nil
}

func (fakeTokens) Verify(string) (domain.AccessClaims, error) {
	return domain.AccessClaims{}, errors.New("unused")
}

func (fakeTokens) TTL() time.Duration { return 15 * time.Minute }

// fakeOpaque makes tokens "tok-1", "tok-2"...; the hash of a token is "hash:" + the token.
type fakeOpaque struct{ n int }

func (f *fakeOpaque) New() (string, []byte, error) {
	f.n++
	raw := "tok-" + strconv.Itoa(f.n)
	return raw, f.Hash(raw), nil
}

func (*fakeOpaque) Hash(raw string) []byte { return []byte("hash:" + raw) }

type sentVerification struct{ to, name, token, lang string }

type fakeMailer struct{ sent []sentVerification }

func (f *fakeMailer) SendVerification(_ context.Context, to, name, token, lang string, _ time.Duration) error {
	f.sent = append(f.sent, sentVerification{to, name, token, lang})
	return nil
}

type authFixture struct {
	auth   *Auth
	tx     *fakeTx
	users  *fakeUsers
	verify *fakeVerifications
	sess   *fakeSessions
	revoke *fakeRevocations
	hasher *fakeHasher
	mailer *fakeMailer
	clock  *testClock
	audit  *fakeAudit
}

const reuseGrace = 10 * time.Second

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	f := &authFixture{
		tx: &fakeTx{}, users: &fakeUsers{byEmail: map[string]domain.User{}}, verify: &fakeVerifications{},
		sess: &fakeSessions{}, revoke: &fakeRevocations{}, hasher: &fakeHasher{}, mailer: &fakeMailer{},
		clock: &testClock{time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)}, audit: &fakeAudit{},
	}
	auth, err := NewAuth(Deps{
		Tx: f.tx, Users: f.users, Sessions: f.sess, Verifications: f.verify, Permissions: fakePermissions{},
		Revocations: f.revoke, Hasher: f.hasher, Tokens: fakeTokens{}, Opaque: &fakeOpaque{}, Mailer: f.mailer,
		Clock: f.clock, Log: slog.New(slog.DiscardHandler), Audit: f.audit, Dispatch: func(work func()) { work() },
	}, Options{
		TermsVersion: "2026-11-03", VerificationTTL: 24 * time.Hour, RefreshTTL: time.Hour,
		ReuseGrace: reuseGrace, PermissionCacheTTL: time.Minute, RevokedFor: 20 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.auth = auth
	return f
}

// account stores a user that can log in with "right-password".
func (f *authFixture) account(email string, mutate func(*domain.User)) {
	hash := "hashed:right-password"
	verified := time.Now()
	u := domain.User{ID: uuid.New(), Email: &email, PasswordHash: &hash, Status: authz.StatusActive, EmailVerifiedAt: &verified, FullName: "A"}
	if mutate != nil {
		mutate(&u)
	}
	f.users.byEmail[email] = u
}

func TestLoginRefusesWhatIsNotAnActiveVerifiedAccountWithTheRightPassword(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t)
	f.account("ann@example.vn", nil)
	f.account("off@example.vn", func(u *domain.User) { u.Status = "inactive" })
	f.account("new@example.vn", func(u *domain.User) { u.EmailVerifiedAt = nil })

	cases := []struct {
		name, email, password string
		want                  error
	}{
		{"unknown email", "nobody@example.vn", "right-password", domain.ErrInvalidCredentials},
		{"wrong password", "ann@example.vn", "wrong-password", domain.ErrInvalidCredentials},
		{"inactive account", "off@example.vn", "right-password", domain.ErrAccountInactive},
		{"unverified email", "new@example.vn", "right-password", domain.ErrEmailUnverified},
		// The account state is only told once the password is right.
		{"inactive account, wrong password", "off@example.vn", "wrong-password", domain.ErrInvalidCredentials},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.auth.Login(ctx, tc.email, tc.password, Client{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
	if f.tx.runs != 0 {
		t.Error("a refused login must not open a session")
	}
	// An unknown email costs a password check too (against the dummy hash), so the answer takes as long.
	if f.hasher.verifies != len(cases) {
		t.Errorf("every attempt must verify one hash, got %d verifications for %d attempts", f.hasher.verifies, len(cases))
	}
}

func TestLoginOpensASessionInTheMostSeniorRole(t *testing.T) {
	f := newAuthFixture(t)
	f.account("ann@example.vn", nil)
	f.users.auth = domain.AuthUser{Status: authz.StatusActive, Roles: []string{authz.RoleLandlord, authz.RoleTenant}}
	f.users.created = []domain.NewUser{{Email: "ann@example.vn", FullName: "A", TermsVersion: "2026-11-03"}} // for Profile

	got, err := f.auth.Login(context.Background(), " ANN@example.vn ", "right-password", Client{IP: "192.0.2.1", UserAgent: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if got.User.ActiveRole != authz.RoleLandlord || got.Tokens.AccessToken != "access-landlord" || got.Tokens.ExpiresIn != 900 ||
		got.Tokens.RefreshExpiresIn != 3600 || got.Tokens.TokenType != "Bearer" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if f.tx.runs != 1 || len(f.sess.opened) != 1 || len(f.sess.inserted) != 1 || f.users.lastRole != authz.RoleLandlord {
		t.Errorf("one transaction with the session, its refresh token and the role: %d runs, %d sessions, %d tokens, role %q",
			f.tx.runs, len(f.sess.opened), len(f.sess.inserted), f.users.lastRole)
	}
	if s := f.sess.opened[0]; s.IP == nil || s.IP.String() != "192.0.2.1" || s.UserAgent == nil || *s.UserAgent != "test" {
		t.Errorf("the client is kept with the session: %+v", s)
	}
}

func TestRegisterOfATakenEmailIsRefused(t *testing.T) {
	f := newAuthFixture(t)
	f.users.createErr = &shared.ConstraintError{Kind: shared.Unique, Constraint: "users_email_key"}

	_, err := f.auth.Register(context.Background(), RegisterInput{FullName: "A", Email: "Taken@Example.vn", Password: "long-enough-1"})
	if !errors.Is(err, domain.ErrEmailTaken) {
		t.Fatalf("want ErrEmailTaken, got %v", err)
	}
	if len(f.mailer.sent) != 0 {
		t.Error("no verification email for a refused registration")
	}
}

func TestRegisterOfAnUnknownWardIsRefused(t *testing.T) {
	f := newAuthFixture(t)
	f.users.createErr = &shared.ConstraintError{Kind: shared.ForeignKey, Constraint: "users_ward_code_fkey"}

	ward := int32(1)
	_, err := f.auth.Register(context.Background(), RegisterInput{FullName: "A", Email: "a@example.vn", Password: "long-enough-1", WardCode: &ward})
	if !errors.Is(err, domain.ErrWardNotFound) {
		t.Fatalf("want ErrWardNotFound, got %v", err)
	}
}

func TestRegisterCreatesATenantAndItsVerificationTokenInOneTransaction(t *testing.T) {
	f := newAuthFixture(t)
	view, err := f.auth.Register(context.Background(), RegisterInput{FullName: "A", Email: "  Ann@Example.VN ", Password: "long-enough-1", Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if f.tx.runs != 1 || len(f.users.created) != 1 || len(f.verify.inserted) != 1 {
		t.Fatalf("one transaction with the user and its token: %d runs, %d users, %d tokens", f.tx.runs, len(f.users.created), len(f.verify.inserted))
	}
	if got := f.users.created[0]; got.Email != "ann@example.vn" || got.LastActiveRole != authz.RoleTenant || got.PasswordHash != "hashed:long-enough-1" {
		t.Errorf("email is normalized, the role is tenant and the password is hashed: %+v", got)
	}
	if len(f.users.roles) != 1 || f.users.roles[0] != authz.RoleTenant {
		t.Errorf("only tenant may be granted, got %v", f.users.roles)
	}
	if view.ActiveRole != authz.RoleTenant || len(f.mailer.sent) != 1 {
		t.Fatalf("view %+v, %d emails", view, len(f.mailer.sent))
	}
	if m := f.mailer.sent[0]; m.to != "ann@example.vn" || m.lang != "en" || m.token != "tok-1" || m.name != "A" {
		t.Errorf("the email goes to the normalized address with the raw token: %+v", m)
	}
	if string(f.verify.inserted[0].Hash) != "hash:tok-1" {
		t.Errorf("only the hash of the token is stored, got %q", f.verify.inserted[0].Hash)
	}
}

func TestAssignRolesRefusesUnknownRolesAndStaffMixedWithOthers(t *testing.T) {
	ctx := context.Background()
	users := &fakeUsers{}
	if err := assignRoles(ctx, users, uuid.New(), []string{authz.RoleLandlord, authz.RoleTenant}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := assignRoles(ctx, users, uuid.New(), []string{authz.RoleAdmin, authz.RoleTenant}, time.Now()); !errors.Is(err, domain.ErrStaffRolesMixed) {
		t.Errorf("admin together with tenant must be refused, got %v", err)
	}
	if err := assignRoles(ctx, users, uuid.New(), []string{"wizard"}, time.Now()); !errors.Is(err, domain.ErrUnknownRole) {
		t.Errorf("an unknown role must be refused, got %v", err)
	}
	if len(users.roles) != 2 {
		t.Errorf("a refused grant must not add any role, got %v", users.roles)
	}
}

// refreshToken stores a refresh token "known" of a session of a tenant.
func (f *authFixture) refreshToken(rotatedAgo *time.Duration) domain.RefreshToken {
	rt := domain.RefreshToken{
		ID: uuid.New(), SessionID: uuid.New(), UserID: uuid.New(), ActiveRole: authz.RoleTenant,
		ExpiresAt: f.clock.t.Add(time.Hour),
	}
	if rotatedAgo != nil {
		at := f.clock.t.Add(-*rotatedAgo)
		rt.RotatedAt = &at
	}
	f.sess.token = rt
	f.users.auth = domain.AuthUser{Status: authz.StatusActive, Roles: []string{authz.RoleTenant}}
	return rt
}

func TestRefreshReuseClosesTheSessionAndBlocksItsAccessTokens(t *testing.T) {
	f := newAuthFixture(t)
	ago := reuseGrace + time.Second
	rt := f.refreshToken(&ago)

	_, err := f.auth.Refresh(context.Background(), "known", Client{})
	if !errors.Is(err, domain.ErrRefreshInvalid) {
		t.Fatalf("a reused token is refused, got %v", err)
	}
	if len(f.sess.revoked) != 1 || f.sess.revoked[0].id != rt.SessionID || f.sess.revoked[0].reason != "reuse_detected" {
		t.Errorf("the session must be closed as reuse_detected: %+v", f.sess.revoked)
	}
	if len(f.revoke.sessions) != 1 || f.revoke.sessions[0] != rt.SessionID.String() {
		t.Errorf("the access tokens of the session must be blocked: %v", f.revoke.sessions)
	}
	if len(f.sess.inserted) != 0 {
		t.Error("no new refresh token may be handed out")
	}
	// The closure must commit: the transaction function returned nil.
	if len(f.tx.errs) != 1 || f.tx.errs[0] != nil {
		t.Errorf("the revocation must not be rolled back: %v", f.tx.errs)
	}
}

func TestRefreshRotatesAFreshTokenAndAllowsTheGraceWindow(t *testing.T) {
	f := newAuthFixture(t)
	rt := f.refreshToken(nil)

	out, err := f.auth.Refresh(context.Background(), "known", Client{UserAgent: "ua"})
	if err != nil {
		t.Fatal(err)
	}
	if out.AccessToken != "access-tenant" || out.RefreshToken == "" {
		t.Errorf("a new pair expected: %+v", out)
	}
	if len(f.sess.rotated) != 1 || f.sess.rotated[0] != rt.ID {
		t.Errorf("the used token must be marked rotated: %v", f.sess.rotated)
	}
	if len(f.sess.inserted) != 1 || f.sess.inserted[0].ParentID == nil || *f.sess.inserted[0].ParentID != rt.ID {
		t.Errorf("the new token descends from the used one: %+v", f.sess.inserted)
	}

	// Presented again inside the grace window (a second tab): accepted, and not marked rotated a second time.
	ago := reuseGrace / 2
	f.refreshToken(&ago)
	f.sess.rotated = nil
	if _, err := f.auth.Refresh(context.Background(), "known", Client{}); err != nil {
		t.Fatalf("inside the grace window the token still works: %v", err)
	}
	if len(f.sess.rotated) != 0 {
		t.Error("an already rotated token is not marked again")
	}
}

func TestRefreshOfAnUnknownTokenIsRefused(t *testing.T) {
	f := newAuthFixture(t)
	if _, err := f.auth.Refresh(context.Background(), "nope", Client{}); !errors.Is(err, domain.ErrRefreshInvalid) {
		t.Fatalf("got %v", err)
	}
	if len(f.revoke.sessions) != 0 {
		t.Error("an unknown token closes no session")
	}
}

func principalOf(userID, sessionID uuid.UUID) authz.Principal {
	return authz.Principal{UserID: userID.String(), SessionID: sessionID.String(), Role: authz.RoleTenant, TokenID: "jti-1", TokenExpiresAt: time.Date(2026, 11, 3, 8, 10, 0, 0, time.UTC)}
}

func TestChangePasswordWithTheWrongCurrentPasswordIsRefused(t *testing.T) {
	f := newAuthFixture(t)
	stored := "hashed:right-password"
	f.users.stored = &stored
	p := principalOf(uuid.New(), uuid.New())

	if err := f.auth.ChangePassword(context.Background(), p, "wrong-password", "another-password-1"); !errors.Is(err, domain.ErrCurrentPasswordWrong) {
		t.Fatalf("want ErrCurrentPasswordWrong, got %v", err)
	}
	if err := f.auth.ChangePassword(context.Background(), p, "right-password", "right-password"); !errors.Is(err, domain.ErrNewPasswordSame) {
		t.Fatalf("want ErrNewPasswordSame, got %v", err)
	}

	// An account without a password has nothing to compare with.
	f.users.stored = nil
	if err := f.auth.ChangePassword(context.Background(), p, "anything", "another-password-1"); !errors.Is(err, domain.ErrCurrentPasswordWrong) {
		t.Fatalf("an account without a password: %v", err)
	}
	if f.tx.runs != 0 || f.users.newHash != "" || len(f.revoke.sessions) != 0 {
		t.Error("a refused change must touch nothing")
	}
}

func TestChangePasswordStoresTheNewHashAndClosesTheOtherSessions(t *testing.T) {
	f := newAuthFixture(t)
	stored := "hashed:right-password"
	f.users.stored = &stored
	here, other := uuid.New(), uuid.New()
	f.sess.closedByAll = []uuid.UUID{other}

	if err := f.auth.ChangePassword(context.Background(), principalOf(uuid.New(), here), "right-password", "another-password-1"); err != nil {
		t.Fatal(err)
	}
	if f.users.newHash != "hashed:another-password-1" {
		t.Errorf("the new password is stored hashed, got %q", f.users.newHash)
	}
	if f.sess.kept == nil || *f.sess.kept != here {
		t.Error("the session that changed the password stays open")
	}
	if len(f.revoke.sessions) != 1 || f.revoke.sessions[0] != other.String() {
		t.Errorf("the access tokens of the closed sessions are blocked: %v", f.revoke.sessions)
	}
}

func TestAuthenticateFailsClosedWhenTheRevocationStoreIsDown(t *testing.T) {
	f := newAuthFixture(t)
	f.auth.tokens = verifyingTokens{}
	f.revoke.revokedErr = shared.ErrUnavailable

	_, err := f.auth.Authenticate(context.Background(), "any")
	if !errors.Is(err, shared.ErrUnavailable) {
		t.Fatalf("an unanswerable revocation check is an error, never a pass: %v", err)
	}
}

// verifyingTokens accepts every token and says it belongs to a fixed session.
type verifyingTokens struct{ fakeTokens }

func (verifyingTokens) Verify(string) (domain.AccessClaims, error) {
	return domain.AccessClaims{UserID: uuid.NewString(), SessionID: uuid.NewString(), Role: authz.RoleTenant, TokenID: "jti"}, nil
}
