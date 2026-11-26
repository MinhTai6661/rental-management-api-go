package reporthttp

import (
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/report"
)

// ReportView is a report as the API returns it. The conversation is a list of its own (GET .../replies).
type ReportView struct {
	ID      uuid.UUID     `json:"id"`
	Type    domain.Kind   `json:"type"`
	Subject string        `json:"subject"`
	Body    string        `json:"body"`
	Status  domain.Status `json:"status"`
	// Deleted: the author deleted it; the recipient can still read it but nothing can be added.
	Deleted      bool       `json:"deleted"`
	CreatedAt    time.Time  `json:"created_at"`
	Reporter     PersonView `json:"reporter"`
	Room         *RoomView  `json:"room"`
	RepliesCount int        `json:"replies_count"`
}

// PersonView is who wrote something and the side they wrote from (the web links to the right profile page).
type PersonView struct {
	ID   uuid.UUID   `json:"id"`
	Name string      `json:"name"`
	Role domain.Side `json:"role"`
}

// RoomView is the room of a room report.
type RoomView struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// ReplyView is one message of the conversation.
type ReplyView struct {
	ID        uuid.UUID  `json:"id"`
	Body      string     `json:"body"`
	CreatedAt time.Time  `json:"created_at"`
	Author    PersonView `json:"author"`
}

func reportViewOf(v domain.View) ReportView {
	out := ReportView{
		ID: v.ID, Type: v.Kind, Subject: v.Subject, Body: v.Body, Status: v.Status, Deleted: v.Deleted(), CreatedAt: v.CreatedAt,
		Reporter:     PersonView{ID: v.Reporter.ID, Name: v.Reporter.Name, Role: domain.SideOf(v.Report, v.Reporter)},
		RepliesCount: v.RepliesCount,
	}
	if v.RoomID != nil {
		out.Room = &RoomView{ID: *v.RoomID, Name: v.RoomName}
	}
	return out
}

func reportViewsOf(views []domain.View) []ReportView {
	out := make([]ReportView, len(views))
	for i, v := range views {
		out[i] = reportViewOf(v)
	}
	return out
}

func replyViewsOf(report domain.Report, replies []domain.Reply) []ReplyView {
	out := make([]ReplyView, len(replies))
	for i, r := range replies {
		out[i] = ReplyView{
			ID: r.ID, Body: r.Body, CreatedAt: r.CreatedAt,
			Author: PersonView{ID: r.Author.ID, Name: r.Author.Name, Role: domain.SideOf(report, r.Author)},
		}
	}
	return out
}
