package property

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// Seeder gives the demo landlord some buildings and rooms.
type Seeder struct {
	buildings *Buildings
	rooms     *Rooms
}

// NewSeeder returns a seeder on the building and room use cases (so the rules apply to the demo data too).
func NewSeeder(b *Buildings, r *Rooms) *Seeder { return &Seeder{buildings: b, rooms: r} }

// SeedDemo gives the demo landlord a first building with default rent, area and charges and a few rooms: P101 to
// P105 follow the building, P106 has its own rent, and A1 stands outside any building. It is safe to repeat: it
// only fills a building that has no rent yet and no rooms yet.
func (s *Seeder) SeedDemo(ctx context.Context, landlordID uuid.UUID) error {
	ward, err := s.buildings.buildings.FirstWardCode(ctx)
	var wardCode *int32
	if err == nil {
		wardCode = &ward
	}
	if _, err := s.buildings.EnsureFirstBuilding(ctx, landlordID, FirstBuilding{
		Name: "Nhà trọ Demo", WardCode: wardCode, AddressDetail: "12 Lê Lợi",
	}); err != nil {
		return err
	}
	rows, _, err := s.buildings.List(ctx, landlordID, BuildingFilter{}, 1, 0)
	if err != nil || len(rows) == 0 {
		return err
	}
	building := rows[0]
	if building.PriceVND == nil {
		if err := s.seedDefaults(ctx, landlordID, building); err != nil {
			return err
		}
	}
	if building.RoomsCount > 0 {
		return nil // already has its rooms
	}
	return s.seedRooms(ctx, landlordID, building.ID)
}

func (s *Seeder) seedDefaults(ctx context.Context, landlordID uuid.UUID, b domain.BuildingSummary) error {
	price, area, period, unit := int64(2_500_000), 20.0, int32(1), domain.UnitMonth
	description := "Nhà trọ yên tĩnh, gần chợ."
	_, err := s.buildings.Update(ctx, landlordID, b.ID, BuildingInput{
		Name: b.Name, WardCode: b.WardCode, AddressDetail: b.AddressDetail, Description: &description,
		PriceVND: &price, Area: &area, MinPeriod: &period, PeriodUnit: &unit,
		Charges: []domain.ChargeInput{
			{Name: "Điện", Kind: "electricity", AmountVND: 3_500, Unit: "per_kwh"},
			{Name: "Nước", Kind: "water", AmountVND: 20_000, Unit: "per_cubic_meter"},
			{Name: "Internet", Kind: "internet", AmountVND: 100_000, Unit: "per_period"},
		},
	})
	return err
}

func (s *Seeder) seedRooms(ctx context.Context, landlordID, buildingID uuid.UUID) error {
	if _, err := s.rooms.CreateRun(ctx, landlordID, buildingID, RunInput{NamePrefix: "P", From: 101, To: 105}); err != nil {
		return err
	}
	own := int64(3_000_000)
	if _, err := s.rooms.Create(ctx, landlordID, RoomInput{BuildingID: &buildingID, Name: "P106", PriceVND: &own}); err != nil {
		return err
	}
	address, price, area, status := "45 Trần Phú", int64(1_800_000), 18.0, domain.StatusMaintenance
	_, err := s.rooms.Create(ctx, landlordID, RoomInput{
		Name: "A1", AddressDetail: &address, PriceVND: &price, Area: &area, Status: &status,
	})
	return err
}
