package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/model"
	"rental-management/internal/service"
	"rental-management/pkg/apperr"
	"rental-management/pkg/httpx"
)

// AuthHandler serves the auth routes: it binds the request, calls the service and writes the answer.
type AuthHandler struct {
	svc     *service.AuthService
	binder  *httpx.Binder
	limiter httpx.Limiter
}

// NewAuthHandler returns the handler. The limiter counts the login attempts per address and email.
func NewAuthHandler(svc *service.AuthService, binder *httpx.Binder, limiter httpx.Limiter) *AuthHandler {
	return &AuthHandler{svc: svc, binder: binder, limiter: limiter}
}

// Login attempts allowed per minute for one address and email, on top of the limit per address alone.
const (
	loginEmailLimit  = 5
	loginEmailWindow = time.Minute
)

func clientOf(c *gin.Context) service.AuthClient {
	return service.AuthClient{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()}
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req model.RegisterRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	in := service.RegisterInput{
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

func (h *AuthHandler) Login(c *gin.Context) {
	var req model.LoginRequest
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
func (h *AuthHandler) limitLoginEmail(c *gin.Context, email string) error {
	sum := sha256.Sum256([]byte(service.NormalizeEmail(email)))
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

func (h *AuthHandler) Refresh(c *gin.Context) {
	var req model.RefreshRequest
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

func (h *AuthHandler) Logout(c *gin.Context) {
	var req model.LogoutRequest
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

func (h *AuthHandler) LogoutAll(c *gin.Context) {
	p, _ := httpx.PrincipalOf(c)
	if err := h.svc.LogoutAll(c.Request.Context(), p); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func (h *AuthHandler) Me(c *gin.Context) {
	p, _ := httpx.PrincipalOf(c)
	user, err := h.svc.Me(c.Request.Context(), p)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, user)
}

func (h *AuthHandler) SwitchRole(c *gin.Context) {
	var req model.SwitchRoleRequest
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

func (h *AuthHandler) ChangePassword(c *gin.Context) {
	var req model.ChangePasswordRequest
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

func (h *AuthHandler) VerifyEmail(c *gin.Context) {
	var req model.VerifyEmailRequest
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

func (h *AuthHandler) ResendEmail(c *gin.Context) {
	var req model.ResendEmailRequest
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
