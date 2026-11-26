package oauthadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// The Zalo endpoints: OAuth v4 (authorization and token) and the Graph API for the profile.
const (
	zaloAuthorizeURL = "https://oauth.zaloapp.com/v4/permission"
	zaloTokenURL     = "https://oauth.zaloapp.com/v4/access_token" //nolint:gosec // a URL, not a credential
	zaloMeURL        = "https://graph.zalo.me/v2.0/me"
)

// zaloAPI is what the Zalo provider needs from Zalo; ZaloClient is the HTTP implementation and tests use a fake.
type zaloAPI interface {
	// AuthorizeURL is the page that asks the person to allow the app.
	AuthorizeURL(redirectURL, state, codeChallenge string) string
	// ExchangeCode trades the authorization code (with the PKCE verifier) for the access token of the person.
	ExchangeCode(ctx context.Context, code, codeVerifier string) (accessToken string, err error)
	// Me reads the profile of the person the access token belongs to.
	Me(ctx context.Context, accessToken string) (ZaloMe, error)
}

// ZaloMe is the profile Zalo gives; it has no email.
type ZaloMe struct {
	ID      string
	Name    string
	Picture string
}

// ZaloClientConfig are the settings of the client; the URLs and the HTTP client exist so tests can use a fake server.
type ZaloClientConfig struct {
	AppID     string
	AppSecret string
	// Optional: defaults are Zalo's endpoints and a client with a timeout.
	AuthorizeURL string
	TokenURL     string
	MeURL        string
	HTTPClient   *http.Client
}

// ZaloClient talks HTTP to Zalo with the standard library: x/oauth2 cannot send the `secret_key` header Zalo wants
// on the token request. It is the one place that knows how Zalo answers (errors come with HTTP 200 and a non-zero
// `error` in the body).
type ZaloClient struct {
	cfg ZaloClientConfig
}

// NewZaloClient returns the client.
func NewZaloClient(c ZaloClientConfig) *ZaloClient {
	if c.AuthorizeURL == "" {
		c.AuthorizeURL = zaloAuthorizeURL
	}
	if c.TokenURL == "" {
		c.TokenURL = zaloTokenURL
	}
	if c.MeURL == "" {
		c.MeURL = zaloMeURL
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: requestTimeout}
	}
	return &ZaloClient{cfg: c}
}

// AuthorizeURL builds the authorization URL (OAuth v4 with PKCE S256).
func (z *ZaloClient) AuthorizeURL(redirectURL, state, codeChallenge string) string {
	query := url.Values{
		"app_id": {z.cfg.AppID}, "redirect_uri": {redirectURL}, "code_challenge": {codeChallenge},
		"code_challenge_method": {"S256"}, "state": {state},
	}
	return z.cfg.AuthorizeURL + "?" + query.Encode()
}

// ExchangeCode trades the authorization code for an access token.
func (z *ZaloClient) ExchangeCode(ctx context.Context, code, codeVerifier string) (string, error) {
	form := url.Values{
		"app_id": {z.cfg.AppID}, "code": {code}, "grant_type": {"authorization_code"}, "code_verifier": {codeVerifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, z.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("secret_key", z.cfg.AppSecret)
	var body struct {
		AccessToken string `json:"access_token"` //nolint:gosec // a field of the answer, not a credential in code
	}
	if err := z.do(req, &body); err != nil {
		return "", err
	}
	if body.AccessToken == "" {
		return "", errors.New("zalo returned no access token")
	}
	return body.AccessToken, nil
}

// Me reads the person's id, name and picture.
func (z *ZaloClient) Me(ctx context.Context, accessToken string) (ZaloMe, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, z.cfg.MeURL+"?fields=id,name,picture", nil)
	if err != nil {
		return ZaloMe{}, err
	}
	req.Header.Set("access_token", accessToken)
	var body struct {
		ID      json.RawMessage `json:"id"`
		Name    string          `json:"name"`
		Picture struct {
			Data struct {
				URL string `json:"url"`
			} `json:"data"`
		} `json:"picture"`
	}
	if err := z.do(req, &body); err != nil {
		return ZaloMe{}, err
	}
	id := strings.Trim(strings.TrimSpace(string(body.ID)), `"`)
	if id == "" || id == "null" {
		return ZaloMe{}, errors.New("zalo returned a profile without an id")
	}
	return ZaloMe{ID: id, Name: strings.TrimSpace(body.Name), Picture: body.Picture.Data.URL}, nil
}

// do sends the request and decodes the JSON answer into out. A non-2xx status, an unreadable body and a Zalo error
// body (non-zero `error`) are all errors; the message is for logs, never for people.
func (z *ZaloClient) do(req *http.Request, out any) error {
	req.Header.Set("Accept", "application/json")
	resp, err := z.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("zalo is unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("read zalo answer: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("zalo answered HTTP %d", resp.StatusCode)
	}
	var envelope struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Name    string          `json:"error_name"`
		Reason  string          `json:"error_reason"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("zalo answer is not JSON: %w", err)
	}
	if code := zaloErrorCode(envelope.Error); code != 0 {
		return fmt.Errorf("zalo error %d: %s", code, firstNonEmpty(envelope.Message, envelope.Name, envelope.Reason))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode zalo answer: %w", err)
	}
	return nil
}

// zaloErrorCode reads the `error` of an answer, which Zalo writes as a number or as a string; 0 and absent mean no error.
func zaloErrorCode(raw json.RawMessage) int {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if text == "" || text == "null" || text == "0" {
		return 0
	}
	if code, err := strconv.Atoi(text); err == nil {
		return code
	}
	return -1 // an error we cannot read is still an error
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
