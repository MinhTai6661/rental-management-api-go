package manager

import "github.com/google/uuid"

// What a manager may do on a room, decided from the hand-over of that room (nil when the caller manages nothing
// there or is not acting as a manager) and the account asking. The landlord of a room is never decided here: the
// landlord's rights are the contexts' own. The rule that never bends: a manager takes no part in the bills, payments
// or registrations of a contract in which they are themselves the tenant.

// holdsContract says whether the account is the tenant of the contract.
func holdsContract(tenantID *uuid.UUID, me uuid.UUID) bool { return tenantID != nil && *tenantID == me }

// CanSeeContract: a manager sees the contracts of the rooms handed over, except their own tenancy.
func CanSeeContract(g *Assignment, tenantID *uuid.UUID, me uuid.UUID) bool {
	return g.Active() && !holdsContract(tenantID, me)
}

// CanWorkOnInvoice: a manager writes, edits, previews and sends the bills of the rooms handed over, except those of
// their own tenancy.
func CanWorkOnInvoice(g *Assignment, tenantID *uuid.UUID, me uuid.UUID) bool {
	return CanSeeContract(g, tenantID, me)
}

// CanDeleteInvoice: a manager deletes only a draft they wrote themselves (the bill must also be one they may work on).
func CanDeleteInvoice(g *Assignment, tenantID *uuid.UUID, me uuid.UUID, draft bool, createdBy *uuid.UUID) bool {
	return CanWorkOnInvoice(g, tenantID, me) && draft && createdBy != nil && *createdBy == me
}

// CanSetInvoiceStatus: marking a bill paid or unpaid, or recording money received, needs payments.review.
func CanSetInvoiceStatus(g *Assignment, tenantID *uuid.UUID, me uuid.UUID) bool {
	return CanWorkOnInvoice(g, tenantID, me) && g.Can(PaymentsReview)
}

// CanReviewPayment: confirming or rejecting a payment needs payments.review, and never the manager's own payment.
func CanReviewPayment(g *Assignment, tenantID, payerID *uuid.UUID, me uuid.UUID) bool {
	if payerID != nil && *payerID == me {
		return false
	}
	return CanSetInvoiceStatus(g, tenantID, me)
}

// CanViewPayment: a manager sees the payments of the rooms handed over, except those of their own tenancy or their own.
func CanViewPayment(g *Assignment, tenantID, payerID *uuid.UUID, me uuid.UUID) bool {
	if payerID != nil && *payerID == me {
		return false
	}
	return CanSeeContract(g, tenantID, me)
}

// CanReviewRegistration: approving or rejecting a registration needs contracts.review_registrations, and never on the
// manager's own registration.
func CanReviewRegistration(g *Assignment, tenantID *uuid.UUID, me uuid.UUID) bool {
	return CanSeeContract(g, tenantID, me) && g.Can(ReviewRegistrations)
}

// CanProposeOffer: writing an offer of the room needs contracts.propose.
func CanProposeOffer(g *Assignment) bool { return g.Can(ContractsPropose) }

// OfferNeedsApproval: an offer written by a manager waits for the landlord unless the manager holds contracts.create.
func OfferNeedsApproval(g *Assignment) bool { return !g.Can(ContractsCreate) }
