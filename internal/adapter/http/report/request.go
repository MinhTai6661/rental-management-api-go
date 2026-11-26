package reporthttp

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/report"
	usecase "rental-management/internal/usecase/report"
)

// createRequest is the body of POST /v1/reports.
type createRequest struct {
	Type    string     `json:"type" validate:"required,oneof=room feature"`
	RoomID  *uuid.UUID `json:"room_id"`
	Subject string     `json:"subject" validate:"required,max=150"`
	Body    string     `json:"body" validate:"required,max=5000"`
}

// replyRequest is the body of POST /v1/reports/{id}/replies.
type replyRequest struct {
	Body string `json:"body" validate:"required,max=5000"`
}

// statusRequest is the body of PUT /v1/reports/{id}/status.
type statusRequest struct {
	Status string `json:"status" validate:"required,oneof=open in_progress resolved"`
}

// createInputFromJSON reads the body of a new report.
func createInputFromJSON(c *gin.Context, binder *httpx.Binder) (usecase.CreateInput, error) {
	var req createRequest
	if err := binder.JSON(c, &req); err != nil {
		return usecase.CreateInput{}, err
	}
	kind, err := domain.ParseKind(req.Type)
	if err != nil {
		return usecase.CreateInput{}, apperr.Invalid(apperr.Field{Name: "type", Rule: apperr.RuleOneOf})
	}
	subject, body := strings.TrimSpace(req.Subject), strings.TrimSpace(req.Body)
	if err := requireText(map[string]string{"subject": subject, "body": body}); err != nil {
		return usecase.CreateInput{}, err
	}
	return usecase.CreateInput{Kind: kind, RoomID: req.RoomID, Subject: subject, Body: body}, nil
}

// replyBodyFromJSON reads the text of a message.
func replyBodyFromJSON(c *gin.Context, binder *httpx.Binder) (string, error) {
	var req replyRequest
	if err := binder.JSON(c, &req); err != nil {
		return "", err
	}
	body := strings.TrimSpace(req.Body)
	return body, requireText(map[string]string{"body": body})
}

// statusFromJSON reads the status to set.
func statusFromJSON(c *gin.Context, binder *httpx.Binder) (domain.Status, error) {
	var req statusRequest
	if err := binder.JSON(c, &req); err != nil {
		return "", err
	}
	status, err := domain.ParseStatus(req.Status)
	if err != nil {
		return "", apperr.Invalid(apperr.Field{Name: "status", Rule: apperr.RuleOneOf})
	}
	return status, nil
}

// requireText fails for a field that is blank once trimmed.
func requireText(texts map[string]string) error {
	var fields []apperr.Field
	for _, name := range []string{"subject", "body"} {
		if text, ok := texts[name]; ok && text == "" {
			fields = append(fields, apperr.Field{Name: name, Rule: apperr.RuleRequired})
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return apperr.Invalid(fields...)
}

// filterFromQuery reads ?status, ?type and ?search of a list.
func filterFromQuery(c *gin.Context) (domain.Filter, error) {
	var filter domain.Filter
	var fields []apperr.Field
	statuses := make([]string, 0, 3)
	for _, s := range domain.Statuses() {
		statuses = append(statuses, string(s))
	}
	if raw, field := httpx.QueryOneOf(c, "status", statuses...); field != nil {
		fields = append(fields, *field)
	} else if raw != nil {
		status := domain.Status(*raw)
		filter.Status = &status
	}
	kinds := make([]string, 0, 2)
	for _, k := range domain.Kinds() {
		kinds = append(kinds, string(k))
	}
	if raw, field := httpx.QueryOneOf(c, "type", kinds...); field != nil {
		fields = append(fields, *field)
	} else if raw != nil {
		kind := domain.Kind(*raw)
		filter.Kind = &kind
	}
	if search := strings.TrimSpace(c.Query("search")); len([]rune(search)) > 100 {
		fields = append(fields, apperr.Field{Name: "search", Rule: apperr.RuleMaxString, Params: map[string]string{"max": "100"}})
	} else {
		filter.Search = search
	}
	if len(fields) > 0 {
		return domain.Filter{}, apperr.Invalid(fields...)
	}
	return filter, nil
}
