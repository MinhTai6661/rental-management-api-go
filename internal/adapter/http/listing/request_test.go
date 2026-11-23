package listinghttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/listing"
)

func TestUpdateRequestTellsAbsentFromNull(t *testing.T) {
	var r updateRequest
	if err := json.Unmarshal([]byte(`{"title": "New"}`), &r); err != nil {
		t.Fatal(err)
	}
	in, err := r.input()
	if err != nil || in.Title == nil || *in.Title != "New" || in.Description.Set {
		t.Errorf("title only: %+v, %v", in, err)
	}
	r = updateRequest{}
	if err := json.Unmarshal([]byte(`{"description": null}`), &r); err != nil {
		t.Fatal(err)
	}
	if in, err = r.input(); err != nil || in.Title != nil || !in.Description.Set || in.Description.Value != nil {
		t.Errorf("null clears the description: %+v, %v", in, err)
	}
	r = updateRequest{}
	long := strings.Repeat("a", domain.MaxDescriptionLength+1)
	if err := json.Unmarshal([]byte(`{"description": "`+long+`"}`), &r); err != nil {
		t.Fatal(err)
	}
	if _, err = r.input(); err == nil {
		t.Error("a long description is refused")
	}
}

func TestRequiredNote(t *testing.T) {
	blank, ok := "  ", "Thiếu ảnh"
	for _, n := range []*string{nil, &blank} {
		if _, err := requiredNote(n); err == nil {
			t.Errorf("%v must be refused", n)
		}
	}
	if got, err := requiredNote(&ok); err != nil || got != ok {
		t.Errorf("%q %v", got, err)
	}
}

func ctxWithQuery(query string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x?"+query, nil)
	return c
}

func TestListFilterReadsEveryFilterAndReportsEveryBadOne(t *testing.T) {
	f, err := listFilter(ctxWithQuery("status=pending&search=Gần+chợ&ward_code=4&price_from=1&price_to=9&area_from=1.5&sort=-price"))
	if err != nil || f.Status == nil || *f.Status != domain.StatusPending || f.Search != "Gần chợ" || *f.WardCode != 4 ||
		*f.PriceFrom != 1 || *f.PriceTo != 9 || *f.AreaFrom != 1.5 || f.Sort != "-price" {
		t.Fatalf("filter = %+v, %v", f, err)
	}
	_, err = listFilter(ctxWithQuery("status=bogus&sort=up&price_from=9&price_to=1&ward_code=0&search=" + strings.Repeat("a", 101)))
	e, ok := apperr.As(err)
	if !ok || e.Kind != apperr.KindValidation {
		t.Fatalf("err = %v", err)
	}
	got := map[string]bool{}
	for _, f := range e.Fields {
		got[f.Name] = true
	}
	for _, want := range []string{"status", "sort", "price_from", "ward_code", "search"} {
		if !got[want] {
			t.Errorf("no error on %s: %+v", want, e.Fields)
		}
	}
}

func TestOfferFilter(t *testing.T) {
	f, err := offerFilter(ctxWithQuery("house_name=Sao+Mai&landlord_id=0190e3e0-0000-7000-8000-000000000001&sort=area"))
	if err != nil || f.HouseName != "Sao Mai" || f.LandlordID == nil || f.Sort != "area" {
		t.Fatalf("filter = %+v, %v", f, err)
	}
	// "status" and "created_at" listing sorts are not offer sorts.
	if _, err := offerFilter(ctxWithQuery("sort=submitted_at")); err == nil {
		t.Error("an unknown sort is refused")
	}
	if f, err := offerFilter(ctxWithQuery("")); err != nil || f.Sort != "" {
		t.Errorf("no filter: %+v %v", f, err)
	}
}
