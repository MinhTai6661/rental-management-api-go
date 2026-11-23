package listinghttp

import (
	"errors"
	"fmt"
	"testing"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/listing"
)

func TestListingErrorsMapToTheAPIsKindsCodesAndParams(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		kind   apperr.Kind
		code   string
		field  string
		rule   string
		params map[string]string
	}{
		{"not found", domain.ErrNotFound, apperr.KindNotFound, apperr.CodeResourceNotFound, "", "", nil},
		{"room", domain.ErrRoomNotFound, apperr.KindValidation, apperr.CodeValidationFailed, "room_id", apperr.RuleExists, nil},
		{"ids", domain.ErrItemsNotFound, apperr.KindValidation, apperr.CodeValidationFailed, "ids", apperr.RuleExists, nil},
		{"note", domain.ErrNoteRequired, apperr.KindValidation, apperr.CodeValidationFailed, "note", apperr.RuleRequired, nil},
		{"transition", domain.ErrInvalidTransition, apperr.KindConflict, apperr.CodeListingTransition, "", "", nil},
		{"unpublish first", domain.ErrUnpublishFirst, apperr.KindConflict, apperr.CodeListingUnpublish, "", "", nil},
		{"room listed", &domain.RoomAlreadyListedError{Room: "P101"}, apperr.KindConflict, apperr.CodeListingRoomListed, "", "", map[string]string{"room": "P101"}},
		{"limit", &domain.LimitError{Limit: 4}, apperr.KindConflict, apperr.CodeListingLimit, "", "", map[string]string{"limit": "4"}},
	}
	for _, tc := range cases {
		// A use case may wrap what it returns; the mapping must still find it.
		got, ok := apperr.As(mapListingError(fmt.Errorf("wrapped: %w", tc.err)))
		if !ok || got.Kind != tc.kind || got.Code != tc.code {
			t.Errorf("%s: got %+v", tc.name, got)
			continue
		}
		if tc.field == "" {
			if fmt.Sprint(got.Params) != fmt.Sprint(tc.params) {
				t.Errorf("%s: params %v, want %v", tc.name, got.Params, tc.params)
			}
			continue
		}
		if len(got.Fields) != 1 || got.Fields[0].Name != tc.field || got.Fields[0].Rule != tc.rule {
			t.Errorf("%s: fields %+v, want %s %s", tc.name, got.Fields, tc.field, tc.rule)
		}
	}
}

func TestUnknownErrorsAreLeftAlone(t *testing.T) {
	boom := errors.New("boom")
	if got := mapListingError(boom); !errors.Is(got, boom) {
		t.Errorf("%v", got)
	}
	if mapListingError(nil) != nil {
		t.Error("no error, no mapping")
	}
	own := apperr.Malformed()
	if got := mapListingError(own); !errors.Is(got, own) {
		t.Errorf("%v", got)
	}
}
