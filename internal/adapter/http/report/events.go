package reporthttp

import (
	"context"

	"rental-management/internal/adapter/realtime"
	domain "rental-management/internal/domain/report"
	usecase "rental-management/internal/usecase/report"
)

// Fanout carries a frame to the sockets open on a report, on every API instance (Redis Pub/Sub).
type Fanout interface {
	Publish(ctx context.Context, frame realtime.ReportFrame) error
}

// EventPublisher is the report use cases' ReportEvents: it turns an event into the JSON frame of the conversation
// protocol (the same DTOs as the HTTP API) and hands it to the fan-out.
type EventPublisher struct{ fanout Fanout }

// NewEventPublisher returns the publisher.
func NewEventPublisher(fanout Fanout) *EventPublisher { return &EventPublisher{fanout: fanout} }

// Publish sends the event to the open sockets of its report. Typing goes to the other party only.
func (p *EventPublisher) Publish(ctx context.Context, e usecase.ReportEvent) error {
	frame := realtime.ReportFrame{ReportID: e.Report.ID}
	switch e.Kind {
	case usecase.EventReply:
		if e.Reply == nil {
			return nil
		}
		frame.Data = mustJSON(replyFrame{Type: "reply", Reply: replyViewsOf(e.Report, []domain.Reply{*e.Reply})[0]})
	case usecase.EventStatus:
		frame.Data = mustJSON(statusFrame{Type: "status", Status: e.Report.Status})
	case usecase.EventTyping:
		frame.ExceptUser = &e.UserID
		frame.Data = mustJSON(typingFrame{Type: "typing", UserID: e.UserID})
	default:
		return nil
	}
	return p.fanout.Publish(ctx, frame)
}

var _ usecase.ReportEvents = (*EventPublisher)(nil)
