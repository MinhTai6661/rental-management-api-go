package httpadapter

import (
	"context"

	usecase "rental-management/internal/usecase/identity"
	"rental-management/pkg/authz"
)

// Authenticator lets httpx.RequireAuth use the identity use cases: it turns their errors into API errors.
type Authenticator struct{ auth *usecase.Auth }

// NewAuthenticator returns the authenticator of the use cases.
func NewAuthenticator(auth *usecase.Auth) *Authenticator { return &Authenticator{auth: auth} }

// Authenticate turns a bearer token into the caller of the request.
func (a *Authenticator) Authenticate(ctx context.Context, bearer string) (authz.Principal, error) {
	p, err := a.auth.Authenticate(ctx, bearer)
	return p, mapError(err)
}
