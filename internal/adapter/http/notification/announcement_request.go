package notificationhttp

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/notification"
)

// targetingRequest is the criteria of a platform message.
type targetingRequest struct {
	Roles                 []string `json:"roles"`
	ProvinceCodes         []int    `json:"province_codes"`
	WardCodes             []int    `json:"ward_codes"`
	PlanCodes             []string `json:"plan_codes"`
	RegisteredWithinDays  *int     `json:"registered_within_days"`
	RegisteredAtLeastDays *int     `json:"registered_at_least_days"`
	ActiveWithinDays      *int     `json:"active_within_days"`
	RegisteredBefore      *string  `json:"registered_before"`
	UserIDs               []string `json:"user_ids"`
}

// announcementRequest is the body of POST and PUT /v1/{admin,my}/announcements. A PUT is a full replacement: what is
// not sent goes back to its default (type notice, notification on, popup off, once, priority 0, medium).
type announcementRequest struct {
	Audience         string            `json:"audience" validate:"required"`
	Targeting        *targetingRequest `json:"targeting"`
	PeopleIDs        []string          `json:"people_ids" validate:"omitempty,max=200,dive,uuid"`
	Type             *string           `json:"type"`
	Title            map[string]string `json:"title"`
	Body             map[string]string `json:"body"`
	SendNotification *bool             `json:"send_notification"`
	ShowPopup        *bool             `json:"show_popup"`
	StartsAt         *time.Time        `json:"starts_at"`
	EndsAt           *time.Time        `json:"ends_at"`
	Frequency        *string           `json:"frequency"`
	FrequencyDays    *int              `json:"frequency_days"`
	MaxPerPeriod     *int              `json:"max_per_period"`
	MaxTotal         *int              `json:"max_total"`
	Priority         *int              `json:"priority"`
	PopupSize        *string           `json:"popup_size"`
	CTALabel         map[string]string `json:"cta_label"`
	CTAURL           *string           `json:"cta_url"`
	Publish          *bool             `json:"publish"`
	Reshow           bool              `json:"reshow"`
}

// reachRequest is the body of POST /v1/{admin,my}/announcements/reach: an audience to count, nothing is saved.
type reachRequest struct {
	Audience  string            `json:"audience" validate:"required"`
	Targeting *targetingRequest `json:"targeting"`
	PeopleIDs []string          `json:"people_ids" validate:"omitempty,max=200,dive,uuid"`
}

// parsed is a request read into the domain's words.
type parsed struct {
	content domain.Content
	publish bool
	reshow  bool
}

// parse reads the request. defaultPublish is what "publish" means when absent (a create puts the message out, an
// update does not). The enums are checked against the domain's sets, so there is one list of values.
func (requestBody announcementRequest) parse(defaultPublish bool) (parsed, error) {
	var fields []apperr.Field
	bad := func(name, rule string) { fields = append(fields, apperr.Field{Name: name, Rule: rule}) }

	scope, ok := domain.ParseScope(requestBody.Audience)
	if !ok {
		bad("audience", apperr.RuleOneOf)
	}
	content := domain.Content{
		Scope: scope, Type: domain.TypeNotice, SendNotification: true, Frequency: domain.FrequencyOnce, PopupSize: domain.SizeMedium,
		Title: textsOf(requestBody.Title), Body: textsOf(requestBody.Body), CTALabel: textsOf(requestBody.CTALabel),
		StartsAt: requestBody.StartsAt, EndsAt: requestBody.EndsAt, FrequencyDays: requestBody.FrequencyDays, MaxPerPeriod: requestBody.MaxPerPeriod, MaxTotal: requestBody.MaxTotal,
	}
	if requestBody.SendNotification != nil {
		content.SendNotification = *requestBody.SendNotification
	}
	if requestBody.ShowPopup != nil {
		content.ShowPopup = *requestBody.ShowPopup
	}
	if requestBody.Priority != nil {
		content.Priority = *requestBody.Priority
	}
	if requestBody.CTAURL != nil {
		content.CTAURL = strings.TrimSpace(*requestBody.CTAURL)
	}
	if requestBody.Type != nil {
		if content.Type, ok = domain.ParseType(*requestBody.Type); !ok {
			bad("type", apperr.RuleOneOf)
		}
	}
	if requestBody.Frequency != nil {
		if content.Frequency, ok = domain.ParseFrequency(*requestBody.Frequency); !ok {
			bad("frequency", apperr.RuleOneOf)
		}
	}
	if requestBody.PopupSize != nil {
		if content.PopupSize, ok = domain.ParsePopupSize(*requestBody.PopupSize); !ok {
			bad("popup_size", apperr.RuleOneOf)
		}
	}
	criteria, people, more := audienceOf(requestBody.Targeting, requestBody.PeopleIDs)
	fields = append(fields, more...)
	content.Criteria, content.PeopleIDs = criteria, people
	if len(fields) > 0 {
		return parsed{}, apperr.Invalid(fields...)
	}
	parsedRequest := parsed{content: content, publish: defaultPublish, reshow: requestBody.Reshow}
	if requestBody.Publish != nil {
		parsedRequest.publish = *requestBody.Publish
	}
	return parsedRequest, nil
}

// parse reads the audience to count.
func (requestBody reachRequest) parse() (domain.Scope, domain.Criteria, []uuid.UUID, error) {
	var fields []apperr.Field
	scope, ok := domain.ParseScope(requestBody.Audience)
	if !ok {
		fields = append(fields, apperr.Field{Name: "audience", Rule: apperr.RuleOneOf})
	}
	criteria, people, more := audienceOf(requestBody.Targeting, requestBody.PeopleIDs)
	fields = append(fields, more...)
	if len(fields) > 0 {
		return "", domain.Criteria{}, nil, apperr.Invalid(fields...)
	}
	return scope, criteria, people, nil
}

// audienceOf reads the criteria and the people picked; the second result lists the fields that did not parse.
func audienceOf(targeting *targetingRequest, peopleIDs []string) (domain.Criteria, []uuid.UUID, []apperr.Field) {
	var fields []apperr.Field
	people, ok := uuidsOf(peopleIDs)
	if !ok {
		fields = append(fields, apperr.Field{Name: "people_ids", Rule: apperr.RuleUUID})
	}
	var content domain.Criteria
	if targeting != nil {
		content = domain.Criteria{
			Roles: targeting.Roles, ProvinceCodes: targeting.ProvinceCodes, WardCodes: targeting.WardCodes, PlanCodes: targeting.PlanCodes,
			RegisteredWithinDays: targeting.RegisteredWithinDays, RegisteredAtLeastDays: targeting.RegisteredAtLeastDays, ActiveWithinDays: targeting.ActiveWithinDays,
		}
		if targeting.RegisteredBefore != nil && *targeting.RegisteredBefore != "" {
			day, err := time.Parse("2006-01-02", *targeting.RegisteredBefore)
			if err != nil {
				fields = append(fields, apperr.Field{Name: "targeting.registered_before", Rule: apperr.RuleDate})
			} else {
				content.RegisteredBefore = &day
			}
		}
		ids, ok := uuidsOf(targeting.UserIDs)
		if !ok {
			fields = append(fields, apperr.Field{Name: "targeting.user_ids", Rule: apperr.RuleUUID})
		}
		content.UserIDs = ids
	}
	return content, people, fields
}

func uuidsOf(raw []string) ([]uuid.UUID, bool) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, text := range raw {
		id, err := uuid.Parse(text)
		if err != nil {
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

// textsOf keeps the texts of the supported languages, trimmed, and drops the empty ones.
func textsOf(in map[string]string) domain.Texts {
	out := domain.Texts{}
	for _, locale := range domain.Locales() {
		if text := strings.TrimSpace(in[locale]); text != "" {
			out[locale] = text
		}
	}
	return out
}
