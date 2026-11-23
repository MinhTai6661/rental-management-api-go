package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/domain/shared"
	"rental-management/internal/model"
	"rental-management/pkg/apperr"
	"rental-management/pkg/authz"
	"rental-management/pkg/mail"
)

// These tests run the auth rules against hand-written fakes of the repositories: no database, no Redis. A fake
// embeds the interface it stands for, so a method the test does not expect to be called panics.

type fakeTx struct{ runs int }

func (f *fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	f.runs++
	return fn(ctx)
}

type fakeUsers struct {
	UserRepository
	byEmail   map[string]model.User
	createErr error
	created   []model.NewUser
	roles     []string
}

func (f *fakeUsers) FindByEmail(_ context.Context, email string) (model.User, error) {
	if u, ok := f.byEmail[email]; ok {
		return u, nil
	}
	return model.User{}, shared.ErrNotFound
}

func (f *fakeUsers) Create(_ context.Context, u model.NewUser) error {
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

func (f *fakeUsers) Profile(_ context.Context, id uuid.UUID) (model.UserProfile, error) {
	return model.UserProfile{ID: id, Email: &f.created[0].Email, FullName: f.created[0].FullName, Status: authz.StatusActive, TermsVersion: &f.created[0].TermsVersion}, nil
}

func (f *fakeUsers) AuthUser(context.Context, uuid.UUID) (model.AuthUser, error) {
	return model.AuthUser{Status: authz.StatusActive, Roles: f.roles}, nil
}

type fakeVerifications struct {
	VerificationRepository
	inserted []model.NewVerificationToken
}

func (f *fakeVerifications) Insert(_ context.Context, t model.NewVerificationToken, _ time.Time) error {
	f.inserted = append(f.inserted, t)
	return nil
}

type fakePermissions struct{ PermissionRepository }

func (fakePermissions) CodesOfRole(context.Context, string) ([]string, error) {
	return []string{"rooms.view"}, nil
}

type authFixture struct {
	svc    *AuthService
	tx     *fakeTx
	users  *fakeUsers
	verify *fakeVerifications
	mailer *mail.Recorder
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	renderer, err := mail.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	clock := &testClock{time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)}
	f := &authFixture{
		tx: &fakeTx{}, users: &fakeUsers{byEmail: map[string]model.User{}}, verify: &fakeVerifications{},
		mailer: &mail.Recorder{},
	}
	f.svc, err = NewAuthService(AuthDeps{
		Tx: f.tx, Users: f.users, Verifications: f.verify, Permissions: fakePermissions{},
		Tokens: newTestIssuer(clock), Mailer: f.mailer, Renderer: renderer, Log: slog.New(slog.DiscardHandler),
		Now: clock.now, Dispatch: func(work func()) { work() },
	}, AuthOptions{
		TermsVersion: "2026-11-03", WebURL: "http://localhost:2001", VerificationTTL: 24 * time.Hour,
		RefreshTTL: time.Hour, PermissionCacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// account stores a user that can log in with "right-password".
func (f *authFixture) account(t *testing.T, email string, mutate func(*model.User)) {
	t.Helper()
	hash, err := HashPassword("right-password")
	if err != nil {
		t.Fatal(err)
	}
	verified := time.Now()
	u := model.User{ID: uuid.New(), Email: &email, PasswordHash: &hash, Status: authz.StatusActive, EmailVerifiedAt: &verified, FullName: "A"}
	if mutate != nil {
		mutate(&u)
	}
	f.users.byEmail[email] = u
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("want error code %q, got %v", code, err)
	}
}

func TestLoginRefusesWhatIsNotAnActiveVerifiedAccountWithTheRightPassword(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t)
	f.account(t, "ann@example.vn", nil)
	f.account(t, "off@example.vn", func(u *model.User) { u.Status = "inactive" })
	f.account(t, "new@example.vn", func(u *model.User) { u.EmailVerifiedAt = nil })

	cases := []struct {
		name, email, password, code string
	}{
		{"unknown email", "nobody@example.vn", "right-password", apperr.CodeInvalidCredentials},
		{"wrong password", "ann@example.vn", "wrong-password", apperr.CodeInvalidCredentials},
		{"inactive account", "off@example.vn", "right-password", apperr.CodeAccountInactive},
		{"unverified email", "new@example.vn", "right-password", apperr.CodeEmailUnverified},
		// The account state is only told once the password is right.
		{"inactive account, wrong password", "off@example.vn", "wrong-password", apperr.CodeInvalidCredentials},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.svc.Login(ctx, tc.email, tc.password, AuthClient{})
			wantCode(t, err, tc.code)
		})
	}
	if f.tx.runs != 0 {
		t.Error("a refused login must not open a session")
	}
}

func TestRegisterOfATakenEmailIsAValidationErrorOnEmail(t *testing.T) {
	f := newAuthFixture(t)
	f.users.createErr = &shared.ConstraintError{Kind: shared.Unique, Constraint: "users_email_key"}

	_, err := f.svc.Register(context.Background(), RegisterInput{FullName: "A", Email: "Taken@Example.vn", Password: "long-enough-1"})
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeValidationFailed || len(ae.Fields) != 1 ||
		ae.Fields[0].Name != "email" || ae.Fields[0].Rule != apperr.RuleUnique {
		t.Fatalf("want a unique validation error on email, got %v", err)
	}
	if len(f.mailer.Sent()) != 0 {
		t.Error("no verification email for a refused registration")
	}
}

func TestRegisterOfAnUnknownWardIsAValidationErrorOnWardCode(t *testing.T) {
	f := newAuthFixture(t)
	f.users.createErr = &shared.ConstraintError{Kind: shared.ForeignKey, Constraint: "users_ward_code_fkey"}

	ward := int32(1)
	_, err := f.svc.Register(context.Background(), RegisterInput{FullName: "A", Email: "a@example.vn", Password: "long-enough-1", WardCode: &ward})
	var ae *apperr.Error
	if !errors.As(err, &ae) || len(ae.Fields) != 1 || ae.Fields[0].Name != "ward_code" || ae.Fields[0].Rule != apperr.RuleExists {
		t.Fatalf("want an exists validation error on ward_code, got %v", err)
	}
}

func TestRegisterCreatesATenantAndItsVerificationTokenInOneTransaction(t *testing.T) {
	f := newAuthFixture(t)
	view, err := f.svc.Register(context.Background(), RegisterInput{FullName: "A", Email: "  Ann@Example.VN ", Password: "long-enough-1", Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if f.tx.runs != 1 || len(f.users.created) != 1 || len(f.verify.inserted) != 1 {
		t.Fatalf("one transaction with the user and its token: %d runs, %d users, %d tokens", f.tx.runs, len(f.users.created), len(f.verify.inserted))
	}
	if got := f.users.created[0]; got.Email != "ann@example.vn" || got.LastActiveRole != authz.RoleTenant {
		t.Errorf("email is normalized and the role is tenant: %+v", got)
	}
	if len(f.users.roles) != 1 || f.users.roles[0] != authz.RoleTenant {
		t.Errorf("only tenant may be granted, got %v", f.users.roles)
	}
	if view.ActiveRole != authz.RoleTenant || len(f.mailer.Sent()) != 1 {
		t.Errorf("view %+v, %d emails", view, len(f.mailer.Sent()))
	}
}

func TestAssignRolesRefusesUnknownRolesAndStaffMixedWithOthers(t *testing.T) {
	ctx := context.Background()
	users := &fakeUsers{}
	if err := assignRoles(ctx, users, uuid.New(), []string{authz.RoleLandlord, authz.RoleTenant}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := assignRoles(ctx, users, uuid.New(), []string{authz.RoleAdmin, authz.RoleTenant}, time.Now()); err == nil {
		t.Error("admin together with tenant must be refused")
	}
	if err := assignRoles(ctx, users, uuid.New(), []string{"wizard"}, time.Now()); err == nil {
		t.Error("an unknown role must be refused")
	}
	if len(users.roles) != 2 {
		t.Errorf("a refused grant must not add any role, got %v", users.roles)
	}
}
