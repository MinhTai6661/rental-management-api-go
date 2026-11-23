package invitation

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invitation"
)

// The interfaces below are what the invitation use cases need from the outside world; the adapters implement them
// structurally. Repository lookups return shared.ErrNotFound when there is no such row.

// PeopleRepository reads accounts (read-only: the identity context owns them).
type PeopleRepository interface {
	ByID(ctx context.Context, id uuid.UUID) (domain.Person, error)
	// ByEmail takes an address already normalized (accounts store it in lower case).
	ByEmail(ctx context.Context, normalized string) (domain.Person, error)
	// OptedOut says whether an account with this address switched invitations off.
	OptedOut(ctx context.Context, normalized string) (bool, error)
}

// EmailBlocks remembers the addresses that must never be mailed an invitation, by the hash of the address.
type EmailBlocks interface {
	// Add is idempotent.
	Add(ctx context.Context, hash string) error
	Exists(ctx context.Context, hash string) (bool, error)
}

// OptOutSigner signs the address in the opt-out link, so only who received the email can use it.
type OptOutSigner interface {
	Sign(normalizedEmail string) string
	Verify(normalizedEmail, token string) bool
}

// AuditEvent is one thing worth remembering. The values people typed never go into Meta.
type AuditEvent struct {
	Action       string
	ActorID      uuid.UUID
	TargetUserID *uuid.UUID
	Meta         map[string]any
}

// AuditRecorder writes the audit trail.
type AuditRecorder interface {
	Record(ctx context.Context, e AuditEvent) error
}

// NoAudit is the default: nothing is recorded.
type NoAudit struct{}

// Record does nothing.
func (NoAudit) Record(context.Context, AuditEvent) error { return nil }

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// ClockFunc turns a function (time.Now) into a Clock.
type ClockFunc func() time.Time

// Now calls the function.
func (f ClockFunc) Now() time.Time { return f() }
