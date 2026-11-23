package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/pkg/apperr"
	"rental-management/pkg/httpx"
)

// Request bodies. The validate tags are the rules of the API; errors are reported under the json names.

type registerRequest struct {
	FullName      string  `json:"full_name" validate:"required,max=255"`
	Email         string  `json:"email" validate:"required,email,max=255"`
	Password      string  `json:"password" validate:"required,min=8,max=128"`
	PhoneNumber   *string `json:"phone_number" validate:"omitempty,max=20"`
	AddressDetail *string `json:"address_detail" validate:"omitempty,max=500"`
	WardCode      *int32  `json:"ward_code"`
	AcceptTerms   bool    `json:"accept_terms" validate:"accepted"`
}

type loginRequest struct {
	Email    string `json:"email" validate:"required,email,max=255"`
	Password string `json:"password" validate:"required,max=128"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required,max=512"`
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token" validate:"omitempty,max=512"`
}

type switchRoleRequest struct {
	Role         string `json:"role" validate:"required,oneof=super_admin admin landlord manager tenant"`
	RefreshToken string `json:"refresh_token" validate:"required,max=512"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" validate:"required,max=128"`
	NewPassword     string `json:"new_password" validate:"required,min=8,max=128"`
}

type verifyEmailRequest struct {
	Token string `json:"token" validate:"required,max=512"`
}

type resendEmailRequest struct {
	Email string `json:"email" validate:"required,email,max=255"`
}

type handler struct {
	svc     *Service
	binder  *httpx.Binder
	limiter httpx.Limiter
}

// Login attempts allowed per minute for one address and email, on top of the limit per address alone.
const (
	loginEmailLimit  = 5
	loginEmailWindow = time.Minute
)

func clientOf(c *gin.Context) Client {
	return Client{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()}
}

func (h handler) register(c *gin.Context) {
	var req registerRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	in := RegisterInput{
		FullName: strings.TrimSpace(req.FullName), Email: req.Email, Password: req.Password,
		PhoneNumber: blankToNil(req.PhoneNumber), AddressDetail: blankToNil(req.AddressDetail), WardCode: req.WardCode,
		Lang: httpx.Localize(c).Locale(),
	}
	if in.FullName == "" {
		httpx.Fail(c, apperr.Invalid(apperr.Field{Name: "full_name", Rule: apperr.RuleRequired}))
		return
	}
	user, err := h.svc.Register(c.Request.Context(), in)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, user)
}

func (h handler) login(c *gin.Context) {
	var req loginRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	// Guessing passwords for one account is slowed down more than the address-wide limit allows.
	if err := h.limitLoginEmail(c, req.Email); err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.svc.Login(c.Request.Context(), req.Email, req.Password, clientOf(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}

// limitLoginEmail counts a login attempt against the address and email together. The email is hashed so the
// counter key does not hold a person's address.
func (h handler) limitLoginEmail(c *gin.Context, email string) error {
	sum := sha256.Sum256([]byte(normalizeEmail(email)))
	key := "login-email:" + c.ClientIP() + ":" + hex.EncodeToString(sum[:8])
	res, err := h.limiter.Allow(c.Request.Context(), key, loginEmailLimit, loginEmailWindow)
	if err != nil {
		return apperr.Busy(err)
	}
	if !res.Allowed {
		return apperr.RateLimited(retryAfterSeconds(res.RetryAfter))
	}
	return nil
}

func (h handler) refresh(c *gin.Context) {
	var req refreshRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	tokens, err := h.svc.Refresh(c.Request.Context(), req.RefreshToken, clientOf(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"tokens": tokens})
}

func (h handler) logout(c *gin.Context) {
	var req logoutRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	p, _ := httpx.PrincipalOf(c)
	if err := h.svc.Logout(c.Request.Context(), p, req.RefreshToken); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func (h handler) logoutAll(c *gin.Context) {
	p, _ := httpx.PrincipalOf(c)
	if err := h.svc.LogoutAll(c.Request.Context(), p); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func (h handler) me(c *gin.Context) {
	p, _ := httpx.PrincipalOf(c)
	user, err := h.svc.Me(c.Request.Context(), p)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, user)
}

func (h handler) switchRole(c *gin.Context) {
	var req switchRoleRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	p, _ := httpx.PrincipalOf(c)
	result, err := h.svc.SwitchRole(c.Request.Context(), p, req.Role, req.RefreshToken, clientOf(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}

func (h handler) changePassword(c *gin.Context) {
	var req changePasswordRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	p, _ := httpx.PrincipalOf(c)
	if err := h.svc.ChangePassword(c.Request.Context(), p, req.CurrentPassword, req.NewPassword); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func (h handler) verifyEmail(c *gin.Context) {
	var req verifyEmailRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.svc.VerifyEmail(c.Request.Context(), req.Token); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func (h handler) resendEmail(c *gin.Context) {
	var req resendEmailRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.svc.ResendVerification(c.Request.Context(), req.Email, httpx.Localize(c).Locale()); err != nil {
		httpx.Fail(c, err)
		return
	}
	// Always the same answer, whether or not the address has an account.
	httpx.Accepted(c)
}

// blankToNil turns an empty or whitespace-only optional text into "not given".
func blankToNil(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

func retryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return 1
	}
	return int((d + time.Second - 1) / time.Second)
}
