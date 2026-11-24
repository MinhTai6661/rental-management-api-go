// Package audit holds the use cases of the audit trail: the Recorder the other contexts write through, the readers of
// the trail (everybody's own activity, the history of a room, contract or invoice, the staff's view) and the
// maintenance of its monthly partitions. It declares the ports it needs and imports no framework or driver.
package audit

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/audit"
	"rental-management/pkg/reqctx"
)

// Event is something that happened, as the use case that did it describes it.
type Event struct {
	Action domain.Action
	// Actor says who did it when the request cannot (a login, a job); nil takes the signed in caller of the request.
	Actor *Actor
	Refs  domain.References
	// OwnerID is the landlord whose data it is; the entry says it was done for them when somebody else did it.
	OwnerID *uuid.UUID
	// Changes (field -> {from, to}) and Meta may be any value that marshals to a JSON object; they are scrubbed.
	Changes any
	Meta    any
}

// Actor is who did it. Role and SessionID are taken from the request when they are empty and the request is theirs.
type Actor struct {
	ID        uuid.UUID
	Role      string
	SessionID *uuid.UUID
}

// Recorder writes events into the trail. It is the one port the other contexts use (through their bridges).
type Recorder struct {
	repo  Repository
	clock Clock
	log   *slog.Logger
}

// NewRecorder returns a recorder.
func NewRecorder(repo Repository, clock Clock, log *slog.Logger) *Recorder {
	return &Recorder{repo: repo, clock: clock, log: log}
}

// Record writes one event in the transaction of ctx. A failure to write an important event is returned, so the action
// it describes fails with it; a failure to write a routine one is only logged and never reaches the caller.
func (r *Recorder) Record(ctx context.Context, e Event) error {
	return r.RecordMany(ctx, []Event{e})
}

// RecordMany writes the events in one statement per table (a bulk action describes each row it changed).
func (r *Recorder) RecordMany(ctx context.Context, events []Event) error {
	var important, routine []domain.Entry
	for _, e := range events {
		entry, err := r.entryOf(ctx, e)
		if err != nil {
			return err
		}
		if entry.Action.Tier() == domain.Routine {
			routine = append(routine, entry)
		} else {
			important = append(important, entry)
		}
	}
	if len(important) > 0 {
		if err := r.repo.Append(ctx, important, false); err != nil {
			return fmt.Errorf("record audit entries: %w", err)
		}
	}
	if len(routine) > 0 {
		if err := r.repo.Append(ctx, routine, true); err != nil {
			r.log.Warn("could not record routine audit entries", "error", err, "count", len(routine))
		}
	}
	return nil
}

// entryOf turns an event into the entry to store: who did it, in which role and session, from where.
func (r *Recorder) entryOf(ctx context.Context, e Event) (domain.Entry, error) {
	if _, ok := domain.ParseAction(string(e.Action)); !ok {
		return domain.Entry{}, fmt.Errorf("%w: %q", domain.ErrUnknownAction, e.Action)
	}
	info, fromRequest := reqctx.From(ctx)
	entry := domain.Entry{
		Action: e.Action, Refs: e.Refs, OccurredAt: r.clock.Now(),
		Changes: domain.Sanitize(e.Changes), Meta: domain.Sanitize(e.Meta),
		RequestID: domain.Cut(info.RequestID, domain.MaxRequestIDLen),
		IP:        domain.Cut(info.IP, domain.MaxIPLen), UserAgent: domain.Cut(info.UserAgent, domain.MaxUserAgentLen),
	}
	entry.ActorID, entry.ActorRole, entry.SessionID = actorOf(e.Actor, info)
	if entry.ActorID == nil && entry.ActorRole == "" && !fromRequest {
		entry.ActorRole = domain.SystemRole
	}
	entry.ActorRole = domain.Cut(entry.ActorRole, domain.MaxActorRoleLen)
	if e.OwnerID != nil && *e.OwnerID != uuid.Nil && (entry.ActorID == nil || *e.OwnerID != *entry.ActorID) {
		owner := *e.OwnerID
		entry.ActingFor = &owner
	}
	return entry, nil
}

// actorOf is the actor, the role they acted in and their session: what the event says, completed from the request
// when the request is the actor's own.
func actorOf(given *Actor, info reqctx.Info) (*uuid.UUID, string, *uuid.UUID) {
	requestActor, err := uuid.Parse(info.ActorID)
	hasRequestActor := err == nil && requestActor != uuid.Nil
	requestSession := parseOptional(info.SessionID)
	if given == nil || given.ID == uuid.Nil {
		if given != nil && given.Role != "" { // a role without a person: a job acting as itself
			return nil, given.Role, nil
		}
		if hasRequestActor {
			return &requestActor, info.Role, requestSession
		}
		return nil, "", nil
	}
	id := given.ID
	role, session := given.Role, given.SessionID
	if hasRequestActor && requestActor == id {
		if role == "" {
			role = info.Role
		}
		if session == nil {
			session = requestSession
		}
	}
	return &id, role, session
}

func parseOptional(s string) *uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return nil
	}
	return &id
}

// Scrub erases the address, device and session recorded for a person: the one exception to a trail that is only ever
// added to, used when an account is erased.
func (r *Recorder) Scrub(ctx context.Context, userID uuid.UUID) error {
	return r.repo.ScrubPersonalData(ctx, userID)
}
