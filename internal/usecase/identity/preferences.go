package identity

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/pkg/authz"
)

// SetPreferences changes the preferences the caller sent (a nil field stays as it is) and returns the caller as Me does.
func (a *Auth) SetPreferences(ctx context.Context, p authz.Principal, in PreferencesInput) (UserView, error) {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return UserView{}, domain.ErrUnauthenticated
	}
	if in.AcceptsInvitations != nil {
		if err := a.users.SetAcceptsInvitations(ctx, userID, *in.AcceptsInvitations); err != nil {
			return UserView{}, err
		}
	}
	return a.userView(ctx, userID, p.Role)
}
