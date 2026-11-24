package notificationhttp

import (
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/notification"
)

// bulkRequest is the body of POST /v1/notifications/bulk. The action is checked against the domain's set, not a tag.
type bulkRequest struct {
	Action string   `json:"action" validate:"required"`
	IDs    []string `json:"ids" validate:"required,min=1,max=100,dive,uuid"`
}

// parse reads the action and the ids.
func (r bulkRequest) parse() (domain.BulkAction, []uuid.UUID, error) {
	action, ok := domain.ParseBulkAction(r.Action)
	if !ok {
		return "", nil, apperr.Invalid(apperr.Field{Name: "action", Rule: apperr.RuleOneOf})
	}
	ids := make([]uuid.UUID, 0, len(r.IDs))
	for _, raw := range r.IDs {
		id, err := uuid.Parse(raw)
		if err != nil { // the validator already checked; this keeps a bad value from becoming the nil id
			return "", nil, apperr.Invalid(apperr.Field{Name: "ids", Rule: apperr.RuleUUID})
		}
		ids = append(ids, id)
	}
	return action, ids, nil
}
