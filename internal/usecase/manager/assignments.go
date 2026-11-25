package manager

import (
	"context"
	"slices"
	"strconv"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/manager"
)

// Assignments is the landlord's side of the hand-overs: list them (flat or grouped by manager), change what a manager
// may do, take rooms back. Only the landlord ends a hand-over; a manager never gives a room up.
type Assignments struct{ *core }

// NewAssignments builds the landlord's hand-over use cases.
func NewAssignments(d Deps, o Options) *Assignments { return &Assignments{newCore(d, o)} }

// MaxBulk is how many hand-overs one bulk action takes.
const MaxBulk = 200

// List returns a page of the landlord's active hand-overs, newest first; a nil managerID is every manager.
func (s *Assignments) List(ctx context.Context, landlordID uuid.UUID, managerID *uuid.UUID, limit, offset int) ([]AssignmentDetail, int, error) {
	return s.Assignments.List(ctx, landlordID, managerID, limit, offset)
}

// Get returns one active hand-over of the landlord, or domain.ErrNotFound.
func (s *Assignments) Get(ctx context.Context, landlordID, id uuid.UUID) (AssignmentDetail, error) {
	d, err := s.Assignments.Get(ctx, landlordID, id)
	return d, notFound(err)
}

// GroupView is one manager's hand-overs summed up: how many rooms and whether all, some or none of them notify the
// landlord and hold each capability.
type GroupView struct {
	Group
	Notify       domain.Flag
	Capabilities map[domain.Capability]domain.Flag
}

// Groups returns a page of the landlord's managers (the most recently given first), each with the sum of their
// hand-overs, and how many managers there are in all. The rooms of one manager are read with List and a managerID.
func (s *Assignments) Groups(ctx context.Context, landlordID uuid.UUID, limit, offset int) ([]GroupView, int, error) {
	groups, total, err := s.Assignments.Groups(ctx, landlordID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	out := make([]GroupView, len(groups))
	for i, g := range groups {
		v := GroupView{Group: g, Notify: domain.FlagOf(g.NotifyCount, g.RoomCount), Capabilities: map[domain.Capability]domain.Flag{}}
		for _, info := range domain.Catalogue() {
			v.Capabilities[info.Code] = domain.FlagOf(g.Capabilities[info.Code], g.RoomCount)
		}
		out[i] = v
	}
	return out, total, nil
}

// BusyRoomIDs lists every room of the landlord that has a manager.
func (s *Assignments) BusyRoomIDs(ctx context.Context, landlordID uuid.UUID) ([]uuid.UUID, error) {
	return s.Assignments.BusyRoomIDs(ctx, landlordID)
}

// UpdateInput changes what a manager may do on rooms: the capabilities to give and to take, and the notify switch
// (nil keeps it).
type UpdateInput struct {
	Grant          []domain.Capability
	Revoke         []domain.Capability
	NotifyLandlord *bool
}

// Update changes one hand-over of the landlord.
func (s *Assignments) Update(ctx context.Context, landlordID, id uuid.UUID, in UpdateInput) (AssignmentDetail, error) {
	err := s.Tx.WithinTx(ctx, func(ctx context.Context) error {
		found, err := s.Assignments.ForUpdate(ctx, landlordID, []uuid.UUID{id})
		if err != nil {
			return err
		}
		if len(found) == 0 {
			return domain.ErrNotFound
		}
		return s.apply(ctx, landlordID, found, in)
	})
	if err != nil {
		return AssignmentDetail{}, err
	}
	return s.Get(ctx, landlordID, id)
}

// BulkResult tells which hand-overs a bulk action did; ids that are not the landlord's active hand-overs are not
// listed (they are skipped, not failures).
type BulkResult struct {
	Done []uuid.UUID
}

// BulkUpdate changes what the manager may do on every one of the hand-overs.
func (s *Assignments) BulkUpdate(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID, in UpdateInput) (BulkResult, error) {
	var res BulkResult
	err := s.Tx.WithinTx(ctx, func(ctx context.Context) error {
		found, err := s.Assignments.ForUpdate(ctx, landlordID, distinct(ids))
		if err != nil {
			return err
		}
		if err := s.apply(ctx, landlordID, found, in); err != nil {
			return err
		}
		for _, a := range found {
			res.Done = append(res.Done, a.ID)
		}
		return nil
	})
	return res, err
}

// apply gives, takes and switches on the locked hand-overs and records each change.
func (s *Assignments) apply(ctx context.Context, landlordID uuid.UUID, found []domain.Assignment, in UpdateInput) error {
	for _, a := range found {
		before, after := a.Update(in.Grant, in.Revoke, in.NotifyLandlord)
		if !before.Changed(after) {
			continue
		}
		if err := s.Assignments.Save(ctx, a); err != nil {
			return err
		}
		changes := map[string]any{}
		if before.NotifyLandlord != after.NotifyLandlord {
			changes["notify_landlord"] = map[string]any{"from": before.NotifyLandlord, "to": after.NotifyLandlord}
		}
		if !slices.Equal(before.Capabilities, after.Capabilities) {
			changes["capabilities"] = map[string]any{"from": before.Capabilities, "to": after.Capabilities}
		}
		manager := a.ManagerID
		if err := s.Audit.Record(ctx, AuditEvent{
			Action: AuditUpdated, ActorID: landlordID, RoomID: a.RoomID, OwnerID: landlordID, TargetUserID: &manager,
			Changes: changes, Meta: map[string]any{"assignment_id": a.ID.String()},
		}); err != nil {
			return err
		}
	}
	return nil
}

// Revoke takes one room back from its manager; domain.ErrNotFound when it is not the landlord's active hand-over.
func (s *Assignments) Revoke(ctx context.Context, landlordID, id uuid.UUID) error {
	res, err := s.BulkRevoke(ctx, landlordID, []uuid.UUID{id})
	if err != nil {
		return err
	}
	if len(res.Done) == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// BulkRevoke takes the rooms back. A manager left with no room loses the manager role, and the offers of rooms they
// wrote that still wait for the landlord are withdrawn. Each manager hears once, with how many rooms and which.
func (s *Assignments) BulkRevoke(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) (BulkResult, error) {
	var (
		res     BulkResult
		revoked []domain.Assignment
	)
	err := s.Tx.WithinTx(ctx, func(ctx context.Context) error {
		found, err := s.Assignments.ForUpdate(ctx, landlordID, distinct(ids))
		if err != nil {
			return err
		}
		if len(found) == 0 {
			return nil
		}
		revoked = found
		doneIDs := make([]uuid.UUID, len(found))
		for i, a := range found {
			doneIDs[i] = a.ID
		}
		if err := s.Assignments.Revoke(ctx, doneIDs, s.Clock.Now()); err != nil {
			return err
		}
		res.Done = doneIDs
		managers := map[uuid.UUID]bool{}
		for _, a := range found {
			manager := a.ManagerID
			managers[manager] = true
			if err := s.Audit.Record(ctx, AuditEvent{
				Action: AuditRevoked, ActorID: landlordID, RoomID: a.RoomID, OwnerID: landlordID, TargetUserID: &manager,
				Meta: map[string]any{"assignment_id": a.ID.String()},
			}); err != nil {
				return err
			}
		}
		for manager := range managers {
			left, err := s.Assignments.CountActive(ctx, manager)
			if err != nil {
				return err
			}
			if left > 0 {
				continue
			}
			if err := s.Roles.Revoke(ctx, manager); err != nil {
				return err
			}
			if err := s.Offers.WithdrawAwaiting(ctx, manager); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil || len(revoked) == 0 {
		return res, err
	}
	s.tellRevoked(ctx, landlordID, revoked)
	return res, nil
}

// tellRevoked sends each manager one message for the rooms taken from them.
func (s *Assignments) tellRevoked(ctx context.Context, landlordID uuid.UUID, revoked []domain.Assignment) {
	roomIDs := make([]uuid.UUID, len(revoked))
	for i, a := range revoked {
		roomIDs[i] = a.RoomID
	}
	rooms, err := s.Rooms.Names(ctx, roomIDs)
	if err != nil {
		s.Log.Warn("name the rooms taken from a manager", "error", err)
	}
	byManager := map[uuid.UUID][]uuid.UUID{}
	var order []uuid.UUID
	for _, a := range revoked {
		if _, seen := byManager[a.ManagerID]; !seen {
			order = append(order, a.ManagerID)
		}
		byManager[a.ManagerID] = append(byManager[a.ManagerID], a.RoomID)
	}
	landlord := s.landlordName(ctx, landlordID)
	for _, m := range order {
		taken := byManager[m]
		s.notify(ctx, m, NotifyRevoked, "", "/profile", map[string]string{
			"landlord": landlord, "count": strconv.Itoa(len(taken)), "rooms": domain.NamesLabel(roomNames(rooms, taken)),
		})
	}
}
