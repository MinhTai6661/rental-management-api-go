package listingpg

import (
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/listing"
)

// The mapping between the generated listing rows and the listing domain types.

func listingOf(r sqlcgen.Listing) domain.Listing {
	return domain.Listing{
		ID: r.ID, RoomID: r.RoomID, LandlordID: r.LandlordID, Title: r.Title, Description: r.Description,
		Status: domain.Status(r.Status), SubmittedAt: r.SubmittedAt, ReviewedByID: r.ReviewedByID, ReviewedAt: r.ReviewedAt,
		ReviewNote: r.ReviewNote, PublishedAt: r.PublishedAt, UnpublishedAt: r.UnpublishedAt, CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

func listingsOf(rows []sqlcgen.Listing) []domain.Listing {
	out := make([]domain.Listing, len(rows))
	for i, r := range rows {
		out[i] = listingOf(r)
	}
	return out
}

func detailOf(r sqlcgen.SearchListingsRow) domain.Detail {
	d := domain.Detail{
		Listing: domain.Listing{
			ID: r.ID, RoomID: r.RoomID, LandlordID: r.LandlordID, Title: r.Title, Description: r.Description,
			Status: domain.Status(r.Status), SubmittedAt: r.SubmittedAt, ReviewedByID: r.ReviewedByID, ReviewedAt: r.ReviewedAt,
			ReviewNote: r.ReviewNote, PublishedAt: r.PublishedAt, UnpublishedAt: r.UnpublishedAt, CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		},
		Room: domain.RoomRef{
			ID: r.RoomID, Name: r.RoomName, Status: r.RoomStatus, PriceVND: r.RoomPriceVnd,
			MinPeriod: int32(r.RoomMinimumRentalPeriod), PeriodUnit: r.RoomRentalPeriodUnit,
			AddressDetail: r.RoomAddressDetail, Area: r.RoomAreaSquareMeters, Description: r.RoomDescription,
			Cover: coverOf(r.RoomCoverKey, r.RoomCoverThumbKey), Ward: wardOf(r.WardCode, r.WardName, r.ProvinceCode, r.ProvinceName),
			BuildingID: r.RoomBuildingID, Live: r.RoomLive,
		},
		Landlord: domain.Person{ID: r.LandlordID, FullName: r.LandlordFullName, Email: r.LandlordEmail, PhoneNumber: r.LandlordPhoneNumber},
	}
	if r.ReviewedByID != nil && r.ReviewerFullName != nil {
		d.Reviewer = &domain.Person{ID: *r.ReviewedByID, FullName: *r.ReviewerFullName, Email: r.ReviewerEmail, PhoneNumber: r.ReviewerPhoneNumber}
	}
	return d
}

func coverOf(key, thumb *string) *domain.Cover {
	if key == nil || thumb == nil {
		return nil
	}
	return &domain.Cover{Key: *key, ThumbKey: *thumb}
}

func wardOf(code *int32, name *string, provinceCode *int32, provinceName *string) *domain.WardRef {
	if code == nil || name == nil || provinceCode == nil || provinceName == nil {
		return nil
	}
	return &domain.WardRef{Code: *code, Name: *name, ProvinceCode: *provinceCode, ProvinceName: *provinceName}
}

func offerOf(r sqlcgen.SearchOffersRow) domain.Offer {
	return domain.Offer{
		RoomID: r.RoomID, Name: r.Name, BuildingID: r.BuildingID, HouseName: r.HouseName, Area: r.AreaSquareMeters,
		PriceVND: r.PriceVnd, MinPeriod: int32(r.MinimumRentalPeriod), PeriodUnit: r.RentalPeriodUnit,
		Description: r.Description, AddressDetail: r.AddressDetail,
		Ward: wardOf(r.WardCode, r.WardName, r.ProvinceCode, r.ProvinceName), Cover: coverOf(r.CoverKey, r.CoverThumbKey),
		Listing:  domain.OfferText{ID: r.ListingID, Title: r.ListingTitle, Description: r.ListingDescription},
		Landlord: domain.OfferLandlord{ID: r.LandlordID, FullName: r.LandlordFullName}, CreatedAt: r.CreatedAt,
	}
}
