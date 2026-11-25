package statshttp

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/stats"
	usecase "rental-management/internal/usecase/stats"
)

// Limits of the ranking of rooms and buildings.
const (
	rankingLimit   = 50
	dashboardLimit = 10
)

// rangeOf reads ?from and ?to (days, YYYY-MM-DD) as the months they fall in. A request with neither is the last 12
// months; with only to, the 12 months that end with its month; with only from, up to today.
func rangeOf(c *gin.Context, today time.Time, fields *[]apperr.Field) (domain.MonthRange, bool) {
	var from, to *time.Time
	for _, q := range []struct {
		name string
		dst  **time.Time
	}{{"from", &from}, {"to", &to}} {
		raw := c.Query(q.name)
		if raw == "" {
			continue
		}
		day, err := time.ParseInLocation(time.DateOnly, raw, time.UTC)
		if err != nil {
			*fields = append(*fields, apperr.Field{Name: q.name, Rule: apperr.RuleDate})
			continue
		}
		*q.dst = &day
	}
	if len(*fields) > 0 {
		return domain.MonthRange{}, false
	}
	end := today
	if to != nil {
		end = *to
	}
	if from == nil {
		return domain.DefaultRange(end), true
	}
	rng, err := domain.NewMonthRange(*from, end)
	if err != nil {
		field, _ := rangeField(err) // NewMonthRange fails only with the two errors of a range
		*fields = append(*fields, field)
		return domain.MonthRange{}, false
	}
	return rng, true
}

// queryOf reads what a landlord asks: the range, the building to look at, the sort key and the length of a ranking
// (defaultLimit when absent). Every bad value is reported at once.
func queryOf(c *gin.Context, landlordID uuid.UUID, role string, today time.Time, defaultLimit int) (usecase.Query, error) {
	var fields []apperr.Field
	rng, _ := rangeOf(c, today, &fields)
	q := usecase.Query{LandlordID: landlordID, Role: role, Range: rng, Limit: defaultLimit}
	if id, field := httpx.QueryUUID(c, "building_id"); field != nil {
		fields = append(fields, *field)
	} else {
		q.BuildingID = id
	}
	sortKey, err := domain.ParseSort(c.Query("sort"))
	if err != nil {
		fields = append(fields, apperr.Field{Name: "sort", Rule: apperr.RuleOneOf})
	}
	q.Sort = sortKey
	if limit, field := httpx.QueryInt64(c, "limit", 1, usecase.MaxLimit); field != nil {
		fields = append(fields, *field)
	} else if limit != nil {
		q.Limit = int(*limit)
	}
	if len(fields) > 0 {
		return usecase.Query{}, apperr.Invalid(fields...)
	}
	return q, nil
}
