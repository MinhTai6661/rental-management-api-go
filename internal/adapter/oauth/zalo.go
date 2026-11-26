package oauthadapter

import (
	"context"
	"errors"
	"fmt"

	domain "rental-management/internal/domain/identity"
)

// Zalo signs people in with Zalo (OAuth v4 with PKCE). Zalo gives no email, so such an account has none.
type Zalo struct {
	api         zaloAPI
	redirectURL string
}

// NewZalo returns the provider on a client; redirectURL is the callback registered with Zalo.
func NewZalo(api *ZaloClient, redirectURL string) *Zalo {
	return &Zalo{api: api, redirectURL: redirectURL}
}

// Code is the name of the provider.
func (*Zalo) Code() string { return domain.ProviderZalo }

// VouchesForEmail: Zalo gives no email.
func (*Zalo) VouchesForEmail() bool { return false }

// AuthorizationURL is where to send the person; only the S256 challenge of the verifier is sent.
func (z *Zalo) AuthorizationURL(state, codeVerifier string) string {
	return z.api.AuthorizeURL(z.redirectURL, state, domain.PKCEChallenge(codeVerifier))
}

// Profile trades the code for an access token and reads who the person is.
func (z *Zalo) Profile(ctx context.Context, code, codeVerifier string) (domain.SocialProfile, error) {
	if code == "" {
		return domain.SocialProfile{}, errors.New("zalo sent no code")
	}
	token, err := z.api.ExchangeCode(ctx, code, codeVerifier)
	if err != nil {
		return domain.SocialProfile{}, fmt.Errorf("zalo token exchange: %w", err)
	}
	me, err := z.api.Me(ctx, token)
	if err != nil {
		return domain.SocialProfile{}, fmt.Errorf("zalo profile: %w", err)
	}
	return domain.SocialProfile{Provider: domain.ProviderZalo, ID: me.ID, Name: me.Name}, nil
}
