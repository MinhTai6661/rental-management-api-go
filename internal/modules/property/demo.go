package property

import (
	"context"

	"github.com/google/uuid"

	sqlcgen "rental-management/internal/adapter/persistence/postgres/sqlc"
)

// SeedDemo gives the demo landlord a first building with default rent, area and charges and a few rooms: P101 to
// P105 follow the building, P106 has its own rent, and A1 stands outside any building. It goes through the service,
// so the rules apply, and it is safe to repeat: it only fills a building that has no rent yet and no rooms yet.
func SeedDemo(ctx context.Context, svc *Service, landlordID uuid.UUID) error {
	ward, err := svc.store.Queries().PropertyFirstWardCode(ctx)
	var wardCode *int32
	if err == nil {
		wardCode = &ward
	}
	if _, err := svc.EnsureFirstBuilding(ctx, landlordID, FirstBuilding{
		Name: "Nhà trọ Demo", WardCode: wardCode, AddressDetail: "12 Lê Lợi",
	}); err != nil {
		return err
	}
	rows, err := svc.store.Queries().SearchBuildings(ctx, sqlcgen.SearchBuildingsParams{LandlordID: landlordID, PageLimit: 1})
	if err != nil || len(rows) == 0 {
		return err
	}
	building := rows[0]
	if building.PriceVnd == nil {
		if err := seedDemoDefaults(ctx, svc, landlordID, building); err != nil {
			return err
		}
	}
	if building.RoomsCount > 0 {
		return nil // already has its rooms
	}
	return seedDemoRooms(ctx, svc, landlordID, building.ID)
}

func seedDemoDefaults(ctx context.Context, svc *Service, landlordID uuid.UUID, b sqlcgen.SearchBuildingsRow) error {
	price, area, period, unit := int64(2_500_000), 20.0, int32(1), UnitMonth
	description := "Nhà trọ yên tĩnh, gần chợ."
	_, err := svc.UpdateBuilding(ctx, landlordID, b.ID, BuildingInput{
		Name: b.Name, WardCode: b.WardCode, AddressDetail: b.AddressDetail, Description: &description,
		PriceVND: &price, Area: &area, MinPeriod: &period, PeriodUnit: &unit,
		Charges: []ChargeInput{
			{Name: "Điện", Kind: "electricity", AmountVND: 3_500, Unit: "per_kwh"},
			{Name: "Nước", Kind: "water", AmountVND: 20_000, Unit: "per_cubic_meter"},
			{Name: "Internet", Kind: "internet", AmountVND: 100_000, Unit: "per_period"},
		},
	})
	return err
}

func seedDemoRooms(ctx context.Context, svc *Service, landlordID, buildingID uuid.UUID) error {
	if _, err := svc.CreateRoomsInBuilding(ctx, landlordID, buildingID, RunInput{NamePrefix: "P", From: 101, To: 105}); err != nil {
		return err
	}
	own := int64(3_000_000)
	if _, err := svc.CreateRoom(ctx, landlordID, RoomInput{BuildingID: &buildingID, Name: "P106", PriceVND: &own}); err != nil {
		return err
	}
	address, price, area, status := "45 Trần Phú", int64(1_800_000), 18.0, string(StatusMaintenance)
	_, err := svc.CreateRoom(ctx, landlordID, RoomInput{
		Name: "A1", AddressDetail: &address, PriceVND: &price, Area: &area, Status: &status,
	})
	return err
}
