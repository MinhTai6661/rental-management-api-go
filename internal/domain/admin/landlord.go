package admin

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

// ApplicationStatus is where a landlord application stands.
type ApplicationStatus string

// The statuses. An application waits for an admin as soon as it is sent (there is no email step).
const (
	ApplicationPending  ApplicationStatus = "pending_review"
	ApplicationApproved ApplicationStatus = "approved"
	ApplicationRejected ApplicationStatus = "rejected"
)

// ParseApplicationStatus returns the status named s.
func ParseApplicationStatus(s string) (ApplicationStatus, bool) {
	switch st := ApplicationStatus(s); st {
	case ApplicationPending, ApplicationApproved, ApplicationRejected:
		return st, true
	}
	return "", false
}

// ApplicationSort is how the list of applications is ordered.
type ApplicationSort string

// The orders of the list of applications.
const (
	ApplicationNewest ApplicationSort = "newest"
	ApplicationOldest ApplicationSort = "oldest"
	ApplicationByName ApplicationSort = "name"
)

// ParseApplicationSort returns the order named s; "" is the default (newest first).
func ParseApplicationSort(s string) (ApplicationSort, bool) {
	switch o := ApplicationSort(s); o {
	case "":
		return ApplicationNewest, true
	case ApplicationNewest, ApplicationOldest, ApplicationByName:
		return o, true
	}
	return "", false
}

// Application is a request to become a landlord.
type Application struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	ApplicantName  string
	ApplicantEmail *string
	BusinessName   string
	Phone          string
	WardCode       *int32
	AddressDetail  *string
	Note           *string
	Status         ApplicationStatus
	ReviewedByID   *uuid.UUID
	ReviewedAt     *time.Time
	ReviewNote     *string
	CreatedAt      time.Time
}

// ApplicationInput is what a person writes to apply (already trimmed by Normalize).
type ApplicationInput struct {
	BusinessName  string
	Phone         string
	WardCode      *int32
	AddressDetail *string
	Note          *string
}

// Limits of the text of an application and of a landlord profile.
const (
	MaxBusinessName    = 150
	MaxPhone           = 20
	MaxAddress         = 500
	MaxApplicationNote = 1000
	MaxDescription     = 2000
	MaxReviewNote      = 1000
)

// Normalize trims the text and checks the rules of an application.
func (in ApplicationInput) Normalize() (ApplicationInput, error) {
	var v violations
	in.BusinessName = requiredText(&v, "business_name", in.BusinessName, MaxBusinessName)
	in.Phone = requiredText(&v, "phone", in.Phone, MaxPhone)
	in.AddressDetail = optionalText(&v, "address_detail", in.AddressDetail, MaxAddress)
	in.Note = optionalText(&v, "note", in.Note, MaxApplicationNote)
	return in, v.err()
}

// CheckCanApply decides whether the account may send an application: it must act as a tenant, hold the tenant role
// and no staff or landlord role, and have no application waiting.
func CheckCanApply(actingRole string, held []string, hasOpen bool) error {
	switch {
	case slices.Contains(held, authz.RoleLandlord):
		return ErrAlreadyLandlord
	case actingRole != authz.RoleTenant, !slices.Contains(held, authz.RoleTenant), authz.MixesStaffWithOthers(held):
		return ErrNotRenter
	case hasOpen:
		return ErrAlreadyPending
	}
	return nil
}

// CheckReviewable refuses to answer an application twice.
func (a Application) CheckReviewable() error {
	if a.Status != ApplicationPending {
		return ErrNotReviewable
	}
	return nil
}

// NormalizeReview checks the note of an answer: a rejection needs the reason.
func NormalizeReview(note string, reject bool) (string, error) {
	var v violations
	note = strings.TrimSpace(note)
	if reject && note == "" {
		v.add("note", ruleRequired, nil)
	}
	if utf8.RuneCountInString(note) > MaxReviewNote {
		v.add("note", ruleMaxString, maxLen(MaxReviewNote))
	}
	return note, v.err()
}

// FirstBuildingName is the name of the first building of a new landlord: the business name cut to the length a
// building name may have.
func FirstBuildingName(business string) string {
	const maxName = 50
	business = strings.TrimSpace(business)
	if utf8.RuneCountInString(business) > maxName {
		business = strings.TrimSpace(string([]rune(business)[:maxName]))
	}
	return business
}

// Profile is the business side of a landlord.
type Profile struct {
	UserID        uuid.UUID
	BusinessName  string
	Phone         *string
	WardCode      *int32
	AddressDetail *string
	Description   *string
	UpdatedAt     time.Time
}

// ProfileInput is what a landlord writes in their business profile.
type ProfileInput struct {
	BusinessName  string
	Phone         *string
	WardCode      *int32
	AddressDetail *string
	Description   *string
}

// Normalize trims the text and checks the rules of a profile.
func (in ProfileInput) Normalize() (ProfileInput, error) {
	var v violations
	in.BusinessName = requiredText(&v, "business_name", in.BusinessName, MaxBusinessName)
	in.Phone = optionalText(&v, "phone", in.Phone, MaxPhone)
	in.AddressDetail = optionalText(&v, "address_detail", in.AddressDetail, MaxAddress)
	in.Description = optionalText(&v, "description", in.Description, MaxDescription)
	return in, v.err()
}

// ChangedFields names the fields of the profile that differ (for the audit trail, which never keeps the values).
func (p Profile) ChangedFields(in ProfileInput) []string {
	var out []string
	same := func(a, b *string) bool { return deref(a) == deref(b) }
	if p.BusinessName != in.BusinessName {
		out = append(out, "business_name")
	}
	if !same(p.Phone, in.Phone) {
		out = append(out, "phone")
	}
	if (p.WardCode == nil) != (in.WardCode == nil) || (p.WardCode != nil && *p.WardCode != *in.WardCode) {
		out = append(out, "ward_code")
	}
	if !same(p.AddressDetail, in.AddressDetail) {
		out = append(out, "address_detail")
	}
	if !same(p.Description, in.Description) {
		out = append(out, "description")
	}
	return out
}

// Impact is what taking the landlord role away touches.
type Impact struct {
	Rooms                int
	ActiveContracts      int
	PendingRegistrations int
	UnpaidInvoices       int
}

// CanRevoke reports whether the role may be taken away now: no contract is running.
func (i Impact) CanRevoke() bool { return i.ActiveContracts == 0 }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// requiredText trims text and records a rule when it is empty or too long.
func requiredText(v *violations, name, text string, limit int) string {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		v.add(name, ruleRequired, nil)
	case utf8.RuneCountInString(text) > limit:
		v.add(name, ruleMaxString, maxLen(limit))
	}
	return text
}

// optionalText trims text, treats blank as absent and records a rule when it is too long.
func optionalText(v *violations, name string, text *string, limit int) *string {
	if text == nil {
		return nil
	}
	t := strings.TrimSpace(*text)
	if t == "" {
		return nil
	}
	if utf8.RuneCountInString(t) > limit {
		v.add(name, ruleMaxString, maxLen(limit))
	}
	return &t
}

// ApplicationQuery is what the list of applications is narrowed by.
type ApplicationQuery struct {
	Status ApplicationStatus
	// Search is lower case with LIKE wildcards escaped; nil: no search.
	Search *string
	Sort   ApplicationSort
}
