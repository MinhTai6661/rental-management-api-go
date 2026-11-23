package property

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
	"rental-management/internal/domain/shared"
)

// Create adds a building to the landlord, within the limit of their plan.
func (b *Buildings) Create(ctx context.Context, landlordID uuid.UUID, in BuildingInput) (domain.BuildingDetail, error) {
	in, charges, err := in.normalized()
	if err != nil {
		return domain.BuildingDetail{}, err
	}
	var id uuid.UUID
	err = b.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := b.buildings.LockLandlord(ctx, landlordID); err != nil {
			return err
		}
		if err := b.checkBuildingLimit(ctx, landlordID); err != nil {
			return err
		}
		id, err = b.insertBuilding(ctx, landlordID, in, chargesToCreate(charges))
		return err
	})
	if err != nil {
		return domain.BuildingDetail{}, buildingNameTaken(err)
	}
	return b.Get(ctx, landlordID, id)
}

// insertBuilding checks the ward and the name, takes the next position and stores the building with its charges.
func (b *Buildings) insertBuilding(ctx context.Context, landlordID uuid.UUID, in BuildingInput, charges []domain.ChargeCreate) (uuid.UUID, error) {
	if err := b.checkWard(ctx, in.WardCode); err != nil {
		return uuid.Nil, err
	}
	if err := b.checkBuildingName(ctx, landlordID, in.Name, nil); err != nil {
		return uuid.Nil, err
	}
	id, err := b.buildings.Insert(ctx, landlordID, in.data())
	if err != nil {
		return uuid.Nil, err
	}
	return id, b.charges.AddToBuilding(ctx, id, charges)
}

// checkBuildingName refuses a name another live building of the landlord already uses (any case).
func (b *Buildings) checkBuildingName(ctx context.Context, landlordID uuid.UUID, name string, except *uuid.UUID) error {
	taken, err := b.buildings.NameTaken(ctx, landlordID, name, except)
	if err != nil {
		return err
	}
	if taken {
		return domain.ErrBuildingNameTaken
	}
	return nil
}

// buildingNameTaken turns the unique index refusing a duplicate name (a race the pre-check cannot close) into the
// same error as the pre-check.
func buildingNameTaken(err error) error {
	if shared.IsUnique(err) {
		return domain.ErrBuildingNameTaken
	}
	return err
}

// EnsureFirstBuilding gives a landlord who never had a building their first one, with the position 1. A landlord
// who deleted all their buildings does not get another. It reports whether it created one.
func (b *Buildings) EnsureFirstBuilding(ctx context.Context, landlordID uuid.UUID, first FirstBuilding) (bool, error) {
	created := false
	err := b.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := b.buildings.LockLandlord(ctx, landlordID); err != nil {
			return err
		}
		ever, err := b.buildings.AnyEver(ctx, landlordID)
		if err != nil || ever {
			return err
		}
		in, _, err := BuildingInput{Name: first.Name, WardCode: first.WardCode, AddressDetail: first.AddressDetail}.normalized()
		if err != nil {
			return err
		}
		if _, err := b.insertBuilding(ctx, landlordID, in, nil); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, buildingNameTaken(err)
}
