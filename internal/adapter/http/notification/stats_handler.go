package notificationhttp

import (
	"time"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/notification"
	usecase "rental-management/internal/usecase/notification"
)

// StatsHandler serves the statistics of announcements (/admin/announcements/stats and the landlord's twin).
type StatsHandler struct{ uc *usecase.Statistics }

// NewStatsHandler returns the handler.
func NewStatsHandler(uc *usecase.Statistics) *StatsHandler { return &StatsHandler{uc: uc} }

type statsRangeView struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type statsMessagesView struct {
	Total            int `json:"total"`
	WithNotification int `json:"with_notification"`
	WithPopup        int `json:"with_popup"`
}

type notificationTotalsView struct {
	Recipients int      `json:"recipients"`
	Read       int      `json:"read"`
	ReadRate   *float64 `json:"read_rate"`
}

type popupTotalsView struct {
	Viewers        int      `json:"viewers"`
	Views          int      `json:"views"`
	ViewsPerViewer *float64 `json:"views_per_viewer"`
	Closed         int      `json:"closed"`
	Clicked        int      `json:"clicked"`
	CloseRate      *float64 `json:"close_rate"`
	ClickRate      *float64 `json:"click_rate"`
}

// statsTotalsView holds the figures of the channels asked for: a channel that was not asked for has no fields at all.
type statsTotalsView struct {
	*notificationTotalsView
	*popupTotalsView
}

type popupDayView struct {
	Viewers int `json:"viewers"`
	Closed  int `json:"closed"`
	Clicked int `json:"clicked"`
}

type readDayView struct {
	Read int `json:"read"`
}

type statsDayView struct {
	Date string `json:"date"`
	*popupDayView
	*readDayView
}

type statsView struct {
	Range    statsRangeView     `json:"range"`
	Messages *statsMessagesView `json:"messages,omitempty"`
	Totals   statsTotalsView    `json:"totals"`
	Series   []statsDayView     `json:"series"`
}

func statsViewOf(report usecase.StatsReport) statsView {
	view := statsView{Range: statsRangeView{From: report.First.Format("2006-01-02"), To: report.Last.Format("2006-01-02")}, Series: make([]statsDayView, 0, len(report.Stats.Series))}
	if m := report.Stats.Messages; m != nil {
		view.Messages = &statsMessagesView{Total: m.Total, WithNotification: m.WithNotification, WithPopup: m.WithPopup}
	}
	if n := report.Stats.Totals.Notification; n != nil {
		view.Totals.notificationTotalsView = &notificationTotalsView{Recipients: n.Recipients, Read: n.Read, ReadRate: n.ReadRate}
	}
	if p := report.Stats.Totals.Popup; p != nil {
		view.Totals.popupTotalsView = &popupTotalsView{
			Viewers: p.Viewers, Views: p.Views, ViewsPerViewer: p.ViewsPerViewer, Closed: p.Closed, Clicked: p.Clicked, CloseRate: p.CloseRate, ClickRate: p.ClickRate,
		}
	}
	for _, point := range report.Stats.Series {
		day := statsDayView{Date: point.Day.Format("2006-01-02")}
		if report.Stats.Totals.Popup != nil {
			day.popupDayView = &popupDayView{Viewers: point.Viewers, Closed: point.Closed, Clicked: point.Clicked}
		}
		if report.Stats.Totals.Notification != nil {
			day.readDayView = &readDayView{Read: point.Read}
		}
		view.Series = append(view.Series, day)
	}
	return view
}

// daysOf reads ?from, ?to (days, YYYY-MM-DD, in ICT) and ?channel.
func daysOf(ginCtx *gin.Context) (from, to *time.Time, channel *domain.Channel, err error) {
	var fields []apperr.Field
	for _, detail := range []struct {
		name string
		dst  **time.Time
	}{{"from", &from}, {"to", &to}} {
		raw := ginCtx.Query(detail.name)
		if raw == "" {
			continue
		}
		day, parseErr := time.ParseInLocation("2006-01-02", raw, ict)
		if parseErr != nil {
			fields = append(fields, apperr.Field{Name: detail.name, Rule: apperr.RuleDate})
			continue
		}
		*detail.dst = &day
	}
	if raw := ginCtx.Query("channel"); raw != "" {
		if ch, ok := domain.ParseChannel(raw); ok {
			channel = &ch
		} else {
			fields = append(fields, apperr.Field{Name: "channel", Rule: apperr.RuleOneOf})
		}
	}
	if len(fields) > 0 {
		return nil, nil, nil, apperr.Invalid(fields...)
	}
	return from, to, channel, nil
}

// Overview answers GET /v1/{admin,my}/announcements/stats (?from, ?to, ?channel).
func (handler *StatsHandler) Overview(ginCtx *gin.Context) {
	sender, ok := senderOf(ginCtx)
	if !ok {
		return
	}
	from, to, channel, err := daysOf(ginCtx)
	if err != nil {
		httpx.Fail(ginCtx, err)
		return
	}
	report, err := handler.uc.Overview(ginCtx.Request.Context(), sender, from, to, channel)
	if err != nil {
		httpx.Fail(ginCtx, mapError(err))
		return
	}
	httpx.OK(ginCtx, statsViewOf(report))
}

// Detail answers GET /v1/{admin,my}/announcements/{id}/stats.
func (handler *StatsHandler) Detail(ginCtx *gin.Context) {
	sender, id, ok := target(ginCtx)
	if !ok {
		return
	}
	from, to, channel, err := daysOf(ginCtx)
	if err != nil {
		httpx.Fail(ginCtx, err)
		return
	}
	report, err := handler.uc.Detail(ginCtx.Request.Context(), sender, id, from, to, channel)
	if err != nil {
		httpx.Fail(ginCtx, mapError(err))
		return
	}
	httpx.OK(ginCtx, statsViewOf(report))
}
