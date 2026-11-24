package notification

import (
	"errors"
	"fmt"
	"strings"
)

// The errors of the announcement use cases. They say what went wrong in the domain's words; the HTTP adapter decides
// the status, the code and the fields the client sees. A message that does not exist, or is not the sender's, is
// shared.ErrNotFound.
var (
	// ErrRecalled: the message was recalled, so it can no longer be changed or put out.
	ErrRecalled = errors.New("announcement is recalled")
	// ErrScopeNotAllowed: the sender may not write to this audience.
	ErrScopeNotAllowed = errors.New("audience is not allowed for this sender")
	// ErrAudienceEmpty: a custom audience without any criterion.
	ErrAudienceEmpty = errors.New("custom audience has no criteria")
	// ErrNoPeople: a landlord's message that nobody would get.
	ErrNoPeople = errors.New("nobody to send this to")
	// ErrRangeBackwards and ErrRangeTooLong: a statistics range that ends before it starts, or is longer than MaxStatDays.
	ErrRangeBackwards = errors.New("range ends before it starts")
	ErrRangeTooLong   = errors.New("range is too long")
	// ErrLinkNotAllowed: only the platform's pictures may carry a link.
	ErrLinkNotAllowed = errors.New("only the platform may link a picture")
	// ErrFileNotFound: the file is not in the store.
	ErrFileNotFound = errors.New("file not found")
	// ErrImageType, ErrImageCorrupt, ErrImageDimensions and ErrImageTooLarge are the reasons a picture is refused.
	ErrImageType       = errors.New("picture is not a still WebP, JPEG or PNG")
	ErrImageCorrupt    = errors.New("picture cannot be read as an image")
	ErrImageDimensions = errors.New("picture dimensions are too large")
	ErrImageTooLarge   = errors.New("picture file is too large")
)

// ImageLimitError: the popup already has the most pictures it may have.
type ImageLimitError struct{ Max int }

func (e *ImageLimitError) Error() string { return fmt.Sprintf("popup already has %d pictures", e.Max) }

// ImageDimensionsError is ErrImageDimensions with the limit that was broken.
type ImageDimensionsError struct{ Max int }

func (e *ImageDimensionsError) Error() string { return ErrImageDimensions.Error() }

// Is makes errors.Is(err, ErrImageDimensions) true.
func (e *ImageDimensionsError) Is(target error) bool { return target == ErrImageDimensions }

// FileTooLargeError is ErrImageTooLarge with the limit that was broken.
type FileTooLargeError struct{ MaxKB int }

func (e *FileTooLargeError) Error() string { return ErrImageTooLarge.Error() }

// Is makes errors.Is(err, ErrImageTooLarge) true.
func (e *FileTooLargeError) Is(target error) bool { return target == ErrImageTooLarge }

// Violation is one failed rule of a request field.
type Violation struct {
	Name   string
	Rule   string
	Params map[string]string
}

// The rules the domain reports. The ones that are also HTTP validation rules keep the same key.
const (
	RuleRequired       = "required"
	RuleMaxString      = "max_string"
	RuleMinNumber      = "min_number"
	RuleMaxNumber      = "max_number"
	RuleMaxItems       = "max_items"
	RuleOneOf          = "oneof"
	RuleExists         = "exists"
	RuleAfterStartDate = "after_start_date"
	RuleTitleBodyPair  = "title_body_pair"
	RuleOneLanguage    = "one_language"
	RuleNowhereToGo    = "nowhere_to_go"
	RuleCTAIncomplete  = "cta_incomplete"
	RuleURLUnsafe      = "url_unsafe"
	RuleAudienceEmpty  = "audience_empty"
)

// ValidationError lists the fields that broke a rule.
type ValidationError struct{ Violations []Violation }

// Invalid builds a ValidationError.
func Invalid(v ...Violation) *ValidationError { return &ValidationError{Violations: v} }

func (e *ValidationError) Error() string {
	names := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		names[i] = v.Name + ":" + v.Rule
	}
	return "invalid: " + strings.Join(names, ", ")
}
