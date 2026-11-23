package identity

import "errors"

// The errors of the identity use cases. They say what went wrong in the domain's words; the HTTP adapter decides
// the status code, the code and the fields the client sees.
var (
	// ErrEmailTaken: the email is already registered.
	ErrEmailTaken = errors.New("email is already registered")
	// ErrWardNotFound: the ward of the address does not exist.
	ErrWardNotFound = errors.New("ward does not exist")
	// ErrInvalidCredentials: unknown email or wrong password (deliberately not told apart).
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrAccountInactive: the account is disabled.
	ErrAccountInactive = errors.New("account is not active")
	// ErrEmailUnverified: the account's email was never verified.
	ErrEmailUnverified = errors.New("email is not verified")
	// ErrForbidden: the account holds no role it can act in.
	ErrForbidden = errors.New("no role to act in")
	// ErrUnauthenticated: the caller is not signed in (bad token, unknown account, malformed identity).
	ErrUnauthenticated = errors.New("not authenticated")
	// ErrRefreshInvalid: the refresh token is unknown, expired, reused or of a closed session.
	ErrRefreshInvalid = errors.New("refresh token is not valid")
	// ErrVerificationInvalid: the verification token is unknown, used or expired.
	ErrVerificationInvalid = errors.New("verification token is not valid")
	// ErrRoleNotHeld: the account does not hold the role it asked to switch to.
	ErrRoleNotHeld = errors.New("role is not held by the account")
	// ErrCurrentPasswordWrong: the current password given does not match (or the account has none).
	ErrCurrentPasswordWrong = errors.New("current password is incorrect")
	// ErrNewPasswordSame: the new password equals the current one.
	ErrNewPasswordSame = errors.New("new password is the same as the current one")

	// ErrUnknownRole: a role that does not exist was to be granted.
	ErrUnknownRole = errors.New("unknown role")
	// ErrStaffRolesMixed: a staff role was to be granted together with another kind of role.
	ErrStaffRolesMixed = errors.New("staff roles cannot be mixed with other roles")
)
