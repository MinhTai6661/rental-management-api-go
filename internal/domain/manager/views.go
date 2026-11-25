package manager

import (
	"time"

	"github.com/google/uuid"
)

// RoomInfo is a room of the landlord as the manager rules need it.
type RoomInfo struct {
	ID   uuid.UUID
	Name string
}

// Group is one manager's hand-overs, summed up for the landlord's list.
type Group struct {
	Manager       Person
	RoomCount     int
	AssignmentIDs []uuid.UUID
	RoomIDs       []uuid.UUID
	NotifyCount   int
	// Capabilities counts, per capability code, how many of the hand-overs hold it.
	Capabilities map[Capability]int
	LatestAt     time.Time
}

// ManagedGroup is the rooms one landlord handed to a manager, summed up.
type ManagedGroup struct {
	LandlordID   uuid.UUID
	LandlordName string
	RoomCount    int
	RoomIDs      []uuid.UUID
	LatestAt     time.Time
}

// AssignmentDetail is a hand-over with the names around it.
type AssignmentDetail struct {
	Assignment
	Room    Room
	Manager Person
	// Landlord is the name the manager knows the landlord by (set in the manager's own lists).
	LandlordName string
}
