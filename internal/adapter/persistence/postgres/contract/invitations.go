package contractpg

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/shared"
)

// InvitationRepository reads and writes the offers of rooms by link.
type InvitationRepository struct{ pool *pgxpool.Pool }

// NewInvitationRepository returns a repository on the pool.
func NewInvitationRepository(pool *pgxpool.Pool) *InvitationRepository {
	return &InvitationRepository{pool: pool}
}

// Insert stores an invitation and returns its id.
func (r *InvitationRepository) Insert(ctx context.Context, inv domain.Invitation) (uuid.UUID, error) {
	id, err := postgres.Queries(ctx, r.pool).InsertContractInvitation(ctx, sqlcgen.InsertContractInvitationParams{
		RoomID: inv.RoomID, LandlordID: inv.LandlordID, CreatedByID: inv.CreatedByID, Status: string(inv.Status),
		TokenHash: inv.TokenHash, LinkTokenSealed: inv.Sealed, ExpiresAt: inv.ExpiresAt, PriceVnd: inv.Terms.PriceVND,
		DepositVnd: inv.Terms.DepositVND, OccupantCount: int16(inv.Terms.OccupantCount), //nolint:gosec // validated to 1..50
		StartsOn: inv.Terms.StartsOn, EndsOn: inv.Terms.EndsOn, ContractStatus: string(inv.Terms.ContractStatus),
		Note: inv.Terms.Note, RecipientID: inv.RecipientID, RecipientEmail: inv.RecipientEmail,
	})
	return id, postgres.Translate(err)
}

// Get returns one of the landlord's invitations, or shared.ErrNotFound.
func (r *InvitationRepository) Get(ctx context.Context, landlordID, id uuid.UUID) (domain.InvitationDetail, error) {
	rows, err := postgres.Queries(ctx, r.pool).SearchContractInvitations(ctx, sqlcgen.SearchContractInvitationsParams{
		LandlordID: &landlordID, ID: &id, PageLimit: 1,
	})
	if err != nil {
		return domain.InvitationDetail{}, postgres.Translate(err)
	}
	if len(rows) == 0 {
		return domain.InvitationDetail{}, shared.ErrNotFound
	}
	return invitationDetailOf(rows[0]), nil
}

// List returns a page of the landlord's invitations, open ones first, and how many match in all.
func (r *InvitationRepository) List(ctx context.Context, landlordID uuid.UUID, status *domain.InvitationStatus, limit, offset int) ([]domain.InvitationDetail, int, error) {
	q := postgres.Queries(ctx, r.pool)
	var st *string
	if status != nil {
		s := string(*status)
		st = &s
	}
	rows, err := q.SearchContractInvitations(ctx, sqlcgen.SearchContractInvitationsParams{
		LandlordID: &landlordID, Status: st, PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountContractInvitations(ctx, sqlcgen.CountContractInvitationsParams{LandlordID: &landlordID, Status: st})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.InvitationDetail, len(rows))
	for i, row := range rows {
		out[i] = invitationDetailOf(row)
	}
	return out, int(total), nil
}

// ForUpdate locks the invitation until the transaction ends.
func (r *InvitationRepository) ForUpdate(ctx context.Context, id uuid.UUID) (domain.Invitation, error) {
	row, err := postgres.Queries(ctx, r.pool).LockContractInvitation(ctx, id)
	if err != nil {
		return domain.Invitation{}, postgres.Translate(err)
	}
	return invitationOf(row), nil
}

// Save writes the fields an invitation's life changes.
func (r *InvitationRepository) Save(ctx context.Context, inv domain.Invitation) error {
	return postgres.Translate(postgres.Queries(ctx, r.pool).SaveContractInvitation(ctx, sqlcgen.SaveContractInvitationParams{
		ID: inv.ID, Status: string(inv.Status), TokenHash: inv.TokenHash, LinkTokenSealed: inv.Sealed, ExpiresAt: inv.ExpiresAt,
		AnsweredByID: inv.AnsweredByID, AnsweredAt: inv.AnsweredAt, ContractID: inv.ContractID,
		ApprovedByID: inv.ApprovedByID, ApprovedAt: inv.ApprovedAt, ApprovalNote: inv.ApprovalNote,
	}))
}

// FindByTokenHash returns the invitation a link opens, whatever its status.
func (r *InvitationRepository) FindByTokenHash(ctx context.Context, hash string) (domain.Invitation, error) {
	row, err := postgres.Queries(ctx, r.pool).FindContractInvitationByTokenHash(ctx, hash)
	if err != nil {
		return domain.Invitation{}, postgres.Translate(err)
	}
	return invitationOf(row), nil
}

// FindForRecipient returns the invitation with that id when it is addressed to the account.
func (r *InvitationRepository) FindForRecipient(ctx context.Context, id, recipientID uuid.UUID) (domain.Invitation, error) {
	row, err := postgres.Queries(ctx, r.pool).FindContractInvitationForRecipient(ctx, sqlcgen.FindContractInvitationForRecipientParams{
		ID: id, RecipientID: &recipientID,
	})
	if err != nil {
		return domain.Invitation{}, postgres.Translate(err)
	}
	return invitationOf(row), nil
}

// Offer returns an invitation as the invited person sees it.
func (r *InvitationRepository) Offer(ctx context.Context, id uuid.UUID) (domain.InvitationOffer, error) {
	row, err := postgres.Queries(ctx, r.pool).GetContractInvitationOffer(ctx, id)
	if err != nil {
		return domain.InvitationOffer{}, postgres.Translate(err)
	}
	return invitationOfferOf(offerRow(row)), nil
}

// Inbox returns a page of the open invitations addressed to the account, newest first, and how many there are.
func (r *InvitationRepository) Inbox(ctx context.Context, recipientID uuid.UUID, now time.Time, limit, offset int) ([]domain.InvitationOffer, int, error) {
	q := postgres.Queries(ctx, r.pool)
	rows, err := q.ListContractInvitationInbox(ctx, sqlcgen.ListContractInvitationInboxParams{
		RecipientID: &recipientID, Now: now, PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountContractInvitationInbox(ctx, sqlcgen.CountContractInvitationInboxParams{RecipientID: &recipientID, Now: now})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.InvitationOffer, len(rows))
	for i, row := range rows {
		out[i] = invitationOfferOf(offerRow(row))
	}
	return out, int(total), nil
}

// RevokeOpenOfRooms withdraws the waiting invitations of the rooms and returns them.
func (r *InvitationRepository) RevokeOpenOfRooms(ctx context.Context, roomIDs []uuid.UUID, at time.Time) ([]domain.Invitation, error) {
	rows, err := postgres.Queries(ctx, r.pool).RevokeOpenContractInvitationsOfRooms(ctx, sqlcgen.RevokeOpenContractInvitationsOfRoomsParams{
		RoomIds: roomIDs, At: &at,
	})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.Invitation, len(rows))
	for i, row := range rows {
		out[i] = invitationOf(row)
	}
	return out, nil
}

// GetAuthored returns an offer the manager wrote, while they still manage its room, or shared.ErrNotFound.
func (r *InvitationRepository) GetAuthored(ctx context.Context, authorID, id uuid.UUID) (domain.InvitationDetail, error) {
	rows, err := postgres.Queries(ctx, r.pool).SearchContractInvitations(ctx, sqlcgen.SearchContractInvitationsParams{
		AuthorID: &authorID, ID: &id, PageLimit: 1,
	})
	if err != nil {
		return domain.InvitationDetail{}, postgres.Translate(err)
	}
	if len(rows) == 0 {
		return domain.InvitationDetail{}, shared.ErrNotFound
	}
	return invitationDetailOf(rows[0]), nil
}

// ListAuthored returns a page of the offers the manager wrote (open ones first) and how many match in all.
func (r *InvitationRepository) ListAuthored(ctx context.Context, authorID uuid.UUID, status *domain.InvitationStatus, limit, offset int) ([]domain.InvitationDetail, int, error) {
	q := postgres.Queries(ctx, r.pool)
	var st *string
	if status != nil {
		s := string(*status)
		st = &s
	}
	rows, err := q.SearchContractInvitations(ctx, sqlcgen.SearchContractInvitationsParams{
		AuthorID: &authorID, Status: st, PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	total, err := q.CountContractInvitations(ctx, sqlcgen.CountContractInvitationsParams{AuthorID: &authorID, Status: st})
	if err != nil {
		return nil, 0, postgres.Translate(err)
	}
	out := make([]domain.InvitationDetail, len(rows))
	for i, row := range rows {
		out[i] = invitationDetailOf(row)
	}
	return out, int(total), nil
}

// RevokeAwaitingOfAuthor withdraws the offers the manager wrote that still wait for the landlord and returns them.
func (r *InvitationRepository) RevokeAwaitingOfAuthor(ctx context.Context, authorID uuid.UUID, at time.Time) ([]domain.Invitation, error) {
	rows, err := postgres.Queries(ctx, r.pool).RevokeAwaitingContractInvitationsOfAuthor(ctx, sqlcgen.RevokeAwaitingContractInvitationsOfAuthorParams{
		AuthorID: &authorID, At: &at,
	})
	if err != nil {
		return nil, postgres.Translate(err)
	}
	out := make([]domain.Invitation, len(rows))
	for i, row := range rows {
		out[i] = invitationOf(row)
	}
	return out, nil
}
