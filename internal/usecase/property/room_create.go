package property

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/property"
)

// Create adds a room to the landlord, within the limit of their plan.
func (r *Rooms) Create(ctx context.Context, landlordID uuid.UUID, in RoomInput) (domain.RoomDetail, error) {
	in, charges, err := in.normalized()
	if err != nil {
		return domain.RoomDetail{}, err
	}
	status := statusOrDefault(in.Status, domain.StatusAvailable)
	var id uuid.UUID
	err = r.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := r.buildings.LockLandlord(ctx, landlordID); err != nil {
			return err
		}
		var building *domain.BuildingDefaults
		if in.BuildingID != nil {
			d, err := r.loadBuilding(ctx, landlordID, *in.BuildingID)
			if err != nil {
				return err
			}
			building = &d
		}
		fields := in.fields(charges)
		groups := domain.ResolveInheritance(in.Inherit, domain.CarriedGroups(fields, nil), building != nil)
		values, violations := domain.EffectiveValues(building, groups, fields)
		if len(violations) > 0 {
			return domain.Invalid(violations...)
		}
		if err := r.checkWard(ctx, values.WardCode); err != nil {
			return err
		}
		if err := r.checkRoomLimit(ctx, landlordID, 1); err != nil {
			return err
		}
		if err := r.checkRoomName(ctx, landlordID, in.BuildingID, in.Name, nil); err != nil {
			return err
		}
		id, err = r.rooms.Insert(ctx, landlordID, domain.RoomData{
			BuildingID: in.BuildingID, Name: in.Name, RoomValues: values, Status: status, Inherits: groups,
		})
		if err != nil {
			return err
		}
		if slices.Contains(groups, domain.GroupCharges) {
			err = r.follow(ctx, []uuid.UUID{id}, *in.BuildingID)
		} else {
			err = r.charges.AddToRoom(ctx, id, chargesToCreate(charges))
		}
		if err != nil {
			return err
		}
		if slices.Contains(groups, domain.GroupMedia) {
			if err := r.media.CoverRoomsFromBuilding(ctx, []uuid.UUID{id}); err != nil {
				return err
			}
		}
		if status == domain.StatusRented {
			return r.observer.StatusChanged(ctx, []domain.StatusChange{{RoomID: id, To: status}})
		}
		return nil
	})
	if err != nil {
		return domain.RoomDetail{}, roomNameTaken(err)
	}
	return r.Get(ctx, landlordID, id)
}

// maxNamesShown is how many taken names an error lists.
const maxNamesShown = 5

// CreateRun adds a run of rooms (prefix + number range) to a building in one go. Values the request leaves out
// follow the building, and every new room follows the building's pictures.
func (r *Rooms) CreateRun(ctx context.Context, landlordID, buildingID uuid.UUID, in RunInput) (RunResult, error) {
	names, err := domain.RoomNames(in.NamePrefix, in.From, in.To, in.Padding)
	if err != nil {
		return RunResult{}, err
	}
	own, charges, err := RoomInput{
		Name: "run", AddressDetail: nil, PriceVND: in.PriceVND, Area: in.Area, MinPeriod: in.MinPeriod,
		PeriodUnit: in.PeriodUnit, Description: in.Description, Charges: in.Charges,
	}.normalized()
	if err != nil {
		return RunResult{}, err
	}
	if len(charges) == 0 {
		charges = nil // an empty list is "nothing of my own": the rooms follow the building's charges
	}
	status := statusOrDefault(in.Status, domain.StatusAvailable)

	var ids []uuid.UUID
	err = r.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := r.buildings.LockLandlord(ctx, landlordID); err != nil {
			return err
		}
		row, err := r.buildings.ForUpdate(ctx, landlordID, buildingID)
		if err != nil {
			return notFound(err)
		}
		building := row.BuildingDefaults
		fields := own.fields(charges)
		groups := domain.ResolveInheritance(nil, domain.CarriedGroups(fields, nil), true)
		values, violations := domain.EffectiveValues(&building, groups, fields)
		if len(violations) > 0 {
			return domain.Invalid(violations...)
		}
		if err := r.checkRoomLimit(ctx, landlordID, len(names)); err != nil {
			return err
		}
		if err := r.checkRunNames(ctx, buildingID, names); err != nil {
			return err
		}
		ids, err = r.rooms.InsertRun(ctx, landlordID, domain.RoomRun{
			BuildingID: buildingID, Names: names, RoomValues: values, Status: status, Inherits: groups,
		})
		if err != nil {
			return err
		}
		if slices.Contains(groups, domain.GroupCharges) {
			return r.follow(ctx, ids, buildingID)
		}
		if len(ids) == 0 || len(charges) == 0 {
			return nil
		}
		return r.charges.AddToRooms(ctx, ids, charges)
	})
	if err != nil {
		return RunResult{}, roomNameTaken(err)
	}
	return RunResult{IDs: ids}, nil
}

// checkRunNames refuses a run when any of its names is already used by a live room of the building.
func (r *Rooms) checkRunNames(ctx context.Context, buildingID uuid.UUID, names []string) error {
	lower := make([]string, len(names))
	for i, n := range names {
		lower[i] = strings.ToLower(n)
	}
	taken, err := r.rooms.NamesTakenInBuilding(ctx, buildingID, lower)
	if err != nil || len(taken) == 0 {
		return err
	}
	if len(taken) > maxNamesShown {
		taken = taken[:maxNamesShown]
	}
	return &domain.RoomNamesTakenError{Names: taken}
}
