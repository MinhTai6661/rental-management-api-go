package identity

import (
	"context"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/platform/authz"
)

// UserView is the user as the API shows it.
type UserView struct {
	ID            string    `json:"id"`
	Email         *string   `json:"email"`
	FullName      string    `json:"full_name"`
	PhoneNumber   *string   `json:"phone_number"`
	Status        string    `json:"status"`
	AddressDetail *string   `json:"address_detail"`
	Ward          *WardView `json:"ward"`
	Roles         []string  `json:"roles"`
	ActiveRole    string    `json:"active_role"`
	Permissions   []string  `json:"permissions"`
	Terms         TermsView `json:"terms"`
	CreatedAt     time.Time `json:"created_at"`
}

// WardView is the ward (and its province) of the user's address.
type WardView struct {
	Code     int32        `json:"code"`
	Name     string       `json:"name"`
	Province ProvinceView `json:"province"`
}

// ProvinceView is a province.
type ProvinceView struct {
	Code int32  `json:"code"`
	Name string `json:"name"`
}

// TermsView tells whether the user must accept the terms of use again.
type TermsView struct {
	AcceptedVersion *string `json:"accepted_version"`
	CurrentVersion  string  `json:"current_version"`
	NeedsAcceptance bool    `json:"needs_acceptance"`
}

// userView loads the user and describes it as acting in role (its permissions are the permissions of that role).
func (s *Service) userView(ctx context.Context, id uuid.UUID, role string) (UserView, error) {
	profile, err := s.store.q.GetUserProfile(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	held, err := s.store.q.GetAuthUser(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	permissions, err := s.perms.ForRole(ctx, role)
	if err != nil {
		return UserView{}, err
	}

	view := UserView{
		ID: profile.ID.String(), Email: profile.Email, FullName: profile.FullName, PhoneNumber: profile.PhoneNumber,
		Status: profile.Status, AddressDetail: profile.AddressDetail, Roles: held.Roles, ActiveRole: role,
		Permissions: sortedKeys(permissions), CreatedAt: profile.CreatedAt.UTC(),
		Terms: TermsView{
			AcceptedVersion: profile.TermsVersion,
			CurrentVersion:  s.opts.TermsVersion,
			// Staff accounts are not asked to accept the terms of renting.
			NeedsAcceptance: !hasStaffRole(held.Roles) && deref(profile.TermsVersion) != s.opts.TermsVersion,
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

func hasStaffRole(roles []string) bool {
	for _, r := range roles {
		if authz.IsStaff(r) {
			return true
		}
	}
	return false
}
