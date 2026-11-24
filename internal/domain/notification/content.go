package notification

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Limits of what a sender writes.
const (
	MaxTitleChars    = 150
	MaxBodyChars     = 2000
	MaxCTALabelChars = 40
	MaxCTAURLChars   = 500
	MaxPeopleIDs     = 200
)

// Content is everything a sender writes for an announcement: who it is for, the texts and how it is shown.
type Content struct {
	Scope     Scope
	Criteria  Criteria
	PeopleIDs []uuid.UUID
	Type      Type
	Title     Texts
	Body      Texts
	// SendNotification and ShowPopup choose the channels; at least one is needed.
	SendNotification bool
	ShowPopup        bool
	StartsAt, EndsAt *time.Time
	Frequency        Frequency
	FrequencyDays    *int
	MaxPerPeriod     *int
	MaxTotal         *int
	Priority         int
	PopupSize        PopupSize
	CTALabel         Texts
	CTAURL           string
}

// SafeURL reports whether the address is a page of the app (starts with / but not //) or an https:// address. It keeps
// javascript:, data: and protocol-relative addresses out of buttons and links.
func SafeURL(u string) bool {
	if strings.HasPrefix(u, "/") {
		return !strings.HasPrefix(u, "//")
	}
	return strings.HasPrefix(u, "https://") && len(u) > len("https://")
}

// ValidateContent checks the rules of an announcement that do not need the database. The error is a *ValidationError
// naming every field that broke a rule. A scope that is not allowed for the sender is ErrScopeNotAllowed, which
// ResolveFilter reports.
func ValidateContent(c Content, sender Sender) error {
	var v []Violation
	add := func(name, rule string, params map[string]string) {
		v = append(v, Violation{Name: name, Rule: rule, Params: params})
	}

	filled := 0
	for _, l := range Locales() {
		hasTitle, hasBody := c.Title.Has(l), c.Body.Has(l)
		switch {
		case hasTitle && !hasBody:
			add("body."+l, RuleTitleBodyPair, nil)
		case hasBody && !hasTitle:
			add("title."+l, RuleTitleBodyPair, nil)
		case hasTitle && hasBody:
			filled++
		}
		if utf8.RuneCountInString(c.Title[l]) > MaxTitleChars {
			add("title."+l, RuleMaxString, map[string]string{"max": fmt.Sprint(MaxTitleChars)})
		}
		if utf8.RuneCountInString(c.Body[l]) > MaxBodyChars {
			add("body."+l, RuleMaxString, map[string]string{"max": fmt.Sprint(MaxBodyChars)})
		}
		if utf8.RuneCountInString(c.CTALabel[l]) > MaxCTALabelChars {
			add("cta_label."+l, RuleMaxString, map[string]string{"max": fmt.Sprint(MaxCTALabelChars)})
		}
	}
	if filled == 0 && len(v) == 0 {
		add("title", RuleOneLanguage, nil)
	}
	if !c.SendNotification && !c.ShowPopup {
		add("send_notification", RuleNowhereToGo, nil)
	}
	if c.CTALabel.Any() != (c.CTAURL != "") {
		add("cta_url", RuleCTAIncomplete, nil)
	}
	if c.CTAURL != "" {
		switch {
		case utf8.RuneCountInString(c.CTAURL) > MaxCTAURLChars:
			add("cta_url", RuleMaxString, map[string]string{"max": fmt.Sprint(MaxCTAURLChars)})
		case !SafeURL(c.CTAURL):
			add("cta_url", RuleURLUnsafe, nil)
		}
	}
	if c.Frequency == FrequencyEveryDays && c.FrequencyDays == nil {
		add("frequency_days", RuleRequired, nil)
	}
	v = rangeViolation(v, "frequency_days", c.FrequencyDays, 1, 365)
	v = rangeViolation(v, "max_per_period", c.MaxPerPeriod, 1, 100)
	v = rangeViolation(v, "max_total", c.MaxTotal, 1, 1000)
	prio := c.Priority
	v = rangeViolation(v, "priority", &prio, 0, 10)
	if c.StartsAt != nil && c.EndsAt != nil && !c.EndsAt.After(*c.StartsAt) {
		add("ends_at", RuleAfterStartDate, map[string]string{"other": "starts_at"})
	}
	if c.Scope == ScopeEveryone || c.Scope == ScopeCustom {
		reachable := ReachableRoles(sender.Role)
		for i, role := range c.Criteria.Roles {
			if !containsString(reachable, role) {
				add(fmt.Sprintf("targeting.roles.%d", i), RuleOneOf, nil)
			}
		}
	}
	if c.Scope == ScopeCustom && c.Criteria.Normalize().IsEmpty() {
		add("targeting", RuleAudienceEmpty, nil)
	}
	if c.Scope.IsPicked() {
		switch {
		case len(c.PeopleIDs) == 0:
			add("people_ids", RuleRequired, nil)
		case len(c.PeopleIDs) > MaxPeopleIDs:
			add("people_ids", RuleMaxItems, map[string]string{"max": fmt.Sprint(MaxPeopleIDs)})
		}
	}
	if len(v) == 0 {
		return nil
	}
	return Invalid(v...)
}

func rangeViolation(v []Violation, name string, n *int, lo, hi int) []Violation {
	switch {
	case n == nil:
	case *n < lo:
		v = append(v, Violation{Name: name, Rule: RuleMinNumber, Params: map[string]string{"min": fmt.Sprint(lo)}})
	case *n > hi:
		v = append(v, Violation{Name: name, Rule: RuleMaxNumber, Params: map[string]string{"max": fmt.Sprint(hi)}})
	}
	return v
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
