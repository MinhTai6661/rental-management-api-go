package statshttp

import (
	"errors"
	"strconv"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/stats"
)

// rangeField turns a bad range of months into the field that is wrong; ok is false for any other error.
func rangeField(err error) (apperr.Field, bool) {
	switch {
	case errors.Is(err, domain.ErrRangeBackwards):
		return apperr.Field{Name: "to", Rule: apperr.RuleAfterOrEqual, Params: map[string]string{"other": "from"}}, true
	case errors.Is(err, domain.ErrRangeTooLong):
		return apperr.Field{Name: "from", Rule: apperr.RuleMaxMonths, Params: map[string]string{"max": strconv.Itoa(domain.MaxMonths)}}, true
	}
	return apperr.Field{}, false
}
