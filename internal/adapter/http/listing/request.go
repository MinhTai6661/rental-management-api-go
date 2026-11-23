package listinghttp

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/listing"
	usecase "rental-management/internal/usecase/listing"
)

// The bodies of the listing requests. The validate tags are the rules of the API; errors are reported under the json
// names.

type createRequest struct {
	RoomID      *uuid.UUID `json:"room_id" validate:"required"`
	Title       string     `json:"title" validate:"required,max=150"`
	Description *string    `json:"description" validate:"omitempty,max=5000"`
}

// optional is a field of a partial update that tells apart "absent" (leave it) from null (clear it).
type optional[T any] struct {
	set   bool
	value *T
}

// UnmarshalJSON records that the field was present, and its value (nil for null).
func (o *optional[T]) UnmarshalJSON(b []byte) error {
	o.set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.value = &v
	return nil
}

// updateRequest is a partial update: only the fields the body carries change. The room of a listing never changes.
type updateRequest struct {
	Title       *string          `json:"title" validate:"omitempty,min=1,max=150"`
	Description optional[string] `json:"description"`
}

// input turns the body into the use case's input, reporting the rules the validate tags cannot express.
func (r updateRequest) input() (usecase.UpdateInput, error) {
	if r.Description.value != nil && len([]rune(*r.Description.value)) > domain.MaxDescriptionLength {
		return usecase.UpdateInput{}, apperr.Invalid(apperr.Field{
			Name: "description", Rule: apperr.RuleMaxString, Params: map[string]string{"max": strconv.Itoa(domain.MaxDescriptionLength)},
		})
	}
	return usecase.UpdateInput{Title: r.Title, Description: usecase.Optional[string]{Set: r.Description.set, Value: r.Description.value}}, nil
}

// reviewRequest is an approval (the note is optional) or a refusal or take-down (the reason is required, which the
// handler checks).
type reviewRequest struct {
	Note *string `json:"note" validate:"omitempty,max=1000"`
}

type bulkRequest struct {
	Action string      `json:"action" validate:"required,oneof=approve reject"`
	IDs    []uuid.UUID `json:"ids" validate:"required,min=1,max=100,unique"`
	Note   *string     `json:"note" validate:"omitempty,max=1000"`
}

// requiredNote returns the note of a decision that must say why, or the 422 on `note`.
func requiredNote(note *string) (string, error) {
	if note == nil || strings.TrimSpace(*note) == "" {
		return "", apperr.Invalid(apperr.Field{Name: "note", Rule: apperr.RuleRequired})
	}
	return *note, nil
}

// queryReader gathers the errors of the filters of a list into one 422.
type queryReader struct {
	c      *gin.Context
	fields []apperr.Field
}

func (q *queryReader) keep(f *apperr.Field) {
	if f != nil {
		q.fields = append(q.fields, *f)
	}
}

func (q *queryReader) text(name string, max int) string {
	s := q.c.Query(name)
	if len([]rune(s)) > max {
		q.fields = append(q.fields, apperr.Field{Name: name, Rule: apperr.RuleMaxString, Params: map[string]string{"max": strconv.Itoa(max)}})
	}
	return s
}

func (q *queryReader) sort(allowed []string) string {
	s, f := httpx.QueryOneOf(q.c, "sort", allowed...)
	q.keep(f)
	if s == nil {
		return ""
	}
	return *s
}

// place reads the ward and the province.
func (q *queryReader) place() (ward, province *int32) {
	w, f := httpx.QueryInt64(q.c, "ward_code", 1, 1<<31-1)
	q.keep(f)
	p, f := httpx.QueryInt64(q.c, "province_code", 1, 1<<31-1)
	q.keep(f)
	return int32Ptr(w), int32Ptr(p)
}

// ranges reads the price and area ranges; an end below its start can match nothing, so it is reported on the start.
func (q *queryReader) ranges() (priceFrom, priceTo *int64, areaFrom, areaTo *float64) {
	const maxMoney = 1_000_000_000_000
	var f *apperr.Field
	priceFrom, f = httpx.QueryInt64(q.c, "price_from", 0, maxMoney)
	q.keep(f)
	priceTo, f = httpx.QueryInt64(q.c, "price_to", 0, maxMoney)
	q.keep(f)
	areaFrom, f = httpx.QueryFloat(q.c, "area_from", 0, 99999.99)
	q.keep(f)
	areaTo, f = httpx.QueryFloat(q.c, "area_to", 0, 99999.99)
	q.keep(f)
	if priceFrom != nil && priceTo != nil && *priceTo < *priceFrom {
		q.fields = append(q.fields, apperr.Field{Name: "price_from", Rule: apperr.RuleLTEField, Params: map[string]string{"other": strconv.FormatInt(*priceTo, 10)}})
	}
	if areaFrom != nil && areaTo != nil && *areaTo < *areaFrom {
		q.fields = append(q.fields, apperr.Field{Name: "area_from", Rule: apperr.RuleLTEField, Params: map[string]string{"other": strconv.FormatFloat(*areaTo, 'f', -1, 64)}})
	}
	return priceFrom, priceTo, areaFrom, areaTo
}

func (q *queryReader) err() error {
	if len(q.fields) == 0 {
		return nil
	}
	return apperr.Invalid(q.fields...)
}

// listFilter reads the filters of a list of listings (the landlord's own, or the admins' review list).
func listFilter(c *gin.Context) (usecase.ListFilter, error) {
	q := &queryReader{c: c}
	f := usecase.ListFilter{}
	status, field := httpx.QueryOneOf(c, "status", "draft", "pending", "approved", "rejected", "unpublished")
	q.keep(field)
	if status != nil {
		s, _ := domain.ParseStatus(*status)
		f.Status = &s
	}
	f.Search = q.text("search", 100)
	f.WardCode, f.ProvinceCode = q.place()
	f.PriceFrom, f.PriceTo, f.AreaFrom, f.AreaTo = q.ranges()
	f.Sort = q.sort(domain.ListingSorts)
	return f, q.err()
}

// offerFilter reads the filters of the search of find rooms.
func offerFilter(c *gin.Context) (usecase.OfferFilter, error) {
	q := &queryReader{c: c}
	f := usecase.OfferFilter{}
	landlord, field := httpx.QueryUUID(c, "landlord_id")
	q.keep(field)
	f.LandlordID = landlord
	f.HouseName = q.text("house_name", 255)
	f.WardCode, f.ProvinceCode = q.place()
	f.PriceFrom, f.PriceTo, f.AreaFrom, f.AreaTo = q.ranges()
	f.Sort = q.sort(domain.OfferSorts)
	return f, q.err()
}
