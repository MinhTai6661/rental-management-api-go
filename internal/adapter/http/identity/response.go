package identityhttp

import (
	"time"

	httpadapter "rental-management/internal/adapter/http"
	usecase "rental-management/internal/usecase/identity"
)

// The bodies of the auth responses. The json tags are the contract with the web app.

// UserView is the user as the API shows it.
type UserView struct {
	ID            string                `json:"id"`
	Email         *string               `json:"email"`
	FullName      string                `json:"full_name"`
	PhoneNumber   *string               `json:"phone_number"`
	Status        string                `json:"status"`
	AddressDetail *string               `json:"address_detail"`
	Ward          *httpadapter.WardView `json:"ward"`
	Roles         []string              `json:"roles"`
	ActiveRole    string                `json:"active_role"`
	Permissions   []string              `json:"permissions"`
	Terms         TermsView             `json:"terms"`
	CreatedAt     time.Time             `json:"created_at"`
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

// tokensBody is the answer of a refresh.
type tokensBody struct {
	Tokens Tokens `json:"tokens"`
}

func userViewOf(u usecase.UserView) UserView {
	view := UserView{
		ID: u.ID, Email: u.Email, FullName: u.FullName, PhoneNumber: u.PhoneNumber, Status: u.Status,
		AddressDetail: u.AddressDetail, Roles: u.Roles, ActiveRole: u.ActiveRole, Permissions: u.Permissions,
		Terms: TermsView{
			AcceptedVersion: u.Terms.AcceptedVersion, CurrentVersion: u.Terms.CurrentVersion,
			NeedsAcceptance: u.Terms.NeedsAcceptance,
		},
		CreatedAt: u.CreatedAt,
	}
	if u.Ward != nil {
		view.Ward = &httpadapter.WardView{
			Code: u.Ward.Code, Name: u.Ward.Name,
			Province: httpadapter.ProvinceView{Code: u.Ward.Province.Code, Name: u.Ward.Province.Name},
		}
	}
	return view
}

func tokensOf(t usecase.Tokens) Tokens {
	return Tokens{
		TokenType: t.TokenType, AccessToken: t.AccessToken, ExpiresIn: t.ExpiresIn,
		RefreshToken: t.RefreshToken, RefreshExpiresIn: t.RefreshExpiresIn,
	}
}

func loginResultOf(r usecase.LoginResult) LoginResult {
	return LoginResult{User: userViewOf(r.User), Tokens: tokensOf(r.Tokens)}
}
