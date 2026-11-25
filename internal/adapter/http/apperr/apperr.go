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
	KindConflict
)

// Error codes: stable machine-readable keys the web app can switch on. Each has a message under
// "errors.<code>" in every locale file.
const (
	CodeValidationFailed     = "validation.failed"
	CodeRequestMalformed     = "request.malformed"
	CodeRequestTooLarge      = "request.too_large"
	CodeRequestRateLimited   = "request.rate_limited"
	CodeUnauthenticated      = "auth.unauthenticated"
	CodeInvalidCredentials   = "auth.invalid_credentials" //nolint:gosec // an error code, not a credential
	CodeRefreshInvalid       = "auth.refresh_invalid"
	CodeAccountInactive      = "auth.account_inactive"
	CodeEmailUnverified      = "auth.email_unverified"
	CodeVerificationInvalid  = "auth.verification_invalid"
	CodeForbidden            = "auth.forbidden"
	CodeProvinceNotFound     = "location.province_not_found"
	CodeResourceNotFound     = "resource.not_found"
	CodeRouteNotFound        = "http.route_not_found"
	CodeMethodNotAllowed     = "http.method_not_allowed"
	CodeServerBusy           = "server.busy"
	CodeBuildingHasRooms     = "property.building_has_rooms"
	CodeRoomHasContract      = "property.room_has_active_contract"
	CodeRoomHasListing       = "property.room_has_open_listing"
	CodeRoomStatusLocked     = "property.room_status_locked"
	CodeMediaLimitReached    = "property.media_limit_reached"
	CodeBuildingLimit        = "plan.building_limit_reached"
	CodeRoomLimit            = "plan.room_limit_reached"
	CodeContractTransition   = "contract.invalid_transition"
	CodeContractRoomOccupied = "contract.room_occupied"
	CodeContractNotVacant    = "contract.room_not_vacant"
	CodeContractNotListed    = "contract.room_not_listed"
	CodeContractRegistered   = "contract.already_registered"
	CodeContractOwnRoom      = "contract.own_room"
	CodeContractCancelDenied = "contract.cancellation_not_allowed"
	CodeContractNotDeletable = "contract.not_deletable"
	CodeContractLimit        = "plan.contract_limit_reached"
	CodeRegistrationLimit    = "plan.registration_limit_reached"
	CodeListingTransition    = "listing.invalid_transition"
	CodeListingRoomListed    = "listing.room_already_listed"
	CodeListingUnpublish     = "listing.unpublish_first"
	CodeListingLimit         = "plan.listing_limit_reached"
	CodePlanWrongAudience    = "plan.wrong_audience"
	CodePlanInactive         = "plan.inactive"
	CodePlanAlreadyOn        = "plan.already_on_plan"
	CodePlanNotPending       = "plan.subscription_not_pending"
	CodeInvoiceNotRunning    = "invoice.contract_not_running"
	CodeInvoiceAlreadyPaid   = "invoice.already_paid"
	CodeInvoiceSendFirst     = "invoice.send_first"
	CodeInvoiceLocked        = "invoice.locked"
	CodeInvoiceReviewPending = "invoice.review_pending_first"
	CodeInvoiceLinkNeedsSent = "invoice.link_needs_sent"
	CodeInvoiceLinkInvalid   = "invoice.link_invalid"
	CodePaymentNotPayable    = "payment.invoice_not_payable"
	CodePaymentReviewed      = "payment.already_reviewed"
	CodeAnnouncementRecalled = "announcement.recalled"
	CodeAnnouncementImageMax = "announcement.image_limit_reached"

	CodeInvitationInvalid         = "invitation.invalid"
	CodeInvitationNotPending      = "invitation.not_pending"
	CodeInvitationLinkUnavailable = "invitation.link_unavailable"
	CodeInvitationOwnOffer        = "invitation.own_offer"
	CodeInvitationNotAwaiting     = "invitation.not_awaiting_approval"
	CodeServerInternal            = "server.internal"
	RuleRequired                  = "required"
	RuleEmail                     = "email"
	RuleMinString                 = "min_string"
	RuleMaxString                 = "max_string"
	RuleMinNumber                 = "min_number"
	RuleMaxNumber                 = "max_number"
	RuleInteger                   = "integer"
	RuleUUID                      = "uuid"
	RuleOneOf                     = "oneof"
	RuleAccepted                  = "accepted"
	RuleUnique                    = "unique"
	RuleExists                    = "exists"
	RuleRoleNotHeld               = "role_not_held"
	RulePasswordIncorrect         = "password_incorrect"
	RulePasswordSame              = "password_same"
	RuleMinItems                  = "min_items"
	RuleMaxItems                  = "max_items"
	RuleDistinct                  = "distinct"
	RuleImageType                 = "image_type"
	RuleImageDimensions           = "image_dimensions"
	RuleImageCorrupt              = "image_corrupt"
	RuleFileTooLarge              = "file_too_large"
	RuleBuildingPriceMissing      = "building_price_missing"
	RuleBuildingPriceInUse        = "building_price_in_use"
	RuleRangeInvalid              = "range_invalid"
	RuleRangeTooMany              = "range_too_many"
	RuleRangeNameTooLong          = "range_name_too_long"
	RuleNamesTaken                = "names_taken"
	RuleSingleCover               = "single_cover"
	RuleRentalPeriodPair          = "rental_period_pair"
	RuleLTEField                  = "lte_field"
	RuleDate                      = "date"
	RuleAfterStartDate            = "after_start_date"
	RuleDateNotPast               = "date_not_past"
	RuleBelowMinMonths            = "below_minimum_months"
	RuleBelowMinDays              = "below_minimum_days"
	RuleFormat                    = "format"
	RuleDateTime                  = "datetime"

	RuleUnknownService          = "unknown_service"
	RulePreviousReadingRequired = "previous_reading_required"
	RuleReadingLowerThanPrev    = "reading_lower_than_previous"
	RuleNewReadingRequired      = "new_reading_required"
	RuleNothingToBill           = "nothing_to_bill"
	RuleDayNeedsEnd             = "day_needs_end"
	RuleInvalidPeriod           = "invalid_period"
	RulePeriodBilled            = "period_billed"
	RulePaymentLabelChars       = "payment_label_chars"

	RuleRecipientInvalid  = "recipient_invalid"
	RuleOwnEmail          = "own_email"
	RuleRecipientMismatch = "recipient_mismatch"
	RuleRequiredWithout   = "required_without"
	RuleLinkInvalid       = "link_invalid"
	RuleCannotManage      = "cannot_manage"
	RuleRoomsChanged      = "rooms_changed"
	RuleRoomNotOwned      = "room_not_owned"
	RuleRoomsManaged      = "rooms_managed"
	RuleRoomsPending      = "rooms_pending"
	RuleReplaceNotListed  = "replace_not_listed"

	RuleAmountExceedsRemaining = "amount_exceeds_remaining"
	RuleMethodNotAccepted      = "method_not_accepted"
	RuleDateNotFuture          = "date_not_future"
	RuleAccountFormat          = "account_number_format"
	RulePhoneFormat            = "phone_format"

	RuleTitleBodyPair  = "title_body_pair"
	RuleOneLanguage    = "one_language"
	RuleNowhereToGo    = "nowhere_to_go"
	RuleCTAIncomplete  = "cta_incomplete"
	RuleURLUnsafe      = "url_unsafe"
	RuleNoPeople       = "no_people"
	RuleAudienceEmpty  = "audience_empty"
	RuleLinkNotAllowed = "link_not_allowed"
	RuleAfterOrEqual   = "after_or_equal"
	RuleMaxDays        = "max_days"
	RuleMaxMonths      = "max_months"

	// Admin rules.
	RuleStaffRolesMixed      = "staff_roles_mixed"
	RuleHasActiveContracts   = "has_active_contracts"
	RuleAlreadyLandlord      = "already_landlord"
	RuleNotRenter            = "not_renter"
	RuleAlreadyPending       = "already_pending"
	RuleNotReviewable        = "not_reviewable"
	RuleStaffCannotErase     = "staff_cannot_erase"
	RuleErasureBlocked       = "erasure_blocked"
	RuleRichTextStructure    = "richtext_structure"
	RuleRichTextNodeType     = "richtext_node_type"
	RuleRichTextMarkType     = "richtext_mark_type"
	RuleRichTextHeadingLevel = "richtext_heading_level"
	RuleRichTextLink         = "richtext_link"
	RuleRichTextTooLarge     = "richtext_too_large"
	RuleRichTextNoHeading    = "richtext_no_heading"
	RuleRichTextEmpty        = "richtext_empty"
)

// Codes lists every error code; each needs a translation under "errors.<code>".
func Codes() []string {
	return []string{
		CodeValidationFailed, CodeRequestMalformed, CodeRequestTooLarge, CodeRequestRateLimited,
		CodeUnauthenticated, CodeInvalidCredentials, CodeRefreshInvalid, CodeAccountInactive, CodeForbidden,
		CodeEmailUnverified, CodeVerificationInvalid, CodeProvinceNotFound, CodeResourceNotFound, CodeRouteNotFound, CodeMethodNotAllowed,
		CodeServerBusy, CodeServerInternal, CodeBuildingHasRooms, CodeRoomHasContract, CodeRoomHasListing,
		CodeRoomStatusLocked, CodeMediaLimitReached, CodeBuildingLimit, CodeRoomLimit, CodeContractTransition,
		CodeContractRoomOccupied, CodeContractNotVacant, CodeContractRegistered, CodeContractOwnRoom, CodeContractCancelDenied,
		CodeContractNotDeletable, CodeContractLimit, CodeRegistrationLimit, CodePlanWrongAudience, CodePlanInactive,
		CodePlanAlreadyOn, CodePlanNotPending, CodeContractNotListed, CodeListingTransition, CodeListingRoomListed,
		CodeListingUnpublish, CodeListingLimit, CodeInvoiceNotRunning, CodeInvoiceAlreadyPaid, CodeInvoiceSendFirst,
		CodeInvoiceLocked, CodeInvoiceReviewPending, CodeInvoiceLinkNeedsSent, CodeInvoiceLinkInvalid,
		CodeInvitationInvalid, CodeInvitationNotPending, CodeInvitationLinkUnavailable, CodeInvitationOwnOffer, CodeInvitationNotAwaiting,
		CodePaymentNotPayable, CodePaymentReviewed, CodeAnnouncementRecalled, CodeAnnouncementImageMax,
	}
}

// Rules lists every validation rule key; each needs a translation under "validation.<rule>".
func Rules() []string {
	return []string{
		RuleRequired, RuleEmail, RuleMinString, RuleMaxString, RuleMinNumber, RuleMaxNumber, RuleInteger,
		RuleUUID, RuleOneOf, RuleAccepted, RuleUnique, RuleExists, RuleRoleNotHeld, RulePasswordIncorrect,
		RulePasswordSame, RuleMinItems, RuleMaxItems, RuleDistinct, RuleImageType, RuleImageDimensions,
		RuleImageCorrupt, RuleFileTooLarge, RuleBuildingPriceMissing, RuleBuildingPriceInUse, RuleRangeInvalid,
		RuleRangeTooMany, RuleRangeNameTooLong, RuleNamesTaken, RuleSingleCover, RuleRentalPeriodPair, RuleLTEField,
		RuleDate, RuleAfterStartDate, RuleDateNotPast, RuleBelowMinMonths, RuleBelowMinDays, RuleFormat, RuleDateTime,
		RuleUnknownService, RulePreviousReadingRequired, RuleReadingLowerThanPrev, RuleNewReadingRequired, RuleNothingToBill,
		RuleDayNeedsEnd, RuleInvalidPeriod, RulePeriodBilled, RulePaymentLabelChars,
		RuleRecipientInvalid, RuleOwnEmail, RuleRecipientMismatch, RuleRequiredWithout, RuleLinkInvalid,
		RuleCannotManage, RuleRoomsChanged, RuleRoomNotOwned, RuleRoomsManaged, RuleRoomsPending, RuleReplaceNotListed,
		RuleAmountExceedsRemaining, RuleMethodNotAccepted, RuleDateNotFuture, RuleAccountFormat, RulePhoneFormat,
		RuleTitleBodyPair, RuleOneLanguage, RuleNowhereToGo, RuleCTAIncomplete, RuleURLUnsafe, RuleNoPeople, RuleAudienceEmpty,
		RuleLinkNotAllowed, RuleAfterOrEqual, RuleMaxDays, RuleMaxMonths,
		RuleStaffRolesMixed, RuleHasActiveContracts, RuleAlreadyLandlord, RuleNotRenter, RuleAlreadyPending, RuleNotReviewable, RuleStaffCannotErase, RuleErasureBlocked, RuleRichTextStructure, RuleRichTextNodeType, RuleRichTextMarkType, RuleRichTextHeadingLevel, RuleRichTextLink, RuleRichTextTooLarge, RuleRichTextNoHeading, RuleRichTextEmpty,
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

// Conflict reports a request that is well-formed but clashes with the current state (HTTP 409).
func Conflict(code string) *Error { return E(KindConflict, code) }

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
