package service

import (
	"context"

	"github.com/google/uuid"

	"rental-management/internal/model"
)

// userView loads the user and describes it as acting in role (its permissions are the permissions of that role).
func (s *AuthService) userView(ctx context.Context, id uuid.UUID, role string) (model.UserView, error) {
	profile, err := s.users.Profile(ctx, id)
	if err != nil {
		return model.UserView{}, err
	}
	held, err := s.users.AuthUser(ctx, id)
	if err != nil {
		return model.UserView{}, err
	}
	permissions, err := s.perms.ForRole(ctx, role)
	if err != nil {
		return model.UserView{}, err
	}

	view := model.UserView{
		ID: profile.ID.String(), Email: profile.Email, FullName: profile.FullName, PhoneNumber: profile.PhoneNumber,
		Status: profile.Status, AddressDetail: profile.AddressDetail, Roles: held.Roles, ActiveRole: role,
		Permissions: sortedPermissionCodes(permissions), CreatedAt: profile.CreatedAt.UTC(),
		Terms: model.TermsView{
			AcceptedVersion: profile.TermsVersion,
			CurrentVersion:  s.opts.TermsVersion,
			// Staff accounts are not asked to accept the terms of renting.
			NeedsAcceptance: !hasStaffRole(held.Roles) && deref(profile.TermsVersion) != s.opts.TermsVersion,
		},
	}
	if profile.WardCode != nil && profile.WardName != nil && profile.ProvinceCode != nil && profile.ProvinceName != nil {
		view.Ward = &model.WardView{
			Code: *profile.WardCode, Name: *profile.WardName,
			Province: model.ProvinceView{Code: *profile.ProvinceCode, Name: *profile.ProvinceName},
		}
	}
	return view, nil
}
