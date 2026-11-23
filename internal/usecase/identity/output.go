package identity

import (
	"net/netip"
	"time"
)

// What the use cases take and give back. The types carry no encoding tags: how they look on the wire is the
// adapter's business.

// Client is where a request came from; it is kept with the login session.
type Client struct {
	IP        string
	UserAgent string
}

// maxUserAgentBytes caps the user agent stored with a session.
const maxUserAgentBytes = 255

func (c Client) ipAddr() *netip.Addr {
	addr, err := netip.ParseAddr(c.IP)
	if err != nil {
		return nil
	}
	return &addr
}

func (c Client) userAgent() *string {
	if c.UserAgent == "" {
		return nil
	}
	ua := c.UserAgent
	if len(ua) > maxUserAgentBytes {
		ua = ua[:maxUserAgentBytes]
	}
	return &ua
}

// RegisterInput is what a person gives to create an account.
type RegisterInput struct {
	FullName      string
	Email         string
	Password      string
	PhoneNumber   *string
	AddressDetail *string
	WardCode      *int32
	// Lang is the language of the verification email ("vi" or "en").
	Lang string
}

// UserView is the user as a client needs to see it.
type UserView struct {
	ID            string
	Email         *string
	FullName      string
	PhoneNumber   *string
	Status        string
	AddressDetail *string
	Ward          *WardView
	Roles         []string
	ActiveRole    string
	Permissions   []string
	Terms         TermsView
	// AcceptsInvitations is false when the person switched invitations off.
	AcceptsInvitations bool
	CreatedAt          time.Time
}

// PreferencesInput is what a person changes in their preferences; a nil field stays as it is.
type PreferencesInput struct {
	AcceptsInvitations *bool
}

// WardView is the ward (and its province) of the user's address.
type WardView struct {
	Code     int32
	Name     string
	Province ProvinceView
}

// ProvinceView is a province.
type ProvinceView struct {
	Code int32
	Name string
}

// TermsView tells whether the user must accept the terms of use again.
type TermsView struct {
	AcceptedVersion *string
	CurrentVersion  string
	NeedsAcceptance bool
}

// Tokens are the credentials a client gets after logging in or refreshing.
type Tokens struct {
	TokenType        string
	AccessToken      string
	ExpiresIn        int
	RefreshToken     string
	RefreshExpiresIn int
}

// LoginResult is the answer of a login or a role switch.
type LoginResult struct {
	User   UserView
	Tokens Tokens
}
