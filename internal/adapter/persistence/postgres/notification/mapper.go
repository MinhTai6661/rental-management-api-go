package notificationpg

import (
	"encoding/json"
	"fmt"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/notification"
)

// notificationOf turns a row into the domain message. A row whose params are not an object of strings is a data error
// (the column is checked to be an object, and only Insert writes it).
func notificationOf(row sqlcgen.Notification) (domain.Notification, error) {
	params := map[string]string{}
	if len(row.Params) > 0 {
		if err := json.Unmarshal(row.Params, &params); err != nil {
			return domain.Notification{}, fmt.Errorf("params of notification %s: %w", row.ID, err)
		}
	}
	n := domain.Notification{
		ID: row.ID, UserID: row.UserID, Kind: domain.Kind(row.Kind), Params: params, ReadAt: row.ReadAt, CreatedAt: row.CreatedAt,
		AnnouncementID: row.AnnouncementID,
	}
	if row.Url != nil {
		n.URL = *row.Url
	}
	if row.AudienceRole != nil {
		n.Audience = domain.Audience(*row.AudienceRole)
	}
	return n, nil
}
