package main

import (
	"context"
	"maps"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/audit"
	adminuc "rental-management/internal/usecase/admin"
	audituc "rental-management/internal/usecase/audit"
	contractuc "rental-management/internal/usecase/contract"
	identityuc "rental-management/internal/usecase/identity"
	invitationuc "rental-management/internal/usecase/invitation"
	invoiceuc "rental-management/internal/usecase/invoice"
	listinguc "rental-management/internal/usecase/listing"
	notificationuc "rental-management/internal/usecase/notification"
	paymentuc "rental-management/internal/usecase/payment"
	planuc "rental-management/internal/usecase/plan"
)

// The audit ports of the other contexts are each their own small interface (the contexts never import each other or
// the audit context); the types below adapt them to the one Recorder. The person who acted comes from the request, or
// is named by the event (a job names nobody and is recorded as the system).

// idOf is the pointer of an id, nil for the zero value (the contexts use uuid.Nil for "none").
func idOf(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// actorOf names the actor an event says, or nobody for the system.
func actorOf(id uuid.UUID) *audituc.Actor { return &audituc.Actor{ID: id} }

// metaWith is meta plus one more key whose value is an id, when there is one.
func metaWith(meta map[string]any, key string, id uuid.UUID) map[string]any {
	if id == uuid.Nil {
		return meta
	}
	out := make(map[string]any, len(meta)+1)
	maps.Copy(out, meta)
	out[key] = id.String()
	return out
}

func refsOf(room, contract, invoice, payment uuid.UUID, target *uuid.UUID) domain.References {
	return domain.References{RoomID: idOf(room), ContractID: idOf(contract), InvoiceID: idOf(invoice), PaymentID: idOf(payment), TargetUserID: target}
}

type contractAudit struct{ r *audituc.Recorder }

// Record writes a contract event.
func (a contractAudit) Record(ctx context.Context, e contractuc.AuditEvent) error {
	return a.r.Record(ctx, audituc.Event{
		Action: domain.Action(e.Action), Actor: actorOf(e.ActorID), OwnerID: idOf(e.OwnerID),
		Refs: refsOf(e.RoomID, e.ContractID, uuid.Nil, uuid.Nil, e.TargetUserID), Meta: metaWith(e.Meta, "invitation_id", e.InvitationID),
	})
}

type invoiceAudit struct{ r *audituc.Recorder }

// Record writes an invoice event.
func (a invoiceAudit) Record(ctx context.Context, e invoiceuc.AuditEvent) error {
	return a.r.Record(ctx, audituc.Event{
		Action: domain.Action(e.Action), Actor: actorOf(e.ActorID), OwnerID: idOf(e.OwnerID),
		Refs: refsOf(e.RoomID, e.ContractID, e.InvoiceID, uuid.Nil, e.TargetUserID), Meta: e.Meta,
	})
}

type paymentAudit struct{ r *audituc.Recorder }

// Record writes a payment event.
func (a paymentAudit) Record(ctx context.Context, e paymentuc.AuditEvent) error {
	return a.r.Record(ctx, audituc.Event{
		Action: domain.Action(e.Action), Actor: actorOf(e.ActorID), OwnerID: idOf(e.OwnerID),
		Refs: refsOf(e.RoomID, e.ContractID, e.InvoiceID, e.PaymentID, e.TargetUserID), Meta: e.Meta,
	})
}

type listingAudit struct{ r *audituc.Recorder }

// Record writes a listing event.
func (a listingAudit) Record(ctx context.Context, e listinguc.AuditEvent) error {
	return a.r.Record(ctx, audituc.Event{
		Action: domain.Action(e.Action), Actor: actorOf(e.ActorID), OwnerID: idOf(e.OwnerID),
		Refs: refsOf(e.RoomID, uuid.Nil, uuid.Nil, uuid.Nil, nil), Meta: metaWith(e.Meta, "listing_id", e.ListingID),
	})
}

type planAudit struct{ r *audituc.Recorder }

// Record writes a plan or subscription event.
func (a planAudit) Record(ctx context.Context, e planuc.AuditEvent) error {
	return a.r.Record(ctx, audituc.Event{
		Action: domain.Action(e.Action), Actor: actorOf(e.ActorID), Refs: domain.References{TargetUserID: e.TargetUserID}, Meta: e.Meta,
	})
}

type notificationAudit struct{ r *audituc.Recorder }

// Record writes an announcement event.
func (a notificationAudit) Record(ctx context.Context, e notificationuc.AuditEvent) error {
	return a.r.Record(ctx, audituc.Event{
		Action: domain.Action(e.Action), Actor: actorOf(e.ActorID), OwnerID: e.OwnerID, Meta: metaWith(e.Meta, "announcement_id", e.AnnouncementID),
	})
}

type invitationAudit struct{ r *audituc.Recorder }

// Record writes an invitee event (a lookup).
func (a invitationAudit) Record(ctx context.Context, e invitationuc.AuditEvent) error {
	return a.r.Record(ctx, audituc.Event{
		Action: domain.Action(e.Action), Actor: actorOf(e.ActorID), Refs: domain.References{TargetUserID: e.TargetUserID}, Meta: e.Meta,
	})
}

type adminAudit struct{ r *audituc.Recorder }

// Record writes an admin event.
func (a adminAudit) Record(ctx context.Context, e adminuc.AuditEvent) error {
	return a.r.Record(ctx, audituc.Event{
		Action: domain.Action(e.Action), Actor: actorOf(e.ActorID), Refs: domain.References{TargetUserID: e.TargetUserID}, Meta: e.Meta,
	})
}

// ScrubPersonalData blanks the address, device and session recorded for an erased account.
func (a adminAudit) ScrubPersonalData(ctx context.Context, userID uuid.UUID) error {
	return a.r.Scrub(ctx, userID)
}

type identityAudit struct{ r *audituc.Recorder }

// Record writes an account event (a sign-in, a role switch...).
func (a identityAudit) Record(ctx context.Context, e identityuc.AuditEvent) error {
	actor := &audituc.Actor{ID: e.ActorID, Role: e.ActorRole}
	if e.SessionID != uuid.Nil {
		actor.SessionID = &e.SessionID
	}
	return a.r.Record(ctx, audituc.Event{
		Action: domain.Action(e.Action), Actor: actor, Refs: domain.References{TargetUserID: e.TargetUserID}, Meta: e.Meta,
	})
}

// adminSettingHistory lets the admin use cases read the history of the settings from the trail.
type adminSettingHistory struct{ r *audituc.Reader }

// Changes lists the setting changes newest first.
func (h adminSettingHistory) Changes(ctx context.Context, page, perPage int) ([]adminuc.SettingChange, bool, error) {
	feed, err := h.r.SettingChanges(ctx, audituc.Page{Page: page, PerPage: perPage})
	if err != nil {
		return nil, false, err
	}
	out := make([]adminuc.SettingChange, len(feed.Items))
	for i, rec := range feed.Items {
		c := adminuc.SettingChange{At: rec.OccurredAt, ActorID: rec.ActorID, ActorName: rec.ActorName, Meta: rec.Meta}
		out[i] = c
	}
	return out, feed.HasMore, nil
}

var (
	_ contractuc.AuditRecorder     = contractAudit{}
	_ invoiceuc.AuditRecorder      = invoiceAudit{}
	_ paymentuc.AuditRecorder      = paymentAudit{}
	_ listinguc.AuditRecorder      = listingAudit{}
	_ planuc.AuditRecorder         = planAudit{}
	_ notificationuc.AuditRecorder = notificationAudit{}
	_ invitationuc.AuditRecorder   = invitationAudit{}
	_ adminuc.Auditor              = adminAudit{}
	_ identityuc.Auditor           = identityAudit{}
	_ adminuc.SettingHistory       = adminSettingHistory{}
)
