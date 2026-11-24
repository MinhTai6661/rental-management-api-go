package notificationhttp

import (
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
)

type personView struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"full_name"`
	Email    *string   `json:"email"`
	// Kind is what the person is to the landlord (only "tenant" for now).
	Kind string `json:"kind,omitempty"`
}

type senderView struct {
	Kind string      `json:"kind"`
	Role string      `json:"role"`
	User *personView `json:"user"`
}

type targetingView struct {
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

type ctaView struct {
	Label map[string]string `json:"label"`
	URL   string            `json:"url"`
}

type popupStatsView struct {
	Seen      int `json:"seen"`
	Dismissed int `json:"dismissed"`
	Clicked   int `json:"clicked"`
}

// announcementCommon is what every announcement answer carries.
type announcementCommon struct {
	ID               uuid.UUID         `json:"id"`
	Sender           senderView        `json:"sender"`
	Audience         string            `json:"audience"`
	Targeting        *targetingView    `json:"targeting"`
	Type             string            `json:"type"`
	Title            map[string]string `json:"title"`
	Body             map[string]string `json:"body"`
	SendNotification bool              `json:"send_notification"`
	ShowPopup        bool              `json:"show_popup"`
	Status           string            `json:"status"`
	PublishedAt      *time.Time        `json:"published_at"`
	StartsAt         *time.Time        `json:"starts_at"`
	EndsAt           *time.Time        `json:"ends_at"`
	Frequency        string            `json:"frequency"`
	FrequencyDays    *int              `json:"frequency_days"`
	MaxPerPeriod     *int              `json:"max_per_period"`
	MaxTotal         *int              `json:"max_total"`
	Priority         int               `json:"priority"`
	PopupSize        string            `json:"popup_size"`
	CTA              *ctaView          `json:"cta"`
	Revision         int               `json:"revision"`
	RecipientCount   int               `json:"recipient_count"`
	SentAt           *time.Time        `json:"sent_at"`
	RecalledAt       *time.Time        `json:"recalled_at"`
	ReadCount        int               `json:"read_count"`
	PopupStats       popupStatsView    `json:"popup_stats"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// imageView is one picture of a popup as its sender sees it.
type imageView struct {
	ID           uuid.UUID `json:"id"`
	URL          string    `json:"url"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	SizeBytes    int       `json:"size_bytes"`
	LinkURL      *string   `json:"link_url"`
	DisplayOrder int       `json:"display_order"`
}

func imageViewOf(img domain.Image, urls FileURLs) imageView {
	v := imageView{ID: img.ID, URL: urls.URL(img.FileKey), Width: img.Width, Height: img.Height, SizeBytes: img.SizeBytes, DisplayOrder: img.DisplayOrder}
	if img.LinkURL != "" {
		v.LinkURL = &img.LinkURL
	}
	return v
}

func imageViewsOf(images []domain.Image, urls FileURLs) []imageView {
	out := make([]imageView, len(images))
	for i, img := range images {
		out[i] = imageViewOf(img, urls)
	}
	return out
}

// announcementDetailView is one message in full: the people picked and the pictures.
type announcementDetailView struct {
	announcementCommon
	People []personView `json:"people"`
	Images []imageView  `json:"images"`
}

// announcementRowView is a row of a list.
type announcementRowView struct {
	announcementCommon
	ImageCount int `json:"image_count"`
}

func personViewOf(person domain.Person) personView {
	return personView{ID: person.ID, FullName: person.FullName, Email: person.Email}
}

func targetingViewOf(criteria domain.Criteria) *targetingView {
	if criteria.IsEmpty() {
		return nil
	}
	view := &targetingView{
		Roles: orEmpty(criteria.Roles), ProvinceCodes: orEmptyInts(criteria.ProvinceCodes), WardCodes: orEmptyInts(criteria.WardCodes), PlanCodes: orEmpty(criteria.PlanCodes),
		RegisteredWithinDays: criteria.RegisteredWithinDays, RegisteredAtLeastDays: criteria.RegisteredAtLeastDays, ActiveWithinDays: criteria.ActiveWithinDays,
		UserIDs: []string{},
	}
	if criteria.RegisteredBefore != nil {
		detail := criteria.RegisteredBefore.Format("2006-01-02")
		view.RegisteredBefore = &detail
	}
	for _, id := range criteria.UserIDs {
		view.UserIDs = append(view.UserIDs, id.String())
	}
	return view
}

func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func orEmptyInts(values []int) []int {
	if values == nil {
		return []int{}
	}
	return values
}

func textsView(texts domain.Texts) map[string]string {
	if texts == nil {
		return map[string]string{}
	}
	return texts
}

func commonOf(detail domain.AnnouncementDetail) announcementCommon {
	sender := senderView{Kind: string(detail.SenderKind()), Role: detail.SenderRole}
	if detail.CreatedBy != nil {
		person := personViewOf(*detail.CreatedBy)
		sender.User = &person
	}
	view := announcementCommon{
		ID: detail.ID, Sender: sender, Audience: string(detail.Scope), Targeting: targetingViewOf(detail.Criteria), Type: string(detail.Type),
		Title: textsView(detail.Title), Body: textsView(detail.Body), SendNotification: detail.SendNotification, ShowPopup: detail.ShowPopup,
		Status: string(detail.Status), PublishedAt: detail.PublishedAt, StartsAt: detail.StartsAt, EndsAt: detail.EndsAt,
		Frequency: string(detail.Frequency), FrequencyDays: detail.FrequencyDays, MaxPerPeriod: detail.MaxPerPeriod, MaxTotal: detail.MaxTotal,
		Priority: detail.Priority, PopupSize: string(detail.PopupSize), Revision: detail.Revision, RecipientCount: detail.RecipientCount,
		SentAt: detail.SentAt, RecalledAt: detail.RecalledAt, ReadCount: detail.ReadCount,
		PopupStats: popupStatsView{Seen: detail.PopupStats.Seen, Dismissed: detail.PopupStats.Dismissed, Clicked: detail.PopupStats.Clicked},
		CreatedAt:  detail.CreatedAt, UpdatedAt: detail.UpdatedAt,
	}
	if detail.CTAURL != "" {
		view.CTA = &ctaView{Label: textsView(detail.CTALabel), URL: detail.CTAURL}
	}
	return view
}

func detailViewOf(detail domain.AnnouncementDetail, urls FileURLs) announcementDetailView {
	view := announcementDetailView{announcementCommon: commonOf(detail), People: []personView{}, Images: imageViewsOf(detail.Images, urls)}
	for _, person := range detail.People {
		personView := personViewOf(person)
		personView.Kind = "tenant"
		view.People = append(view.People, personView)
	}
	return view
}

func rowViewsOf(rows []domain.AnnouncementDetail) []announcementRowView {
	out := make([]announcementRowView, 0, len(rows))
	for _, detail := range rows {
		out = append(out, announcementRowView{announcementCommon: commonOf(detail), ImageCount: len(detail.Images)})
	}
	return out
}
