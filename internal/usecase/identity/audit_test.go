package identity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/pkg/authz"
)

type fakeAudit struct {
	events []AuditEvent
	err    error
}

func (f *fakeAudit) Record(_ context.Context, e AuditEvent) error {
	f.events = append(f.events, e)
	return f.err
}

func (f *fakeAudit) actions() []string {
	out := make([]string, len(f.events))
	for i, e := range f.events {
		out[i] = e.Action
	}
	return out
}

func TestLoginIsRecordedWithItsSession(t *testing.T) {
	f := newAuthFixture(t)
	f.account("ann@example.vn", nil)
	f.users.auth = domain.AuthUser{Status: authz.StatusActive, Roles: []string{authz.RoleTenant}}
	f.users.created = []domain.NewUser{{Email: "ann@example.vn", FullName: "A", TermsVersion: "2026-11-03"}}
	if _, err := f.auth.Login(context.Background(), "ann@example.vn", "right-password", Client{}); err != nil {
		t.Fatal(err)
	}
	if got := f.audit.actions(); len(got) != 1 || got[0] != AuditLogin {
		t.Fatalf("%v", got)
	}
	e := f.audit.events[0]
	if e.SessionID == uuid.Nil || e.ActorRole != authz.RoleTenant || e.Meta["via"] != "password" || e.ActorID == uuid.Nil {
		t.Errorf("%+v", e)
	}
}

func TestAFailedLoginIsRecordedWithAHashAndNeverTheEmail(t *testing.T) {
	f := newAuthFixture(t)
	f.account("ann@example.vn", nil)
	for _, c := range []struct{ email, password, reason string }{
		{"nobody@example.vn", "right-password", failBadCredentials},
		{" ANN@example.vn", "wrong-password", failBadCredentials},
	} {
		if _, err := f.auth.Login(context.Background(), c.email, c.password, Client{}); !errors.Is(err, domain.ErrInvalidCredentials) {
			t.Fatal(err)
		}
	}
	if got := f.audit.actions(); len(got) != 2 || got[0] != AuditLoginFailed {
		t.Fatalf("%v", got)
	}
	for _, e := range f.audit.events {
		if e.Meta["reason"] != failBadCredentials {
			t.Errorf("%v", e.Meta)
		}
		hash, _ := e.Meta["email_hash"].(string)
		if len(hash) != 64 || strings.Contains(hash, "@") {
			t.Errorf("a sha256 hex, not the email: %q", hash)
		}
	}
	if f.audit.events[0].TargetUserID != nil || f.audit.events[1].TargetUserID == nil {
		t.Error("the person is named only when the account exists")
	}
	if f.audit.events[1].Meta["email_hash"] != emailHash("ann@example.vn") {
		t.Error("the hash is of the normalized, lower-case address")
	}
}

func TestAFailedAuditOfALoginDoesNotFailTheLogin(t *testing.T) {
	f := newAuthFixture(t)
	f.audit.err = errors.New("audit down")
	f.account("ann@example.vn", nil)
	if _, err := f.auth.Login(context.Background(), "ann@example.vn", "wrong", Client{}); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("the refusal stays the answer: %v", err)
	}
}

func TestPasswordChangeAndLogoutAreRecorded(t *testing.T) {
	f := newAuthFixture(t)
	stored := "hashed:right-password"
	f.users.stored = &stored
	user, here := uuid.New(), uuid.New()
	f.sess.closedByAll = []uuid.UUID{uuid.New(), uuid.New()}
	p := principalOf(user, here)
	if err := f.auth.ChangePassword(context.Background(), p, "right-password", "another-password-1"); err != nil {
		t.Fatal(err)
	}
	e := f.audit.events[0]
	if e.Action != AuditPasswordChanged || e.ActorID != user || e.SessionID != here || e.Meta["sessions_closed"] != 2 {
		t.Errorf("%+v", e)
	}
	if err := f.auth.LogoutAll(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if last := f.audit.events[len(f.audit.events)-1]; last.Action != AuditLogout || last.Meta["everywhere"] != true {
		t.Errorf("%+v", last)
	}
}

func TestAReusedRefreshTokenIsRecordedAfterTheSessionIsClosed(t *testing.T) {
	f := newAuthFixture(t)
	ago := reuseGrace + 1
	rt := f.refreshToken(&ago)
	if _, err := f.auth.Refresh(context.Background(), "known", Client{}); !errors.Is(err, domain.ErrRefreshInvalid) {
		t.Fatal(err)
	}
	if got := f.audit.actions(); len(got) != 1 || got[0] != AuditTokenReuse || f.audit.events[0].SessionID != rt.SessionID {
		t.Errorf("%v", got)
	}
}

func TestRegisterIsRecordedInTheSameTransaction(t *testing.T) {
	f := newAuthFixture(t)
	if _, err := f.auth.Register(context.Background(), RegisterInput{Email: "New@Example.vn", Password: "long-enough-password", FullName: "N"}); err != nil {
		t.Fatal(err)
	}
	if got := f.audit.actions(); len(got) != 1 || got[0] != AuditRegister || f.audit.events[0].ActorRole != authz.RoleTenant {
		t.Errorf("%v", got)
	}
}
