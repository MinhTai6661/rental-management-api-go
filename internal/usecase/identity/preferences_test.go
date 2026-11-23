package identity

import (
	"context"
	"testing"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/pkg/authz"
)

func TestSetPreferencesSwitchesInvitationsAndLeavesAbsentFieldsAlone(t *testing.T) {
	f := newAuthFixture(t)
	f.users.created = []domain.NewUser{{Email: "ann@example.vn", FullName: "A", TermsVersion: "2026-11-03"}} // for Profile
	f.users.accepts = true
	p := authz.Principal{UserID: uuid.NewString(), Role: authz.RoleTenant}
	off := false

	got, err := f.auth.SetPreferences(context.Background(), p, PreferencesInput{AcceptsInvitations: &off})
	if err != nil {
		t.Fatal(err)
	}
	if got.AcceptsInvitations || f.users.accepts {
		t.Errorf("invitations should be off: view %v, stored %v", got.AcceptsInvitations, f.users.accepts)
	}

	// Nothing sent means nothing changes (and the stored value is not touched).
	f.users.accepts = true
	got, err = f.auth.SetPreferences(context.Background(), p, PreferencesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.AcceptsInvitations || !f.users.accepts {
		t.Errorf("an empty change must keep the value: view %v, stored %v", got.AcceptsInvitations, f.users.accepts)
	}
}

func TestSetPreferencesRefusesABadSubject(t *testing.T) {
	f := newAuthFixture(t)
	if _, err := f.auth.SetPreferences(context.Background(), authz.Principal{UserID: "not-a-uuid"}, PreferencesInput{}); err == nil {
		t.Error("a caller without a valid id is refused")
	}
}
