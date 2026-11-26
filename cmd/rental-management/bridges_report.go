package main

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/audit"
	audituc "rental-management/internal/usecase/audit"
	reportuc "rental-management/internal/usecase/report"
)

// The bridges of the report context. It imports no other context (the linter enforces it): this file adapts the inbox,
// and the audit trail to its ports (the realtime events go through reporthttp.EventPublisher).

// reportNotifier is the report context's Notifier.
type reportNotifier struct{ m messenger }

// Notify sends a report message to its user, or to every admin.
func (b reportNotifier) Notify(ctx context.Context, n reportuc.Notification) error {
	return sendTo(ctx, b.m, n.ToAdmins, messageOf(n.UserID, n.Kind, n.Params, n.URL, n.Audience))
}

// reportAudit is the report context's AuditRecorder.
type reportAudit struct{ r *audituc.Recorder }

// Record writes a report event. The owner of the room (when the report is about one) is the owner of the data.
func (a reportAudit) Record(ctx context.Context, e reportuc.AuditEvent) error {
	return a.r.Record(ctx, audituc.Event{
		Action: domain.Action(e.Action), Actor: actorOf(e.ActorID), OwnerID: idOf(e.OwnerID),
		Refs: refsOf(e.RoomID, uuid.Nil, uuid.Nil, uuid.Nil, e.TargetUserID), Changes: changesOrNil(e.Changes),
		Meta: metaWith(e.Meta, "report_id", e.ReportID),
	})
}

var (
	_ reportuc.Notifier      = reportNotifier{}
	_ reportuc.AuditRecorder = reportAudit{}
)
