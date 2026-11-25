package main

import (
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/config"
	"rental-management/internal/adapter/persistence/postgres"
	managerpg "rental-management/internal/adapter/persistence/postgres/manager"
	"rental-management/internal/adapter/security"
	contractuc "rental-management/internal/usecase/contract"
	invitationuc "rental-management/internal/usecase/invitation"
	manageruc "rental-management/internal/usecase/manager"
	notificationuc "rental-management/internal/usecase/notification"
	"rental-management/pkg/secretbox"
)

// managers is the use cases of handing rooms over to managers, on Postgres.
type managers struct {
	invitations *manageruc.Invitations
	answers     *manageruc.Answers
	assignments *manageruc.Assignments
	managed     *manageruc.Managed
	access      *manageruc.Access
}

// managerPorts are the collaborators of the manager use cases that come from other contexts; any may be nil (the
// commands that never hand rooms over), which leaves the port at its default.
type managerPorts struct {
	invitees *invitationuc.Invitees
	sealer   *secretbox.Box
	mailer   manageruc.InvitationMailer
	notify   *notificationuc.Deliveries
	offers   *contractuc.ManagerLinks
}

// newManagers builds the manager use cases on Postgres. Who can be invited comes from the invitation use cases, the
// manager role is read and written next to the hand-overs, the offers a manager wrote are withdrawn through the
// contract use cases (the transaction is the caller's), the messages go through the notification use cases and the
// events through the audit trail.
func newManagers(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, p managerPorts) managers {
	deps := manageruc.Deps{
		Tx: postgres.NewTxManager(pool), Invitations: managerpg.NewInvitationRepository(pool),
		Assignments: managerpg.NewAssignmentRepository(pool), Rooms: managerpg.NewRoomReader(pool), Roles: managerpg.NewRoles(pool),
		Tokens: security.ShareTokens{}, Mailer: p.mailer, Audit: managerAudit{r: newAuditRecorder(log, pool)},
		Clock: manageruc.ClockFunc(time.Now), Log: log,
	}
	if p.invitees != nil {
		deps.Invitees = p.invitees
	}
	if p.sealer != nil {
		deps.Sealer = p.sealer
	}
	if p.notify != nil {
		deps.Notifier = managerNotifier{m: p.notify}
	}
	if p.offers != nil {
		deps.Offers = p.offers
	}
	opts := manageruc.Options{InvitationTTL: time.Duration(cfg.InvitationTTLDays) * 24 * time.Hour, WebURL: cfg.WebURL}
	return managers{
		invitations: manageruc.NewInvitations(deps, opts), answers: manageruc.NewAnswers(deps, opts),
		assignments: manageruc.NewAssignments(deps, opts), managed: manageruc.NewManaged(deps, opts), access: manageruc.NewAccess(deps, opts),
	}
}
