package notification

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validContent() Content {
	return Content{
		Scope: ScopeTenants, Type: TypeNotice, SendNotification: true, Frequency: FrequencyOnce, PopupSize: SizeMedium,
		Title: Texts{"vi": "Chào", "en": "Hi"}, Body: Texts{"vi": "Nội dung", "en": "Text"},
	}
}

// rulesOf returns "field:rule" for every violation of the content.
func rulesOf(t *testing.T, c Content, s Sender) map[string]bool {
	t.Helper()
	err := ValidateContent(c, s)
	out := map[string]bool{}
	if err == nil {
		return out
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("%T", err)
	}
	for _, v := range ve.Violations {
		out[v.Name+":"+v.Rule] = true
	}
	return out
}

func TestValidateContentAcceptsAGoodMessage(t *testing.T) {
	if got := rulesOf(t, validContent(), admin); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

func TestValidateContentRules(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	cases := map[string]struct {
		mutate func(*Content)
		want   string
	}{
		"title without body":  {func(c *Content) { c.Body = Texts{"en": "Text"} }, "body.vi:title_body_pair"},
		"body without title":  {func(c *Content) { c.Title = Texts{"en": "Hi"} }, "title.vi:title_body_pair"},
		"no language":         {func(c *Content) { c.Title, c.Body = nil, nil }, "title:one_language"},
		"title too long":      {func(c *Content) { c.Title["vi"] = long(151) }, "title.vi:max_string"},
		"body too long":       {func(c *Content) { c.Body["en"] = long(2001) }, "body.en:max_string"},
		"nowhere to go":       {func(c *Content) { c.SendNotification = false }, "send_notification:nowhere_to_go"},
		"label without url":   {func(c *Content) { c.CTALabel = Texts{"vi": "Mở"} }, "cta_url:cta_incomplete"},
		"url without label":   {func(c *Content) { c.CTAURL = "/find-rooms" }, "cta_url:cta_incomplete"},
		"unsafe url":          {func(c *Content) { c.CTALabel, c.CTAURL = Texts{"vi": "Mở"}, "javascript:alert(1)" }, "cta_url:url_unsafe"},
		"label too long":      {func(c *Content) { c.CTALabel, c.CTAURL = Texts{"vi": long(41)}, "/x" }, "cta_label.vi:max_string"},
		"every_days no days":  {func(c *Content) { c.Frequency = FrequencyEveryDays }, "frequency_days:required"},
		"days out of range":   {func(c *Content) { c.Frequency, c.FrequencyDays = FrequencyEveryDays, intp(400) }, "frequency_days:max_number"},
		"max per period zero": {func(c *Content) { c.MaxPerPeriod = intp(0) }, "max_per_period:min_number"},
		"max total too big":   {func(c *Content) { c.MaxTotal = intp(1001) }, "max_total:max_number"},
		"priority too big":    {func(c *Content) { c.Priority = 11 }, "priority:max_number"},
		"ends before start": {func(c *Content) {
			s := time.Now()
			e := s.Add(-time.Hour)
			c.StartsAt, c.EndsAt = &s, &e
		}, "ends_at:after_start_date"},
		"role not reachable": {func(c *Content) { c.Scope, c.Criteria = ScopeCustom, Criteria{Roles: []string{"super_admin"}} }, "targeting.roles.0:oneof"},
		"custom empty":       {func(c *Content) { c.Scope = ScopeCustom }, "targeting:audience_empty"},
		"picked no people":   {func(c *Content) { c.Scope = ScopeSelectedPeople }, "people_ids:required"},
		"picked too many": {func(c *Content) {
			c.Scope = ScopeSelectedPeople
			for range 201 {
				c.PeopleIDs = append(c.PeopleIDs, uuid.New())
			}
		}, "people_ids:max_items"},
	}
	for name, tc := range cases {
		c := validContent()
		tc.mutate(&c)
		sender := admin
		if c.Scope == ScopeSelectedPeople {
			sender = landlord
		}
		if got := rulesOf(t, c, sender); !got[tc.want] {
			t.Errorf("%s: want %s, got %v", name, tc.want, got)
		}
	}
}

func TestValidateContentSuperAdminMayNameAdmins(t *testing.T) {
	c := validContent()
	c.Scope, c.Criteria = ScopeCustom, Criteria{Roles: []string{"admin"}}
	if got := rulesOf(t, c, super); len(got) != 0 {
		t.Errorf("%v", got)
	}
	if got := rulesOf(t, c, admin); !got["targeting.roles.0:oneof"] {
		t.Errorf("an admin may not name admins: %v", got)
	}
}

func TestSafeURL(t *testing.T) {
	for u, want := range map[string]bool{
		"/find-rooms": true, "//evil.com": false, "https://x.vn": true, "http://x.vn": false, "javascript:alert(1)": false, "": false, "https://": false, "/": true,
	} {
		if got := SafeURL(u); got != want {
			t.Errorf("%q: %v", u, got)
		}
	}
}
