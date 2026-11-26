package report

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

var (
	author   = uuid.New()
	landlord = uuid.New()
	other    = uuid.New()
	now      = time.Now()
)

func roomReport() Report {
	return Report{ID: uuid.New(), ReporterID: author, Kind: KindRoom, RoomLandlordID: &landlord}
}

func featureReport() Report { return Report{ID: uuid.New(), ReporterID: author, Kind: KindFeature} }

func deleted(r Report) Report {
	r.DeletedAt = &now
	return r
}

func TestCanCreate(t *testing.T) {
	cases := []struct {
		role string
		kind Kind
		want bool
	}{
		{authz.RoleTenant, KindRoom, true}, {authz.RoleTenant, KindFeature, true},
		{authz.RoleLandlord, KindRoom, false}, {authz.RoleLandlord, KindFeature, true},
		{authz.RoleAdmin, KindFeature, false}, {authz.RoleManager, KindFeature, false}, {authz.RoleTenant, "other", false},
	}
	for _, c := range cases {
		if got := CanCreate(c.role, c.kind); got != c.want {
			t.Errorf("CanCreate(%s, %s) = %v, want %v", c.role, c.kind, got, c.want)
		}
	}
}

func TestPermissions(t *testing.T) {
	authorV := Viewer{UserID: author, Role: authz.RoleTenant}
	landlordV := Viewer{UserID: landlord, Role: authz.RoleLandlord}
	landlordAsTenant := Viewer{UserID: landlord, Role: authz.RoleTenant}
	strangerLandlord := Viewer{UserID: other, Role: authz.RoleLandlord}
	admin := Viewer{UserID: other, Role: authz.RoleAdmin}
	super := Viewer{UserID: other, Role: authz.RoleSuperAdmin}
	stranger := Viewer{UserID: other, Role: authz.RoleTenant}

	cases := []struct {
		name                         string
		v                            Viewer
		r                            Report
		view, reply, status, deleteR bool
	}{
		{"author of a room report", authorV, roomReport(), true, true, false, true},
		{"landlord of the room", landlordV, roomReport(), true, true, true, false},
		{"landlord acting as tenant", landlordAsTenant, roomReport(), false, false, false, false},
		{"another landlord", strangerLandlord, roomReport(), false, false, false, false},
		{"admin on a room report", admin, roomReport(), false, false, false, false},
		{"super admin on a room report", super, roomReport(), true, true, true, false},
		{"stranger", stranger, roomReport(), false, false, false, false},
		{"admin on a feature report", admin, featureReport(), true, true, true, false},
		{"landlord on a feature report", landlordV, featureReport(), false, false, false, false},
		{"author of a feature report", authorV, featureReport(), true, true, false, true},
		{"author, deleted", authorV, deleted(roomReport()), false, false, false, false},
		{"landlord, deleted", landlordV, deleted(roomReport()), true, false, false, false},
		{"admin, deleted feature report", admin, deleted(featureReport()), true, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanView(c.v, c.r); got != c.view {
				t.Errorf("CanView = %v, want %v", got, c.view)
			}
			if got := CanReply(c.v, c.r); got != c.reply {
				t.Errorf("CanReply = %v, want %v", got, c.reply)
			}
			if got := CanSetStatus(c.v, c.r); got != c.status {
				t.Errorf("CanSetStatus = %v, want %v", got, c.status)
			}
			if got := CanDelete(c.v, c.r); got != c.deleteR {
				t.Errorf("CanDelete = %v, want %v", got, c.deleteR)
			}
		})
	}
}

func TestAuthorize(t *testing.T) {
	authorV := Viewer{UserID: author, Role: authz.RoleTenant}
	landlordV := Viewer{UserID: landlord, Role: authz.RoleLandlord}
	stranger := Viewer{UserID: other, Role: authz.RoleTenant}
	cases := []struct {
		name string
		v    Viewer
		r    Report
		fn   func(Viewer, Report) bool
		want error
	}{
		{"allowed", landlordV, roomReport(), CanSetStatus, nil},
		{"cannot see", stranger, roomReport(), CanReply, ErrNotFound},
		{"author deleted it", authorV, deleted(roomReport()), CanReply, ErrNotFound},
		{"sees but not allowed", authorV, roomReport(), CanSetStatus, ErrForbidden},
		{"deleted, read only", landlordV, deleted(roomReport()), CanSetStatus, ErrReadOnly},
	}
	for _, c := range cases {
		if got := Authorize(c.v, c.r, c.fn); !errors.Is(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestInboxOf(t *testing.T) {
	id := uuid.New()
	if s := InboxOf(Viewer{UserID: id, Role: authz.RoleSuperAdmin}); !s.Everything {
		t.Error("super admin sees everything")
	}
	if s := InboxOf(Viewer{UserID: id, Role: authz.RoleAdmin}); !s.Features || s.Everything || s.LandlordID != nil {
		t.Errorf("admin scope = %+v", s)
	}
	if s := InboxOf(Viewer{UserID: id, Role: authz.RoleLandlord}); s.LandlordID == nil || *s.LandlordID != id || s.Features {
		t.Errorf("landlord scope = %+v", s)
	}
	for _, role := range []string{authz.RoleTenant, authz.RoleManager, ""} {
		if s := InboxOf(Viewer{UserID: id, Role: role}); s != (InboxScope{}) {
			t.Errorf("%q scope = %+v, want none", role, s)
		}
	}
}

func TestRecipientOf(t *testing.T) {
	if r, ok := RecipientOf(roomReport()); !ok || r.UserID != landlord || r.Admins {
		t.Errorf("room recipient = %+v %v", r, ok)
	}
	if r, ok := RecipientOf(featureReport()); !ok || !r.Admins {
		t.Errorf("feature recipient = %+v %v", r, ok)
	}
	if _, ok := RecipientOf(Report{Kind: KindRoom}); ok {
		t.Error("a room report with no known owner goes to nobody")
	}
}

func TestSideOf(t *testing.T) {
	room, feature := roomReport(), featureReport()
	cases := []struct {
		name string
		r    Report
		p    Person
		want Side
	}{
		{"staff", room, Person{ID: other, IsStaff: true}, SideAdmin},
		{"room landlord", room, Person{ID: landlord, IsLandlord: true}, SideLandlord},
		{"landlord who is the tenant of this room", room, Person{ID: other, IsLandlord: true}, SideTenant},
		{"tenant", room, Person{ID: author}, SideTenant},
		{"landlord on a feature report", feature, Person{ID: other, IsLandlord: true}, SideLandlord},
		{"tenant on a feature report", feature, Person{ID: author}, SideTenant},
	}
	for _, c := range cases {
		if got := SideOf(c.r, c.p); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestParse(t *testing.T) {
	for _, k := range Kinds() {
		if got, err := ParseKind(string(k)); err != nil || got != k {
			t.Errorf("ParseKind(%s)", k)
		}
	}
	for _, s := range Statuses() {
		if got, err := ParseStatus(string(s)); err != nil || got != s {
			t.Errorf("ParseStatus(%s)", s)
		}
	}
	if _, err := ParseKind("x"); !errors.Is(err, ErrUnknownKind) {
		t.Error("unknown kind")
	}
	if _, err := ParseStatus(""); !errors.Is(err, ErrUnknownStatus) {
		t.Error("unknown status")
	}
}
