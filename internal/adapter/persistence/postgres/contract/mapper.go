package contractpg

import (
	"github.com/google/uuid"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/contract"
)

// The mapping between the generated contract rows and the contract domain types.

func cancellationStatusOf(s *string) *domain.CancellationStatus {
	if s == nil {
		return nil
	}
	v := domain.CancellationStatus(*s)
	return &v
}

func cancellationStatusToDB(s *domain.CancellationStatus) *string {
	if s == nil {
		return nil
	}
	v := string(*s)
	return &v
}

func contractOfLock(r sqlcgen.GetContractForUpdateRow) domain.Contract {
	return domain.Contract{
		ID: r.ID, RoomID: r.RoomID, LandlordID: r.LandlordID, TenantID: r.TenantID, TenantName: r.TenantName,
		TenantPhone: r.TenantPhone, Status: domain.Status(r.Status), PriceVND: r.PriceVnd, DepositVND: r.DepositVnd,
		OccupantCount: int(r.OccupantCount), StartsOn: r.StartsOn, EndsOn: r.EndsOn, Notes: r.Notes,
		ConfirmedAt: r.ConfirmedAt, DisplayOrder: int(r.DisplayOrder),
		Cancellation: domain.Cancellation{
			Status: cancellationStatusOf(r.CancellationStatus), RequestedAt: r.CancellationRequestedAt,
			Reason: r.CancellationReason, ResponseNote: r.CancellationResponseNote,
		},
	}
}

func detailOf(r sqlcgen.SearchContractsRow) domain.Detail {
	d := domain.Detail{
		Contract: domain.Contract{
			ID: r.ID, RoomID: r.RoomID, LandlordID: r.LandlordID, TenantID: r.TenantID, TenantName: r.TenantName,
			TenantPhone: r.TenantPhone, Status: domain.Status(r.Status), PriceVND: r.PriceVnd, DepositVND: r.DepositVnd,
			OccupantCount: int(r.OccupantCount), StartsOn: r.StartsOn, EndsOn: r.EndsOn, Notes: r.Notes,
			ConfirmedAt: r.ConfirmedAt, DisplayOrder: int(r.DisplayOrder),
			Cancellation: domain.Cancellation{
				Status: cancellationStatusOf(r.CancellationStatus), RequestedAt: r.CancellationRequestedAt,
				Reason: r.CancellationReason, ResponseNote: r.CancellationResponseNote,
			},
		},
		Room: domain.RoomRef{
			ID: r.RoomID, BuildingID: r.RoomBuildingID, Name: r.RoomName, Status: r.RoomStatus, PriceVND: r.RoomPriceVnd,
			MinPeriod: int32(r.RoomMinimumRentalPeriod), PeriodUnit: domain.PeriodUnit(r.RoomRentalPeriodUnit),
			AddressDetail: r.RoomAddressDetail, Description: r.RoomDescription, Area: r.RoomAreaSquareMeters,
		},
		Landlord:  domain.Person{ID: r.LandlordID, FullName: r.LandlordFullName, Email: r.LandlordEmail, PhoneNumber: r.LandlordPhoneNumber},
		Billing:   billingOf(r),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.TenantID != nil && r.TenantFullName != nil {
		d.Tenant = &domain.Person{ID: *r.TenantID, FullName: *r.TenantFullName, Email: r.TenantEmail, PhoneNumber: r.TenantPhoneNumber}
	}
	if r.RoomCoverKey != nil && r.RoomCoverThumbKey != nil {
		d.Room.Cover = &domain.Cover{Key: *r.RoomCoverKey, ThumbKey: *r.RoomCoverThumbKey}
	}
	if r.WardCode != nil && r.WardName != nil && r.ProvinceCode != nil && r.ProvinceName != nil {
		d.Room.Ward = &domain.WardRef{Code: *r.WardCode, Name: *r.WardName, Province: domain.ProvinceRef{Code: *r.ProvinceCode, Name: *r.ProvinceName}}
	}
	return d
}

func roomFactsOf(id, landlordID uuid.UUID, name, status string, priceVND int64, minPeriod int16, unit string, deleted, listed bool) domain.RoomFacts {
	return domain.RoomFacts{
		ID: id, LandlordID: landlordID, Name: name, Status: status, PriceVND: priceVND, MinPeriod: int32(minPeriod),
		PeriodUnit: domain.PeriodUnit(unit), Live: !deleted, Listed: listed,
	}
}

func invitationOf(r sqlcgen.ContractInvitation) domain.Invitation {
	return domain.Invitation{
		ID: r.ID, RoomID: r.RoomID, LandlordID: r.LandlordID, CreatedByID: r.CreatedByID, Status: domain.InvitationStatus(r.Status),
		TokenHash: r.TokenHash, Sealed: r.LinkTokenSealed, ExpiresAt: r.ExpiresAt,
		Terms: domain.InvitationTerms{
			PriceVND: r.PriceVnd, DepositVND: r.DepositVnd, OccupantCount: int(r.OccupantCount), StartsOn: r.StartsOn,
			EndsOn: r.EndsOn, ContractStatus: domain.Status(r.ContractStatus), Note: r.Note,
		},
		RecipientID: r.RecipientID, RecipientEmail: r.RecipientEmail, AnsweredByID: r.AnsweredByID, AnsweredAt: r.AnsweredAt,
		ContractID: r.ContractID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func invitationDetailOf(r sqlcgen.SearchContractInvitationsRow) domain.InvitationDetail {
	inv := invitationOf(r.ContractInvitation)
	d := domain.InvitationDetail{Invitation: inv, RoomName: r.RoomName, BuildingName: r.BuildingName, LandlordName: r.LandlordName}
	if inv.RecipientID != nil && r.RecipientFullName != nil {
		d.Recipient = &domain.Person{ID: *inv.RecipientID, FullName: *r.RecipientFullName, Email: r.RecipientAccountEmail}
	}
	if inv.AnsweredByID != nil && r.AnsweredByFullName != nil {
		d.AnsweredBy = &domain.Person{ID: *inv.AnsweredByID, FullName: *r.AnsweredByFullName, Email: r.AnsweredByEmail}
	}
	return d
}

// offerRow is a row of the two queries that read an invitation as the invited person sees it (the one invitation and
// the inbox select the same columns, so one mapper serves both).
type offerRow sqlcgen.GetContractInvitationOfferRow

func invitationOfferOf(r offerRow) domain.InvitationOffer {
	inv := invitationOf(r.ContractInvitation)
	o := domain.InvitationOffer{
		Invitation: inv, HouseName: r.BuildingName, Landlord: domain.Person{ID: inv.LandlordID, FullName: r.LandlordFullName},
		Room: domain.RoomRef{
			ID: inv.RoomID, BuildingID: r.RoomBuildingID, Name: r.RoomName, Status: r.RoomStatus, PriceVND: r.RoomPriceVnd,
			MinPeriod: int32(r.RoomMinimumRentalPeriod), PeriodUnit: domain.PeriodUnit(r.RoomRentalPeriodUnit),
			AddressDetail: r.RoomAddressDetail, Description: r.RoomDescription, Area: r.RoomAreaSquareMeters,
		},
	}
	if r.RoomCoverKey != nil && r.RoomCoverThumbKey != nil {
		o.Room.Cover = &domain.Cover{Key: *r.RoomCoverKey, ThumbKey: *r.RoomCoverThumbKey}
	}
	if r.WardCode != nil && r.WardName != nil && r.ProvinceCode != nil && r.ProvinceName != nil {
		o.Room.Ward = &domain.WardRef{Code: *r.WardCode, Name: *r.WardName, Province: domain.ProvinceRef{Code: *r.ProvinceCode, Name: *r.ProvinceName}}
	}
	return o
}

// billingOf reads the summary of the bills of a contract; the query gives the epoch when no bill is due.
func billingOf(r sqlcgen.SearchContractsRow) domain.Billing {
	b := domain.Billing{
		DueCount: int(r.DueCount), DueAmountVND: r.DueAmountVnd, Partial: r.BillingPartial, ReviewCount: int(r.ReviewCount),
		Overdue: r.BillingOverdue, UnpaidInvoices: int(r.UnpaidInvoices),
	}
	if r.NextDueAt.Unix() > 0 {
		next := r.NextDueAt
		b.NextDueAt = &next
	}
	return b
}
