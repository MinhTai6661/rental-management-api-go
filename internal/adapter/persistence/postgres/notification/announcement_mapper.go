package notificationpg

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
	domain "rental-management/internal/domain/notification"
)

// criteriaJSON is how the criteria are stored in announcements.targeting. The domain type carries no tags; this
// adapter-private shape does, and keeps the stored JSON stable.
type criteriaJSON struct {
	Roles                 []string    `json:"roles,omitempty"`
	ProvinceCodes         []int       `json:"province_codes,omitempty"`
	WardCodes             []int       `json:"ward_codes,omitempty"`
	PlanCodes             []string    `json:"plan_codes,omitempty"`
	RegisteredWithinDays  *int        `json:"registered_within_days,omitempty"`
	RegisteredAtLeastDays *int        `json:"registered_at_least_days,omitempty"`
	ActiveWithinDays      *int        `json:"active_within_days,omitempty"`
	RegisteredBefore      *string     `json:"registered_before,omitempty"`
	UserIDs               []uuid.UUID `json:"user_ids,omitempty"`
}

const dateLayout = "2006-01-02"

// criteriaBytes is the JSON to store; nil (NULL) when no criterion is set.
func criteriaBytes(c domain.Criteria) ([]byte, error) {
	if c.IsEmpty() {
		return nil, nil
	}
	j := criteriaJSON{
		Roles: c.Roles, ProvinceCodes: c.ProvinceCodes, WardCodes: c.WardCodes, PlanCodes: c.PlanCodes,
		RegisteredWithinDays: c.RegisteredWithinDays, RegisteredAtLeastDays: c.RegisteredAtLeastDays,
		ActiveWithinDays: c.ActiveWithinDays, UserIDs: c.UserIDs,
	}
	if c.RegisteredBefore != nil {
		d := c.RegisteredBefore.Format(dateLayout)
		j.RegisteredBefore = &d
	}
	return json.Marshal(j)
}

func criteriaOf(raw []byte) (domain.Criteria, error) {
	if len(raw) == 0 {
		return domain.Criteria{}, nil
	}
	var j criteriaJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return domain.Criteria{}, err
	}
	c := domain.Criteria{
		Roles: j.Roles, ProvinceCodes: j.ProvinceCodes, WardCodes: j.WardCodes, PlanCodes: j.PlanCodes,
		RegisteredWithinDays: j.RegisteredWithinDays, RegisteredAtLeastDays: j.RegisteredAtLeastDays,
		ActiveWithinDays: j.ActiveWithinDays, UserIDs: j.UserIDs,
	}
	if j.RegisteredBefore != nil {
		t, err := time.Parse(dateLayout, *j.RegisteredBefore)
		if err != nil {
			return domain.Criteria{}, err
		}
		c.RegisteredBefore = &t
	}
	return c, nil
}

// textsBytes is the JSON of a text per language; never "null" (the columns refuse it), so nil becomes {}.
func textsBytes(t domain.Texts) ([]byte, error) {
	if t == nil {
		t = domain.Texts{}
	}
	return json.Marshal(t)
}

// optionalTextsBytes is NULL for no text at all.
func optionalTextsBytes(t domain.Texts) ([]byte, error) {
	if len(t) == 0 {
		return nil, nil
	}
	return json.Marshal(t)
}

func textsOf(raw []byte) (domain.Texts, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var t domain.Texts
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	return t, nil
}

func int16Ptr(n *int) *int16 {
	if n == nil {
		return nil
	}
	v := int16(*n) //nolint:gosec // the domain and a CHECK keep it within 1..1000
	return &v
}

func intPtr(n *int16) *int {
	if n == nil {
		return nil
	}
	v := int(*n)
	return &v
}

func int32s(in []int) []int32 {
	if in == nil {
		return nil
	}
	out := make([]int32, len(in))
	for i, n := range in {
		out[i] = int32(n) //nolint:gosec // administrative codes fit an int32
	}
	return out
}

// announcementOf turns a row into the domain message.
func announcementOf(row sqlcgen.Announcement) (domain.Announcement, error) {
	scope, ok := domain.ParseScope(row.Audience)
	if !ok {
		return domain.Announcement{}, fmt.Errorf("announcement %s has audience %q", row.ID, row.Audience)
	}
	criteria, err := criteriaOf(row.Targeting)
	if err != nil {
		return domain.Announcement{}, fmt.Errorf("targeting of announcement %s: %w", row.ID, err)
	}
	title, err := textsOf(row.Title)
	if err != nil {
		return domain.Announcement{}, fmt.Errorf("title of announcement %s: %w", row.ID, err)
	}
	body, err := textsOf(row.Body)
	if err != nil {
		return domain.Announcement{}, fmt.Errorf("body of announcement %s: %w", row.ID, err)
	}
	label, err := textsOf(row.CtaLabel)
	if err != nil {
		return domain.Announcement{}, fmt.Errorf("cta of announcement %s: %w", row.ID, err)
	}
	a := domain.Announcement{
		ID: row.ID, CreatedByID: row.CreatedByID, SenderRole: row.SenderRole, LandlordID: row.LandlordID, Scope: scope,
		Criteria: criteria, Type: domain.Type(row.Type), Title: title, Body: body,
		SendNotification: row.SendNotification, ShowPopup: row.ShowPopup,
		PublishedAt: row.PublishedAt, StartsAt: row.StartsAt, EndsAt: row.EndsAt, Frequency: domain.Frequency(row.Frequency),
		FrequencyDays: intPtr(row.FrequencyDays), MaxPerPeriod: intPtr(row.MaxPerPeriod), MaxTotal: intPtr(row.MaxTotal),
		Priority: int(row.Priority), PopupSize: domain.PopupSize(row.PopupSize), CTALabel: label,
		Revision: int(row.Revision), RecipientCount: int(row.RecipientCount), SentAt: row.SentAt, RecalledAt: row.RecalledAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.CtaUrl != nil {
		a.CTAURL = *row.CtaUrl
	}
	return a, nil
}

// jsonObject is the JSON of a map of strings; a nil map is {} (the column refuses JSON null).
func jsonObject(m map[string]string) ([]byte, error) {
	if m == nil {
		m = map[string]string{}
	}
	return json.Marshal(m)
}

// imageOf turns a row into the domain picture.
func imageOf(row sqlcgen.AnnouncementImage) domain.Image {
	img := domain.Image{
		ID: row.ID, FileKey: row.FileKey, ContentType: row.ContentType, Width: int(row.Width), Height: int(row.Height),
		SizeBytes: int(row.SizeBytes), DisplayOrder: int(row.DisplayOrder),
	}
	if row.LinkUrl != nil {
		img.LinkURL = *row.LinkUrl
	}
	return img
}
