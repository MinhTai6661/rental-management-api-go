package adminpg

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/adapter/persistence/postgres"
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/admin"
)

// PersonalDataRepository reads and erases the data of one person.
type PersonalDataRepository struct{ pool *pgxpool.Pool }

// NewPersonalDataRepository returns a repository on the pool.
func NewPersonalDataRepository(pool *pgxpool.Pool) *PersonalDataRepository {
	return &PersonalDataRepository{pool: pool}
}

// Facts tells whether a running or waiting contract keeps the account from being erased.
func (r *PersonalDataRepository) Facts(ctx context.Context, userID uuid.UUID) (domain.ErasureFacts, error) {
	v, err := postgres.Queries(ctx, r.pool).AdminErasureBlockers(ctx, &userID)
	return domain.ErasureFacts{AsTenant: v.AsTenant, AsLandlord: v.AsLandlord}, postgres.Translate(err)
}

// Export gathers a copy of what is kept about the person.
func (r *PersonalDataRepository) Export(ctx context.Context, userID uuid.UUID) (domain.Export, error) {
	q := postgres.Queries(ctx, r.pool)
	acc, err := q.AdminExportAccount(ctx, userID)
	if err != nil {
		return domain.Export{}, postgres.Translate(err)
	}
	out := domain.Export{Account: domain.ExportAccount{
		ID: acc.ID.String(), Email: acc.Email, FullName: acc.FullName, PhoneNumber: acc.PhoneNumber, Status: acc.Status,
		AddressDetail: acc.AddressDetail, WardCode: acc.WardCode, Roles: acc.Roles, TermsVersion: acc.TermsVersion,
		TermsAcceptedAt: acc.TermsAcceptedAt, AcceptsInvitations: acc.AcceptsInvitations, CreatedAt: acc.CreatedAt,
	}}
	if p, err := NewProfileRepository(r.pool).Get(ctx, userID); err == nil {
		out.Landlord = &p
	}
	rooms, err := q.AdminExportRooms(ctx, userID)
	if err != nil {
		return domain.Export{}, postgres.Translate(err)
	}
	for _, v := range rooms {
		out.Rooms = append(out.Rooms, domain.ExportRoom{ID: v.ID.String(), Name: v.Name, Address: v.AddressDetail, PriceVND: v.PriceVnd, Status: v.Status})
	}
	contracts, err := q.AdminExportRenterContracts(ctx, &userID)
	if err != nil {
		return domain.Export{}, postgres.Translate(err)
	}
	for _, v := range contracts {
		out.Contracts = append(out.Contracts, domain.ExportContract{
			ID: v.ID.String(), RoomName: v.RoomName, Status: v.Status, PriceVND: v.PriceVnd, StartsOn: v.StartsOn, EndsOn: v.EndsOn,
		})
	}
	payments, err := q.AdminExportPayments(ctx, &userID)
	if err != nil {
		return domain.Export{}, postgres.Translate(err)
	}
	for _, v := range payments {
		out.Payments = append(out.Payments, domain.ExportPayment{
			ID: v.ID.String(), InvoiceID: v.InvoiceID.String(), Method: v.Method, Status: v.Status, AmountVND: v.AmountVnd,
			Note: v.Note, PaidAt: v.PaidAt, CreatedAt: v.CreatedAt,
		})
	}
	return out, nil
}

// Erase deletes or blanks everything personal in the transaction of ctx: open invitations and subscriptions are ended,
// payment methods, applications, profile, notifications and roles are deleted, and the account is blanked and
// removed. Contracts, bills and payments stay, without a name.
func (r *PersonalDataRepository) Erase(ctx context.Context, userID uuid.UUID, now time.Time) error {
	q := postgres.Queries(ctx, r.pool)
	steps := []func() error{
		func() error {
			return q.AdminRevokeOpenInvitationsOfLandlord(ctx, sqlcgen.AdminRevokeOpenInvitationsOfLandlordParams{UserID: userID, Now: now})
		},
		func() error {
			return q.AdminCancelOpenSubscriptions(ctx, sqlcgen.AdminCancelOpenSubscriptionsParams{UserID: userID, Now: now})
		},
		func() error { return q.AdminDeleteLandlordPaymentMethods(ctx, userID) },
		func() error { return q.AdminDeleteLandlordApplicationsOfUser(ctx, userID) },
		func() error { return q.AdminDeleteLandlordProfile(ctx, userID) },
		func() error { return q.AdminDeleteNotificationsOfUser(ctx, userID) },
		func() error { return q.AdminDeleteUserRoles(ctx, userID) },
		func() error {
			return q.AdminAnonymizeUser(ctx, sqlcgen.AdminAnonymizeUserParams{ID: userID, DisplayName: domain.ErasedName, Now: now})
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return postgres.Translate(err)
		}
	}
	return nil
}
