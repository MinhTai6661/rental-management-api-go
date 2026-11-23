package identity

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

// Register creates a tenant account that cannot log in until its email is verified, and sends the verification
// email. Staff and landlord roles are never granted here.
func (a *Auth) Register(ctx context.Context, in RegisterInput) (UserView, error) {
	email := domain.NormalizeEmail(in.Email)
	hash, err := a.hasher.Hash(in.Password)
	if err != nil {
		return UserView{}, err
	}
	verifyToken, verifyHash, err := a.opaque.New()
	if err != nil {
		return UserView{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return UserView{}, fmt.Errorf("make user id: %w", err)
	}
	now := a.clock.Now()
	role := authz.RoleTenant

	err = a.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := a.users.Create(ctx, domain.NewUser{
			ID: id, Email: email, PasswordHash: hash, FullName: in.FullName, PhoneNumber: in.PhoneNumber,
			WardCode: in.WardCode, AddressDetail: in.AddressDetail, LastActiveRole: role,
			TermsVersion: a.opts.TermsVersion, TermsAcceptedAt: now, CreatedAt: now,
		}); err != nil {
			return err
		}
		if err := assignRoles(ctx, a.users, id, []string{role}, now); err != nil {
			return err
		}
		return a.insertVerificationToken(ctx, id, verifyHash, now)
	})
	switch {
	case shared.IsUnique(err):
		return UserView{}, domain.ErrEmailTaken
	case shared.IsForeignKey(err):
		return UserView{}, domain.ErrWardNotFound
	case err != nil:
		return UserView{}, err
	}

	a.sendVerification(ctx, in.FullName, email, verifyToken, in.Lang)
	return a.userView(ctx, id, role)
}
