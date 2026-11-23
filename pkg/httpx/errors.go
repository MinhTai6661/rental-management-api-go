package httpx

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"rental-management/pkg/apperr"
	"rental-management/pkg/db"
	"rental-management/pkg/i18n"
)

type (
	errorBody struct {
		Error errorDetail `json:"error"`
	}
	errorDetail struct {
		Code    string              `json:"code"`
		Message string              `json:"message"`
		Fields  map[string][]string `json:"fields,omitempty"`
	}
)

// Fail turns any error into the API's error response and stops the handler chain.
// It is the only place errors become HTTP, so every endpoint answers the same way.
func Fail(c *gin.Context, err error) {
	ae := classify(err)
	if ae.Kind == apperr.KindInternal {
		// The cause is for the logs only; the client gets a generic message.
		Log(c).Error("unhandled error", "error", err, "method", c.Request.Method, "path", c.Request.URL.Path)
	}
	if c.Writer.Written() {
		c.Abort()
		return
	}
	for name, value := range ae.Headers {
		c.Header(name, value)
	}
	l := Localize(c)
	c.AbortWithStatusJSON(statusOf(ae.Kind), errorBody{Error: errorDetail{
		Code:    ae.Code,
		Message: l.T("errors."+ae.Code, ae.Params),
		Fields:  renderFields(l, ae.Fields),
	}})
}

// classify maps an arbitrary error to an *apperr.Error.
func classify(err error) *apperr.Error {
	if ae, ok := apperr.As(err); ok {
		return ae
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return apperr.TooLarge()
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled), db.IsBusy(err):
		return apperr.Busy(err) // a cancelled or timed-out query, or a lock that could not be had
	case db.IsNotFound(err):
		return apperr.NotFound().WithCause(err) // a lookup that found no row and was not mapped by the module
	}
	return apperr.Internal(err)
}

func statusOf(kind apperr.Kind) int {
	switch kind {
	case apperr.KindUnauthenticated:
		return http.StatusUnauthorized
	case apperr.KindForbidden:
		return http.StatusForbidden
	case apperr.KindNotFound:
		return http.StatusNotFound
	case apperr.KindMethodNotAllowed:
		return http.StatusMethodNotAllowed
	case apperr.KindBadRequest:
		return http.StatusBadRequest
	case apperr.KindTooLarge:
		return http.StatusRequestEntityTooLarge
	case apperr.KindValidation:
		return http.StatusUnprocessableEntity
	case apperr.KindRateLimited:
		return http.StatusTooManyRequests
	case apperr.KindBusy:
		return http.StatusServiceUnavailable
	case apperr.KindConflict:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// renderFields translates every failed rule into a message under its field name.
func renderFields(l i18n.Localizer, fields []apperr.Field) map[string][]string {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string][]string, len(fields))
	for _, f := range fields {
		params := map[string]string{"attribute": attributeName(l, f.Name)}
		for k, v := range f.Params {
			params[k] = v
		}
		out[f.Name] = append(out[f.Name], l.T("validation."+f.Rule, params))
	}
	return out
}

// attributeName is the human name of a field: a translation when one exists, else the field with spaces.
func attributeName(l i18n.Localizer, field string) string {
	// Nested fields ("charges.2.name") are named after their last segment.
	if i := strings.LastIndex(field, "."); i >= 0 {
		field = field[i+1:]
	}
	if key := "validation.attributes." + field; l.Has(key) {
		return l.T(key, nil)
	}
	return strings.ReplaceAll(field, "_", " ")
}
