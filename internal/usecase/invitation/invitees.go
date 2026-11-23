// Package invitation holds the people side shared by every kind of invitation: finding the account a landlord means
// to invite (exact match only, so nobody can browse the accounts), deciding who an invitation is addressed to, and
// the opt-out of invitation emails. It declares the ports it needs (ports.go) and imports no framework, driver or
// other context; the contexts that send invitations reach it through a port of their own.
package invitation

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invitation"
	"rental-management/internal/domain/shared"
)

// AuditLookup is the audit action of a lookup.
const AuditLookup = "invitation.lookup"

// Deps are the collaborators of Invitees. Signer is optional (without it no opt-out link exists); a nil Audit is
// NoAudit.
type Deps struct {
	People PeopleRepository
	Blocks EmailBlocks
	Signer OptOutSigner
	Audit  AuditRecorder
	Log    *slog.Logger
}

// Options are the settings of Invitees.
type Options struct {
	// WebURL is the origin of the web app: the opt-out link points there.
	WebURL string
}

// Invitees finds and addresses the people an invitation is for.
type Invitees struct {
	people PeopleRepository
	blocks EmailBlocks
	signer OptOutSigner
	audit  AuditRecorder
	log    *slog.Logger
	opts   Options
}

// NewInvitees builds the use cases.
func NewInvitees(d Deps, o Options) *Invitees {
	i := &Invitees{people: d.People, blocks: d.Blocks, signer: d.Signer, audit: d.Audit, log: d.Log, opts: o}
	if i.audit == nil {
		i.audit = NoAudit{}
	}
	if i.log == nil {
		i.log = slog.Default()
	}
	return i
}

// Find returns the account inviterID may invite with this id or email, or nil when there is none: unknown, not
// invitable, or a value that cannot name an account. Which of those it was is never told.
func (s *Invitees) Find(ctx context.Context, inviterID uuid.UUID, by domain.LookupBy, value string) (*domain.Person, error) {
	var (
		person domain.Person
		err    error
	)
	value = strings.TrimSpace(value)
	switch by {
	case domain.LookupByEmail:
		person, err = s.people.ByEmail(ctx, domain.NormalizeEmail(value))
	case domain.LookupByID:
		id, perr := uuid.Parse(value)
		if perr != nil {
			return nil, nil //nolint:nilnil // "nobody" is a normal answer
		}
		person, err = s.people.ByID(ctx, id)
	default:
		return nil, nil //nolint:nilnil // "nobody" is a normal answer
	}
	if errors.Is(err, shared.ErrNotFound) {
		return nil, nil //nolint:nilnil // "nobody" is a normal answer
	}
	if err != nil {
		return nil, err
	}
	if !domain.Invitable(person, inviterID) {
		return nil, nil //nolint:nilnil // "nobody" is a normal answer
	}
	return &person, nil
}

// Lookup is Find, remembered in the audit trail: how the landlord looked (never what they typed) and whether
// somebody was found.
func (s *Invitees) Lookup(ctx context.Context, inviterID uuid.UUID, by domain.LookupBy, value string) (*domain.Person, error) {
	person, err := s.Find(ctx, inviterID, by, value)
	if err != nil {
		return nil, err
	}
	event := AuditEvent{Action: AuditLookup, ActorID: inviterID, Meta: map[string]any{"by": string(by), "found": person != nil}}
	if person != nil {
		event.TargetUserID = &person.ID
	}
	if err := s.audit.Record(ctx, event); err != nil {
		return nil, err
	}
	return person, nil
}

// Resolve works out who an invitation is for from the id and the address the landlord gave. An id must name an
// account that can be invited; an address is the account's when one with that address can be invited, else a plain
// address. Naming two different accounts, or the landlord's own address, is refused.
func (s *Invitees) Resolve(ctx context.Context, inviterID uuid.UUID, recipientID *uuid.UUID, email *string) (domain.Addressee, error) {
	var normalized *string
	if email != nil {
		if n := domain.NormalizeEmail(*email); n != "" {
			normalized = &n
		}
	}

	var byID *domain.Person
	if recipientID != nil {
		found, err := s.Find(ctx, inviterID, domain.LookupByID, recipientID.String())
		if err != nil {
			return domain.Addressee{}, err
		}
		if found == nil {
			return domain.Addressee{}, domain.ErrRecipientInvalid
		}
		byID = found
	}

	var byEmail *domain.Person
	if normalized != nil {
		own, err := s.isInviterEmail(ctx, inviterID, *normalized)
		if err != nil {
			return domain.Addressee{}, err
		}
		if own {
			return domain.Addressee{}, domain.ErrOwnEmail
		}
		if byEmail, err = s.Find(ctx, inviterID, domain.LookupByEmail, *normalized); err != nil {
			return domain.Addressee{}, err
		}
	}

	if byID != nil && byEmail != nil && byID.ID != byEmail.ID {
		return domain.Addressee{}, domain.ErrRecipientMismatch
	}
	recipient := byID
	if recipient == nil {
		recipient = byEmail
	}
	if recipient != nil {
		// An account is addressed through the account: its address is the one on file, not a second recipient.
		return domain.Addressee{Recipient: recipient}, nil
	}
	return domain.Addressee{Email: normalized}, nil
}

// isInviterEmail says whether the address belongs to the account that is inviting.
func (s *Invitees) isInviterEmail(ctx context.Context, inviterID uuid.UUID, normalized string) (bool, error) {
	person, err := s.people.ByEmail(ctx, normalized)
	if errors.Is(err, shared.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return person.ID == inviterID, nil
}

// Person returns the account with the id (the one answering an invitation).
func (s *Invitees) Person(ctx context.Context, id uuid.UUID) (domain.Person, error) {
	return s.people.ByID(ctx, id)
}

// MayMail says whether an invitation may be emailed to the address: it did not opt out from an earlier email and no
// account with that address switched invitations off.
func (s *Invitees) MayMail(ctx context.Context, email string) (bool, error) {
	normalized := domain.NormalizeEmail(email)
	blocked, err := s.blocks.Exists(ctx, domain.BlockHash(normalized))
	if err != nil || blocked {
		return false, err
	}
	optedOut, err := s.people.OptedOut(ctx, normalized)
	if err != nil {
		return false, err
	}
	return !optedOut, nil
}

// OptOutURL is the link of the invitation email that stops further ones; empty when links cannot be signed.
func (s *Invitees) OptOutURL(email string) string {
	if s.signer == nil {
		return ""
	}
	normalized := domain.NormalizeEmail(email)
	q := url.Values{"email": {normalized}, "token": {s.signer.Sign(normalized)}}
	return strings.TrimRight(s.opts.WebURL, "/") + "/invitation-opt-out?" + q.Encode()
}

// OptOut remembers that the address wants no more invitation emails. It needs the token of the link the email
// carried; repeating it is harmless.
func (s *Invitees) OptOut(ctx context.Context, email, token string) error {
	normalized := domain.NormalizeEmail(email)
	if s.signer == nil || !s.signer.Verify(normalized, token) {
		return domain.ErrOptOutInvalid
	}
	return s.blocks.Add(ctx, domain.BlockHash(normalized))
}
