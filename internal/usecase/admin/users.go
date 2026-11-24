package admin

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/admin"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

// Users is the admin's management of accounts and their roles.
type Users struct{ *core }

// NewUsers builds the use cases.
func NewUsers(d Deps, o Options) *Users { return &Users{newCore(d, o)} }

// UserQuery is what the user list is narrowed by, as the client wrote it.
type UserQuery struct {
	Status, RoleName, Search, Sort string
	WardCode, ProvinceCode         *int32
}

// List returns a page of accounts. The actor is never listed, and the accounts of staff only show to a super admin.
func (u *Users) List(ctx context.Context, actor Actor, q UserQuery, limit, offset int) ([]domain.Account, int, error) {
	f := domain.UserFilter{
		ViewerID: actor.ID, IncludeStaff: domain.SeesStaff(actor.roles()), Search: domain.EscapeLike(q.Search),
		WardCode: q.WardCode, ProvinceCode: q.ProvinceCode,
	}
	if q.Status != "" {
		if q.Status != domain.StatusActive && q.Status != domain.StatusInactive {
			return nil, 0, domain.Invalid("status", domain.RuleOneOf, nil)
		}
		f.Status = &q.Status
	}
	if q.RoleName != "" {
		if authz.Seniority(q.RoleName) == 0 {
			return nil, 0, domain.Invalid("role_name", domain.RuleOneOf, nil)
		}
		f.RoleName = &q.RoleName
	}
	sort, ok := domain.ParseUserSort(q.Sort)
	if !ok {
		return nil, 0, domain.Invalid("sort_by", domain.RuleOneOf, nil)
	}
	f.Sort = sort
	return u.users.List(ctx, f, limit, offset)
}

// Roles lists the roles the actor may give, one page of them.
func (u *Users) Roles(actor Actor, limit, offset int) (roles []string, total int) {
	all := domain.AssignableBy(actor.roles())
	if offset >= len(all) {
		return nil, len(all)
	}
	return all[offset:min(offset+limit, len(all))], len(all)
}

// Delete removes an account (it can no longer sign in and its sessions are closed).
func (u *Users) Delete(ctx context.Context, actor Actor, id uuid.UUID) error {
	sessions, err := u.deleteOne(ctx, actor, id)
	if err != nil {
		return err
	}
	return u.closeSessions(ctx, sessions)
}

// BulkDelete removes the accounts the actor may remove; an id that does not exist is skipped without a word and one
// that is refused is reported while the others still run.
func (u *Users) BulkDelete(ctx context.Context, actor Actor, ids []uuid.UUID) (domain.BulkResult, error) {
	if err := domain.CheckBulkIDs(len(ids)); err != nil {
		return domain.BulkResult{}, err
	}
	var (
		result   = domain.BulkResult{Done: []uuid.UUID{}, Failed: []domain.BulkFailure{}}
		sessions []uuid.UUID
		seen     = make(map[uuid.UUID]struct{}, len(ids))
	)
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		name, closed, err := u.deleteNamed(ctx, actor, id)
		switch {
		case errors.Is(err, shared.ErrNotFound):
		case errors.Is(err, domain.ErrForbidden):
			result.Failed = append(result.Failed, domain.BulkFailure{ID: id, Name: name, Reason: err})
		case err != nil:
			return result, err
		default:
			result.Done = append(result.Done, id)
			sessions = append(sessions, closed...)
		}
	}
	return result, u.closeSessions(ctx, sessions)
}

func (u *Users) deleteOne(ctx context.Context, actor Actor, id uuid.UUID) ([]uuid.UUID, error) {
	_, sessions, err := u.deleteNamed(ctx, actor, id)
	return sessions, err
}

// deleteNamed deletes one account in its own transaction and returns its name (for a failure) and the closed sessions.
func (u *Users) deleteNamed(ctx context.Context, actor Actor, id uuid.UUID) (string, []uuid.UUID, error) {
	var (
		name     string
		sessions []uuid.UUID
	)
	err := u.tx.WithinTx(ctx, func(ctx context.Context) error {
		target, err := u.users.Lock(ctx, id)
		if err != nil {
			return err
		}
		name = target.FullName
		if err := domain.CanDelete(actor.ID, actor.roles(), id, target.Roles); err != nil {
			return err
		}
		now := u.clock.Now()
		if err := u.users.SoftDelete(ctx, id, now); err != nil {
			return err
		}
		if sessions, err = u.users.RevokeSessions(ctx, id, reasonAccountDeleted, now); err != nil {
			return err
		}
		return u.audit.Record(ctx, AuditEvent{
			Action: AuditUserDeleted, ActorID: actor.ID, TargetUserID: &id, Meta: map[string]any{"roles": target.Roles},
		})
	})
	return name, sessions, err
}

// AssignRoles makes the account hold exactly the roles. Taking the landlord role away is refused while contracts run
// (and the landlord is told); giving it sets the landlord up as an approved application would.
func (u *Users) AssignRoles(ctx context.Context, actor Actor, id uuid.UUID, roles []string, note string) (domain.Account, error) {
	next, err := domain.ValidateRoles(roles)
	if err != nil {
		return domain.Account{}, err
	}
	err = u.tx.WithinTx(ctx, func(ctx context.Context) error {
		target, err := u.users.Lock(ctx, id)
		if err != nil {
			return err
		}
		if err := domain.CanChangeRoles(actor.ID, actor.roles(), id, target.Roles, next); err != nil {
			return err
		}
		now := u.clock.Now()
		if domain.LosesLandlord(target.Roles, next) {
			if err := u.dropLandlord(ctx, actor, target, note); err != nil {
				return err
			}
		}
		if err := u.users.ReplaceRoles(ctx, id, next, now); err != nil {
			return err
		}
		if domain.GainsLandlord(target.Roles, next) {
			if err := u.setUpLandlord(ctx, id, nil); err != nil {
				return err
			}
		}
		if target.LastActiveRole != nil && !slices.Contains(next, *target.LastActiveRole) {
			if err := u.users.SetLastActiveRole(ctx, id, nil, now); err != nil {
				return err
			}
		}
		return u.audit.Record(ctx, AuditEvent{
			Action: AuditRolesChanged, ActorID: actor.ID, TargetUserID: &id,
			Meta: map[string]any{"from": sortedCopy(target.Roles), "to": sortedCopy(next), "note": note},
		})
	})
	if err != nil {
		return domain.Account{}, err
	}
	return u.users.Get(ctx, id)
}

// Impact tells what taking the landlord role away from the account would touch.
func (u *Users) Impact(ctx context.Context, id uuid.UUID) (domain.Impact, error) {
	acc, err := u.users.Get(ctx, id)
	if err != nil {
		return domain.Impact{}, err
	}
	if !slices.Contains(acc.Roles, authz.RoleLandlord) {
		return domain.Impact{}, domain.ErrNotLandlord
	}
	return u.profiles.Impact(ctx, id)
}

// RevokeLandlord takes the landlord role away. The rooms, contracts and profile stay; the rooms stop being offered.
func (u *Users) RevokeLandlord(ctx context.Context, actor Actor, id uuid.UUID, note string) error {
	return u.tx.WithinTx(ctx, func(ctx context.Context) error {
		target, err := u.users.Lock(ctx, id)
		if err != nil {
			return err
		}
		if !slices.Contains(target.Roles, authz.RoleLandlord) {
			return domain.ErrNotLandlord
		}
		if err := u.dropLandlord(ctx, actor, target, note); err != nil {
			return err
		}
		rest := slices.DeleteFunc(slices.Clone(target.Roles), func(r string) bool { return r == authz.RoleLandlord })
		if len(rest) == 0 {
			rest = []string{authz.RoleTenant}
		}
		return u.users.ReplaceRoles(ctx, id, rest, u.clock.Now())
	})
}

// dropLandlord is what taking the landlord role away does besides changing the roles: it checks that no contract runs,
// forgets the role as the last one used, tells the landlord and writes the audit trail. The caller changes the roles.
func (u *Users) dropLandlord(ctx context.Context, actor Actor, target domain.Account, note string) error {
	impact, err := u.profiles.Impact(ctx, target.ID)
	if err != nil {
		return err
	}
	if err := domain.CheckRevocable(impact.ActiveContracts); err != nil {
		return err
	}
	if target.LastActiveRole != nil && *target.LastActiveRole == authz.RoleLandlord {
		if err := u.users.SetLastActiveRole(ctx, target.ID, nil, u.clock.Now()); err != nil {
			return err
		}
	}
	if err := u.audit.Record(ctx, AuditEvent{
		Action: AuditLandlordRevoked, ActorID: actor.ID, TargetUserID: &target.ID,
		Meta: map[string]any{"reason": note, "rooms": impact.Rooms, "unpaid_invoices": impact.UnpaidInvoices},
	}); err != nil {
		return err
	}
	return u.notifier.Notify(ctx, Notification{
		UserID: target.ID, Kind: NotifyLandlordRevoked, Params: map[string]string{"reason": note}, URL: "/profile",
	})
}

// closeSessions makes the closed sessions stop working at once (their access tokens would otherwise live on).
func (c *core) closeSessions(ctx context.Context, sessions []uuid.UUID) error {
	if len(sessions) == 0 || c.revocations == nil {
		return nil
	}
	sids := make([]string, len(sessions))
	for i, s := range sessions {
		sids[i] = s.String()
	}
	return c.revocations.RevokeSessions(ctx, sids, c.opts.RevokedFor)
}

func sortedCopy(roles []string) []string {
	out := slices.Clone(roles)
	slices.Sort(out)
	return out
}

func trim(s string) string { return strings.TrimSpace(s) }
