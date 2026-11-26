// Package report holds the rules of problem reports: who a report goes to, who may read it, write under it, change
// where it stands and delete it. It is plain Go: no database, no HTTP, no framework.
package report

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Kind says what a report is about, and so who it goes to.
type Kind string

// The kinds.
const (
	// KindRoom is about a room, written by its tenant for the landlord of that room.
	KindRoom Kind = "room"
	// KindFeature is about a feature of the app, written by a tenant or a landlord for the admins.
	KindFeature Kind = "feature"
)

// Status says where a report stands; only the recipient changes it.
type Status string

// The statuses.
const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusResolved   Status = "resolved"
)

// ErrUnknownKind and ErrUnknownStatus are returned for a value that is not in its set.
var (
	ErrUnknownKind   = errors.New("unknown report kind")
	ErrUnknownStatus = errors.New("unknown report status")
)

// Kinds lists the kinds.
func Kinds() []Kind { return []Kind{KindRoom, KindFeature} }

// Statuses lists the statuses.
func Statuses() []Status { return []Status{StatusOpen, StatusInProgress, StatusResolved} }

// ParseKind returns the kind named s, or ErrUnknownKind.
func ParseKind(s string) (Kind, error) {
	for _, k := range Kinds() {
		if string(k) == s {
			return k, nil
		}
	}
	return "", ErrUnknownKind
}

// ParseStatus returns the status named s, or ErrUnknownStatus.
func ParseStatus(s string) (Status, error) {
	for _, st := range Statuses() {
		if string(st) == s {
			return st, nil
		}
	}
	return "", ErrUnknownStatus
}

// Report is a problem somebody reported.
type Report struct {
	ID         uuid.UUID
	ReporterID uuid.UUID
	Kind       Kind
	// RoomID is the room a room report is about; nil for a feature report.
	RoomID *uuid.UUID
	// RoomLandlordID is the owner of that room (who the report goes to); nil for a feature report.
	RoomLandlordID *uuid.UUID
	Subject        string
	Body           string
	Status         Status
	CreatedAt      time.Time
	// DeletedAt is set when the author deleted the report.
	DeletedAt *time.Time
}

// Deleted says whether the author deleted the report.
func (r Report) Deleted() bool { return r.DeletedAt != nil }

// NewReport is what is stored for a report that was just written.
type NewReport struct {
	ReporterID uuid.UUID
	Kind       Kind
	RoomID     *uuid.UUID
	Subject    string
	Body       string
	At         time.Time
}

// RoomRef is a room a report can be about, with the landlord it goes to.
type RoomRef struct {
	ID         uuid.UUID
	Name       string
	LandlordID uuid.UUID
}

// Person is somebody who appears on a report, with what is needed to tell which side they wrote from.
type Person struct {
	ID   uuid.UUID
	Name string
	// IsStaff is true for an account holding the admin or super admin role; IsLandlord for one holding the landlord role.
	IsStaff    bool
	IsLandlord bool
}

// Side is which side of a conversation a person wrote from.
type Side string

// The sides.
const (
	SideAdmin    Side = "admin"
	SideLandlord Side = "landlord"
	SideTenant   Side = "tenant"
)

// SideOf is the side the person is on in the report. An account can be a renter and a landlord, so it comes from the
// report where it can: in a room report the landlord of the room is the landlord and everybody else a tenant.
func SideOf(r Report, p Person) Side {
	switch {
	case p.IsStaff:
		return SideAdmin
	case r.Kind == KindRoom:
		if r.RoomLandlordID != nil && *r.RoomLandlordID == p.ID {
			return SideLandlord
		}
		return SideTenant
	case p.IsLandlord:
		return SideLandlord
	}
	return SideTenant
}

// View is a report as a reader sees it.
type View struct {
	Report
	RoomName     string
	Reporter     Person
	RepliesCount int
}

// Reply is one message of the conversation under a report.
type Reply struct {
	ID        uuid.UUID
	ReportID  uuid.UUID
	Body      string
	CreatedAt time.Time
	Author    Person
}

// Filter narrows a list; nil and empty mean no filter.
type Filter struct {
	Status *Status
	Kind   *Kind
	// Search is a word to find in the subject or the body.
	Search string
}
