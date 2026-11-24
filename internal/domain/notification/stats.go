package notification

import (
	"math"
	"time"

	"github.com/google/uuid"
)

// The day ranges of the statistics.
const (
	// MaxStatDays is the longest range one request may ask for (more is a mistake, or an attempt to make the server count
	// for ever).
	MaxStatDays = 92
	// DefaultStatDays is the range a screen opens with.
	DefaultStatDays = 30
)

// ResolveDays is the first and the last day a request asks for (both at the start of their day, in the day's own zone):
// the days it gave, or the last DefaultStatDays days up to the end day (today when none). today must be the start of
// today.
func ResolveDays(from, to *time.Time, today time.Time) (first, last time.Time) {
	last = today
	if to != nil {
		last = *to
	}
	first = last.AddDate(0, 0, -(DefaultStatDays - 1))
	if from != nil {
		first = *from
	}
	return first, last
}

// Days lists the days from first to last, oldest first. A backwards range is ErrRangeBackwards and a range of more than
// MaxStatDays days is ErrRangeTooLong.
func Days(first, last time.Time) ([]time.Time, error) {
	if first.After(last) {
		return nil, ErrRangeBackwards
	}
	var days []time.Time
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		days = append(days, d)
		if len(days) > MaxStatDays {
			return nil, ErrRangeTooLong
		}
	}
	return days, nil
}

// Percent is part of whole as a percentage with one decimal; nil when there is no whole (a rate of nothing is not 0).
func Percent(part, whole int) *float64 {
	if whole <= 0 {
		return nil
	}
	v := math.Round(float64(part)/float64(whole)*1000) / 10
	return &v
}

// PerViewer is views per viewer with one decimal; nil without viewers.
func PerViewer(views, viewers int) *float64 {
	if viewers <= 0 {
		return nil
	}
	v := math.Round(float64(views)/float64(viewers)*10) / 10
	return &v
}

// StatsQuery says whose messages to count and when. With AnnouncementID it is that one message (whatever its state);
// otherwise the messages put out and not recalled of the platform (LandlordID nil) or of a landlord. The cohort is the
// messages published within [CohortFrom, CohortTo); the series count the events of the days First to Last (each a day
// start in ICT) over every message of the scope.
type StatsQuery struct {
	AnnouncementID       *uuid.UUID
	LandlordID           *uuid.UUID
	CohortFrom, CohortTo time.Time
	First, Last          time.Time
	// Channel narrows the figures; nil asks for both.
	Channel *Channel
}

// WantsNotification says whether the notification channel is asked for.
func (q StatsQuery) WantsNotification() bool {
	return q.Channel == nil || *q.Channel == ChannelNotification
}

// WantsPopup says whether the popup channel is asked for.
func (q StatsQuery) WantsPopup() bool { return q.Channel == nil || *q.Channel == ChannelPopup }

// Messages counts the messages of a cohort; Recipients is the sum of what the notifications went to.
type Messages struct{ Total, WithNotification, WithPopup, Recipients int }

// NotificationTotals are what recipients did with the notification.
type NotificationTotals struct {
	Recipients, Read int
	ReadRate         *float64
}

// PopupTotals are what people did with the popup.
type PopupTotals struct {
	Viewers, Views, Closed, Clicked int
	ViewsPerViewer                  *float64
	CloseRate, ClickRate            *float64
}

// Totals are the figures of the channels asked for (nil for a channel not asked for).
type Totals struct {
	Notification *NotificationTotals
	Popup        *PopupTotals
}

// DayPoint is what happened on one day: people who first saw the popup, closed it, pressed its button, and
// notifications read.
type DayPoint struct {
	Day                            time.Time
	Viewers, Closed, Clicked, Read int
}

// Stats is the statistics of a set of messages.
type Stats struct {
	// Messages is set for the overview only.
	Messages *Messages
	Totals   Totals
	Series   []DayPoint
}
