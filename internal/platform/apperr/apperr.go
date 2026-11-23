// Package apperr defines the errors the application returns to the HTTP layer. A service returns one of these;
// httpx.Fail is the only place that turns it into a response. The package imports nothing.
package apperr

import (
	"errors"
	"fmt"
)

// Kind says what went wrong, independent of HTTP; httpx maps it to a status code.
type Kind int

const (
	KindInternal Kind = iota
	KindUnauthenticated
	KindForbidden
	KindNotFound
	KindMethodNotAllowed
	KindBadRequest
	KindTooLarge
	KindValidation
	KindRateLimited
	KindBusy
)

// Error codes: stable machine-readable keys the web app can switch on. Each has a message under
// "errors.<code>" in every locale file.
const (
	CodeValidationFailed    = "validation.failed"
	CodeRequestMalformed    = "request.malformed"
	CodeRequestTooLarge     = "request.too_large"
	CodeRequestRateLimited  = "request.rate_limited"
	CodeUnauthenticated     = "auth.unauthenticated"
	CodeInvalidCredentials  = "auth.invalid_credentials" //nolint:gosec // an error code, not a credential
	CodeRefreshInvalid      = "auth.refresh_invalid"
	CodeAccountInactive     = "auth.account_inactive"
	CodeEmailUnverified     = "auth.email_unverified"
	CodeVerificationInvalid = "auth.verification_invalid"
	CodeForbidden           = "auth.forbidden"
	CodeProvinceNotFound    = "location.province_not_found"
	CodeResourceNotFound    = "resource.not_found"
	CodeRouteNotFound       = "http.route_not_found"
	CodeMethodNotAllowed    = "http.method_not_allowed"
	CodeServerBusy          = "server.busy"
	CodeServerInternal      = "server.internal"
	RuleRequired            = "required"
	RuleEmail               = "email"
	RuleMinString           = "min_string"
	RuleMaxString           = "max_string"
	RuleMinNumber           = "min_number"
	RuleMaxNumber           = "max_number"
	RuleInteger             = "integer"
	RuleUUID                = "uuid"
	RuleOneOf               = "oneof"
	RuleAccepted            = "accepted"
	RuleUnique              = "unique"
	RuleExists              = "exists"
	RuleRoleNotHeld         = "role_not_held"
	RulePasswordIncorrect   = "password_incorrect"
	RulePasswordSame        = "password_same"
)

// Codes lists every error code; each needs a translation under "errors.<code>".
func Codes() []string {
	return []string{
		CodeValidationFailed, CodeRequestMalformed, CodeRequestTooLarge, CodeRequestRateLimited,
		CodeUnauthenticated, CodeInvalidCredentials, CodeRefreshInvalid, CodeAccountInactive, CodeForbidden,
		CodeEmailUnverified, CodeVerificationInvalid, CodeProvinceNotFound, CodeResourceNotFound, CodeRouteNotFound, CodeMethodNotAllowed,
		CodeServerBusy, CodeServerInternal,
	}
}

// Rules lists every validation rule key; each needs a translation under "validation.<rule>".
func Rules() []string {
	return []string{
		RuleRequired, RuleEmail, RuleMinString, RuleMaxString, RuleMinNumber, RuleMaxNumber, RuleInteger,
		RuleUUID, RuleOneOf, RuleAccepted, RuleUnique, RuleExists, RuleRoleNotHeld, RulePasswordIncorrect,
		RulePasswordSame,
	}
}

// Field is one failed validation rule of a request field.
type Field struct {
	Name   string            // request field name, e.g. "per_page"
	Rule   string            // one of the Rule* keys
	Params map[string]string // message placeholders, e.g. {"min": "8"}
}

// Error is the single error type services return to handlers.
type Error struct {
	Kind Kind
	Code string
	// Params are placeholders of the error message.
	Params map[string]string
	// Fields are the failed validation rules (Kind KindValidation).
	Fields []Field
	// Headers are copied to the response (Retry-After, Allow...).
	Headers map[string]string
	// Cause is logged but never sent to the client.
	Cause error
}

// E builds an error of the given kind and code.
func E(kind Kind, code string) *Error { return &Error{Kind: kind, Code: code} }

// Invalid reports failed validation rules (HTTP 422).
func Invalid(fields ...Field) *Error {
	return &Error{Kind: KindValidation, Code: CodeValidationFailed, Fields: fields}
}

// WithHeader returns the error with a response header set.
func (e *Error) WithHeader(name, value string) *Error {
	if e.Headers == nil {
		e.Headers = map[string]string{}
	}
	e.Headers[name] = value
	return e
}

// WithParams returns the error with message placeholders set.
func (e *Error) WithParams(params map[string]string) *Error {
	e.Params = params
	return e
}

// WithCause returns the error with the underlying cause attached (for the logs only).
func (e *Error) WithCause(cause error) *Error {
	e.Cause = cause
	return e
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Cause)
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Cause }

// As reports whether err is (or wraps) an *Error and returns it.
func As(err error) (*Error, bool) {
	var ae *Error
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}

// Shortcuts for the errors used everywhere.

func Unauthenticated() *Error     { return E(KindUnauthenticated, CodeUnauthenticated) }
func InvalidCredentials() *Error  { return E(KindUnauthenticated, CodeInvalidCredentials) }
func RefreshInvalid() *Error      { return E(KindUnauthenticated, CodeRefreshInvalid) }
func AccountInactive() *Error     { return E(KindForbidden, CodeAccountInactive) }
func Forbidden() *Error           { return E(KindForbidden, CodeForbidden) }
func EmailUnverified() *Error     { return E(KindForbidden, CodeEmailUnverified) }
func VerificationInvalid() *Error { return E(KindBadRequest, CodeVerificationInvalid) }
func NotFound() *Error            { return E(KindNotFound, CodeResourceNotFound) }
func Malformed() *Error           { return E(KindBadRequest, CodeRequestMalformed) }
func TooLarge() *Error            { return E(KindTooLarge, CodeRequestTooLarge) }
func Busy(cause error) *Error     { return E(KindBusy, CodeServerBusy).WithCause(cause) }
func Internal(cause error) *Error { return E(KindInternal, CodeServerInternal).WithCause(cause) }

// RateLimited is a 429 that tells the client how long to wait.
func RateLimited(retryAfterSeconds int) *Error {
	return E(KindRateLimited, CodeRequestRateLimited).WithHeader("Retry-After", fmt.Sprint(retryAfterSeconds))
}

// MethodNotAllowed is a 405 carrying the Allow header.
func MethodNotAllowed(allow string) *Error {
	return E(KindMethodNotAllowed, CodeMethodNotAllowed).WithHeader("Allow", allow)
}
