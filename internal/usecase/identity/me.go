package identity

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/pkg/authz"
)

// Me describes the caller, acting in the role of the current request.
func (a *Auth) Me(ctx context.Context, p authz.Principal) (UserView, error) {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return UserView{}, domain.ErrUnauthenticated
	}
	return a.userView(ctx, userID, p.Role)
}
