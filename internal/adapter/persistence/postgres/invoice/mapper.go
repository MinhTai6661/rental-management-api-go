package invoicepg

import (
	"time"

	"github.com/google/uuid"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/invoice"
)

func sourceOf(s *string) *domain.Source {
	if s == nil {
		return nil
	}
	v := domain.Source(*s)
	return &v
}

func sourceToDB(s *domain.Source) *string {
	if s == nil {
		return nil
	}
	v := string(*s)
	return &v
}

// detailOf maps a row of the search (the due list has the same columns) to a bill with its contract; the lines and the
// readings are filled in by the caller.
func detailOf(row sqlcgen.SearchInvoicesRow) domain.Detail {
	inv := domain.Invoice{
		ID: row.ID, ContractID: row.ContractID, CreatedByID: row.CreatedByID, Number: row.InvoiceNumber, Name: row.Name,
		PaymentLabel: row.PaymentLabel, PaymentCode: row.PaymentCode, PeriodStart: row.PeriodStart, PeriodEnd: row.PeriodEnd,
		TotalVND: row.TotalAmountVnd, DueAt: row.DueAt, Status: domain.Status(row.Status), SentAt: row.SentAt,
		SentVia: sourceOf(row.SentVia), SentByID: row.SentByID, PaidAt: row.PaidAt, Note: row.Note,
		Delivery: domain.Delivery(row.Delivery), Share: shareOf(row.ShareTokenHash, row.ShareTokenSealed, row.ShareExpiresAt),
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	d := domain.Detail{
		Invoice: inv,
		Contract: domain.ContractFacts{
			ID: row.ContractID, RoomID: row.RoomID, RoomName: row.RoomName, LandlordID: row.LandlordID,
			LandlordName: row.LandlordFullName, TenantID: row.TenantID, TenantName: row.TenantName, TenantPhone: row.TenantPhone,
			TenantAccountName: row.TenantFullName, TenantEmail: row.TenantEmail, Status: row.ContractStatus,
			PriceVND: row.ContractPriceVnd, OccupantCount: int(row.OccupantCount), StartsOn: row.StartsOn, EndsOn: row.EndsOn,
			BilledByDay: row.RoomRentalPeriodUnit == "day",
		},
	}
	if row.CreatedByID != nil && row.CreatedByName != nil {
		d.CreatedBy = &domain.Person{ID: *row.CreatedByID, Name: *row.CreatedByName}
	}
	return d
}

func shareOf(hash, sealed *string, expires *time.Time) domain.Share {
	s := domain.Share{ExpiresAt: expires}
	if hash != nil {
		s.TokenHash = *hash
	}
	if sealed != nil {
		s.Sealed = *sealed
	}
	return s
}

func readingOf(id, chargeID, invoiceID uuid.UUID, readOn time.Time, value, consumption float64, price int64, name, unit string) domain.Reading {
	return domain.Reading{
		ID: id, ChargeID: chargeID, ChargeName: name, ChargeUnit: unit, InvoiceID: invoiceID, ReadOn: readOn, Value: value,
		Consumption: consumption, UnitPriceVND: price,
	}
}
