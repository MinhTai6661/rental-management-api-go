package notification

import (
	"time"

	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

// SenderKind says whose message an announcement is: the platform's (written by an admin or super admin) or a landlord's.
type SenderKind string

// The sender kinds.
const (
	SenderPlatform SenderKind = "platform"
	SenderLandlord SenderKind = "landlord"
)

// Scope is who an announcement goes to. The platform writes to everyone, to the landlords, to the tenants or to a custom
// audience (criteria); a landlord writes to their own tenants, to all their people or to some of them picked one by one.
type Scope string

// The scopes.
const (
	ScopeEveryone       Scope = "everyone"
	ScopeLandlords      Scope = "landlords"
	ScopeTenants        Scope = "tenants"
	ScopeCustom         Scope = "custom"
	ScopeMyTenants      Scope = "my_tenants"
	ScopeMyPeople       Scope = "my_people"
	ScopeSelectedPeople Scope = "selected_people"
)

// Scopes lists every scope.
func Scopes() []Scope {
	return []Scope{ScopeEveryone, ScopeLandlords, ScopeTenants, ScopeCustom, ScopeMyTenants, ScopeMyPeople, ScopeSelectedPeople}
}

// ParseScope returns the scope named s.
func ParseScope(s string) (Scope, bool) {
	for _, sc := range Scopes() {
		if string(sc) == s {
			return sc, true
		}
	}
	return "", false
}

// AllowedFor says whether a sender of the kind may use the scope.
func (s Scope) AllowedFor(k SenderKind) bool {
	switch k {
	case SenderPlatform:
		return s == ScopeEveryone || s == ScopeLandlords || s == ScopeTenants || s == ScopeCustom
	case SenderLandlord:
		return s == ScopeMyTenants || s == ScopeMyPeople || s == ScopeSelectedPeople
	}
	return false
}

// IsPicked is true for the scope where the sender names the people one by one.
func (s Scope) IsPicked() bool { return s == ScopeSelectedPeople }

// NotificationAudience is the side of the inbox the notifications of the scope are for: the landlords' notifications
// show in their landlord inbox, the tenants' in the tenant inbox, the others in every inbox.
func (s Scope) NotificationAudience() Audience {
	switch s {
	case ScopeLandlords:
		return AudienceLandlord
	case ScopeTenants, ScopeMyTenants, ScopeSelectedPeople:
		return AudienceTenant
	}
	return AudienceNone
}

// Type is how a message looks and what it is about.
type Type string

// The types.
const (
	TypeNotice    Type = "notice"
	TypeWarning   Type = "warning"
	TypePromotion Type = "promotion"
	TypeWhatsNew  Type = "whats_new"
	TypeWelcome   Type = "welcome"
)

// ParseType returns the type named s.
func ParseType(s string) (Type, bool) {
	switch t := Type(s); t {
	case TypeNotice, TypeWarning, TypePromotion, TypeWhatsNew, TypeWelcome:
		return t, true
	}
	return "", false
}

// Frequency is how often a popup comes back for the same person.
type Frequency string

// The frequencies.
const (
	FrequencyOnce         Frequency = "once"
	FrequencyEverySession Frequency = "every_session"
	FrequencyEveryDays    Frequency = "every_days"
)

// ParseFrequency returns the frequency named s.
func ParseFrequency(s string) (Frequency, bool) {
	switch f := Frequency(s); f {
	case FrequencyOnce, FrequencyEverySession, FrequencyEveryDays:
		return f, true
	}
	return "", false
}

// PopupSize is the size of the popup window.
type PopupSize string

// The popup sizes.
const (
	SizeSmall  PopupSize = "small"
	SizeMedium PopupSize = "medium"
	SizeLarge  PopupSize = "large"
	SizeFull   PopupSize = "full"
)

// ParsePopupSize returns the size named s.
func ParsePopupSize(s string) (PopupSize, bool) {
	switch p := PopupSize(s); p {
	case SizeSmall, SizeMedium, SizeLarge, SizeFull:
		return p, true
	}
	return "", false
}

// Status is where a message is in its life. It is never stored: it follows from the dates.
type Status string

// The statuses.
const (
	StatusDraft     Status = "draft"
	StatusScheduled Status = "scheduled"
	StatusActive    Status = "active"
	StatusEnded     Status = "ended"
	StatusRecalled  Status = "recalled"
)

// ParseStatus returns the status named s.
func ParseStatus(s string) (Status, bool) {
	switch st := Status(s); st {
	case StatusDraft, StatusScheduled, StatusActive, StatusEnded, StatusRecalled:
		return st, true
	}
	return "", false
}

// Channel is a way a message reaches people: a notification in the inbox or a popup.
type Channel string

// The channels.
const (
	ChannelNotification Channel = "notification"
	ChannelPopup        Channel = "popup"
)

// ParseChannel returns the channel named s.
func ParseChannel(s string) (Channel, bool) {
	switch c := Channel(s); c {
	case ChannelNotification, ChannelPopup:
		return c, true
	}
	return "", false
}

// The locales a message is written in.
const (
	LocaleVI = "vi"
	LocaleEN = "en"
)

// Locales lists the languages of a message.
func Locales() []string { return []string{LocaleVI, LocaleEN} }

// Texts is one text per language ({"vi": ..., "en": ...}).
type Texts map[string]string

// Has reports whether the language has a text.
func (t Texts) Has(locale string) bool { return t[locale] != "" }

// Any reports whether at least one language has a text.
func (t Texts) Any() bool {
	for _, l := range Locales() {
		if t.Has(l) {
			return true
		}
	}
	return false
}

// Announcement is a message from the platform or from a landlord: a notification in the inbox, a popup, or both.
type Announcement struct {
	ID          uuid.UUID
	CreatedByID *uuid.UUID
	// SenderRole is the role the sender acted in when writing it; it is stored, so a later role change changes nothing.
	SenderRole string
	// LandlordID is the landlord whose people a landlord's message is for (nil for the platform's).
	LandlordID       *uuid.UUID
	Scope            Scope
	Criteria         Criteria
	Type             Type
	Title, Body      Texts
	SendNotification bool
	ShowPopup        bool
	PublishedAt      *time.Time
	StartsAt         *time.Time
	EndsAt           *time.Time
	Frequency        Frequency
	FrequencyDays    *int
	MaxPerPeriod     *int
	MaxTotal         *int
	Priority         int
	PopupSize        PopupSize
	CTALabel         Texts
	CTAURL           string
	// Revision is raised by an edit that asks to show the popup again to the people who closed it.
	Revision int
	// RecipientCount is how many people the message went to (or, without a notification, is for).
	RecipientCount int
	SentAt         *time.Time
	RecalledAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SenderKind is derived from the stored role of the sender.
func (a Announcement) SenderKind() SenderKind {
	if a.SenderRole == authz.RoleLandlord {
		return SenderLandlord
	}
	return SenderPlatform
}

// StatusAt is where the message is in its life at the given time: recalled beats draft beats scheduled beats ended
// beats active.
func (a Announcement) StatusAt(now time.Time) Status {
	switch {
	case a.RecalledAt != nil:
		return StatusRecalled
	case a.PublishedAt == nil:
		return StatusDraft
	case a.StartsAt != nil && a.StartsAt.After(now):
		return StatusScheduled
	case a.EndsAt != nil && !a.EndsAt.After(now):
		return StatusEnded
	}
	return StatusActive
}

// Owned reports whether the sender may see and change the message: any admin or super admin for the platform's, the
// landlord themselves for a landlord's. Anybody else is told it does not exist.
func (a Announcement) Owned(by Sender) bool {
	switch a.SenderKind() {
	case SenderPlatform:
		return authz.IsStaff(by.Role)
	default:
		return by.Role == authz.RoleLandlord && a.LandlordID != nil && *a.LandlordID == by.UserID
	}
}

// DeliveryDue reports whether the notifications of the message should go out now: it is published, has a notification,
// has not been sent, is not recalled and its start has come.
func (a Announcement) DeliveryDue(now time.Time) bool {
	return a.PublishedAt != nil && a.SendNotification && a.SentAt == nil && a.RecalledAt == nil &&
		(a.StartsAt == nil || !a.StartsAt.After(now))
}

// Person is somebody a message is for or from, as the sender's screens show them.
type Person struct {
	ID       uuid.UUID
	FullName string
	Email    *string
}

// PopupStats are what people did with the popup of a message.
type PopupStats struct{ Seen, Dismissed, Clicked int }

// AnnouncementDetail is a message with what happened to it.
type AnnouncementDetail struct {
	Announcement
	CreatedBy *Person
	// People are the ones the sender picked one by one (selected_people).
	People []Person
	// Images are the pictures of the popup, in order.
	Images     []Image
	ReadCount  int
	PopupStats PopupStats
	// Status is where the message is in its life when it was read.
	Status Status
}

// AnnouncementQuery is what a list of messages is asked for. Without Landlord it lists the platform's messages;
// with it, that landlord's.
type AnnouncementQuery struct {
	LandlordID *uuid.UUID
	Status     *Status
	From, To   *time.Time
	Type       *Type
	Channel    *Channel
	Limit      int
	Offset     int
}
