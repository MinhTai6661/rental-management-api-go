package property

import (
	"errors"
	"fmt"
	"strings"
)

// The errors of the property use cases. They say what went wrong in the domain's words; the HTTP adapter decides the
// status, the code, the fields and the message parameters the client sees.
var (
	// ErrNotFound: the building, room or picture does not exist or is not the landlord's.
	ErrNotFound = errors.New("not found")
	// ErrWardNotFound: the ward of an address does not exist.
	ErrWardNotFound = errors.New("ward does not exist")
	// ErrBuildingNotFound: the building a room is put in is not the landlord's.
	ErrBuildingNotFound = errors.New("building does not exist")
	// ErrItemsNotFound: not every id of a list names one of the landlord's items.
	ErrItemsNotFound = errors.New("some ids do not exist")
	// ErrBuildingNameTaken: another live building of the landlord has the name (any case).
	ErrBuildingNameTaken = errors.New("building name is already used")
	// ErrRoomNameTaken: another live room in the same building (or outside any) has the name.
	ErrRoomNameTaken = errors.New("room name is already used")
	// ErrRoomStatusLocked: a running contract keeps the room rented.
	ErrRoomStatusLocked = errors.New("room status is locked by a contract")
	// ErrRoomHasContract: a running contract blocks deleting the room.
	ErrRoomHasContract = errors.New("room has an active contract")
	// ErrRoomHasListing: an open listing blocks deleting the room.
	ErrRoomHasListing = errors.New("room has an open listing")
	// ErrRangeInvalid: the number range of a run of rooms is backwards or negative.
	ErrRangeInvalid = errors.New("room range is invalid")
	// ErrPictureKindInvalid: the kind of a picture is neither photo nor cover.
	ErrPictureKindInvalid = errors.New("picture kind is invalid")
	// ErrArrangementMismatch: an arrangement does not list exactly the current pictures, once each.
	ErrArrangementMismatch = errors.New("arrangement does not match the pictures")
	// ErrArrangementManyCovers: an arrangement marks more than one cover.
	ErrArrangementManyCovers = errors.New("arrangement has more than one cover")

	// The reasons the image processor refuses a picture (it reports them, the use case adds the limits).
	ErrImageType       = errors.New("image is not a still WebP, JPEG or PNG")
	ErrImageCorrupt    = errors.New("image cannot be decoded")
	ErrImageDimensions = errors.New("image dimensions are too large")
	ErrImageTooLarge   = errors.New("image file is too large")

	// ErrFileNotFound: the file store holds nothing under the key.
	ErrFileNotFound = errors.New("file not found")
)

// Violation is one failed rule of a request field.
type Violation struct {
	Name   string
	Rule   string
	Params map[string]string
}

// The rules the domain itself reports (the others are sentinel errors above).
const (
	RuleRequired             = "required"
	RuleBuildingPriceMissing = "building_price_missing"
	RuleRentalPeriodPair     = "rental_period_pair"
)

// ValidationError lists the fields that broke a rule.
type ValidationError struct{ Violations []Violation }

// Invalid builds a ValidationError.
func Invalid(v ...Violation) *ValidationError { return &ValidationError{Violations: v} }

func (e *ValidationError) Error() string {
	names := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		names[i] = v.Name + ":" + v.Rule
	}
	return "invalid: " + strings.Join(names, ", ")
}

// BuildingLimitError: the landlord's plan allows no more buildings.
type BuildingLimitError struct{ Limit int }

func (e *BuildingLimitError) Error() string { return fmt.Sprintf("plan allows %d buildings", e.Limit) }

// RoomLimitError: the landlord's plan allows no more rooms.
type RoomLimitError struct{ Limit int }

func (e *RoomLimitError) Error() string { return fmt.Sprintf("plan allows %d rooms", e.Limit) }

// MediaLimitError: the room or building already has the most pictures it may have.
type MediaLimitError struct{ Max int }

func (e *MediaLimitError) Error() string { return fmt.Sprintf("at most %d pictures", e.Max) }

// BuildingHasRoomsError: a building that still holds rooms cannot be deleted.
type BuildingHasRoomsError struct{ Count int }

func (e *BuildingHasRoomsError) Error() string { return fmt.Sprintf("building has %d rooms", e.Count) }

// BuildingPriceInUseError: the rent of a building cannot be removed while rooms follow it.
type BuildingPriceInUseError struct{ Count int }

func (e *BuildingPriceInUseError) Error() string {
	return fmt.Sprintf("%d rooms follow the building's rent", e.Count)
}

// RoomNamesTakenError lists (some of) the names of a run that live rooms of the building already use.
type RoomNamesTakenError struct{ Names []string }

func (e *RoomNamesTakenError) Error() string {
	return "room names taken: " + strings.Join(e.Names, ", ")
}

// RangeTooManyError: a run asks for more rooms than the maximum.
type RangeTooManyError struct{ Max int }

func (e *RangeTooManyError) Error() string { return fmt.Sprintf("a run has at most %d rooms", e.Max) }

// RangeNameTooLongError: a name of a run is longer than the maximum.
type RangeNameTooLongError struct{ Max int }

func (e *RangeNameTooLongError) Error() string {
	return fmt.Sprintf("a room name has at most %d characters", e.Max)
}

// ImageDimensionsError: the picture is larger than Max pixels on a side.
type ImageDimensionsError struct{ Max int }

func (e *ImageDimensionsError) Error() string { return fmt.Sprintf("image is over %d pixels", e.Max) }

// FileTooLargeError: the picture file is larger than MaxKB kilobytes.
type FileTooLargeError struct{ MaxKB int }

func (e *FileTooLargeError) Error() string { return fmt.Sprintf("image is over %d KB", e.MaxKB) }
