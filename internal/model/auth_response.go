package model

import "time"

// UserView is the user as the API shows it.
type UserView struct {
	ID            string    `json:"id"`
	Email         *string   `json:"email"`
	FullName      string    `json:"full_name"`
	PhoneNumber   *string   `json:"phone_number"`
	Status        string    `json:"status"`
	AddressDetail *string   `json:"address_detail"`
	Ward          *WardView `json:"ward"`
	Roles         []string  `json:"roles"`
	ActiveRole    string    `json:"active_role"`
	Permissions   []string  `json:"permissions"`
	Terms         TermsView `json:"terms"`
	CreatedAt     time.Time `json:"created_at"`
}

// WardView is the ward (and its province) of the user's address.
type WardView struct {
	Code     int32        `json:"code"`
	Name     string       `json:"name"`
	Province ProvinceView `json:"province"`
}

// ProvinceView is a province.
type ProvinceView struct {
	Code int32  `json:"code"`
	Name string `json:"name"`
}

// TermsView tells whether the user must accept the terms of use again.
type TermsView struct {
	AcceptedVersion *string `json:"accepted_version"`
	CurrentVersion  string  `json:"current_version"`
	NeedsAcceptance bool    `json:"needs_acceptance"`
}

// Tokens are the credentials a client gets after logging in or refreshing.
type Tokens struct {
	TokenType        string `json:"token_type"`
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
}

// LoginResult is the answer of a login or a role switch.
type LoginResult struct {
	User   UserView `json:"user"`
	Tokens Tokens   `json:"tokens"`
}
