package report

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

type seedRooms struct{ room RoomRef }

func (s seedRooms) FirstRentedFrom(context.Context, uuid.UUID, uuid.UUID) (RoomRef, error) {
	return s.room, nil
}

func (s seedRooms) RentedBy(context.Context, uuid.UUID, uuid.UUID) (RoomRef, error) {
	return s.room, nil
}

func TestSeedDemoIsIdempotent(t *testing.T) {
	w := newWorld(t)
	rooms := seedRooms{room: RoomRef{ID: w.room, Name: "P101", LandlordID: w.landlord.UserID}}
	seeder := NewSeeder(Deps{Tx: fakeTx{}, Reports: w.repo, Rooms: rooms, Clock: ClockFunc(time.Now)})
	ctx := context.Background()
	wrote, err := seeder.SeedDemo(ctx, w.tenant.UserID, w.landlord.UserID, w.admin.UserID)
	if err != nil || wrote != 2 {
		t.Fatalf("first run: %d, %v", wrote, err)
	}
	wrote, err = seeder.SeedDemo(ctx, w.tenant.UserID, w.landlord.UserID, w.admin.UserID)
	if err != nil || wrote != 0 {
		t.Fatalf("second run: %d, %v", wrote, err)
	}
}
