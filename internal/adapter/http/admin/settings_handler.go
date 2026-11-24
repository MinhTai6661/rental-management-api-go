package adminhttp

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/admin"
	usecase "rental-management/internal/usecase/admin"
)

// SettingsHandler serves the super admin's system settings.
type SettingsHandler struct {
	uc     *usecase.Settings
	binder *httpx.Binder
}

// NewSettingsHandler returns the handler.
func NewSettingsHandler(uc *usecase.Settings, binder *httpx.Binder) *SettingsHandler {
	return &SettingsHandler{uc: uc, binder: binder}
}

// SettingView is one setting with the value in force.
type SettingView struct {
	Key        string  `json:"key"`
	Group      string  `json:"group"`
	Type       string  `json:"type"`
	Min        float64 `json:"min"`
	Max        float64 `json:"max"`
	Default    float64 `json:"default"`
	Value      float64 `json:"value"`
	Overridden bool    `json:"overridden"`
}

// SettingsView is every setting and the version of the overrides.
type SettingsView struct {
	Settings []SettingView `json:"settings"`
	Version  string        `json:"version"`
}

func settingsView(l usecase.SettingList, defaults func(domain.Definition) float64) SettingsView {
	out := SettingsView{Settings: make([]SettingView, len(l.Items)), Version: l.Version}
	for i, it := range l.Items {
		d := it.Definition
		out.Settings[i] = SettingView{
			Key: d.Key, Group: d.Group, Type: string(d.Kind), Min: d.Min, Max: d.Max, Default: defaults(d),
			Value: it.Value, Overridden: it.Overridden,
		}
	}
	return out
}

// List answers GET /v1/admin/settings. The settings are a fixed small catalog, so they are not paged.
func (h *SettingsHandler) List(c *gin.Context) {
	l, err := h.uc.List(c.Request.Context())
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, settingsView(l, h.uc.DefaultOf))
}

type settingsRequest struct {
	Settings []struct {
		Key   string   `json:"key" validate:"required,max=100"`
		Value *float64 `json:"value"`
	} `json:"settings" validate:"required,min=1,max=50,dive"`
}

// Update answers PUT /v1/admin/settings {settings: [{key, value}]}: a null value puts the setting back to its default.
func (h *SettingsHandler) Update(c *gin.Context) {
	actor, ok := actorOf(c)
	if !ok {
		return
	}
	var req settingsRequest
	if err := h.binder.JSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	changes := make([]domain.SettingChange, len(req.Settings))
	for i, s := range req.Settings {
		changes[i] = domain.SettingChange{Key: s.Key, Value: s.Value}
	}
	l, err := h.uc.Update(c.Request.Context(), actor, changes)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	httpx.OK(c, settingsView(l, h.uc.DefaultOf))
}

type settingChangeView struct {
	Key       string     `json:"key"`
	From      *float64   `json:"from"`
	To        *float64   `json:"to"`
	ChangedAt time.Time  `json:"changed_at"`
	ChangedBy *changedBy `json:"changed_by"`
}

type changedBy struct {
	ID   uuid.UUID `json:"id"`
	Name *string   `json:"name"`
}

// History answers GET /v1/admin/settings/history (?page, ?per_page): who changed which setting from what to what,
// newest first. It is a feed from the audit trail, so it says whether another page follows rather than counting.
func (h *SettingsHandler) History(c *gin.Context) {
	page, err := httpx.ParsePage(c, 20)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.uc.History(c.Request.Context(), page.Page, page.PerPage)
	if err != nil {
		httpx.Fail(c, mapError(err))
		return
	}
	out := make([]settingChangeView, len(result.Items))
	for i, it := range result.Items {
		out[i] = settingChangeView{Key: it.Key, From: it.From, To: it.To, ChangedAt: it.At}
		if it.ActorID != nil {
			name := it.ActorName
			var shown *string
			if name != "" {
				shown = &name
			}
			out[i].ChangedBy = &changedBy{ID: *it.ActorID, Name: shown}
		}
	}
	httpx.List(c, out, httpx.FeedMeta(page, result.HasMore))
}
