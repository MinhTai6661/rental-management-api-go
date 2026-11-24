package admin

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/admin"
)

// PersonalData is the data subject's rights: a copy of what the platform keeps, and erasure.
type PersonalData struct{ *core }

// NewPersonalData builds the use cases.
func NewPersonalData(d Deps, o Options) *PersonalData { return &PersonalData{newCore(d, o)} }

// Export returns a copy of the data kept about the account (no proof pictures, nothing about other people).
func (p *PersonalData) Export(ctx context.Context, actor Actor) (domain.Export, error) {
	data, err := p.personal.Export(ctx, actor.ID)
	if err != nil {
		return domain.Export{}, err
	}
	return data, p.audit.Record(ctx, AuditEvent{Action: AuditPersonalDataExported, ActorID: actor.ID})
}

// ErasureCheck lists why the account cannot be erased yet (empty: it can).
func (p *PersonalData) ErasureCheck(ctx context.Context, actor Actor) ([]string, error) {
	facts, err := p.personal.Facts(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	return facts.Blockers(), nil
}

// Erase deletes the account for good after the person confirms with their password: what is personal is deleted or
// blanked, the contracts, bills and payments stay without a name, the sessions are closed and the account is removed.
func (p *PersonalData) Erase(ctx context.Context, actor Actor, password string) error {
	acc, err := p.users.Get(ctx, actor.ID)
	if err != nil {
		return err
	}
	if err := domain.CheckErasable(acc.Roles, domain.ErasureFacts{}); err != nil {
		return err
	}
	hash, err := p.users.PasswordHash(ctx, actor.ID)
	if err != nil {
		return err
	}
	if hash == nil {
		return domain.ErrPasswordWrong
	}
	ok, err := p.hasher.Verify(password, *hash)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrPasswordWrong
	}
	var closed []uuid.UUID
	err = p.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := p.users.Lock(ctx, actor.ID); err != nil {
			return err
		}
		facts, err := p.personal.Facts(ctx, actor.ID)
		if err != nil {
			return err
		}
		if err := domain.CheckErasable(acc.Roles, facts); err != nil {
			return err
		}
		now := p.clock.Now()
		if err := p.audit.Record(ctx, AuditEvent{Action: AuditUserErased, ActorID: actor.ID, TargetUserID: &actor.ID}); err != nil {
			return err
		}
		if closed, err = p.users.RevokeSessions(ctx, actor.ID, reasonAccountErased, now); err != nil {
			return err
		}
		if err := p.personal.Erase(ctx, actor.ID, now); err != nil {
			return err
		}
		return p.audit.ScrubPersonalData(ctx, actor.ID)
	})
	if err != nil {
		return err
	}
	return p.closeSessions(ctx, closed)
}
