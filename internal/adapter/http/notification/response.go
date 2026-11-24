package notificationhttp

import (
	"time"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/notification"
)

// NotificationView is a message of the inbox. The API sends the kind and its params; the web translates them.
type NotificationView struct {
	ID        uuid.UUID         `json:"id"`
	Kind      string            `json:"kind"`
	Params    map[string]string `json:"params"`
	URL       *string           `json:"url"`
	ReadAt    *time.Time        `json:"read_at"`
	CreatedAt time.Time         `json:"created_at"`
}

func viewOf(n domain.Notification) NotificationView {
	v := NotificationView{ID: n.ID, Kind: string(n.Kind), Params: n.Params, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt}
	if v.Params == nil {
		v.Params = map[string]string{}
	}
	if n.URL != "" {
		url := n.URL
		v.URL = &url
	}
	return v
}

func viewsOf(rows []domain.Notification) []NotificationView {
	out := make([]NotificationView, 0, len(rows))
	for _, n := range rows {
		out = append(out, viewOf(n))
	}
	return out
}

// feedMeta is the paging of the inbox plus how many messages are unread.
type feedMeta struct {
	httpx.Meta
	UnreadCount int `json:"unread_count"`
}

// latestMeta is the "meta" of the bell: it is not paged, so it only has the unread count.
type latestMeta struct {
	UnreadCount int `json:"unread_count"`
}
