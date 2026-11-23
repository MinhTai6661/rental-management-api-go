package property

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// Update replaces the fields of a building and pushes what changed into the rooms that follow it, all in one
// transaction.
func (b *Buildings) Update(ctx context.Context, landlordID, id uuid.UUID, in BuildingInput) (domain.BuildingDetail, error) {
	in, charges, err := in.normalized()
	if err != nil {
		return domain.BuildingDetail{}, err
	}
	err = b.tx.WithinTx(ctx, func(ctx context.Context) error {
		current, err := b.buildings.ForUpdate(ctx, landlordID, id)
		if err != nil {
			return notFound(err)
		}
		if err := b.checkWard(ctx, in.WardCode); err != nil {
			return err
		}
		if err := b.checkBuildingName(ctx, landlordID, in.Name, &id); err != nil {
			return err
		}
		if in.PriceVND == nil {
			following, err := b.buildings.CountRoomsFollowingPrice(ctx, id)
			if err != nil {
				return err
			}
			if following > 0 {
				return &domain.BuildingPriceInUseError{Count: following}
			}
		}
		if err := b.buildings.Update(ctx, landlordID, id, in.data()); err != nil {
			return err
		}
		before, after := current.BuildingDefaults, in.defaults()
		reached, err := b.buildings.PushToRooms(ctx, id, after, domain.AddressChanged(before, after), domain.Propagation(before, after))
		if err != nil {
			return err
		}
		if in.Charges != nil {
			changed, err := b.syncBuildingCharges(ctx, id, charges)
			if err != nil {
				return err
			}
			if changed {
				followers, err := b.rooms.FollowingCharges(ctx, id)
				if err != nil {
					return err
				}
				if err := b.follow(ctx, followers, id); err != nil {
					return err
				}
				reached = append(reached, followers...)
			}
		}
		return b.contentChanged(ctx, reached)
	})
	if err != nil {
		return domain.BuildingDetail{}, buildingNameTaken(err)
	}
	return b.Get(ctx, landlordID, id)
}
