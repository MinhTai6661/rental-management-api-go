package audit

import (
	"time"

	"github.com/google/uuid"
)

// Field limits of an entry; longer values are cut.
const (
	MaxActorRoleLen = 20
	MaxIPLen        = 45
	MaxUserAgentLen = 120
	MaxRequestIDLen = 64
)

// SystemRole is the role of an entry made by a job or a command, with nobody signed in.
const SystemRole = "system"

// References are the objects an entry is about; only these five become columns, so the history of one room, contract,
// invoice or person is an indexed lookup.
type References struct {
	RoomID       *uuid.UUID
	ContractID   *uuid.UUID
	InvoiceID    *uuid.UUID
	PaymentID    *uuid.UUID
	TargetUserID *uuid.UUID
}

// Entry is one thing that happened, ready to store.
type Entry struct {
	Action Action
	// ActorID is who did it (nil for the system); ActorRole the role they acted in, SystemRole for a job.
	ActorID    *uuid.UUID
	ActorRole  string
	SessionID  *uuid.UUID
	RequestID  string
	IP         string
	UserAgent  string
	ActingFor  *uuid.UUID // the landlord who owns the data, when somebody else acted on it
	Refs       References
	Changes    map[string]any // field -> {from, to}, sanitized
	Meta       map[string]any // sanitized
	OccurredAt time.Time
}

// Record is an entry read back, with the names of the two people it mentions.
type Record struct {
	Entry
	ID         int64
	Tier       Tier
	ActorName  string
	TargetName string
}

// Counted is a record that stands for Count identical ones (a bulk action shows once).
type Counted struct {
	Record
	Count int
}

// Cut shortens s to at most n characters.
func Cut(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
