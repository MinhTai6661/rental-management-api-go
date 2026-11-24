package admin

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/admin"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

var ctx = context.Background()

func actorOf(a *domain.Account, role string) Actor { return Actor{ID: a.ID, Role: role} }

func TestDeleteRules(t *testing.T) {
	w := newWorld()
	admin, other := w.account(authz.RoleAdmin), w.account(authz.RoleAdmin)
	super, tenant := w.account(authz.RoleSuperAdmin), w.account(authz.RoleTenant)
	users := NewUsers(w.deps(), Options{})

	for name, tc := range map[string]struct {
		actor  Actor
		target uuid.UUID
		ok     bool
	}{
		"admin deletes a tenant":     {actorOf(admin, authz.RoleAdmin), tenant.ID, true},
		"admin cannot delete admin":  {actorOf(admin, authz.RoleAdmin), other.ID, false},
		"admin cannot delete super":  {actorOf(admin, authz.RoleAdmin), super.ID, false},
		"super cannot delete itself": {actorOf(super, authz.RoleSuperAdmin), super.ID, false},
		"super deletes an admin":     {actorOf(super, authz.RoleSuperAdmin), other.ID, true},
		"nobody deletes a stranger":  {actorOf(super, authz.RoleSuperAdmin), uuid.Must(uuid.NewV7()), false},
	} {
		t.Run(name, func(t *testing.T) {
			w.deleted, w.revoked = nil, nil
			err := users.Delete(ctx, tc.actor, tc.target)
			if tc.ok {
				if err != nil || !slices.Contains(w.deleted, tc.target) || len(w.revoked) != 1 {
					t.Fatalf("want deleted with sessions closed: err=%v deleted=%v revoked=%v", err, w.deleted, w.revoked)
				}
				return
			}
			if err == nil || len(w.deleted) != 0 {
				t.Fatalf("must be refused, got %v", err)
			}
		})
	}
}

func TestBulkDeleteReportsRefusedAndSkipsUnknown(t *testing.T) {
	w := newWorld()
	admin, otherAdmin, tenant := w.account(authz.RoleAdmin), w.account(authz.RoleAdmin), w.account(authz.RoleTenant)
	users := NewUsers(w.deps(), Options{})
	ghost := uuid.Must(uuid.NewV7())

	res, err := users.BulkDelete(ctx, actorOf(admin, authz.RoleAdmin), []uuid.UUID{tenant.ID, otherAdmin.ID, ghost, tenant.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Done) != 1 || res.Done[0] != tenant.ID || len(res.Failed) != 1 || res.Failed[0].ID != otherAdmin.ID || res.Failed[0].Name == "" {
		t.Fatalf("got %+v", res)
	}
	if len(w.revoked) != 1 {
		t.Fatalf("the deleted account's session must be closed: %v", w.revoked)
	}
	if _, err := users.BulkDelete(ctx, actorOf(admin, authz.RoleAdmin), nil); !errors.Is(err, domain.ErrNoIDs) {
		t.Fatalf("no ids: %v", err)
	}
	if _, err := users.BulkDelete(ctx, actorOf(admin, authz.RoleAdmin), make([]uuid.UUID, 101)); !errors.Is(err, domain.ErrTooManyIDs) {
		t.Fatalf("101 ids: %v", err)
	}
}

func TestListHidesStaffFromAdminsAndTheViewerFromThemselves(t *testing.T) {
	w := newWorld()
	admin, super, tenant := w.account(authz.RoleAdmin), w.account(authz.RoleSuperAdmin), w.account(authz.RoleTenant)
	users := NewUsers(w.deps(), Options{})

	items, total, err := users.List(ctx, actorOf(admin, authz.RoleAdmin), UserQuery{}, 10, 0)
	if err != nil || total != 1 || items[0].ID != tenant.ID {
		t.Fatalf("admin sees only the tenant: %v %+v", err, items)
	}
	_, total, _ = users.List(ctx, actorOf(super, authz.RoleSuperAdmin), UserQuery{}, 10, 0)
	if total != 2 {
		t.Fatalf("super admin sees the admin and the tenant, not itself: %d", total)
	}
	for _, q := range []UserQuery{{RoleName: "wizard"}, {Status: "dead"}, {Sort: "nope"}} {
		var ve *domain.ValidationError
		if _, _, err := users.List(ctx, actorOf(admin, authz.RoleAdmin), q, 10, 0); !errors.As(err, &ve) {
			t.Fatalf("%+v must be a validation error: %v", q, err)
		}
	}
}

func TestAssignRoles(t *testing.T) {
	w := newWorld()
	admin, super := w.account(authz.RoleAdmin), w.account(authz.RoleSuperAdmin)
	tenant, landlord := w.account(authz.RoleTenant), w.account(authz.RoleLandlord, authz.RoleTenant)
	landlord.LastActiveRole = ptr(authz.RoleLandlord)
	users := NewUsers(w.deps(), Options{})
	asAdmin := actorOf(admin, authz.RoleAdmin)

	if _, err := users.AssignRoles(ctx, asAdmin, tenant.ID, nil, ""); !errors.Is(err, domain.ErrRolesEmpty) {
		t.Fatalf("empty roles must be refused: %v", err)
	}
	if _, err := users.AssignRoles(ctx, asAdmin, tenant.ID, []string{authz.RoleAdmin}, ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("an admin cannot make an admin: %v", err)
	}
	if _, err := users.AssignRoles(ctx, actorOf(super, authz.RoleSuperAdmin), tenant.ID, []string{authz.RoleSuperAdmin}, ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("nobody gives super admin: %v", err)
	}
	if _, err := users.AssignRoles(ctx, asAdmin, tenant.ID, []string{authz.RoleAdmin, authz.RoleTenant}, ""); !errors.Is(err, domain.ErrStaffRolesMixed) {
		t.Fatalf("staff must not mix: %v", err)
	}

	// Gaining the landlord role sets the landlord up.
	got, err := users.AssignRoles(ctx, asAdmin, tenant.ID, []string{authz.RoleLandlord, authz.RoleTenant}, "")
	if err != nil || !slices.Equal(got.Roles, []string{authz.RoleLandlord, authz.RoleTenant}) {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, ok := w.profiles[tenant.ID]; !ok {
		t.Fatal("a new landlord gets a business profile")
	}

	// Losing it is blocked while contracts run, and allowed (with a notice) otherwise.
	w.impact = domain.Impact{ActiveContracts: 2}
	var blocked *domain.HasActiveContractsError
	if _, err := users.AssignRoles(ctx, asAdmin, landlord.ID, []string{authz.RoleTenant}, "x"); !errors.As(err, &blocked) || blocked.Count != 2 {
		t.Fatalf("running contracts must block: %v", err)
	}
	w.impact = domain.Impact{}
	w.notes = nil
	if _, err := users.AssignRoles(ctx, asAdmin, landlord.ID, []string{authz.RoleTenant}, "abuse"); err != nil {
		t.Fatal(err)
	}
	if landlord.LastActiveRole != nil || !slices.Equal(w.kinds(), []string{NotifyLandlordRevoked}) {
		t.Fatalf("last role must be forgotten and the landlord told: %v %v", landlord.LastActiveRole, w.kinds())
	}
	if !slices.Contains(w.actions(), AuditRolesChanged) {
		t.Fatalf("audit: %v", w.actions())
	}
}

func TestRevokeLandlord(t *testing.T) {
	w := newWorld()
	admin, tenant, landlord := w.account(authz.RoleAdmin), w.account(authz.RoleTenant), w.account(authz.RoleLandlord, authz.RoleTenant)
	users := NewUsers(w.deps(), Options{})
	if err := users.RevokeLandlord(ctx, actorOf(admin, authz.RoleAdmin), tenant.ID, ""); !errors.Is(err, domain.ErrNotLandlord) {
		t.Fatalf("not a landlord: %v", err)
	}
	if _, err := users.Impact(ctx, tenant.ID); !errors.Is(err, domain.ErrNotLandlord) {
		t.Fatalf("impact of a non landlord: %v", err)
	}
	w.impact = domain.Impact{ActiveContracts: 1}
	if err := users.RevokeLandlord(ctx, actorOf(admin, authz.RoleAdmin), landlord.ID, "r"); err == nil {
		t.Fatal("a running contract blocks the revoke")
	}
	w.impact = domain.Impact{Rooms: 3}
	if err := users.RevokeLandlord(ctx, actorOf(admin, authz.RoleAdmin), landlord.ID, "r"); err != nil || slices.Contains(landlord.Roles, authz.RoleLandlord) {
		t.Fatalf("revoke: %v %v", err, landlord.Roles)
	}
}

func TestSubmitApplication(t *testing.T) {
	w := newWorld()
	tenant, landlord, manager := w.account(authz.RoleTenant), w.account(authz.RoleLandlord, authz.RoleTenant), w.account(authz.RoleTenant, authz.RoleManager)
	apps := NewApplications(w.deps(), Options{})
	in := domain.ApplicationInput{BusinessName: "Nhà trọ An", Phone: "0900000000"}

	if _, err := apps.Submit(ctx, actorOf(landlord, authz.RoleLandlord), in); !errors.Is(err, domain.ErrAlreadyLandlord) {
		t.Fatalf("landlord: %v", err)
	}
	if _, err := apps.Submit(ctx, actorOf(manager, authz.RoleManager), in); !errors.Is(err, domain.ErrNotRenter) {
		t.Fatalf("acting as manager: %v", err)
	}
	if _, err := apps.Submit(ctx, actorOf(tenant, authz.RoleTenant), domain.ApplicationInput{}); err == nil {
		t.Fatal("blank input")
	}
	app, err := apps.Submit(ctx, actorOf(tenant, authz.RoleTenant), in)
	if err != nil || app.Status != domain.ApplicationPending {
		t.Fatalf("submit: %v %+v", err, app)
	}
	if len(w.notes) != 1 || !w.notes[0].ToAdmins || w.notes[0].Kind != NotifyApplicationReceived {
		t.Fatalf("admins must be told: %+v", w.notes)
	}
	if _, err := apps.Submit(ctx, actorOf(tenant, authz.RoleTenant), in); !errors.Is(err, domain.ErrAlreadyPending) {
		t.Fatalf("second application: %v", err)
	}
	mine, err := apps.Mine(ctx, actorOf(tenant, authz.RoleTenant))
	if err != nil || mine == nil || mine.ID != app.ID {
		t.Fatalf("mine: %v %v", mine, err)
	}
	if err := apps.Withdraw(ctx, actorOf(tenant, authz.RoleTenant)); err != nil {
		t.Fatal(err)
	}
	if mine, _ := apps.Mine(ctx, actorOf(tenant, authz.RoleTenant)); mine != nil {
		t.Fatal("withdrawn application is gone")
	}
	if !slices.Equal(w.actions(), []string{AuditApplicationSubmitted, AuditApplicationWithdrawn}) {
		t.Fatalf("audit: %v", w.actions())
	}
}

func TestApproveAndRejectApplication(t *testing.T) {
	w := newWorld()
	admin, tenant := w.account(authz.RoleAdmin), w.account(authz.RoleTenant)
	apps := NewApplications(w.deps(), Options{})
	asAdmin := actorOf(admin, authz.RoleAdmin)
	addr := "12 Lê Lợi"
	submit := func() domain.Application {
		a, err := apps.Submit(ctx, actorOf(tenant, authz.RoleTenant), domain.ApplicationInput{BusinessName: strings.Repeat("a", 80), Phone: "090", AddressDetail: &addr})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	rejected := submit()
	if err := apps.Reject(ctx, asAdmin, rejected.ID, " "); err == nil {
		t.Fatal("a rejection needs a reason")
	}
	if err := apps.Reject(ctx, asAdmin, rejected.ID, "thiếu giấy tờ"); err != nil {
		t.Fatal(err)
	}
	if err := apps.Approve(ctx, asAdmin, rejected.ID, ""); !errors.Is(err, domain.ErrNotReviewable) {
		t.Fatalf("an answered application cannot be answered again: %v", err)
	}
	if slices.Contains(tenant.Roles, authz.RoleLandlord) {
		t.Fatal("a rejection gives no role")
	}

	approved := submit()
	w.notes = nil
	if err := apps.Approve(ctx, asAdmin, approved.ID, ""); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tenant.Roles, authz.RoleLandlord) || !slices.Contains(tenant.Roles, authz.RoleTenant) {
		t.Fatalf("landlord is added and tenant kept: %v", tenant.Roles)
	}
	if p := w.profiles[tenant.ID]; p.BusinessName != approved.BusinessName {
		t.Fatalf("profile from the application: %+v", p)
	}
	if len(w.firsts) != 1 || len([]rune(w.firsts[0].Name)) != 50 || w.firsts[0].AddressDetail != addr {
		t.Fatalf("first building: %+v", w.firsts)
	}
	if !slices.Equal(w.kinds(), []string{NotifyApplicationApproved}) || w.notes[0].UserID != tenant.ID {
		t.Fatalf("applicant must be told: %+v", w.notes)
	}
}

func TestApplicationBulk(t *testing.T) {
	w := newWorld()
	admin := w.account(authz.RoleAdmin)
	apps := NewApplications(w.deps(), Options{})
	var ids []uuid.UUID
	for range 2 {
		u := w.account(authz.RoleTenant)
		a, _ := apps.Submit(ctx, actorOf(u, authz.RoleTenant), domain.ApplicationInput{BusinessName: "B", Phone: "1"})
		ids = append(ids, a.ID)
	}
	if err := apps.Approve(ctx, actorOf(admin, authz.RoleAdmin), ids[0], ""); err != nil {
		t.Fatal(err)
	}
	res, err := apps.Bulk(ctx, actorOf(admin, authz.RoleAdmin), false, ids, "")
	if err != nil || len(res.Done) != 1 || res.Done[0] != ids[1] || len(res.Failed) != 1 || res.Failed[0].ID != ids[0] || res.Failed[0].Name != "B" {
		t.Fatalf("got %+v %v", res, err)
	}
	if _, err := apps.Bulk(ctx, actorOf(admin, authz.RoleAdmin), true, ids, ""); err == nil {
		t.Fatal("a bulk rejection needs a reason")
	}
}

func TestSettings(t *testing.T) {
	w := newWorld()
	super := w.account(authz.RoleSuperAdmin)
	s := NewSettings(w.deps(), Options{Defaults: map[string]float64{"invoices.due_days": 10}})
	asSuper := actorOf(super, authz.RoleSuperAdmin)
	f := func(v float64) *float64 { return &v }

	list, err := s.List(ctx)
	if err != nil || list.Version == "" {
		t.Fatal(err)
	}
	for _, it := range list.Items {
		if it.Definition.Key == "invoices.due_days" && (it.Value != 10 || it.Overridden) {
			t.Fatalf("the configured default shows: %+v", it)
		}
	}
	var ve *domain.ValidationError
	_, err = s.Update(ctx, asSuper, []domain.SettingChange{{Key: "invoices.due_days", Value: f(1000)}, {Key: "evil", Value: f(1)}})
	if !errors.As(err, &ve) || len(ve.Violations) != 2 || ve.Violations[0].Name != "settings.0.value" || ve.Violations[1].Name != "settings.1.key" {
		t.Fatalf("got %v", err)
	}
	if len(w.settings) != 0 {
		t.Fatal("a bad request changes nothing")
	}
	after, err := s.Update(ctx, asSuper, []domain.SettingChange{{Key: "invoices.due_days", Value: f(14)}})
	if err != nil || w.settings["invoices.due_days"] != 14 || after.Version == list.Version {
		t.Fatalf("set: %v %v", err, w.settings)
	}
	if _, err := s.Update(ctx, asSuper, []domain.SettingChange{{Key: "invoices.due_days", Value: f(14)}}); err != nil || len(w.events) != 1 {
		t.Fatalf("an unchanged value is not audited: %v", w.events)
	}
	if _, err := s.Update(ctx, asSuper, []domain.SettingChange{{Key: "invoices.due_days"}}); err != nil || len(w.settings) != 0 || len(w.events) != 2 {
		t.Fatalf("null resets: %v %v", err, w.settings)
	}
}

func legalInput() domain.LegalInput {
	body := []byte(`{"type":"doc","content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"1. Use"}]}]}`)
	text := domain.LegalText{Title: "Terms", Body: body}
	return domain.LegalInput{Content: domain.LegalContent{VI: text, EN: text}, Summary: domain.ChangeSummary{VI: "đổi", EN: "changed"}}
}

func TestLegalPublishAndAccept(t *testing.T) {
	w := newWorld()
	super, tenant := w.account(authz.RoleSuperAdmin), w.account(authz.RoleTenant)
	l := NewLegal(w.deps(), Options{TermsVersion: "2026-01"})

	if v, _ := l.CurrentVersion(ctx); v != "2026-01" {
		t.Fatalf("nothing published: the configured version, got %q", v)
	}
	bad := legalInput()
	bad.Summary.VI = ""
	if _, err := l.Publish(ctx, actorOf(super, authz.RoleSuperAdmin), domain.LegalTerms, bad); err == nil {
		t.Fatal("a change summary is required")
	}
	v1, err := l.Publish(ctx, actorOf(super, authz.RoleSuperAdmin), domain.LegalTerms, legalInput())
	if err != nil || v1 != "2026-11-24.090000" {
		t.Fatalf("publish: %q %v", v1, err)
	}
	w.duplicateVersions = 1
	v2, err := l.Publish(ctx, actorOf(super, authz.RoleSuperAdmin), domain.LegalPrivacy, legalInput())
	if err != nil || v2 != "2026-11-24.090001" {
		t.Fatalf("a taken second moves the version one second on: %q %v", v2, err)
	}
	if cur, _ := l.CurrentVersion(ctx); cur != v2 {
		t.Fatalf("the newest publication of any kind decides: %q", cur)
	}
	if need, _, _ := l.NeedsAcceptance(ctx, tenant.Roles, nil); !need {
		t.Fatal("a tenant who never accepted must accept")
	}
	if need, _, _ := l.NeedsAcceptance(ctx, super.Roles, nil); need {
		t.Fatal("staff is not asked")
	}
	docs, _ := l.Updates(ctx, tenant.ID, 5)
	if len(docs) != 2 {
		t.Fatalf("never accepted: the newest of each kind: %d", len(docs))
	}
	if _, err := l.Accept(ctx, actorOf(tenant, authz.RoleTenant)); err != nil {
		t.Fatal(err)
	}
	w.now = w.now.Add(time.Hour)
	if docs, _ := l.Updates(ctx, tenant.ID, 5); len(docs) != 0 {
		t.Fatalf("nothing new since accepting: %d", len(docs))
	}
	if cur, err := l.Current(ctx, domain.LegalKind("terms")); err != nil || cur == nil || cur.Version != v1 {
		t.Fatalf("current terms: %v %v", cur, err)
	}
	if _, err := l.Document(ctx, domain.LegalTerms, w.docs[1].ID); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("a document of the other kind is not found: %v", err)
	}
}

func TestErase(t *testing.T) {
	w := newWorld()
	tenant, admin := w.account(authz.RoleTenant), w.account(authz.RoleAdmin)
	hash := "stored"
	w.hash, w.password = &hash, "right"
	p := NewPersonalData(w.deps(), Options{})
	asTenant := actorOf(tenant, authz.RoleTenant)

	if err := p.Erase(ctx, actorOf(admin, authz.RoleAdmin), "right"); !errors.Is(err, domain.ErrStaffCannotErase) {
		t.Fatalf("staff: %v", err)
	}
	if err := p.Erase(ctx, asTenant, "wrong"); !errors.Is(err, domain.ErrPasswordWrong) || len(w.erased) != 0 {
		t.Fatalf("wrong password: %v", err)
	}
	w.facts = domain.ErasureFacts{AsTenant: true}
	var blocked *domain.ErasureBlockedError
	if err := p.Erase(ctx, asTenant, "right"); !errors.As(err, &blocked) || len(w.erased) != 0 {
		t.Fatalf("a running rental blocks: %v", err)
	}
	if got, _ := p.ErasureCheck(ctx, asTenant); !slices.Equal(got, []string{domain.BlockerRunningRental}) {
		t.Fatalf("check: %v", got)
	}
	w.facts = domain.ErasureFacts{}
	if err := p.Erase(ctx, asTenant, "right"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(w.erased, tenant.ID) || !slices.Contains(w.scrubbed, tenant.ID) || len(w.revoked) != 1 {
		t.Fatalf("erased=%v scrubbed=%v revoked=%v", w.erased, w.scrubbed, w.revoked)
	}
	if w.actions()[0] != AuditUserErased {
		t.Fatalf("the audit event comes first: %v", w.actions())
	}
	if data, err := p.Export(ctx, asTenant); err != nil || data.Account.ID != tenant.ID.String() {
		t.Fatalf("export: %v", err)
	}
}

func ptr(s string) *string { return &s }
