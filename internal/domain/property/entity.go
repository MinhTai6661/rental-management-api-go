package property

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Building is a building as a use case locks it for a change.
type Building struct {
	ID uuid.UUID
	BuildingData
}

// BuildingData is what a building stores: its name and what it offers its rooms.
type BuildingData struct {
	Name string
	BuildingDefaults
}

// Room is a room as a use case locks it for a change.
type Room struct {
	ID uuid.UUID
	RoomData
}

// RoomData is what a room stores. Inherits lists the groups that follow the building, in the fixed order.
type RoomData struct {
	BuildingID *uuid.UUID
	Name       string
	RoomValues
	Status   Status
	Inherits []Group
}

// Follows reports whether the room follows its building for a group.
func (r RoomData) Follows(g Group) bool { return slices.Contains(r.Inherits, g) }

// State is what the room currently follows, in the form the inheritance rules take.
func (r RoomData) State() RoomState {
	return RoomState{InheritsCharges: r.Follows(GroupCharges), InheritsMedia: r.Follows(GroupMedia)}
}

// ContentDiffers reports whether next changes something a listing shows: the name, the building, what the room
// follows or any of its values (the status is not part of it).
func (r RoomData) ContentDiffers(next RoomData) bool {
	return r.Name != next.Name || !equalPtr(r.BuildingID, next.BuildingID) || !slices.Equal(r.Inherits, next.Inherits) ||
		!equalPtr(r.WardCode, next.WardCode) || r.AddressDetail != next.AddressDetail ||
		!equalPtr(r.Area, next.Area) || r.PriceVND != next.PriceVND || r.MinPeriod != next.MinPeriod ||
		r.PeriodUnit != next.PeriodUnit || !equalPtr(r.Description, next.Description)
}

// RoomRef is a room as a bulk operation sees it.
type RoomRef struct {
	ID     uuid.UUID
	Name   string
	Status Status
}

// RoomRun is a run of new rooms in one building: one name each, the same values.
type RoomRun struct {
	BuildingID uuid.UUID
	Names      []string
	RoomValues
	Status   Status
	Inherits []Group
}

// RoomCopySource is a room of a building that is being copied.
type RoomCopySource struct {
	ID              uuid.UUID
	InheritsCharges bool
	InheritsMedia   bool
}

// RoomCopyPair says which new room is the copy of which source room.
type RoomCopyPair struct {
	Source uuid.UUID
	Dest   uuid.UUID
}

// BuildingToDelete is a building considered for deletion, with how many live rooms it holds.
type BuildingToDelete struct {
	ID         uuid.UUID
	Name       string
	RoomsCount int
}

// Charge is a stored charge of a building or room.
type Charge struct {
	ChargeRow
	Name      string
	Kind      string
	AmountVND int64
	Unit      string
	Note      *string
	Order     int
}

// SameAs reports whether the charge already has the values and position of a submitted item.
func (c Charge) SameAs(in ChargeInput, order int) bool {
	return c.Name == in.Name && c.Kind == in.Kind && c.AmountVND == in.AmountVND && c.Unit == in.Unit &&
		c.Order == order && equalPtr(c.Note, in.Note)
}

// FollowsBuilding says whether the charge is a copy of a building charge.
func (c Charge) FollowsBuilding() bool { return c.TemplateID != nil }

// Media is a stored picture of a building or room.
type Media struct {
	ID           uuid.UUID
	RoomID       *uuid.UUID // set on the pictures of a room
	Kind         string
	FileKey      string
	ThumbKey     string
	ContentType  string
	Width        int
	Height       int
	SizeBytes    int
	OriginalName *string
	DisplayOrder int
}

// NewMedia is a picture to store.
type NewMedia struct {
	Kind         string
	FileKey      string
	ThumbKey     string
	ContentType  string
	SizeBytes    int
	Width        int
	Height       int
	OriginalName *string
	DisplayOrder int
}

// MediaFile is the new image of a picture that keeps its place and kind.
type MediaFile struct {
	FileKey      string
	ThumbKey     string
	ContentType  string
	SizeBytes    int
	Width        int
	Height       int
	OriginalName *string
}

// MediaPlace is the new kind and 1-based position of a picture.
type MediaPlace struct {
	ID       uuid.UUID
	Kind     string
	Position int
}

// Cover is the cover picture of a building or room: its two file keys.
type Cover struct {
	Key      string
	ThumbKey string
}

// MediaCopy is a duplicated file pair of a picture, to record as a new row.
type MediaCopy struct {
	SourceID uuid.UUID
	File     string
	Thumb    string
}

// RoomMediaCopy is a MediaCopy for the pictures of one new room.
type RoomMediaCopy struct {
	RoomID uuid.UUID
	MediaCopy
}

// ProcessedImage is a verified picture ready to store, with its thumbnail.
type ProcessedImage struct {
	Data        []byte
	ContentType string // "image/webp" or "image/jpeg"
	Ext         string // "webp" or "jpg"
	Width       int
	Height      int
	Thumb       []byte // JPEG
}

// FileInfo describes a stored file.
type FileInfo struct {
	Size        int64
	ContentType string
}

// Read models of the queries. Money is whole dong; times are as stored.

// ProvinceRef is a province inside a ward reference.
type ProvinceRef struct {
	Code int32
	Name string
}

// WardRef is the ward of an address with its province.
type WardRef struct {
	Code     int32
	Name     string
	Province ProvinceRef
}

// BuildingRef names the building of a room.
type BuildingRef struct {
	ID   uuid.UUID
	Name string
}

// BuildingSummary is a building as a list shows it.
type BuildingSummary struct {
	ID             uuid.UUID
	Name           string
	WardCode       *int32
	Ward           *WardRef
	AddressDetail  string
	Description    *string
	PriceVND       *int64
	Area           *float64
	MinPeriod      *int
	PeriodUnit     *string
	Cover          *Cover
	DisplayOrder   int
	RoomsCount     int
	AvailableRooms int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// BuildingDetail is one building with its charges and pictures.
type BuildingDetail struct {
	BuildingSummary
	Charges []Charge
	Media   []Media
}

// RoomSummary is a room as a list shows it (the description is only shown on the detail).
type RoomSummary struct {
	ID            uuid.UUID
	Name          string
	Status        Status
	DisplayOrder  int
	Building      *BuildingRef
	PriceVND      int64
	PeriodUnit    string
	MinPeriod     int
	Area          *float64
	AddressDetail string
	Ward          *WardRef
	Description   *string
	Inherits      []Group
	Cover         *Cover
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Follows reports whether the room follows its building for a group.
func (r RoomSummary) Follows(g Group) bool { return slices.Contains(r.Inherits, g) }

// RoomDetail is one room with its charges and pictures.
type RoomDetail struct {
	RoomSummary
	Charges []Charge
	Media   []Media
}

// CopyResult is what copying a building answers.
type CopyResult struct {
	Building    BuildingDetail
	RoomsCopied int
}

// BuildingQuery narrows and pages the list of a landlord's buildings. NameLike is an escaped, lower case LIKE
// pattern body; Sort is "", "name", "-name", "created_at" or "-created_at" ("" = the landlord's own order).
type BuildingQuery struct {
	NameLike     *string
	WardCode     *int32
	ProvinceCode *int32
	Sort         string
	Limit        int
	Offset       int
}

// RoomQuery narrows and pages the list of a landlord's rooms.
type RoomQuery struct {
	NameLike     *string
	Status       *string
	BuildingID   *uuid.UUID
	NoBuilding   bool
	WardCode     *int32
	ProvinceCode *int32
	PriceFrom    *int64
	PriceTo      *int64
	AreaFrom     *float64
	AreaTo       *float64
	Sort         string
	Limit        int
	Offset       int
}

// NormalizeCharges trims the text of a charge list and reports blank names as "charges.<i>.name required". A nil
// list stays nil.
func NormalizeCharges(items []ChargeInput) ([]ChargeInput, []Violation) {
	if items == nil {
		return nil, nil
	}
	out := make([]ChargeInput, len(items))
	var violations []Violation
	for i, item := range items {
		item.Name = strings.TrimSpace(item.Name)
		if item.Name == "" {
			violations = append(violations, Violation{Name: chargeNameField(i), Rule: RuleRequired})
		}
		item.Note = BlankToNil(item.Note)
		out[i] = item
	}
	return out, violations
}

func chargeNameField(i int) string { return "charges." + strconv.Itoa(i) + ".name" }
