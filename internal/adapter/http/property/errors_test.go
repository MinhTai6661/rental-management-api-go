package propertyhttp

import (
	"errors"
	"fmt"
	"testing"

	domain "rental-management/internal/domain/property"
	"rental-management/pkg/apperr"
)

func TestPropertyErrorsMapToTheAPIsKindsCodesAndParams(t *testing.T) {
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
		{"ward", domain.ErrWardNotFound, apperr.KindValidation, apperr.CodeValidationFailed, "ward_code", apperr.RuleExists, nil},
		{"building of a room", domain.ErrBuildingNotFound, apperr.KindValidation, apperr.CodeValidationFailed, "building_id", apperr.RuleExists, nil},
		{"ids", domain.ErrItemsNotFound, apperr.KindValidation, apperr.CodeValidationFailed, "ids", apperr.RuleExists, nil},
		{"building name", domain.ErrBuildingNameTaken, apperr.KindValidation, apperr.CodeValidationFailed, "name", apperr.RuleUnique, nil},
		{"room name", domain.ErrRoomNameTaken, apperr.KindValidation, apperr.CodeValidationFailed, "name", apperr.RuleUnique, nil},
		{"status locked", domain.ErrRoomStatusLocked, apperr.KindConflict, apperr.CodeRoomStatusLocked, "", "", nil},
		{"has contract", domain.ErrRoomHasContract, apperr.KindConflict, apperr.CodeRoomHasContract, "", "", nil},
		{"has listing", domain.ErrRoomHasListing, apperr.KindConflict, apperr.CodeRoomHasListing, "", "", nil},
		{"has rooms", &domain.BuildingHasRoomsError{Count: 4}, apperr.KindConflict, apperr.CodeBuildingHasRooms, "", "", map[string]string{"count": "4"}},
		{"building limit", &domain.BuildingLimitError{Limit: 2}, apperr.KindConflict, apperr.CodeBuildingLimit, "", "", map[string]string{"limit": "2"}},
		{"room limit", &domain.RoomLimitError{Limit: 9}, apperr.KindConflict, apperr.CodeRoomLimit, "", "", map[string]string{"limit": "9"}},
		{"media limit", &domain.MediaLimitError{Max: 7}, apperr.KindConflict, apperr.CodeMediaLimitReached, "", "", map[string]string{"max": "7"}},
		{"price in use", &domain.BuildingPriceInUseError{Count: 3}, apperr.KindValidation, apperr.CodeValidationFailed, "price_vnd", apperr.RuleBuildingPriceInUse, map[string]string{"count": "3"}},
		{"range", domain.ErrRangeInvalid, apperr.KindValidation, apperr.CodeValidationFailed, "name_prefix", apperr.RuleRangeInvalid, map[string]string{}},
		{"too many", &domain.RangeTooManyError{Max: 100}, apperr.KindValidation, apperr.CodeValidationFailed, "name_prefix", apperr.RuleRangeTooMany, map[string]string{"max": "100"}},
		{"name too long", &domain.RangeNameTooLongError{Max: 50}, apperr.KindValidation, apperr.CodeValidationFailed, "name_prefix", apperr.RuleRangeNameTooLong, map[string]string{"max": "50"}},
		{"names taken", &domain.RoomNamesTakenError{Names: []string{"p1", "p2"}}, apperr.KindValidation, apperr.CodeValidationFailed, "name_prefix", apperr.RuleNamesTaken, map[string]string{"names": "p1, p2"}},
		{"picture kind", domain.ErrPictureKindInvalid, apperr.KindValidation, apperr.CodeValidationFailed, "kind", apperr.RuleOneOf, nil},
		{"arrangement", domain.ErrArrangementMismatch, apperr.KindValidation, apperr.CodeValidationFailed, "items", apperr.RuleExists, nil},
		{"covers", domain.ErrArrangementManyCovers, apperr.KindValidation, apperr.CodeValidationFailed, "items", apperr.RuleSingleCover, nil},
		{"image type", domain.ErrImageType, apperr.KindValidation, apperr.CodeValidationFailed, "file", apperr.RuleImageType, nil},
		{"image corrupt", domain.ErrImageCorrupt, apperr.KindValidation, apperr.CodeValidationFailed, "file", apperr.RuleImageCorrupt, nil},
		{"dimensions", &domain.ImageDimensionsError{Max: 4096}, apperr.KindValidation, apperr.CodeValidationFailed, "file", apperr.RuleImageDimensions, map[string]string{"max": "4096"}},
		{"too large", &domain.FileTooLargeError{MaxKB: 1024}, apperr.KindValidation, apperr.CodeValidationFailed, "file", apperr.RuleFileTooLarge, map[string]string{"max_kb": "1024"}},
	}
	for _, tc := range cases {
		// A use case may wrap what it returns; the mapping must still find it.
		got, ok := apperr.As(mapPropertyError(fmt.Errorf("wrapped: %w", tc.err)))
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
		if len(got.Fields) != 1 || got.Fields[0].Name != tc.field || got.Fields[0].Rule != tc.rule || fmt.Sprint(got.Fields[0].Params) != fmt.Sprint(tc.params) {
			t.Errorf("%s: fields %+v, want %s %s %v", tc.name, got.Fields, tc.field, tc.rule, tc.params)
		}
	}
}

func TestValidationErrorKeepsEveryField(t *testing.T) {
	err := mapPropertyError(domain.Invalid(
		domain.Violation{Name: "name", Rule: domain.RuleRequired},
		domain.Violation{Name: "price_vnd", Rule: domain.RuleBuildingPriceMissing},
		domain.Violation{Name: "minimum_rental_period", Rule: domain.RuleRentalPeriodPair},
	))
	got, ok := apperr.As(err)
	if !ok || len(got.Fields) != 3 {
		t.Fatalf("%+v", got)
	}
	// The domain's rule names are the API's rule keys.
	if domain.RuleRequired != apperr.RuleRequired || domain.RuleBuildingPriceMissing != apperr.RuleBuildingPriceMissing ||
		domain.RuleRentalPeriodPair != apperr.RuleRentalPeriodPair {
		t.Error("the domain and the API disagree on a rule name")
	}
}

func TestUnknownErrorsAreLeftAlone(t *testing.T) {
	boom := errors.New("boom")
	if got := mapPropertyError(boom); !errors.Is(got, boom) {
		t.Errorf("%v", got)
	}
	if mapPropertyError(nil) != nil {
		t.Error("no error, no mapping")
	}
	// An error the binder already made is the API's own.
	own := apperr.Malformed()
	if got := mapPropertyError(own); !errors.Is(got, own) {
		t.Errorf("%v", got)
	}
}
