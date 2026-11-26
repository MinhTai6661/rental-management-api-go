// Package oauthadapter implements the social login providers of the identity use cases: Google through
// golang.org/x/oauth2 (the only package that imports it) and Zalo through a small client of its own, because Zalo does
// not follow the standard token request. Both speak plain HTTP with timeouts; neither holds state.
package oauthadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	domain "rental-management/internal/domain/identity"
)

// requestTimeout bounds every call to a provider: a slow provider must not hold a request open.
const requestTimeout = 10 * time.Second

// maxBodyBytes caps what is read from a provider, so a hostile or broken answer cannot fill memory.
const maxBodyBytes = 1 << 20

// googleUserInfoURL is the OpenID Connect userinfo endpoint of Google.
const googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

// GoogleConfig are the settings of the Google provider. Only ClientID, ClientSecret and RedirectURL are needed; the
// other fields exist so tests can point the provider at a fake server.
type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	// RedirectURL is the callback of the API registered in the Google Cloud console.
	RedirectURL string
	// Endpoint, UserInfoURL and HTTPClient default to Google's and to a client with a timeout.
	Endpoint    *oauth2.Endpoint
	UserInfoURL string
	HTTPClient  *http.Client
}

// Google signs people in with Google: OAuth 2 authorization code with PKCE, then the OpenID Connect userinfo
// endpoint for the profile. The userinfo answer comes straight from Google over TLS in answer to the access token
// we just obtained, so it needs no ID-token signature check (and no key rotation handling).
type Google struct {
	cfg         *oauth2.Config
	userInfoURL string
	httpClient  *http.Client
}

// NewGoogle returns the provider.
func NewGoogle(c GoogleConfig) *Google {
	endpoint := google.Endpoint
	if c.Endpoint != nil {
		endpoint = *c.Endpoint
	}
	g := &Google{
		cfg: &oauth2.Config{
			ClientID: c.ClientID, ClientSecret: c.ClientSecret, RedirectURL: c.RedirectURL, Endpoint: endpoint,
			Scopes: []string{"openid", "email", "profile"},
		},
		userInfoURL: c.UserInfoURL, httpClient: c.HTTPClient,
	}
	if g.userInfoURL == "" {
		g.userInfoURL = googleUserInfoURL
	}
	if g.httpClient == nil {
		g.httpClient = &http.Client{Timeout: requestTimeout}
	}
	return g
}

// Code is the name of the provider.
func (*Google) Code() string { return domain.ProviderGoogle }

// VouchesForEmail: Google reports whether it verified the address (email_verified).
func (*Google) VouchesForEmail() bool { return true }

// AuthorizationURL is where to send the person; state comes back unchanged and the verifier stays here (only its
// S256 challenge is sent).
func (g *Google) AuthorizationURL(state, codeVerifier string) string {
	return g.cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(codeVerifier))
}

type googleUserInfo struct {
	Subject       string `json:"sub"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

// Profile trades the code (with the PKCE verifier) for a token and reads who the person is.
func (g *Google) Profile(ctx context.Context, code, codeVerifier string) (domain.SocialProfile, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, g.httpClient)
	token, err := g.cfg.Exchange(ctx, code, oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return domain.SocialProfile{}, fmt.Errorf("google token exchange: %w", err)
	}
	// The oauth2 client adds the bearer token and uses the client set above (and so its timeout).
	resp, err := g.cfg.Client(ctx, token).Get(g.userInfoURL)
	if err != nil {
		return domain.SocialProfile{}, fmt.Errorf("google userinfo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return domain.SocialProfile{}, fmt.Errorf("google userinfo answered HTTP %d", resp.StatusCode)
	}
	var info googleUserInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&info); err != nil {
		return domain.SocialProfile{}, fmt.Errorf("google userinfo body: %w", err)
	}
	if info.Subject == "" {
		return domain.SocialProfile{}, errors.New("google returned a profile without an id")
	}
	profile := domain.SocialProfile{Provider: domain.ProviderGoogle, ID: info.Subject, Name: strings.TrimSpace(info.Name)}
	if email := strings.TrimSpace(info.Email); email != "" {
		profile.Email, profile.EmailVerified = &email, info.EmailVerified
	}
	if profile.Name == "" && profile.Email != nil {
		profile.Name = *profile.Email
	}
	return profile, nil
}
