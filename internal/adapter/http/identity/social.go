package identityhttp

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"

	httpadapter "rental-management/internal/adapter/http"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/identity"
	usecase "rental-management/internal/usecase/identity"
)

// The values of the `error` and `link_error` query parameters the web app reads after a round trip to a provider.
const (
	errorSocialFailed      = "social_failed"
	errorAccountInactive   = "account_inactive"
	errorLinkElsewhere     = "link_elsewhere"
	errorLinkProviderTaken = "link_provider_taken"
	errorLinkStaff         = "link_staff"
	errorLinkEmailMismatch = "link_email_mismatch"
	errorLinkNoEmail       = "link_no_email"
	errorLinkUnlinked      = "link_unlinked"
)

const (
	// socialRateLimit is the requests per minute and address of the routes of a person who is not signed in. It has its
	// own bucket: the login limit counts per address and email, which these routes do not have.
	socialRateLimit      = 30
	linkRateLimitPerHour = 20
	loginPath            = "/login"
	profilePath          = "/profile"
)

// SocialHandler serves signing in with Google and Zalo and the linking of those accounts.
type SocialHandler struct {
	uc     *usecase.Social
	binder *httpx.Binder
	// webURL is the origin of the web app: the callbacks end by redirecting there.
	webURL string
}

// NewSocialHandler returns the handler; webURL is the origin of the web app.
func NewSocialHandler(uc *usecase.Social, binder *httpx.Binder, webURL string) *SocialHandler {
	return &SocialHandler{uc: uc, binder: binder, webURL: webURL}
}

// MountSocial adds the social login routes to the /v1 group. The routes of a person who is not signed in share a
// named per-address limit; the ones of the account page need the caller.
func MountSocial(v1 *gin.RouterGroup, h *SocialHandler, auth httpx.Authenticator, limiter httpx.Limiter) {
	limit := httpx.RateLimit(limiter, "auth-social", socialRateLimit, time.Minute)
	group := v1.Group("/auth")
	group.GET("/providers", limit, h.Providers)
	group.POST("/social/exchange", limit, h.Exchange)
	// One route pair per provider the application knows: no wildcard next to /auth/me and the like.
	for _, code := range domain.KnownProviders() {
		group.GET("/"+code, limit, h.redirectTo(code))
		group.GET("/"+code+"/callback", limit, h.callbackOf(code))
	}

	account := v1.Group("/me/oauth-accounts", httpx.RequireAuth(auth))
	account.GET("", h.Accounts)
	account.POST("/:provider", httpx.RateLimitPerUser(limiter, "oauth-link", linkRateLimitPerHour, time.Hour), h.Link)
	account.DELETE("/:provider", h.Unlink)
}

// ProviderBody is a provider the web app can offer a button for.
type ProviderBody struct {
	Provider string `json:"provider"`
	// RequiresMatchingEmail: it only links to an account whose email it gives too.
	RequiresMatchingEmail bool `json:"requires_matching_email"`
}

// Providers lists the providers that are switched on, so the web draws one button for each. It is public.
func (h *SocialHandler) Providers(c *gin.Context) {
	providers := make([]ProviderBody, 0)
	for _, p := range h.uc.Providers() {
		providers = append(providers, ProviderBody{Provider: p.Code, RequiresMatchingEmail: p.RequiresMatchingEmail})
	}
	httpx.OK(c, gin.H{"providers": providers})
}

// redirectTo starts a login: it sends the browser to the provider.
func (h *SocialHandler) redirectTo(provider string) gin.HandlerFunc {
	return func(c *gin.Context) {
		target, err := h.uc.BeginLogin(c.Request.Context(), provider, c.Query("redirect"))
		if err != nil {
			httpx.Fail(c, httpadapter.MapIdentityError(err))
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Redirect(http.StatusFound, target)
	}
}

// callbackOf ends the round trip: it sends the browser back to the web app with what happened in the query.
func (h *SocialHandler) callbackOf(provider string) gin.HandlerFunc {
	return func(c *gin.Context) {
		out := h.uc.Complete(c.Request.Context(), usecase.Callback{
			Provider: provider, State: c.Query("state"), Code: c.Query("code"),
		})
		// The URL may carry a one-time code: keep it out of caches and referrers.
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Redirect(http.StatusFound, h.landingOf(out))
	}
}

// landingOf is the URL of the web app the outcome sends the browser to. A login carries only the one-time code, never tokens.
func (h *SocialHandler) landingOf(out usecase.Outcome) string {
	query := url.Values{}
	switch {
	case out.Intent == domain.IntentLink && out.Err != nil:
		query.Set("link_error", errorValueOf(out.Err))
		query.Set("provider", out.Provider)
		return h.webLocation(profilePath, query)
	case out.Intent == domain.IntentLink:
		query.Set("linked", out.Provider)
		return h.webLocation(profilePath, query)
	case out.Err != nil:
		query.Set("error", errorValueOf(out.Err))
		return h.webLocation(loginPath, query)
	}
	query.Set("code", out.LoginCode)
	return h.webLocation("/"+out.ReturnPath, query)
}

func (h *SocialHandler) webLocation(path string, query url.Values) string {
	target, err := url.Parse(h.webURL)
	if err != nil {
		return h.webURL
	}
	target.Path, target.RawQuery = path, query.Encode()
	return target.String()
}

// errorValueOf is the value the web app switches on for a refusal.
func errorValueOf(err error) string {
	for _, known := range []struct {
		err   error
		value string
	}{
		{domain.ErrAccountInactive, errorAccountInactive},
		{domain.ErrLinkElsewhere, errorLinkElsewhere},
		{domain.ErrLinkProviderTaken, errorLinkProviderTaken},
		{domain.ErrLinkStaff, errorLinkStaff},
		{domain.ErrLinkEmailMismatch, errorLinkEmailMismatch},
		{domain.ErrLinkNoEmail, errorLinkNoEmail},
		{domain.ErrLinkUnlinked, errorLinkUnlinked},
	} {
		if errors.Is(err, known.err) {
			return known.value
		}
	}
	return errorSocialFailed
}

// Exchange trades the one-time code of a social sign-in for the credentials of a normal login.
func (h *SocialHandler) Exchange(c *gin.Context) {
	var req ExchangeRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	result, err := h.uc.Exchange(c.Request.Context(), req.Code, clientOf(c))
	if err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	httpx.OK(c, loginResultOf(result))
}

// LinkedProviderBody is one provider on the account page.
type LinkedProviderBody struct {
	Provider              string     `json:"provider"`
	Linked                bool       `json:"linked"`
	LinkedAt              *time.Time `json:"linked_at"`
	CanUnlink             bool       `json:"can_unlink"`
	Unlinked              bool       `json:"unlinked"`
	RequiresMatchingEmail bool       `json:"requires_matching_email"`
}

// Accounts lists every enabled provider for the caller.
func (h *SocialHandler) Accounts(c *gin.Context) {
	p, _ := httpx.PrincipalOf(c)
	view, err := h.uc.Accounts(c.Request.Context(), p)
	if err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	providers := make([]LinkedProviderBody, len(view.Providers))
	for i, v := range view.Providers {
		providers[i] = LinkedProviderBody{
			Provider: v.Provider, Linked: v.Linked, CanUnlink: v.CanUnlink, Unlinked: v.Unlinked,
			RequiresMatchingEmail: v.RequiresMatchingEmail,
		}
		if v.Linked {
			providers[i].LinkedAt = v.LinkedAt
		}
	}
	httpx.OK(c, gin.H{"has_password": view.HasPassword, "providers": providers})
}

// Link answers with the URL the web app opens to link the provider to the caller's account.
func (h *SocialHandler) Link(c *gin.Context) {
	p, _ := httpx.PrincipalOf(c)
	target, err := h.uc.BeginLink(c.Request.Context(), p, c.Param("provider"))
	if err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	httpx.OK(c, gin.H{"url": target})
}

// Unlink removes a provider from the caller's account.
func (h *SocialHandler) Unlink(c *gin.Context) {
	p, _ := httpx.PrincipalOf(c)
	if err := h.uc.Unlink(c.Request.Context(), p, c.Param("provider")); err != nil {
		httpx.Fail(c, httpadapter.MapIdentityError(err))
		return
	}
	httpx.NoContent(c)
}
