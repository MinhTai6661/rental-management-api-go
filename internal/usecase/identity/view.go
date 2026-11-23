package identity

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
)

// userView loads the user and describes it as acting in role (its permissions are the permissions of that role).
func (a *Auth) userView(ctx context.Context, id uuid.UUID, role string) (UserView, error) {
	profile, err := a.users.Profile(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	held, err := a.users.AuthUser(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	permissions, err := a.perms.ForRole(ctx, role)
	if err != nil {
		return UserView{}, err
	}

	view := UserView{
		ID: profile.ID.String(), Email: profile.Email, FullName: profile.FullName, PhoneNumber: profile.PhoneNumber,
		Status: profile.Status, AddressDetail: profile.AddressDetail, Roles: held.Roles, ActiveRole: role,
		Permissions: sortedPermissionCodes(permissions), CreatedAt: profile.CreatedAt.UTC(),
		AcceptsInvitations: profile.AcceptsInvitations,
		Terms: TermsView{
			AcceptedVersion: profile.TermsVersion,
			CurrentVersion:  a.opts.TermsVersion,
			// Staff accounts are not asked to accept the terms of renting.
			NeedsAcceptance: !domain.HasStaffRole(held.Roles) && deref(profile.TermsVersion) != a.opts.TermsVersion,
		},
	}
	if profile.WardCode != nil && profile.WardName != nil && profile.ProvinceCode != nil && profile.ProvinceName != nil {
		view.Ward = &WardView{
			Code: *profile.WardCode, Name: *profile.WardName,
			Province: ProvinceView{Code: *profile.ProvinceCode, Name: *profile.ProvinceName},
		}
	}
	return view, nil
}
