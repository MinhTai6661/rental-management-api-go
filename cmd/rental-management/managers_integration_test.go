//go:build integration

package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/config"
	"rental-management/internal/adapter/http/httpx"
	invoicehttp "rental-management/internal/adapter/http/invoice"
	managerhttp "rental-management/internal/adapter/http/manager"
	"rental-management/internal/adapter/http/testenv"
	invoiceuc "rental-management/internal/usecase/invoice"
	manageruc "rental-management/internal/usecase/manager"
	"rental-management/pkg/authz"
)

// The manager context is tested through the real composition code: the invitation by link, the hand-over, the role of
// the account, and what a manager may then do with the contracts and bills of the rooms handed to them.

type managerStack struct {
	*bridgeEnv
	manager uuid.UUID
}

func newManagerStack(t *testing.T) *managerStack {
	t.Helper()
	e := &managerStack{bridgeEnv: newBridgeEnv(t)}
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO roles (code, seniority, is_staff) VALUES ('manager', 4, false)`); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ('m@example.vn', 'Minh Manager') RETURNING id`).Scan(&e.manager); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_code) VALUES ($1, 'tenant')`, e.manager); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DataEncryptionKey: testDataKey, InvitationTTLDays: 7}
	log := slog.New(slog.DiscardHandler)
	invitees, err := newInvitees(cfg, log, e.pool)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := newSealer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	handovers := newManagers(cfg, log, e.pool, managerPorts{invitees: invitees, sealer: sealer, notify: e.inbox.deliveries, offers: e.rentals.managerLinks})
	billing, err := newInvoices(cfg, log, e.pool, e.plans, sealer, e.inbox.deliveries, invoiceManagers{access: handovers.access, people: invitees})
	if err != nil {
		t.Fatal(err)
	}
	e.auth["mgr-as-tenant"] = principalOf(e.manager, authz.RoleTenant)
	e.auth["mgr"] = principalOf(e.manager, authz.RoleManager)
	binder := httpx.NewBinder()
	v1 := e.engine.Group("/v1")
	managerhttp.MountManagers(v1,
		managerhttp.NewInvitationHandler(handovers.invitations, handovers.answers, manageruc.ClockFunc(time.Now), binder),
		managerhttp.NewAssignmentHandler(handovers.assignments, handovers.managed, binder), e.auth, allowAll{})
	invoicehttp.MountInvoices(v1, invoicehttp.NewInvoiceHandler(billing.invoices, binder, invoiceuc.ClockFunc(time.Now), nil),
		invoicehttp.NewMeterHandler(billing.invoices), invoicehttp.NewPublicHandler(billing.public), e.auth, allowAll{})
	return e
}

type invitationIssued struct {
	Invitation struct {
		ID uuid.UUID `json:"id"`
	} `json:"invitation"`
	Link string `json:"link"`
}

type assignmentRow struct {
	ID           uuid.UUID `json:"id"`
	Capabilities []string  `json:"capabilities"`
	Room         struct {
		ID uuid.UUID `json:"id"`
	} `json:"room"`
}

// handOver invites by public link and has the manager account accept: the rooms are theirs.
func (e *managerStack) handOver(caps []string, rooms ...uuid.UUID) invitationIssued {
	e.t.Helper()
	w := e.call(http.MethodPost, "/v1/my/manager-invitations", map[string]any{"room_ids": rooms, "capabilities": caps}, "landlord")
	testenv.ExpectStatus(e.t, w, http.StatusCreated)
	issued := testenv.DecodeData[invitationIssued](e.t, w)
	_, token, ok := strings.Cut(issued.Link, "token=")
	if !ok {
		e.t.Fatalf("no token in the link %q", issued.Link)
	}
	testenv.ExpectStatus(e.t, e.call(http.MethodPost, "/v1/manager-invitations/accept", map[string]any{"token": token}, "mgr-as-tenant"), http.StatusCreated)
	return issued
}

func (e *managerStack) roleCount() int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM user_roles WHERE user_id = $1 AND role_code = 'manager'`, e.manager).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *managerStack) status(method, path string, body any, bearer string) int {
	e.t.Helper()
	return e.call(method, path, body, bearer).Code
}

func TestManagerInvitationGivesTheRoomsAndTheRoleAndRevokingTakesThemBack(t *testing.T) {
	e := newManagerStack(t)
	r1, r2 := e.newRoom("P1", "rented"), e.newRoom("P2", "rented")

	// Only the landlord's own rooms can be offered, and a room has one manager at a time.
	foreign := e.newRoomFor("landlord2", "X1", "available")
	if got := e.status(http.MethodPost, "/v1/my/manager-invitations", map[string]any{"room_ids": []uuid.UUID{foreign.ID}}, "landlord"); got != http.StatusUnprocessableEntity {
		t.Fatalf("a room of somebody else: %d", got)
	}
	issued := e.handOver([]string{"contracts.propose"}, r1.ID)
	if e.roleCount() != 1 {
		t.Fatal("accepting gives the manager role")
	}
	if got := e.status(http.MethodPost, "/v1/my/manager-invitations", map[string]any{"room_ids": []uuid.UUID{r1.ID}}, "landlord"); got != http.StatusUnprocessableEntity {
		t.Fatalf("a room that has a manager: %d", got)
	}
	// The link is spent, whoever opens it.
	_, token, _ := strings.Cut(issued.Link, "token=")
	if got := e.status(http.MethodPost, "/v1/manager-invitations/accept", map[string]any{"token": token}, "mgr-as-tenant"); got != http.StatusNotFound {
		t.Fatalf("a used link: %d", got)
	}

	// The manager sees their rooms only in the manager role; the tenant role has no such list.
	w := e.call(http.MethodGet, "/v1/my/managed-rooms", nil, "mgr")
	testenv.ExpectStatus(t, w, http.StatusOK)
	rows := testenv.DecodeData[[]assignmentRow](t, w)
	if len(rows) != 1 || rows[0].Room.ID != r1.ID {
		t.Fatalf("managed rooms: %+v", rows)
	}
	if got := e.status(http.MethodGet, "/v1/my/managed-rooms", nil, "mgr-as-tenant"); got != http.StatusForbidden {
		t.Fatalf("managed rooms in tenant mode: %d", got)
	}
	// The landlord's list, and the settings, are the landlord's alone.
	if got := e.status(http.MethodGet, "/v1/my/room-managers", nil, "mgr"); got != http.StatusForbidden {
		t.Fatalf("the manager lists hand-overs: %d", got)
	}
	if got := e.status(http.MethodDelete, "/v1/my/room-managers/"+rows[0].ID.String(), nil, "landlord2"); got != http.StatusNotFound {
		t.Fatalf("somebody else's hand-over: %d", got)
	}

	// Capabilities change room by room and a revoke drags what depends on it along.
	e.handOverTo(r2.ID, []string{"contracts.propose", "contracts.create"})
	list := testenv.DecodeData[[]assignmentRow](t, e.call(http.MethodGet, "/v1/my/room-managers?manager_id="+e.manager.String(), nil, "landlord"))
	if len(list) != 2 {
		t.Fatalf("hand-overs: %+v", list)
	}
	for _, a := range list {
		if a.Room.ID == r2.ID {
			w := e.call(http.MethodPut, "/v1/my/room-managers/"+a.ID.String(), map[string]any{"revoke": []string{"contracts.propose"}}, "landlord")
			testenv.ExpectStatus(t, w, http.StatusOK)
			if got := testenv.DecodeData[assignmentRow](t, w).Capabilities; len(got) != 0 {
				t.Fatalf("create needs propose: %v", got)
			}
		}
	}

	// Taking back the last room takes the role away; taking back one of two does not.
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/my/room-managers/bulk", map[string]any{"action": "revoke", "ids": []uuid.UUID{list[0].ID}}, "landlord"), http.StatusOK)
	if e.roleCount() != 1 {
		t.Fatal("one room is still handed over")
	}
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/my/room-managers/bulk", map[string]any{"action": "revoke", "ids": []uuid.UUID{list[1].ID}}, "landlord"), http.StatusOK)
	if e.roleCount() != 0 {
		t.Fatal("no room left: the role goes")
	}
}

// handOverTo hands one more room to the same account.
func (e *managerStack) handOverTo(room uuid.UUID, caps []string) {
	e.t.Helper()
	e.handOver(caps, room)
}

func TestManagerCannotAnswerTheirOwnInvitationOrAnInvalidOne(t *testing.T) {
	e := newManagerStack(t)
	r := e.newRoom("P1", "rented")
	w := e.call(http.MethodPost, "/v1/my/manager-invitations", map[string]any{"room_ids": []uuid.UUID{r.ID}}, "landlord")
	testenv.ExpectStatus(t, w, http.StatusCreated)
	issued := testenv.DecodeData[invitationIssued](t, w)
	_, token, _ := strings.Cut(issued.Link, "token=")
	// The landlord (in their tenant mode) cannot manage their own rooms, and a made-up link says nothing.
	if got := e.status(http.MethodPost, "/v1/manager-invitations/accept", map[string]any{"token": token}, "landlord-as-tenant"); got != http.StatusUnprocessableEntity {
		t.Fatalf("own invitation: %d", got)
	}
	if got := e.status(http.MethodPost, "/v1/manager-invitations/accept", map[string]any{"token": strings.Repeat("a", 64)}, "mgr-as-tenant"); got != http.StatusNotFound {
		t.Fatalf("invalid link: %d", got)
	}
	// Another landlord does not even know the invitation exists.
	for _, path := range []string{"/link"} {
		if got := e.status(http.MethodGet, "/v1/my/manager-invitations/"+issued.Invitation.ID.String()+path, nil, "landlord2"); got != http.StatusNotFound {
			t.Fatalf("somebody else's invitation %s: %d", path, got)
		}
	}
	if got := e.status(http.MethodDelete, "/v1/my/manager-invitations/"+issued.Invitation.ID.String(), nil, "landlord2"); got != http.StatusNotFound {
		t.Fatalf("withdraw somebody else's invitation: %d", got)
	}
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/my/manager-invitations/"+issued.Invitation.ID.String(), nil, "landlord"), http.StatusNoContent)
	if got := e.status(http.MethodPost, "/v1/manager-invitations/accept", map[string]any{"token": token}, "mgr-as-tenant"); got != http.StatusNotFound {
		t.Fatalf("a withdrawn invitation: %d", got)
	}
}

func TestManagerWorksOnTheBillsOfTheirRoomsOnly(t *testing.T) {
	e := newManagerStack(t)
	r1, r2 := e.newRoom("P1", "rented"), e.newRoom("P2", "rented")
	c1, c2 := e.contractsOf(r1.ID)[0].ID, e.contractsOf(r2.ID)[0].ID
	e.handOver(nil, r1.ID)

	// The contracts: the room handed over, not the other.
	w := e.call(http.MethodGet, "/v1/contracts", nil, "mgr")
	testenv.ExpectStatus(t, w, http.StatusOK)
	got := testenv.DecodeData[[]contractRow](t, w)
	if len(got) != 1 || got[0].ID != c1 {
		t.Fatalf("contracts of the manager: %+v", got)
	}
	if got := e.status(http.MethodGet, "/v1/contracts/"+c2.String(), nil, "mgr"); got != http.StatusNotFound {
		t.Fatalf("a contract of another room: %d", got)
	}
	// A manager changes no contract.
	if got := e.status(http.MethodPut, "/v1/contracts/"+c1.String(), map[string]any{"status": "ended"}, "mgr"); got == http.StatusOK {
		t.Fatalf("a manager edited a contract: %d", got)
	}

	// The bills: written and sent for the room handed over, not for the other.
	if got := e.status(http.MethodPost, "/v1/contracts/"+c2.String()+"/invoices", map[string]any{}, "mgr"); got != http.StatusNotFound {
		t.Fatalf("a bill on another room: %d", got)
	}
	w = e.call(http.MethodPost, "/v1/contracts/"+c1.String()+"/invoices", map[string]any{}, "mgr")
	testenv.ExpectStatus(t, w, http.StatusCreated)
	bill := testenv.DecodeData[invoiceRow](t, w)
	var author uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT created_by_id FROM invoices WHERE id = $1`, bill.ID).Scan(&author); err != nil || author != e.manager {
		t.Fatalf("the manager is the author: %v %v", author, err)
	}
	testenv.ExpectStatus(t, e.call(http.MethodPost, "/v1/invoices/"+bill.ID.String()+"/send", nil, "mgr"), http.StatusOK)
	// Settling a bill needs payments.review, which was not given.
	if got := e.status(http.MethodPatch, "/v1/invoices/"+bill.ID.String(), map[string]any{"status": "paid"}, "mgr"); got != http.StatusForbidden {
		t.Fatalf("settling without the right: %d", got)
	}
	// A manager removes no bill of the landlord's, and no sent bill.
	landlordBill := e.call(http.MethodPost, "/v1/contracts/"+c1.String()+"/invoices", map[string]any{}, "landlord")
	if landlordBill.Code == http.StatusCreated {
		id := testenv.DecodeData[invoiceRow](t, landlordBill).ID
		if got := e.status(http.MethodDelete, "/v1/invoices/"+id.String(), nil, "mgr"); got != http.StatusForbidden {
			t.Fatalf("deleting the landlord's draft: %d", got)
		}
	}
	// In the tenant mode the hand-over means nothing, and after the room is taken back the manager sees nothing.
	if got := e.status(http.MethodGet, "/v1/invoices/"+bill.ID.String(), nil, "mgr-as-tenant"); got != http.StatusNotFound {
		t.Fatalf("a bill in tenant mode: %d", got)
	}
	list := testenv.DecodeData[[]assignmentRow](t, e.call(http.MethodGet, "/v1/my/room-managers", nil, "landlord"))
	testenv.ExpectStatus(t, e.call(http.MethodDelete, "/v1/my/room-managers/"+list[0].ID.String(), nil, "landlord"), http.StatusNoContent)
	if got := e.status(http.MethodGet, "/v1/invoices/"+bill.ID.String(), nil, "mgr"); got != http.StatusNotFound {
		t.Fatalf("a bill after the hand-over ended: %d", got)
	}
}
