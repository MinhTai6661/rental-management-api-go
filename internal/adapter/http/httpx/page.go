package httpx

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
)

// MaxPerPage is the largest page a client may ask for.
const MaxPerPage = 100

// DefaultPerPage is the page size when the client does not ask for one.
const DefaultPerPage = 20

// PageRequest is the page a client asked for.
type PageRequest struct {
	Page    int
	PerPage int
}

// Offset is the number of rows to skip for a SQL OFFSET.
func (p PageRequest) Offset() int { return (p.Page - 1) * p.PerPage }

// Meta is the "meta" block of a list response. Tables people browse report the total; feeds (notifications,
// audit log...) skip the COUNT and only say whether another page exists: fetch PerPage+1 rows and compare.
type Meta struct {
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Total   *int  `json:"total,omitempty"`
	HasMore *bool `json:"has_more,omitempty"`
}

// TotalMeta describes the page req out of total rows.
func TotalMeta(req PageRequest, total int) Meta {
	return Meta{Page: req.Page, PerPage: req.PerPage, Total: &total}
}

// FeedMeta describes the page req of a feed that only knows whether more rows follow.
func FeedMeta(req PageRequest, hasMore bool) Meta {
	return Meta{Page: req.Page, PerPage: req.PerPage, HasMore: &hasMore}
}

// ParsePage reads ?page and ?per_page. Both are optional: page defaults to 1, per_page to defaultPerPage
// (DefaultPerPage when 0). Bad values give a 422 naming the field: integer, page at least 1, per_page 1 to MaxPerPage.
func ParsePage(c *gin.Context, defaultPerPage int) (PageRequest, error) {
	if defaultPerPage <= 0 {
		defaultPerPage = DefaultPerPage
	}
	req := PageRequest{Page: 1, PerPage: defaultPerPage}
	var fields []apperr.Field

	if raw := c.Query("page"); raw != "" {
		n, field := intParam("page", raw, 1, 0)
		if field != nil {
			fields = append(fields, *field)
		} else {
			req.Page = n
		}
	}
	if raw := c.Query("per_page"); raw != "" {
		n, field := intParam("per_page", raw, 1, MaxPerPage)
		if field != nil {
			fields = append(fields, *field)
		} else {
			req.PerPage = n
		}
	}
	if len(fields) > 0 {
		return PageRequest{}, apperr.Invalid(fields...)
	}
	return req, nil
}

// intParam parses an integer query value; upper 0 means "no upper bound".
func intParam(name, raw string, lower, upper int) (int, *apperr.Field) {
	n, err := strconv.Atoi(raw)
	switch {
	case err != nil:
		return 0, &apperr.Field{Name: name, Rule: apperr.RuleInteger}
	case n < lower:
		return 0, &apperr.Field{Name: name, Rule: apperr.RuleMinNumber, Params: map[string]string{"min": strconv.Itoa(lower)}}
	case upper > 0 && n > upper:
		return 0, &apperr.Field{Name: name, Rule: apperr.RuleMaxNumber, Params: map[string]string{"max": strconv.Itoa(upper)}}
	}
	return n, nil
}
