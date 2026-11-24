package audithttp

import (
	"strconv"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/audit"
)

func itoa(n int) string { return strconv.Itoa(n) }

type actorView struct {
	ID   *uuid.UUID `json:"id"`
	Name *string    `json:"name"`
	Role *string    `json:"role"`
}

type targetView struct {
	ID   uuid.UUID `json:"id"`
	Name *string   `json:"name"`
}

// entryView is one line of the trail. A person who sees what others did to their account gets only the role of the
// actor and nulls for the rest (the domain has already removed them).
type entryView struct {
	ID         int64          `json:"id"`
	Tier       string         `json:"tier"`
	Action     string         `json:"action"`
	Actor      actorView      `json:"actor"`
	ActingFor  *uuid.UUID     `json:"acting_for_id"`
	TargetUser *targetView    `json:"target_user"`
	RoomID     *uuid.UUID     `json:"room_id"`
	ContractID *uuid.UUID     `json:"contract_id"`
	InvoiceID  *uuid.UUID     `json:"invoice_id"`
	PaymentID  *uuid.UUID     `json:"payment_id"`
	Changes    map[string]any `json:"changes"`
	Meta       map[string]any `json:"meta"`
	IP         *string        `json:"ip"`
	UserAgent  *string        `json:"user_agent"`
	SessionID  *uuid.UUID     `json:"session_id"`
	RequestID  *string        `json:"request_id"`
	CreatedAt  time.Time      `json:"created_at"`
	// Count is how many identical entries of one burst this line stands for (only in a person's own activity).
	Count *int `json:"count,omitempty"`
}

func entriesOf(records []domain.Record) []entryView {
	out := make([]entryView, len(records))
	for i, r := range records {
		out[i] = entryOf(r)
	}
	return out
}

func entryOf(r domain.Record) entryView {
	v := entryView{
		ID: r.ID, Tier: string(r.Tier), Action: string(r.Action),
		Actor:     actorView{ID: r.ActorID, Name: textOrNil(r.ActorName), Role: textOrNil(r.ActorRole)},
		ActingFor: r.ActingFor, RoomID: r.Refs.RoomID, ContractID: r.Refs.ContractID, InvoiceID: r.Refs.InvoiceID,
		PaymentID: r.Refs.PaymentID, Changes: r.Changes, Meta: r.Meta, IP: textOrNil(r.IP), UserAgent: textOrNil(r.UserAgent),
		SessionID: r.SessionID, RequestID: textOrNil(r.RequestID), CreatedAt: r.OccurredAt,
	}
	if r.Refs.TargetUserID != nil {
		v.TargetUser = &targetView{ID: *r.Refs.TargetUserID, Name: textOrNil(r.TargetName)}
	}
	return v
}

func textOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
