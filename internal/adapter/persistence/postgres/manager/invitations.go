// Package managerpg is the Postgres adapter of the manager context: the invitations to manage rooms, the hand-overs of
// rooms, the landlord's rooms as the manager rules see them, and the manager role of an account.
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

// InvitationRepository reads and writes the invitations to manage rooms.
type InvitationRepository struct{ pool *pgxpool.Pool }

// NewInvitationRepository returns a repository on the pool.
func NewInvitationRepository(pool *pgxpool.Pool) *InvitationRepository {
	return &InvitationRepository{pool: pool}
}

// Insert stores an invitation with its rooms and capabilities and returns its id.
func (r *InvitationRepository) Insert(ctx context.Context, inv domain.Invitation) (uuid.UUID, error) {
	q := postgres.Queries(ctx, r.pool)
	id, err := q.InsertManagerInvitation(ctx, sqlcgen.InsertManagerInvitationParams{
		LandlordID: inv.LandlordID, TokenHash: inv.TokenHash, LinkTokenSealed: inv.Sealed, ExpiresAt: inv.ExpiresAt,
		NotifyLandlord: inv.NotifyLandlord, RecipientID: inv.RecipientID, RecipientEmail: inv.RecipientEmail,
	})
	if err != nil {
		return uuid.Nil, postgres.Translate(err)
	}
	if err := q.InsertManagerInvitationRooms(ctx, sqlcgen.InsertManagerInvitationRoomsParams{InvitationID: id, RoomIds: inv.RoomIDs}); err != nil {
		return uuid.Nil, postgres.Translate(err)
	}
	if len(inv.Capabilities) > 0 {
		if err := q.InsertManagerInvitationCapabilities(ctx, sqlcgen.InsertManagerInvitationCapabilitiesParams{
			InvitationID: id, Capabilities: domain.Strings(inv.Capabilities),
		}); err != nil {
			return uuid.Nil, postgres.Translate(err)
		}
	}
	return id, nil
}

// withDetails fills the rooms and capabilities of the invitations.
type parts struct {
	rooms map[uuid.UUID][]domain.Room
	caps  map[uuid.UUID][]string
}

func (r *InvitationRepository) partsOf(ctx context.Context, ids []uuid.UUID) (parts, error) {
	p := parts{rooms: map[uuid.UUID][]domain.Room{}, caps: map[uuid.UUID][]string{}}
	if len(ids) == 0 {
		return p, nil
	}
	q := postgres.Queries(ctx, r.pool)
	rooms, err := q.ListManagerInvitationRooms(ctx, ids)
	if err != nil {
		return p, postgres.Translate(err)
	}
	for _, row := range rooms {
		p.rooms[row.InvitationID] = append(p.rooms[row.InvitationID], domain.Room{ID: row.RoomID, Name: row.RoomName, BuildingName: row.BuildingName})
	}
	caps, err := q.ListManagerInvitationCapabilities(ctx, ids)
	if err != nil {
		return p, postgres.Translate(err)
	}
	for _, row := range caps {
		p.caps[row.InvitationID] = append(p.caps[row.InvitationID], row.Capability)
	}
	return p, nil
}

func (p parts) invitation(row sqlcgen.ManagerInvitation) domain.Invitation {
	inv := invitationOf(row)
	for _, room := range p.rooms[row.ID] {
		inv.RoomIDs = append(inv.RoomIDs, room.ID)
	}
	inv.Capabilities = domain.KnownCapabilities(p.caps[row.ID])
	return inv
}

func invitationOf(r sqlcgen.ManagerInvitation) domain.Invitation {
	return domain.Invitation{
		ID: r.ID, LandlordID: r.LandlordID, Status: domain.InvitationStatus(r.Status), TokenHash: r.TokenHash, Sealed: r.LinkTokenSealed,
		ExpiresAt: r.ExpiresAt, NotifyLandlord: r.NotifyLandlord, RecipientID: r.RecipientID, RecipientEmail: r.RecipientEmail,
		AnsweredByID: r.AnsweredByID, AnsweredAt: r.AnsweredAt, CreatedAt: r.CreatedAt,
	}
}

func (r *InvitationRepository) detailsOf(ctx context.Context, rows []sqlcgen.SearchManagerInvitationsRow) ([]domain.InvitationDetail, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ManagerInvitation.ID
	}
	p, err := r.partsOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]domain.InvitationDetail, len(rows))
	for i, row := range rows {
		d := domain.InvitationDetail{Invitation: p.invitation(row.ManagerInvitation), Rooms: p.rooms[row.ManagerInvitation.ID]}
		if d.RecipientID != nil && row.RecipientFullName != nil {
			d.Recipient = &domain.Person{ID: *d.RecipientID, FullName: *row.RecipientFullName, Email: row.RecipientAccountEmail}
		}
		out[i] = d
	}
	return out, nil
}

// Get returns one of the landlord's invitations, or shared.ErrNotFound.
func (r *InvitationRepository) Get(ctx context.Context, landlordID, id uuid.UUID) (domain.InvitationDetail, error) {
	rows, err := postgres.Queries(ctx, r.pool).SearchManagerInvitations(ctx, sqlcgen.SearchManagerInvitationsParams{
		LandlordID: landlordID, ID: &id, PageLimit: 1,
	})
	if err != nil {
		return domain.InvitationDetail{}, postgres.Translate(err)
	}
	if len(rows) == 0 {
		return domain.InvitationDetail{}, shared.ErrNotFound
	}
	d, err := r.detailsOf(ctx, rows)
	if err != nil {
		return domain.InvitationDetail{}, err
	}
	return d[0], nil
}

// List returns a page of the landlord's invitations, open ones first, and how many match in all.
func (r *InvitationRepository) List(ctx context.Context, landlordID uuid.UUID, status *domain.InvitationStatus, limit, offset int) ([]domain.InvitationDetail, int, error) {
	q := postgres.Queries(ctx, r.pool)
	var st *string
	if status != nil {
		s := string(*status)
		st = &s
	}
	rows, err := q.SearchManagerInvitations(ctx, sqlcgen.SearchManagerInvitationsParams{
		LandlordID: landlordID, Status: st, PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountManagerInvitations(ctx, sqlcgen.CountManagerInvitationsParams{LandlordID: landlordID, Status: st})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out, err := r.detailsOf(ctx, rows)
	return out, int(total), err
}

// ForUpdate locks the invitation until the transaction ends and returns it with its rooms and capabilities.
func (r *InvitationRepository) ForUpdate(ctx context.Context, id uuid.UUID) (domain.Invitation, error) {
	row, err := postgres.Queries(ctx, r.pool).LockManagerInvitation(ctx, id)
	if err != nil {
		return domain.Invitation{}, postgres.Translate(err)
	}
	return r.full(ctx, row)
}

func (r *InvitationRepository) full(ctx context.Context, row sqlcgen.ManagerInvitation) (domain.Invitation, error) {
	p, err := r.partsOf(ctx, []uuid.UUID{row.ID})
	if err != nil {
		return domain.Invitation{}, err
	}
	return p.invitation(row), nil
}

// Save writes the fields an invitation's life changes.
func (r *InvitationRepository) Save(ctx context.Context, inv domain.Invitation) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).SaveManagerInvitation(ctx, sqlcgen.SaveManagerInvitationParams{
		ID: inv.ID, Status: string(inv.Status), TokenHash: inv.TokenHash, LinkTokenSealed: inv.Sealed, ExpiresAt: inv.ExpiresAt,
		AnsweredByID: inv.AnsweredByID, AnsweredAt: inv.AnsweredAt,
	}))
}

// FindByTokenHash returns the invitation a link opens, whatever its status.
func (r *InvitationRepository) FindByTokenHash(ctx context.Context, hash string) (domain.Invitation, error) {
	row, err := postgres.Queries(ctx, r.pool).FindManagerInvitationByTokenHash(ctx, hash)
	if err != nil {
		return domain.Invitation{}, postgres.Translate(err)
	}
	return r.full(ctx, row)
}

// FindForRecipient returns the invitation with that id when it is addressed to the account.
func (r *InvitationRepository) FindForRecipient(ctx context.Context, id, recipientID uuid.UUID) (domain.Invitation, error) {
	row, err := postgres.Queries(ctx, r.pool).FindManagerInvitationForRecipient(ctx, sqlcgen.FindManagerInvitationForRecipientParams{
		ID: id, RecipientID: &recipientID,
	})
	if err != nil {
		return domain.Invitation{}, postgres.Translate(err)
	}
	return r.full(ctx, row)
}

// Offer returns an invitation as the invited person sees it.
func (r *InvitationRepository) Offer(ctx context.Context, id uuid.UUID) (domain.InvitationOffer, error) {
	q := postgres.Queries(ctx, r.pool)
	row, err := q.GetManagerInvitationByID(ctx, id)
	if err != nil {
		return domain.InvitationOffer{}, postgres.Translate(err)
	}
	name, err := q.GetManagerInvitationLandlordName(ctx, id)
	if err != nil {
		return domain.InvitationOffer{}, postgres.Translate(err)
	}
	p, err := r.partsOf(ctx, []uuid.UUID{id})
	if err != nil {
		return domain.InvitationOffer{}, err
	}
	return domain.InvitationOffer{Invitation: p.invitation(row), Rooms: p.rooms[id], LandlordName: name}, nil
}

// Inbox returns a page of the open invitations addressed to the account, newest first, and how many there are.
func (r *InvitationRepository) Inbox(ctx context.Context, recipientID uuid.UUID, now time.Time, limit, offset int) ([]domain.InvitationOffer, int, error) {
	q := postgres.Queries(ctx, r.pool)
	rows, err := q.ListManagerInvitationInbox(ctx, sqlcgen.ListManagerInvitationInboxParams{
		RecipientID: &recipientID, Now: now, PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountManagerInvitationInbox(ctx, sqlcgen.CountManagerInvitationInboxParams{RecipientID: &recipientID, Now: now})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ManagerInvitation.ID
	}
	p, err := r.partsOf(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	out := make([]domain.InvitationOffer, len(rows))
	for i, row := range rows {
		out[i] = domain.InvitationOffer{Invitation: p.invitation(row.ManagerInvitation), Rooms: p.rooms[row.ManagerInvitation.ID], LandlordName: row.LandlordName}
	}
	return out, int(total), nil
}

// LockOpenOfRooms locks the waiting (unexpired) invitations that hold any of the rooms and returns them with their rooms.
func (r *InvitationRepository) LockOpenOfRooms(ctx context.Context, roomIDs []uuid.UUID, now time.Time) ([]domain.Invitation, error) {
	rows, err := postgres.Queries(ctx, r.pool).LockOpenManagerInvitationsOfRooms(ctx, sqlcgen.LockOpenManagerInvitationsOfRoomsParams{Now: now, RoomIds: roomIDs})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	return r.fullAll(ctx, rows)
}

func (r *InvitationRepository) fullAll(ctx context.Context, rows []sqlcgen.ManagerInvitation) ([]domain.Invitation, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	p, err := r.partsOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Invitation, len(rows))
	for i, row := range rows {
		out[i] = p.invitation(row)
	}
	return out, nil
}

// RemoveRooms takes the rooms out of the invitation.
func (r *InvitationRepository) RemoveRooms(ctx context.Context, invitationID uuid.UUID, roomIDs []uuid.UUID) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).DeleteManagerInvitationRooms(ctx, sqlcgen.DeleteManagerInvitationRoomsParams{
		InvitationID: invitationID, RoomIds: roomIDs,
	}))
}

// RevokeOpenOfRooms withdraws the waiting invitations of rooms that are gone and returns them.
func (r *InvitationRepository) RevokeOpenOfRooms(ctx context.Context, roomIDs []uuid.UUID, at time.Time) ([]domain.Invitation, error) {
	rows, err := postgres.Queries(ctx, r.pool).RevokeOpenManagerInvitationsOfRooms(ctx, sqlcgen.RevokeOpenManagerInvitationsOfRoomsParams{At: &at, RoomIds: roomIDs})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	return r.fullAll(ctx, rows)
}
