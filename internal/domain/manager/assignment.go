package manager

import (
	"time"

	"github.com/google/uuid"
)

// AssignmentStatus says whether a hand-over of a room is in force.
type AssignmentStatus string

// The statuses of a hand-over.
const (
	AssignmentActive  AssignmentStatus = "active"
	AssignmentRevoked AssignmentStatus = "revoked"
)

// Assignment is one room handed to one manager. A hand-over exists only after the person asked accepted; a room has
// at most one active hand-over.
type Assignment struct {
	ID         uuid.UUID
	LandlordID uuid.UUID
	RoomID     uuid.UUID
	ManagerID  uuid.UUID
	Status     AssignmentStatus
	// NotifyLandlord: tell the landlord when the manager sends or edits a bill.
	NotifyLandlord bool
	Capabilities   []Capability
	AssignedByID   uuid.UUID
	AcceptedAt     time.Time
	RevokedAt      *time.Time
	CreatedAt      time.Time
}

// Active says whether the hand-over is in force (false for nil).
func (a *Assignment) Active() bool { return a != nil && a.Status == AssignmentActive }

// Can says whether the manager holds the capability on this room (false for nil or a revoked hand-over).
func (a *Assignment) Can(c Capability) bool { return a.Active() && Has(a.Capabilities, c) }

// Settings are the capabilities and the notify switch of a hand-over, for before/after records.
type Settings struct {
	Capabilities   []string
	NotifyLandlord bool
}

// SettingsOf returns the settings of the hand-over.
func (a *Assignment) SettingsOf() Settings {
	return Settings{Capabilities: Strings(a.Capabilities), NotifyLandlord: a.NotifyLandlord}
}

// Update applies a grant, a revoke and a notify switch (nil keeps it) and returns the settings before and after.
func (a *Assignment) Update(grant, revoke []Capability, notify *bool) (before, after Settings) {
	before = a.SettingsOf()
	a.Capabilities = Change(a.Capabilities, grant, revoke)
	if notify != nil {
		a.NotifyLandlord = *notify
	}
	return before, a.SettingsOf()
}

// Changed says whether the two settings differ.
func (s Settings) Changed(other Settings) bool {
	if s.NotifyLandlord != other.NotifyLandlord || len(s.Capabilities) != len(other.Capabilities) {
		return true
	}
	for i := range s.Capabilities {
		if s.Capabilities[i] != other.Capabilities[i] {
			return true
		}
	}
	return false
}

// Flag says how many of a group's hand-overs hold something: all of them, some, or none.
type Flag string

// The flags.
const (
	FlagAll  Flag = "all"
	FlagSome Flag = "some"
	FlagNone Flag = "none"
)

// FlagOf turns "held of total" into a Flag (a group with nothing in it holds nothing).
func FlagOf(held, total int) Flag {
	switch {
	case total > 0 && held >= total:
		return FlagAll
	case held > 0:
		return FlagSome
	}
	return FlagNone
}
