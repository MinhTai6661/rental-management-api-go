package httpx

import (
	"math"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/pkg/apperr"
)

// ParseUUIDParam reads a UUID path parameter. A value that does not parse is a 404, the same answer as an id that
// does not exist, so a client cannot tell the two apart.
func ParseUUIDParam(c *gin.Context, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		return uuid.Nil, apperr.NotFound()
	}
	return id, nil
}

// The Query* helpers read an optional list filter. Each returns nil when the parameter is absent or empty and a
// field error when the value is bad; a handler gathers the errors of all its filters into one apperr.Invalid.

// QueryInt64 reads an integer within [lower, upper].
func QueryInt64(c *gin.Context, name string, lower, upper int64) (*int64, *apperr.Field) {
	raw := c.Query(name)
	if raw == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, &apperr.Field{Name: name, Rule: apperr.RuleInteger}
	}
	if field := checkBounds(name, n < lower, n > upper, strconv.FormatInt(lower, 10), strconv.FormatInt(upper, 10)); field != nil {
		return nil, field
	}
	return &n, nil
}

// QueryFloat reads a decimal number within [lower, upper]. A value that is not a finite number is reported as
// "at least lower" (there is no dedicated "number" rule).
func QueryFloat(c *gin.Context, name string, lower, upper float64) (*float64, *apperr.Field) {
	raw := c.Query(name)
	if raw == "" {
		return nil, nil
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, &apperr.Field{Name: name, Rule: apperr.RuleMinNumber, Params: map[string]string{"min": formatFloat(lower)}}
	}
	if field := checkBounds(name, n < lower, n > upper, formatFloat(lower), formatFloat(upper)); field != nil {
		return nil, field
	}
	return &n, nil
}

// QueryUUID reads a UUID.
func QueryUUID(c *gin.Context, name string) (*uuid.UUID, *apperr.Field) {
	raw := c.Query(name)
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, &apperr.Field{Name: name, Rule: apperr.RuleUUID}
	}
	return &id, nil
}

// QueryBool reads true, false, 1 or 0.
func QueryBool(c *gin.Context, name string) (*bool, *apperr.Field) {
	switch raw := c.Query(name); raw {
	case "":
		return nil, nil
	case "true", "1":
		v := true
		return &v, nil
	case "false", "0":
		v := false
		return &v, nil
	}
	return nil, &apperr.Field{Name: name, Rule: apperr.RuleOneOf}
}

// QueryOneOf reads a value that must be one of allowed.
func QueryOneOf(c *gin.Context, name string, allowed ...string) (*string, *apperr.Field) {
	raw := c.Query(name)
	if raw == "" {
		return nil, nil
	}
	for _, a := range allowed {
		if raw == a {
			return &raw, nil
		}
	}
	return nil, &apperr.Field{Name: name, Rule: apperr.RuleOneOf}
}

func checkBounds(name string, below, above bool, lower, upper string) *apperr.Field {
	switch {
	case below:
		return &apperr.Field{Name: name, Rule: apperr.RuleMinNumber, Params: map[string]string{"min": lower}}
	case above:
		return &apperr.Field{Name: name, Rule: apperr.RuleMaxNumber, Params: map[string]string{"max": upper}}
	}
	return nil
}

func formatFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
