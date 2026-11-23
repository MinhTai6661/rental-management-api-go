package paymentpg

import (
	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/payment"
)

func paymentOf(row sqlcgen.Payment) domain.Payment {
	return domain.Payment{
		ID: row.ID, InvoiceID: row.InvoiceID, PayerID: row.PayerID, Method: row.Method, AmountVND: row.AmountVnd,
		Status: domain.Status(row.Status), Note: row.Note, ProofKeys: keysOf(row.ProofKeys), ExternalReference: row.ExternalReference,
		ReviewedByID: row.ReviewedByID, ReviewedAt: row.ReviewedAt, ReviewNote: row.ReviewNote, RecordedByID: row.RecordedByID,
		PaidAt: row.PaidAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func detailOf(row sqlcgen.SearchPaymentsRow) domain.Detail {
	d := domain.Detail{
		Payment: domain.Payment{
			ID: row.ID, InvoiceID: row.InvoiceID, PayerID: row.PayerID, Method: row.Method, AmountVND: row.AmountVnd,
			Status: domain.Status(row.Status), Note: row.Note, ProofKeys: keysOf(row.ProofKeys), ExternalReference: row.ExternalReference,
			ReviewedByID: row.ReviewedByID, ReviewedAt: row.ReviewedAt, ReviewNote: row.ReviewNote, RecordedByID: row.RecordedByID,
			PaidAt: row.PaidAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		},
		Invoice: domain.InvoiceFacts{
			ID: row.InvoiceID, ContractID: row.ContractID, RoomID: row.RoomID, RoomName: row.RoomName, LandlordID: row.LandlordID,
			TenantID: row.TenantID, Number: row.InvoiceNumber, Name: row.InvoiceName, PaymentCode: row.PaymentCode,
			TotalVND: row.TotalAmountVnd, Status: domain.InvoiceStatus(row.InvoiceStatus), DueAt: row.DueAt,
		},
	}
	if row.PayerID != nil && row.PayerFullName != nil {
		d.Payer = &domain.Person{ID: *row.PayerID, Name: *row.PayerFullName, Email: row.PayerEmail}
	}
	return d
}

// keysOf never returns nil, so a payment with no proof says so as an empty list.
func keysOf(keys []string) []string {
	if keys == nil {
		return []string{}
	}
	return keys
}
