package admin

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"rental-management/pkg/authz"
)

// LegalKind is which legal document: the terms of use or the privacy policy.
type LegalKind string

// The kinds of legal document.
const (
	LegalTerms   LegalKind = "terms"
	LegalPrivacy LegalKind = "privacy"
)

// ParseLegalKind returns the kind named s, or ErrUnknownKind.
func ParseLegalKind(s string) (LegalKind, error) {
	switch k := LegalKind(s); k {
	case LegalTerms, LegalPrivacy:
		return k, nil
	}
	return "", ErrUnknownKind
}

// Limits of a legal document.
const (
	MaxLegalTitle    = 200
	MaxChangeSummary = 2000
)

// LegalText is a legal document in one language: a title and a body that is a TipTap document.
type LegalText struct {
	Title string          `json:"title"`
	Body  json.RawMessage `json:"body"`
}

// LegalContent holds the document in both languages.
type LegalContent struct {
	VI LegalText `json:"vi"`
	EN LegalText `json:"en"`
}

// ChangeSummary says in both languages what changed, shown to the people asked to accept the new version.
type ChangeSummary struct {
	VI string `json:"vi"`
	EN string `json:"en"`
}

// LegalInput is a new version to publish.
type LegalInput struct {
	Content LegalContent
	Summary ChangeSummary
}

// Normalize trims the titles and summaries and checks every rule; each failure names the field
// (content.vi.title, content.en.body, change_summary.vi...).
func (in LegalInput) Normalize() (LegalInput, error) {
	var v violations
	for _, lang := range []struct {
		code string
		text *LegalText
		sum  *string
	}{{"vi", &in.Content.VI, &in.Summary.VI}, {"en", &in.Content.EN, &in.Summary.EN}} {
		lang.text.Title = requiredText(&v, "content."+lang.code+".title", lang.text.Title, MaxLegalTitle)
		if len(lang.text.Body) == 0 {
			v.add("content."+lang.code+".body", ruleRequired, nil)
		} else if rule := CheckRichText(lang.text.Body); rule != "" {
			v.add("content."+lang.code+".body", rule, nil)
		}
		*lang.sum = strings.TrimSpace(*lang.sum)
		switch {
		case *lang.sum == "":
			v.add("change_summary."+lang.code, ruleRequired, nil)
		case utf8.RuneCountInString(*lang.sum) > MaxChangeSummary:
			v.add("change_summary."+lang.code, ruleMaxString, maxLen(MaxChangeSummary))
		}
	}
	return in, v.err()
}

// Document is a published version of a legal document.
type Document struct {
	ID              uuid.UUID
	Kind            LegalKind
	Version         string
	Content         LegalContent
	Summary         ChangeSummary
	PublishedByID   *uuid.UUID
	PublishedByName string
	PublishedAt     time.Time
}

// NewVersion is the version of a document published at now.
func NewVersion(now time.Time) string { return now.UTC().Format("2006-01-02.150405") }

// NeedsAcceptance reports whether the account must accept the current terms: its accepted version is not the
// current one. Staff accounts are never asked.
func NeedsAcceptance(held []string, accepted *string, current string) bool {
	if HasStaffRole(held) {
		return false
	}
	return deref(accepted) != current
}

// HasStaffRole reports whether any of the roles is a staff role.
func HasStaffRole(roles []string) bool {
	for _, r := range roles {
		if authz.IsStaff(r) {
			return true
		}
	}
	return false
}
