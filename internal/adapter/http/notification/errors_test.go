package notificationhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
	usecase "rental-management/internal/usecase/notification"
)

func TestMapError(t *testing.T) {
	cases := map[string]struct {
		err   error
		kind  apperr.Kind
		field string
	}{
		"not found":    {shared.ErrNotFound, apperr.KindNotFound, ""},
		"wrapped":      {fmt.Errorf("x: %w", shared.ErrNotFound), apperr.KindNotFound, ""},
		"unknown kind": {domain.ErrUnknownKind, apperr.KindValidation, "kind"},
		"too many ids": {domain.ErrTooManyIDs, apperr.KindValidation, "ids"},
		"backwards":    {domain.ErrRangeBackwards, apperr.KindValidation, "to"},
		"too long":     {domain.ErrRangeTooLong, apperr.KindValidation, "to"},
		"link":         {domain.ErrLinkNotAllowed, apperr.KindValidation, "link_url"},
		"image type":   {domain.ErrImageType, apperr.KindValidation, "file"},
		"corrupt":      {domain.ErrImageCorrupt, apperr.KindValidation, "file"},
		"dimensions":   {&domain.ImageDimensionsError{Max: 2048}, apperr.KindValidation, "file"},
		"too large":    {&domain.FileTooLargeError{MaxKB: 2048}, apperr.KindValidation, "file"},
		"image limit":  {&domain.ImageLimitError{Max: 3}, apperr.KindConflict, ""},
		"no file":      {domain.ErrFileNotFound, apperr.KindNotFound, ""},
	}
	for name, c := range cases {
		e, ok := apperr.As(mapError(c.err))
		if !ok || e.Kind != c.kind {
			t.Errorf("%s: %+v", name, e)
			continue
		}
		if c.field != "" && (len(e.Fields) != 1 || e.Fields[0].Name != c.field) {
			t.Errorf("%s: fields %+v", name, e.Fields)
		}
	}
	other := errors.New("boom")
	if !errors.Is(mapError(other), other) {
		t.Error("an unknown error is returned as it is")
	}
}

func TestBulkRequestParse(t *testing.T) {
	id := "0198f8a0-0000-7000-8000-000000000001"
	action, ids, err := bulkRequest{Action: "delete", IDs: []string{id}}.parse()
	if err != nil || action != domain.BulkDelete || len(ids) != 1 || ids[0].String() != id {
		t.Fatalf("%v %v %v", action, ids, err)
	}
	if _, _, err := (bulkRequest{Action: "burn", IDs: []string{id}}).parse(); err == nil {
		t.Error("an unknown action is refused")
	}
	if _, _, err := (bulkRequest{Action: "read", IDs: []string{"nope"}}).parse(); err == nil {
		t.Error("a bad id is refused")
	}
}

func TestViewOfEmptiesToNullAndObject(t *testing.T) {
	v := viewOf(domain.Notification{Kind: domain.InvoiceSent})
	if v.URL != nil || v.Params == nil || v.ReadAt != nil {
		t.Errorf("%+v", v)
	}
	v = viewOf(domain.Notification{URL: "/x"})
	if v.URL == nil || *v.URL != "/x" {
		t.Errorf("%+v", v)
	}
}

func TestRangeAndLimitErrorsCarryTheirParameters(t *testing.T) {
	e, _ := apperr.As(mapError(domain.ErrRangeTooLong))
	if e.Fields[0].Rule != apperr.RuleMaxDays || e.Fields[0].Params["max"] != "92" {
		t.Errorf("%+v", e.Fields)
	}
	e, _ = apperr.As(mapError(domain.ErrRangeBackwards))
	if e.Fields[0].Rule != apperr.RuleAfterOrEqual || e.Fields[0].Params["other"] != "from" {
		t.Errorf("%+v", e.Fields)
	}
	e, _ = apperr.As(mapError(&domain.ImageLimitError{Max: 3}))
	if e.Code != apperr.CodeAnnouncementImageMax || e.Params["max"] != "3" {
		t.Errorf("%+v", e)
	}
}

func TestStatsViewShowsOnlyTheChannelsAsked(t *testing.T) {
	day := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	report := usecase.StatsReport{First: day, Last: day, Stats: domain.Stats{
		Totals: domain.Totals{Popup: &domain.PopupTotals{Viewers: 3, Closed: 1, Clicked: 1, CloseRate: domain.Percent(1, 3), ClickRate: domain.Percent(1, 3)}},
		Series: []domain.DayPoint{{Day: day, Viewers: 3, Closed: 1}},
	}}
	raw, err := json.Marshal(statsViewOf(report))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{`"viewers":3`, `"close_rate":33.3`, `"views_per_viewer":null`, `"date":"2026-11-24"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in %s", want, body)
		}
	}
	for _, not := range []string{`"read"`, `"recipients"`, `"messages"`} {
		if strings.Contains(body, not) {
			t.Errorf("%s must be absent: %s", not, body)
		}
	}
}
