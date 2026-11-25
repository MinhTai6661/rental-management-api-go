package managerpg

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/manager"
	"rental-management/internal/domain/shared"
)

// AssignmentRepository reads and writes the hand-overs of rooms.
type AssignmentRepository struct{ pool *pgxpool.Pool }

// NewAssignmentRepository returns a repository on the pool.
func NewAssignmentRepository(pool *pgxpool.Pool) *AssignmentRepository {
	return &AssignmentRepository{pool: pool}
}

func assignmentOf(r sqlcgen.RoomManager, caps []string) domain.Assignment {
	return domain.Assignment{
		ID: r.ID, LandlordID: r.LandlordID, RoomID: r.RoomID, ManagerID: r.ManagerID, Status: domain.AssignmentStatus(r.Status),
		NotifyLandlord: r.NotifyLandlord, Capabilities: domain.KnownCapabilities(caps), AssignedByID: r.AssignedByID,
		AcceptedAt: r.AcceptedAt, RevokedAt: r.RevokedAt, CreatedAt: r.CreatedAt,
	}
}

// capsOf reads the capabilities of the hand-overs, by hand-over id.
func (r *AssignmentRepository) capsOf(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]string, error) {
	out := map[uuid.UUID][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := postgres.Queries(ctx, r.pool).ListRoomManagerCapabilities(ctx, ids)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	for _, row := range rows {
		out[row.RoomManagerID] = append(out[row.RoomManagerID], row.Capability)
	}
	return out, nil
}

// Insert makes one active hand-over per room for the manager and returns them.
func (r *AssignmentRepository) Insert(ctx context.Context, landlordID, managerID uuid.UUID, roomIDs []uuid.UUID, notify bool, caps []domain.Capability, at time.Time) ([]domain.Assignment, error) {
	q := postgres.Queries(ctx, r.pool)
	rows, err := q.InsertRoomManagers(ctx, sqlcgen.InsertRoomManagersParams{
		LandlordID: landlordID, RoomIds: roomIDs, ManagerID: managerID, NotifyLandlord: notify, AcceptedAt: at,
	})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	ids := make([]uuid.UUID, len(rows))
	out := make([]domain.Assignment, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
		out[i] = domain.Assignment{
			ID: row.ID, LandlordID: landlordID, RoomID: row.RoomID, ManagerID: managerID, Status: domain.AssignmentActive,
			NotifyLandlord: notify, Capabilities: domain.Normalize(caps), AssignedByID: landlordID, AcceptedAt: at, CreatedAt: at,
		}
	}
	if len(caps) > 0 && len(ids) > 0 {
		if err := q.InsertRoomManagerCapabilities(ctx, sqlcgen.InsertRoomManagerCapabilitiesParams{
			AssignmentIds: ids, Capabilities: domain.Strings(caps),
		}); err != nil {
			return nil, postgres.Translate(err)
		}
	}
	return out, nil
}

// ManagedRooms returns the rooms among roomIDs that already have an active manager.
func (r *AssignmentRepository) ManagedRooms(ctx context.Context, roomIDs []uuid.UUID) ([]domain.RoomInfo, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListActiveManagedRoomNames(ctx, roomIDs)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.RoomInfo, len(rows))
	for i, row := range rows {
		out[i] = domain.RoomInfo{ID: row.RoomID, Name: row.RoomName}
	}
	return out, nil
}

// ForUpdate locks the landlord's active hand-overs among the ids and returns them with their capabilities.
func (r *AssignmentRepository) ForUpdate(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) ([]domain.Assignment, error) {
	rows, err := postgres.Queries(ctx, r.pool).LockActiveRoomManagers(ctx, sqlcgen.LockActiveRoomManagersParams{LandlordID: landlordID, Ids: ids})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	found := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		found[i] = row.ID
	}
	caps, err := r.capsOf(ctx, found)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Assignment, len(rows))
	for i, row := range rows {
		out[i] = assignmentOf(row, caps[row.ID])
	}
	return out, nil
}

// Get returns the landlord's active hand-over with the names around it.
func (r *AssignmentRepository) Get(ctx context.Context, landlordID, id uuid.UUID) (domain.AssignmentDetail, error) {
	rows, err := postgres.Queries(ctx, r.pool).SearchRoomManagers(ctx, sqlcgen.SearchRoomManagersParams{LandlordID: landlordID, ID: &id, PageLimit: 1})
	if err != nil {
		return domain.AssignmentDetail{}, postgres.Translate(err)
	}
	if len(rows) == 0 {
		return domain.AssignmentDetail{}, shared.ErrNotFound
	}
	out, err := r.detailsOf(ctx, rows)
	if err != nil {
		return domain.AssignmentDetail{}, err
	}
	return out[0], nil
}

// Save writes the notify switch and replaces the capabilities.
func (r *AssignmentRepository) Save(ctx context.Context, a domain.Assignment) error {
	q := postgres.Queries(ctx, r.pool)
	if err := q.SaveRoomManager(ctx, sqlcgen.SaveRoomManagerParams{ID: a.ID, NotifyLandlord: a.NotifyLandlord}); err != nil {
		return postgres.Translate(err)
	}
	if err := q.DeleteRoomManagerCapabilities(ctx, []uuid.UUID{a.ID}); err != nil {
		return postgres.Translate(err)
	}
	if len(a.Capabilities) == 0 {
		return nil
	}
	return postgres.Translate(q.InsertRoomManagerCapabilities(ctx, sqlcgen.InsertRoomManagerCapabilitiesParams{
		AssignmentIds: []uuid.UUID{a.ID}, Capabilities: domain.Strings(a.Capabilities),
	}))
}

// Revoke ends the hand-overs.
func (r *AssignmentRepository) Revoke(ctx context.Context, ids []uuid.UUID, at time.Time) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).RevokeRoomManagers(ctx, sqlcgen.RevokeRoomManagersParams{Ids: ids, At: &at}))
}

func (r *AssignmentRepository) detailsOf(ctx context.Context, rows []sqlcgen.SearchRoomManagersRow) ([]domain.AssignmentDetail, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.RoomManager.ID
	}
	caps, err := r.capsOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]domain.AssignmentDetail, len(rows))
	for i, row := range rows {
		out[i] = domain.AssignmentDetail{
			Assignment: assignmentOf(row.RoomManager, caps[row.RoomManager.ID]),
			Room:       domain.Room{ID: row.RoomManager.RoomID, Name: row.RoomName, BuildingName: row.BuildingName},
			Manager:    domain.Person{ID: row.RoomManager.ManagerID, FullName: row.ManagerFullName, Email: row.ManagerEmail},
		}
	}
	return out, nil
}

// List returns a page of the landlord's active hand-overs, newest first, and how many there are.
func (r *AssignmentRepository) List(ctx context.Context, landlordID uuid.UUID, managerID *uuid.UUID, limit, offset int) ([]domain.AssignmentDetail, int, error) {
	q := postgres.Queries(ctx, r.pool)
	rows, err := q.SearchRoomManagers(ctx, sqlcgen.SearchRoomManagersParams{
		LandlordID: landlordID, ManagerID: managerID, PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountRoomManagers(ctx, sqlcgen.CountRoomManagersParams{LandlordID: landlordID, ManagerID: managerID})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out, err := r.detailsOf(ctx, rows)
	return out, int(total), err
}

// Groups returns a page of the landlord's managers with the sum of their hand-overs.
func (r *AssignmentRepository) Groups(ctx context.Context, landlordID uuid.UUID, limit, offset int) ([]domain.Group, int, error) {
	q := postgres.Queries(ctx, r.pool)
	rows, err := q.ListRoomManagerGroups(ctx, sqlcgen.ListRoomManagerGroupsParams{
		LandlordID: landlordID, PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountRoomManagerGroups(ctx, landlordID)
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	managerIDs := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		managerIDs[i] = row.ManagerID
	}
	caps := map[uuid.UUID]map[domain.Capability]int{}
	if len(managerIDs) > 0 {
		counts, err := q.ListRoomManagerGroupCapabilities(ctx, sqlcgen.ListRoomManagerGroupCapabilitiesParams{LandlordID: landlordID, ManagerIds: managerIDs})
		if err != nil {
			return nil, 0, postgres.Translate(err)
		}
		for _, c := range counts {
			code, perr := domain.ParseCapability(c.Capability)
			if perr != nil {
				continue // a code this version does not know
			}
			if caps[c.ManagerID] == nil {
				caps[c.ManagerID] = map[domain.Capability]int{}
			}
			caps[c.ManagerID][code] = int(c.Held)
		}
	}
	out := make([]domain.Group, len(rows))
	for i, row := range rows {
		out[i] = domain.Group{
			Manager:   domain.Person{ID: row.ManagerID, FullName: row.ManagerFullName, Email: row.ManagerEmail},
			RoomCount: int(row.RoomCount), AssignmentIDs: row.AssignmentIds, RoomIDs: row.RoomIds, NotifyCount: int(row.NotifyCount),
			Capabilities: caps[row.ManagerID], LatestAt: row.LatestAt,
		}
	}
	return out, int(total), nil
}

// BusyRoomIDs lists every room of the landlord that has a manager.
func (r *AssignmentRepository) BusyRoomIDs(ctx context.Context, landlordID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := postgres.Queries(ctx, r.pool).ListBusyRoomIDs(ctx, landlordID)
	return ids, postgres.Translate(err)
}

// ManagedList returns a page of the rooms handed to the manager, newest first, and how many there are.
func (r *AssignmentRepository) ManagedList(ctx context.Context, managerID uuid.UUID, landlordID *uuid.UUID, limit, offset int) ([]domain.AssignmentDetail, int, error) {
	q := postgres.Queries(ctx, r.pool)
	rows, err := q.SearchManagedRooms(ctx, sqlcgen.SearchManagedRoomsParams{
		ManagerID: managerID, LandlordID: landlordID, PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountManagedRooms(ctx, sqlcgen.CountManagedRoomsParams{ManagerID: managerID, LandlordID: landlordID})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.RoomManager.ID
	}
	caps, err := r.capsOf(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	out := make([]domain.AssignmentDetail, len(rows))
	for i, row := range rows {
		out[i] = domain.AssignmentDetail{
			Assignment:   assignmentOf(row.RoomManager, caps[row.RoomManager.ID]),
			Room:         domain.Room{ID: row.RoomManager.RoomID, Name: row.RoomName, BuildingName: row.BuildingName},
			LandlordName: landlordLabel(row.LandlordBusinessName, row.LandlordFullName),
		}
	}
	return out, int(total), nil
}

// landlordLabel is how a landlord is named to their managers: the business name, else the person's.
func landlordLabel(business *string, name string) string {
	if business != nil && *business != "" {
		return *business
	}
	return name
}

// ManagedGroups returns a page of the landlords that handed rooms to the manager.
func (r *AssignmentRepository) ManagedGroups(ctx context.Context, managerID uuid.UUID, limit, offset int) ([]domain.ManagedGroup, int, error) {
	q := postgres.Queries(ctx, r.pool)
	rows, err := q.ListManagedGroups(ctx, sqlcgen.ListManagedGroupsParams{
		ManagerID: managerID, PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountManagedGroups(ctx, managerID)
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.ManagedGroup, len(rows))
	for i, row := range rows {
		out[i] = domain.ManagedGroup{
			LandlordID: row.LandlordID, LandlordName: landlordLabel(row.LandlordBusinessName, row.LandlordFullName),
			RoomCount: int(row.RoomCount), RoomIDs: row.RoomIds, LatestAt: row.LatestAt,
		}
	}
	return out, int(total), nil
}

// Of returns the manager's active hand-over of the room, or shared.ErrNotFound.
func (r *AssignmentRepository) Of(ctx context.Context, managerID, roomID uuid.UUID) (domain.Assignment, error) {
	row, err := postgres.Queries(ctx, r.pool).FindActiveRoomManager(ctx, sqlcgen.FindActiveRoomManagerParams{RoomID: roomID, ManagerID: managerID})
	if err != nil {
		return domain.Assignment{}, postgres.Translate(err)
	}
	caps, err := r.capsOf(ctx, []uuid.UUID{row.ID})
	if err != nil {
		return domain.Assignment{}, err
	}
	return assignmentOf(row, caps[row.ID]), nil
}

// CountActive counts the manager's active hand-overs.
func (r *AssignmentRepository) CountActive(ctx context.Context, managerID uuid.UUID) (int, error) {
	n, err := postgres.Queries(ctx, r.pool).CountActiveRoomManagersOf(ctx, managerID)
	return int(n), postgres.Translate(err)
}

// ActiveOf lists the manager's active hand-overs.
func (r *AssignmentRepository) ActiveOf(ctx context.Context, managerID uuid.UUID) ([]domain.Assignment, error) {
	rows, err := postgres.Queries(ctx, r.pool).ListActiveRoomManagersOf(ctx, managerID)
	if err != nil {
		return nil, postgres.Translate(err)
	}
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	caps, err := r.capsOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Assignment, len(rows))
	for i, row := range rows {
		out[i] = assignmentOf(row, caps[row.ID])
	}
	return out, nil
}
