// Package contract holds the use cases of rental contracts: a tenant registers for a room, the landlord answers,
// edits, ends or deletes the contract, a tenant asks to end a running one, and the system keeps a "blank" contract for
// a room marked rented that nobody with an account holds. It declares the ports it needs (ports.go) and imports no
// framework, driver or other context: the room status, notifications, audit and plan limits are ports too.
package contract

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/shared"
)

// Deps are the collaborators of the use cases. Nil optional ports get their no-op defaults.
type Deps struct {
	Tx        TxManager
	Contracts ContractRepository
	Rooms     RoomReader
	RoomState RoomStatusSetter
	Notifier  Notifier
	Audit     AuditRecorder
	Plans     PlanLimits
	Clock     Clock
	Log       *slog.Logger

	// The ports of the invitations (the Invitations use cases need them; the others never touch them).
	Invitations InvitationRepository
	Invitees    Invitees
	Tokens      LinkTokens
	// Sealer is optional: without it the links of invitations are not kept and cannot be copied again.
	Sealer Sealer
	// Mailer defaults to NoInvitationMailer.
	Mailer InvitationMailer
}

// Options are the settings of the use cases.
type Options struct {
	// Location is the business time zone: "today" is the calendar date there. Default UTC+7 (Vietnam).
	Location *time.Location
	// InvitationTTL is how long an invitation can be answered. Default 7 days.
	InvitationTTL time.Duration
	// WebURL is the origin of the web app: the links of invitations point there.
	WebURL string
}

// core is what the use case groups share: the ports, the settings and the helpers every group needs.
type core struct {
	tx        TxManager
	contracts ContractRepository
	rooms     RoomReader
	roomState RoomStatusSetter
	notifier  Notifier
	audit     AuditRecorder
	plans     PlanLimits
	clock     Clock
	log       *slog.Logger
	loc       *time.Location

	invitations InvitationRepository
	invitees    Invitees
	tokens      LinkTokens
	sealer      Sealer
	mailer      InvitationMailer
	opts        Options
}

func newCore(d Deps, o Options) *core {
	c := &core{
		tx: d.Tx, contracts: d.Contracts, rooms: d.Rooms, roomState: d.RoomState, notifier: d.Notifier, audit: d.Audit,
		plans: d.Plans, clock: d.Clock, log: d.Log, loc: o.Location,
		invitations: d.Invitations, invitees: d.Invitees, tokens: d.Tokens, sealer: d.Sealer, mailer: d.Mailer, opts: o,
	}
	if c.roomState == nil {
		c.roomState = KeepRoomStatus{}
	}
	if c.notifier == nil {
		c.notifier = NoNotifier{}
	}
	if c.audit == nil {
		c.audit = NoAudit{}
	}
	if c.plans == nil {
		c.plans = UnlimitedPlans{}
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.clock == nil {
		c.clock = ClockFunc(time.Now)
	}
	if c.loc == nil {
		c.loc = time.FixedZone("ICT", 7*60*60)
	}
	if c.mailer == nil {
		c.mailer = NoInvitationMailer{}
	}
	if c.opts.InvitationTTL <= 0 {
		c.opts.InvitationTTL = 7 * 24 * time.Hour
	}
	return c
}

func (c *core) today() time.Time { return domain.Today(c.clock.Now(), c.loc) }

// notFound turns a repository's "no such row" into the domain's not found.
func notFound(err error) error {
	if errors.Is(err, shared.ErrNotFound) {
		return domain.ErrNotFound
	}
	return err
}

// lockRoom locks the room of a contract; a deleted or missing room is domain.ErrNotFound (nothing can be done to the
// contracts of a room that is gone).
func (c *core) lockRoom(ctx context.Context, id uuid.UUID) (domain.RoomFacts, error) {
	room, err := c.rooms.Lock(ctx, id)
	if err != nil {
		return domain.RoomFacts{}, notFound(err)
	}
	if !room.Live {
		return domain.RoomFacts{}, domain.ErrNotFound
	}
	return room, nil
}

// syncRoom keeps the room's status in line with its contracts: rented while one runs, free again when the last one
// is gone. A room under repair stays under repair. room is the locked room as it was when the change began.
func (c *core) syncRoom(ctx context.Context, room domain.RoomFacts) error {
	occ, err := c.contracts.Occupancy(ctx, room.ID)
	if err != nil {
		return err
	}
	status, changed := domain.SyncedRoomStatus(room.Status, occ)
	if !changed {
		return nil
	}
	return c.roomState.SetStatus(ctx, room.LandlordID, room.ID, status)
}

// closeBlank makes room for a contract somebody holds: the blank contracts of the rooms end.
func (c *core) closeBlank(ctx context.Context, roomIDs []uuid.UUID, reason string) error {
	closed, err := c.contracts.CloseBlank(ctx, roomIDs, c.today())
	if err != nil {
		return err
	}
	for _, b := range closed {
		if err := c.audit.Record(ctx, AuditEvent{
			Action: AuditTerminated, ContractID: b.ContractID, RoomID: b.RoomID, OwnerID: b.LandlordID,
			Meta: map[string]any{"reason": reason},
		}); err != nil {
			return err
		}
	}
	return nil
}

// record writes an audit event about a contract.
func (c *core) record(ctx context.Context, action string, actor uuid.UUID, k domain.Contract, meta map[string]any) error {
	return c.audit.Record(ctx, AuditEvent{
		Action: action, ActorID: actor, ContractID: k.ID, RoomID: k.RoomID, OwnerID: k.LandlordID,
		TargetUserID: k.TenantID, Meta: meta,
	})
}

// checkContractLimit refuses a running contract past the landlord's plan limit (the landlord's lock is taken so two
// activations cannot both pass).
func (c *core) checkContractLimit(ctx context.Context, landlordID uuid.UUID) error {
	limit, err := c.plans.MaxActiveContracts(ctx, landlordID)
	if err != nil || limit == nil {
		return err
	}
	if err := c.contracts.LockLandlord(ctx, landlordID); err != nil {
		return err
	}
	count, err := c.contracts.CountRunning(ctx, landlordID)
	if err != nil {
		return err
	}
	if domain.LimitReached(limit, count) {
		return &domain.ContractLimitError{Limit: *limit}
	}
	return nil
}

// notify tells a side of a contract about something. The contract is already stored, so a failure is logged and
// never undoes it. A contract nobody holds has no tenant to tell.
func (c *core) notify(ctx context.Context, d domain.Detail, kind string, toLandlord bool, extra map[string]string) {
	to, url, audience := d.TenantID, "/my-rooms/"+d.ID.String(), AudienceTenant
	if toLandlord {
		to, url, audience = &d.LandlordID, "/contracts/"+d.ID.String(), AudienceLandlord
	}
	if to == nil {
		return
	}
	params := map[string]string{
		"room": d.Room.Name, "tenant": tenantLabel(d), "landlord": d.Landlord.FullName, "contract_id": d.ID.String(),
	}
	for k, v := range extra {
		params[k] = v
	}
	if err := c.notifier.Notify(ctx, Notification{UserID: *to, Kind: kind, Params: params, URL: url, Audience: audience}); err != nil {
		c.log.Warn("notify about a contract", "error", err, "kind", kind, "contract_id", d.ID)
	}
}

// notifyEnded tells both sides that the contract ended.
func (c *core) notifyEnded(ctx context.Context, d domain.Detail, extra map[string]string) {
	c.notify(ctx, d, NotifyContractEnded, false, extra)
	c.notify(ctx, d, NotifyContractEnded, true, extra)
}

// tenantLabel names the tenant of a contract: their account name, else the name the landlord wrote.
func tenantLabel(d domain.Detail) string {
	switch {
	case d.Tenant != nil:
		return d.Tenant.FullName
	case d.TenantName != nil:
		return *d.TenantName
	}
	return ""
}

// BulkFailure is a contract a bulk operation refused; Err is the domain error that says why.
type BulkFailure struct {
	ID   uuid.UUID
	Name string
	Err  error
}

// BulkResult tells which contracts a bulk operation did and which it refused.
type BulkResult struct {
	Done   []uuid.UUID
	Failed []BulkFailure
}

// dateMeta writes an optional date for an audit event.
func dateMeta(d *time.Time) any {
	if d == nil {
		return nil
	}
	return d.Format(domain.DateLayout)
}

func errorsIsNotFound(err error) bool {
	return errors.Is(err, shared.ErrNotFound) || errors.Is(err, domain.ErrNotFound)
}
