package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"rental-management/internal/apperr"
)

// maxBodyBytes caps a JSON request body.
const maxBodyBytes = 1 << 20

// Binder decodes and validates request bodies. Rules go in the `validate` struct tag and errors are reported under
// the field's json name. Supported tags: required, email, min, max, oneof, uuid and accepted (a bool that is true).
type Binder struct {
	validate *validator.Validate
}

// NewBinder builds a Binder.
func NewBinder() *Binder {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || name == "" {
			return f.Name
		}
		return name
	})
	// accepted: the field is a bool and it is true (terms of use).
	_ = v.RegisterValidation("accepted", func(fl validator.FieldLevel) bool {
		f := fl.Field()
		return f.Kind() == reflect.Bool && f.Bool()
	})
	return &Binder{validate: v}
}

// JSON decodes the request body into dst and validates it. The error is an *apperr.Error: 413 for a body over
// 1 MiB, 400 for a body that is not valid JSON or has values of the wrong type, 422 for failed rules.
// An empty body is read as an empty object, so the client sees "required" errors rather than a parse error.
func (b *Binder) JSON(c *gin.Context, dst any) error {
	body := http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
	if err := json.NewDecoder(body).Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return apperr.TooLarge()
		}
		return apperr.Malformed().WithCause(err)
	}
	return b.Struct(dst)
}

// Struct validates an already filled struct.
func (b *Binder) Struct(dst any) error {
	err := b.validate.Struct(dst)
	if err == nil {
		return nil
	}
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return apperr.Internal(err)
	}
	fields := make([]apperr.Field, 0, len(verrs))
	for _, fe := range verrs {
		field, ok := fieldOf(fe)
		if !ok {
			// A tag with no rule key is a mistake in the struct, not in the request.
			return apperr.Internal(fmt.Errorf("validate tag %q on %s has no mapped rule", fe.Tag(), fe.Namespace()))
		}
		fields = append(fields, field)
	}
	return apperr.Invalid(fields...)
}

// fieldOf maps a validator tag to the rule key of the error catalog.
func fieldOf(fe validator.FieldError) (apperr.Field, bool) {
	name := fe.Field()
	switch fe.Tag() {
	case "required":
		return apperr.Field{Name: name, Rule: apperr.RuleRequired}, true
	case "email":
		return apperr.Field{Name: name, Rule: apperr.RuleEmail}, true
	case "min":
		return sizeField(name, fe, apperr.RuleMinString, apperr.RuleMinNumber, "min"), true
	case "max":
		return sizeField(name, fe, apperr.RuleMaxString, apperr.RuleMaxNumber, "max"), true
	case "oneof":
		return apperr.Field{Name: name, Rule: apperr.RuleOneOf}, true
	case "uuid", "uuid4", "uuid7":
		return apperr.Field{Name: name, Rule: apperr.RuleUUID}, true
	case "accepted":
		return apperr.Field{Name: name, Rule: apperr.RuleAccepted}, true
	}
	return apperr.Field{}, false
}

// sizeField picks the string rule (counts characters) or the number rule (compares the value).
func sizeField(name string, fe validator.FieldError, stringRule, numberRule, param string) apperr.Field {
	rule := numberRule
	if fe.Kind() == reflect.String {
		rule = stringRule
	}
	return apperr.Field{Name: name, Rule: rule, Params: map[string]string{param: fe.Param()}}
}
