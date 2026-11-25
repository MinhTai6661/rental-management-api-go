package main

import (
	"context"

	"github.com/google/uuid"

	mailadapter "rental-management/internal/adapter/mail"
	domain "rental-management/internal/domain/audit"
	managerdomain "rental-management/internal/domain/manager"
	audituc "rental-management/internal/usecase/audit"
	contractuc "rental-management/internal/usecase/contract"
	invitationuc "rental-management/internal/usecase/invitation"
	invoiceuc "rental-management/internal/usecase/invoice"
	manageruc "rental-management/internal/usecase/manager"
	"rental-management/pkg/authz"
	"rental-management/pkg/reqctx"
)

// The bridges of the manager context. It imports no other context (the linter enforces it): this file adapts the
// audit trail and the inbox to its ports, and its answer about what a manager may do to the ports of the contexts that
// ask (contracts today).

// managerAudit is the manager context's AuditRecorder.
type managerAudit struct{ r *audituc.Recorder }

// Record writes a manager event. The landlord is the owner, so the entry says a manager acted for them.
func (a managerAudit) Record(ctx context.Context, e manageruc.AuditEvent) error {
	meta := metaWith(e.Meta, "invitation_id", e.InvitationID)
	return a.r.Record(ctx, audituc.Event{
		Action: domain.Action(e.Action), Actor: actorOf(e.ActorID), OwnerID: idOf(e.OwnerID),
		Refs: refsOf(e.RoomID, uuid.Nil, uuid.Nil, uuid.Nil, e.TargetUserID), Changes: changesOrNil(e.Changes), Meta: meta,
	})
}

// changesOrNil keeps an empty change set out of the entry.
func changesOrNil(c map[string]any) any {
	if len(c) == 0 {
		return nil
	}
	return c
}

// managerNotifier is the manager context's Notifier.
type managerNotifier struct{ m messenger }

// Notify sends a manager message to its user.
func (b managerNotifier) Notify(ctx context.Context, n manageruc.Notification) error {
	return b.m.Send(ctx, messageOf(n.UserID, n.Kind, n.Params, n.URL, n.Audience))
}

// managerMailer lets the manager context email an invitation through the mail adapter (which takes plain values).
type managerMailer struct{ m *mailadapter.InvitationMailer }

// SendInvitation implements manageruc.InvitationMailer.
func (b managerMailer) SendInvitation(ctx context.Context, m manageruc.InvitationMail) error {
	return b.m.SendManager(ctx, m.To, m.Landlord, m.Rooms, m.URL, m.ExpiresAt)
}

// actingAsManager says whether the request is made by someone acting in the manager role. What a hand-over allows
// counts only then: an account that is also a landlord or a tenant never leaks manager rights into those modes.
func actingAsManager(ctx context.Context) bool {
	info, ok := reqctx.From(ctx)
	return ok && info.Role == authz.RoleManager
}

// contractManagers tells the contract context what a manager was given on a room. The manager use cases are built after
// the contract ones (they take the offers withdrawer), so access is set once they exist.
type contractManagers struct{ access *manageruc.Access }

// Grant implements contractuc.Managers.
func (m *contractManagers) Grant(ctx context.Context, userID, roomID uuid.UUID) (contractuc.Grant, error) {
	if m.access == nil || !actingAsManager(ctx) {
		return contractuc.Grant{}, nil
	}
	a, err := m.access.Of(ctx, userID, roomID)
	if err != nil || a == nil {
		return contractuc.Grant{}, err
	}
	return contractuc.Grant{Propose: managerdomain.CanProposeOffer(a), Create: a.Can(managerdomain.ContractsCreate)}, nil
}

// managerActing tells the invoice use cases whether the request acts in the manager role.
type managerActing struct{}

// AsManager implements invoiceuc.Acting.
func (managerActing) AsManager(ctx context.Context) bool { return actingAsManager(ctx) }

// invoiceManagers tells the invoice context what a manager was given on rooms.
type invoiceManagers struct {
	access *manageruc.Access
	people *invitationuc.Invitees
}

// Grants implements invoiceuc.Managers.
func (m invoiceManagers) Grants(ctx context.Context, managerID uuid.UUID, roomIDs []uuid.UUID) (map[uuid.UUID]invoiceuc.ManagerGrant, error) {
	out := make(map[uuid.UUID]invoiceuc.ManagerGrant, len(roomIDs))
	name := ""
	for _, room := range roomIDs {
		if _, done := out[room]; done {
			continue
		}
		a, err := m.access.Of(ctx, managerID, room)
		if err != nil {
			return nil, err
		}
		if a == nil {
			continue
		}
		g := invoiceuc.ManagerGrant{Active: true, Settle: a.Can(managerdomain.PaymentsReview), Notify: a.NotifyLandlord}
		if g.Notify {
			if name == "" {
				if p, err := m.people.Person(ctx, managerID); err == nil {
					name = p.FullName
				}
			}
			g.Name = name
		}
		out[room] = g
	}
	return out, nil
}

var (
	_ invoiceuc.Acting           = managerActing{}
	_ invoiceuc.Managers         = invoiceManagers{}
	_ manageruc.AuditRecorder    = managerAudit{}
	_ manageruc.Notifier         = managerNotifier{}
	_ manageruc.InvitationMailer = managerMailer{}
	_ contractuc.Managers        = (*contractManagers)(nil)
)
