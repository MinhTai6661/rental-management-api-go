package audit

import (
	"time"

	"github.com/google/uuid"
)

// roleSuperAdmin is the code of the role that sees everything (the domain does not import pkg/authz).
const (
	roleSuperAdmin = "super_admin"
	roleAdmin      = "admin"
)

// Scope is what a reader of the whole trail may see. Amounts of money are hidden from every staff reader whatever the
// scope: the staff see that a thing happened, not how much.
type Scope struct {
	// IncludeStaff lets the reader see what staff accounts did or what was done to them.
	IncludeStaff bool
	// IncludeSettings lets the reader see the changes of the system settings.
	IncludeSettings bool
}

// ScopeOf is the scope of a staff reader acting in role: a super admin sees all, an admin sees neither staff people
// (the same level or above) nor the system settings.
func ScopeOf(role string) Scope {
	full := role == roleSuperAdmin
	return Scope{IncludeStaff: full, IncludeSettings: full}
}

// StaffRoles are the roles whose people an admin never sees in the trail.
func StaffRoles() []string { return []string{roleAdmin, roleSuperAdmin} }

// ForSubject is an entry as the person it is about may see it. When somebody else acted on the account, only their
// role is shown: not who they are, what changed, or where from.
func ForSubject(r Record, viewer uuid.UUID) Record {
	if r.ActorID != nil && *r.ActorID == viewer {
		return r
	}
	return Record{
		ID: r.ID, Tier: r.Tier,
		Entry: Entry{Action: r.Action, ActorRole: r.ActorRole, OccurredAt: r.OccurredAt},
	}
}

// Collapse merges neighbouring records of the same action, tier, actor and second into one with a count, so a bulk
// action reads as one line. The records must already be newest first.
func Collapse(records []Record) []Counted {
	out := make([]Counted, 0, len(records))
	for _, r := range records {
		if n := len(out); n > 0 && sameBurst(out[n-1].Record, r) {
			out[n-1].Count++
			continue
		}
		out = append(out, Counted{Record: r, Count: 1})
	}
	return out
}

func sameBurst(a, b Record) bool {
	return a.Action == b.Action && a.Tier == b.Tier && a.ActorRole == b.ActorRole && sameID(a.ActorID, b.ActorID) &&
		a.OccurredAt.UTC().Format(time.DateTime) == b.OccurredAt.UTC().Format(time.DateTime)
}

func sameID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
