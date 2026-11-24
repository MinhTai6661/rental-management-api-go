package notificationhttp

import (
	"errors"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

// mapError turns the errors of the notification use cases into the API's errors. Anything it does not know is returned
// as it is: httpx.Fail answers 503 for an overloaded database and 500 for the rest.
func mapError(err error) error {
	switch {
	case errors.Is(err, shared.ErrNotFound):
		return apperr.NotFound()
	case errors.Is(err, domain.ErrUnknownKind):
		return apperr.Invalid(apperr.Field{Name: "kind", Rule: apperr.RuleOneOf})
	case errors.Is(err, domain.ErrRecalled):
		return apperr.Conflict(apperr.CodeAnnouncementRecalled)
	case errors.Is(err, domain.ErrScopeNotAllowed):
		return apperr.Invalid(apperr.Field{Name: "audience", Rule: apperr.RuleOneOf})
	case errors.Is(err, domain.ErrAudienceEmpty):
		return apperr.Invalid(apperr.Field{Name: "targeting", Rule: apperr.RuleAudienceEmpty})
	case errors.Is(err, domain.ErrNoPeople):
		return apperr.Invalid(apperr.Field{Name: "audience", Rule: apperr.RuleNoPeople})
	case errors.Is(err, domain.ErrRangeBackwards):
		return apperr.Invalid(apperr.Field{Name: "to", Rule: apperr.RuleAfterOrEqual, Params: map[string]string{"other": "from"}})
	case errors.Is(err, domain.ErrRangeTooLong):
		return apperr.Invalid(apperr.Field{Name: "to", Rule: apperr.RuleMaxDays, Params: map[string]string{"max": itoa(domain.MaxStatDays)}})
	case errors.Is(err, domain.ErrLinkNotAllowed):
		return apperr.Invalid(apperr.Field{Name: "link_url", Rule: apperr.RuleLinkNotAllowed})
	case errors.Is(err, domain.ErrFileNotFound):
		return apperr.NotFound()
	case errors.Is(err, domain.ErrImageType):
		return apperr.Invalid(apperr.Field{Name: "file", Rule: apperr.RuleImageType})
	case errors.Is(err, domain.ErrImageCorrupt):
		return apperr.Invalid(apperr.Field{Name: "file", Rule: apperr.RuleImageCorrupt})
	case errors.Is(err, domain.ErrTooManyIDs):
		return apperr.Invalid(apperr.Field{Name: "ids", Rule: apperr.RuleMaxItems, Params: map[string]string{"max": itoa(domain.MaxBulkIDs)}})
	}
	var (
		limit        *domain.ImageLimitError
		dimensions   *domain.ImageDimensionsError
		fileTooLarge *domain.FileTooLargeError
	)
	switch {
	case errors.As(err, &limit):
		return apperr.Conflict(apperr.CodeAnnouncementImageMax).WithParams(map[string]string{"max": itoa(limit.Max)})
	case errors.As(err, &dimensions):
		return apperr.Invalid(apperr.Field{Name: "file", Rule: apperr.RuleImageDimensions, Params: map[string]string{"max": itoa(dimensions.Max)}})
	case errors.As(err, &fileTooLarge):
		return apperr.Invalid(apperr.Field{Name: "file", Rule: apperr.RuleFileTooLarge, Params: map[string]string{"max_kb": itoa(fileTooLarge.MaxKB)}})
	}
	var invalid *domain.ValidationError
	if errors.As(err, &invalid) {
		fields := make([]apperr.Field, len(invalid.Violations))
		for i, v := range invalid.Violations {
			fields[i] = apperr.Field{Name: v.Name, Rule: v.Rule, Params: v.Params}
		}
		return apperr.Invalid(fields...)
	}
	return err
}
