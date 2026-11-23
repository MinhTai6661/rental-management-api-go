package identityhttp

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	httpadapter "rental-management/internal/adapter/http"
	domain "rental-management/internal/domain/identity"
	usecase "rental-management/internal/usecase/identity"
	"rental-management/pkg/apperr"
	"rental-management/pkg/httpx"
)

// AuthHandler serves the auth routes: it binds the request, calls the use case and writes the answer.
type AuthHandler struct {
	uc      *usecase.Auth
	binder  *httpx.Binder
	limiter httpx.Limiter
}

// NewAuthHandler returns the handler. The limiter counts the login attempts per address and email.
func NewAuthHandler(uc *usecase.Auth, binder *httpx.Binder, limiter httpx.Limiter) *AuthHandler {
	return &AuthHandler{uc: uc, binder: binder, limiter: limiter}
}

// Login attempts allowed per minute for one address and email, on top of the limit per address alone.
const (
	loginEmailLimit  = 5
	loginEmailWindow = time.Minute
)

func clientOf(c *gin.Context) usecase.Client {
	return usecase.Client{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()}
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	in := usecase.RegisterInput{
		FullName: strings.TrimSpace(req.FullName), Email: req.Email, Password: req.Password,
		PhoneNumber: blankToNil(req.PhoneNumber), AddressDetail: blankToNil(req.AddressDetail), WardCode: req.WardCode,
		Lang: httpx.Localize(c).Locale(),
	}
	if in.FullName == "" {
		httpx.Fail(c, apperr.Invalid(apperr.Field{Name: "full_name", Rule: apperr.RuleRequired}))
		return
	}
	user, err := h.uc.Register(c.Request.Context(), in)
	if err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	httpx.Created(c, userViewOf(user))
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	// Guessing passwords for one account is slowed down more than the address-wide limit allows.
	if err := h.limitLoginEmail(c, req.Email); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	result, err := h.uc.Login(c.Request.Context(), req.Email, req.Password, clientOf(c))
	if err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	httpx.OK(c, loginResultOf(result))
}

// limitLoginEmail counts a login attempt against the address and email together. The email is hashed so the
// counter key does not hold a person's address.
func (h *AuthHandler) limitLoginEmail(c *gin.Context, email string) error {
	sum := sha256.Sum256([]byte(domain.NormalizeEmail(email)))
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
	var req RefreshRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	tokens, err := h.uc.Refresh(c.Request.Context(), req.RefreshToken, clientOf(c))
	if err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	httpx.OK(c, tokensBody{Tokens: tokensOf(tokens)})
}

func (h *AuthHandler) Logout(c *gin.Context) {
	var req LogoutRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	p, _ := httpx.PrincipalOf(c)
	if err := h.uc.Logout(c.Request.Context(), p, req.RefreshToken); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	httpx.NoContent(c)
}

func (h *AuthHandler) LogoutAll(c *gin.Context) {
	p, _ := httpx.PrincipalOf(c)
	if err := h.uc.LogoutAll(c.Request.Context(), p); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	httpx.NoContent(c)
}

func (h *AuthHandler) Me(c *gin.Context) {
	p, _ := httpx.PrincipalOf(c)
	user, err := h.uc.Me(c.Request.Context(), p)
	if err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	httpx.OK(c, userViewOf(user))
}

func (h *AuthHandler) SwitchRole(c *gin.Context) {
	var req SwitchRoleRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	p, _ := httpx.PrincipalOf(c)
	result, err := h.uc.SwitchRole(c.Request.Context(), p, req.Role, req.RefreshToken, clientOf(c))
	if err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	httpx.OK(c, loginResultOf(result))
}

func (h *AuthHandler) ChangePassword(c *gin.Context) {
	var req ChangePasswordRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	p, _ := httpx.PrincipalOf(c)
	if err := h.uc.ChangePassword(c.Request.Context(), p, req.CurrentPassword, req.NewPassword); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	httpx.NoContent(c)
}

func (h *AuthHandler) VerifyEmail(c *gin.Context) {
	var req VerifyEmailRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	if err := h.uc.VerifyEmail(c.Request.Context(), req.Token); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	httpx.NoContent(c)
}

func (h *AuthHandler) ResendEmail(c *gin.Context) {
	var req ResendEmailRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	if err := h.uc.ResendVerification(c.Request.Context(), req.Email, httpx.Localize(c).Locale()); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	// Always the same answer, whether or not the address has an account.
	httpx.Accepted(c)
}
