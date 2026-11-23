// Package httpadapter is the HTTP side of the identity use cases: gin handlers, routes, request and response bodies,
// and the mapping of domain errors to API errors. It is the only identity code that imports gin.
package httpadapter

// Request bodies of the auth routes. The validate tags are the rules of the API; errors are reported under the
// json names.

// RegisterRequest is the body of POST /auth/register.
type RegisterRequest struct {
	FullName      string  `json:"full_name" validate:"required,max=255"`
	Email         string  `json:"email" validate:"required,email,max=255"`
	Password      string  `json:"password" validate:"required,min=8,max=128"`
	PhoneNumber   *string `json:"phone_number" validate:"omitempty,max=20"`
	AddressDetail *string `json:"address_detail" validate:"omitempty,max=500"`
	WardCode      *int32  `json:"ward_code"`
	AcceptTerms   bool    `json:"accept_terms" validate:"accepted"`
}

// LoginRequest is the body of POST /auth/login.
type LoginRequest struct {
	Email    string `json:"email" validate:"required,email,max=255"`
	Password string `json:"password" validate:"required,max=128"`
}

// RefreshRequest is the body of POST /auth/refresh.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required,max=512"`
}

// LogoutRequest is the body of POST /auth/logout.
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" validate:"omitempty,max=512"`
}

// SwitchRoleRequest is the body of POST /auth/switch-role.
type SwitchRoleRequest struct {
	Role         string `json:"role" validate:"required,oneof=super_admin admin landlord manager tenant"`
	RefreshToken string `json:"refresh_token" validate:"required,max=512"`
}

// ChangePasswordRequest is the body of PUT /auth/password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" validate:"required,max=128"`
	NewPassword     string `json:"new_password" validate:"required,min=8,max=128"`
}

// VerifyEmailRequest is the body of POST /auth/email/verify.
type VerifyEmailRequest struct {
	Token string `json:"token" validate:"required,max=512"`
}

// ResendEmailRequest is the body of POST /auth/email/resend.
type ResendEmailRequest struct {
	Email string `json:"email" validate:"required,email,max=255"`
}
