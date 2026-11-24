package admin

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/admin"
	"rental-management/internal/domain/shared"
)

// Legal is the terms of use and the privacy policy: what a super admin publishes and what everybody accepts.
type Legal struct{ *core }

// NewLegal builds the use cases.
func NewLegal(d Deps, o Options) *Legal { return &Legal{newCore(d, o)} }

// publishAttempts is how many times a publication retries when another one took the same second.
const publishAttempts = 3

// Publish puts a new version of a document out. Every publication is a new row and a new version for everybody:
// every account but staff is asked to accept again.
func (l *Legal) Publish(ctx context.Context, actor Actor, kind domain.LegalKind, in domain.LegalInput) (string, error) {
	in, err := in.Normalize()
	if err != nil {
		return "", err
	}
	now := l.clock.Now()
	for attempt := range publishAttempts {
		// Two publications within one second get versions one second apart.
		at := now.Add(time.Duration(attempt) * time.Second)
		doc := domain.Document{
			ID: uuid.Must(uuid.NewV7()), Kind: kind, Version: domain.NewVersion(at), Content: in.Content, Summary: in.Summary,
			PublishedByID: &actor.ID, PublishedAt: now,
		}
		err = l.tx.WithinTx(ctx, func(ctx context.Context) error {
			if err := l.legal.Insert(ctx, doc, now); err != nil {
				return err
			}
			return l.audit.Record(ctx, AuditEvent{
				Action: AuditLegalPublished, ActorID: actor.ID, Meta: map[string]any{"kind": string(kind), "version": doc.Version},
			})
		})
		if !shared.IsUnique(err) {
			return doc.Version, err
		}
	}
	return "", err
}

// Current returns the newest version of a document, or nil when none was published.
func (l *Legal) Current(ctx context.Context, kind domain.LegalKind) (*domain.Document, error) {
	doc, err := l.legal.Latest(ctx, kind)
	if errors.Is(err, shared.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// CurrentVersion is the version everybody must have accepted: the newest publication of any document, else the
// version configured while nothing was published.
func (l *Legal) CurrentVersion(ctx context.Context) (string, error) {
	v, err := l.legal.LatestVersion(ctx)
	if errors.Is(err, shared.ErrNotFound) {
		return l.opts.TermsVersion, nil
	}
	return v, err
}

// Versions lists the publications of a document, newest first.
func (l *Legal) Versions(ctx context.Context, kind domain.LegalKind, limit, offset int) ([]domain.Document, int, error) {
	return l.legal.List(ctx, kind, limit, offset)
}

// Document returns one publication (shared.ErrNotFound when the id belongs to the other document).
func (l *Legal) Document(ctx context.Context, kind domain.LegalKind, id uuid.UUID) (domain.Document, error) {
	return l.legal.Get(ctx, kind, id)
}

// Updates returns what was published since the account last accepted the terms, newest first; an account that never
// accepted gets the newest publication of each document.
func (l *Legal) Updates(ctx context.Context, userID uuid.UUID, limit int) ([]domain.Document, error) {
	accepted, err := l.legal.AcceptedAt(ctx, userID)
	if err != nil {
		return nil, err
	}
	if accepted == nil {
		docs, err := l.legal.NewestPerKind(ctx)
		if err != nil {
			return nil, err
		}
		return docs[:min(limit, len(docs))], nil
	}
	return l.legal.After(ctx, *accepted, limit)
}

// Accept records that the account accepted the version in force.
func (l *Legal) Accept(ctx context.Context, actor Actor) (string, error) {
	version, err := l.CurrentVersion(ctx)
	if err != nil {
		return "", err
	}
	err = l.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := l.legal.SetAccepted(ctx, actor.ID, version, l.clock.Now()); err != nil {
			return err
		}
		return l.audit.Record(ctx, AuditEvent{Action: AuditTermsAccepted, ActorID: actor.ID, Meta: map[string]any{"version": version}})
	})
	return version, err
}

// NeedsAcceptance tells whether an account holding the roles, on the accepted version, must accept the terms again.
func (l *Legal) NeedsAcceptance(ctx context.Context, roles []string, accepted *string) (bool, string, error) {
	current, err := l.CurrentVersion(ctx)
	if err != nil {
		return false, "", err
	}
	return domain.NeedsAcceptance(roles, accepted, current), current, nil
}
