package property

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	sqlcgen "rental-management/internal/repository/sqlc"
	"rental-management/pkg/apperr"
)

// normalizeCharges trims the text of a charge list and reports blank names as "charges.<i>.name required".
func normalizeCharges(items []chargeInput) ([]chargeInput, []apperr.Field) {
	if items == nil {
		return nil, nil
	}
	out := make([]chargeInput, len(items))
	var fields []apperr.Field
	for i, item := range items {
		item.Name = strings.TrimSpace(item.Name)
		if item.Name == "" {
			fields = append(fields, apperr.Field{Name: fmt.Sprintf("charges.%d.name", i), Rule: apperr.RuleRequired})
		}
		item.Note = blankToNil(item.Note)
		out[i] = item
	}
	return out, fields
}

// blankToNil trims s and returns nil for blank text.
func blankToNil(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// storedCharge is a stored charge with its values, so a submitted list can be compared against it.
type storedCharge struct {
	chargeRow
	Name      string
	Kind      string
	AmountVND int64
	Unit      string
	Note      *string
	Order     int
}

func (c storedCharge) sameAs(in chargeInput, order int) bool {
	return c.Name == in.Name && c.Kind == in.Kind && c.AmountVND == in.AmountVND && c.Unit == in.Unit &&
		c.Order == order && equalPtr(c.Note, in.Note)
}

func storedOfBuilding(rows []sqlcgen.ListChargesOfBuildingsRow) []storedCharge {
	out := make([]storedCharge, len(rows))
	for i, r := range rows {
		out[i] = storedCharge{
			chargeRow: chargeRow{ID: r.ID, TemplateID: r.BuildingChargeID}, Name: r.Name, Kind: r.Kind,
			AmountVND: r.AmountVnd, Unit: r.Unit, Note: r.Note, Order: int(r.DisplayOrder),
		}
	}
	return out
}

func storedOfRoom(rows []sqlcgen.ListChargesOfRoomsRow) []storedCharge {
	out := make([]storedCharge, len(rows))
	for i, r := range rows {
		out[i] = storedCharge{
			chargeRow: chargeRow{ID: r.ID, TemplateID: r.BuildingChargeID}, Name: r.Name, Kind: r.Kind,
			AmountVND: r.AmountVnd, Unit: r.Unit, Note: r.Note, Order: int(r.DisplayOrder),
		}
	}
	return out
}

// insertBuildingCharges adds charges to a building in the order given.
func insertBuildingCharges(ctx context.Context, q *sqlcgen.Queries, buildingID uuid.UUID, items []chargeCreate) error {
	for _, c := range items {
		err := q.InsertBuildingCharge(ctx, sqlcgen.InsertBuildingChargeParams{
			BuildingID: &buildingID, Name: c.Input.Name, Kind: c.Input.Kind, AmountVnd: c.Input.AmountVND,
			Unit: c.Input.Unit, Note: c.Input.Note, DisplayOrder: int16(c.Order), //nolint:gosec // at most 30 items
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// insertRoomCharges adds own charges to a room in the order given.
func insertRoomCharges(ctx context.Context, q *sqlcgen.Queries, roomID uuid.UUID, items []chargeCreate) error {
	for _, c := range items {
		err := q.InsertRoomCharge(ctx, sqlcgen.InsertRoomChargeParams{
			RoomID: &roomID, Name: c.Input.Name, Kind: c.Input.Kind, AmountVnd: c.Input.AmountVND,
			Unit: c.Input.Unit, Note: c.Input.Note, DisplayOrder: int16(c.Order), //nolint:gosec // at most 30 items
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// syncCharges makes the stored charges of one owner equal to the submitted list (see planChargeSync); insert adds
// the new ones. It reports whether anything actually changed.
func syncCharges(ctx context.Context, q *sqlcgen.Queries, stored []storedCharge, submitted []chargeInput,
	insert func([]chargeCreate) error) (bool, error) {
	rows := make([]chargeRow, len(stored))
	byID := make(map[uuid.UUID]storedCharge, len(stored))
	for i, c := range stored {
		rows[i] = c.chargeRow
		byID[c.ID] = c
	}
	updates, creates, deletes := planChargeSync(rows, submitted)
	changed := len(creates) > 0 || len(deletes) > 0
	for _, u := range updates {
		if !byID[u.ID].sameAs(u.Input, u.Order) {
			changed = true
			err := q.UpdateCharge(ctx, sqlcgen.UpdateChargeParams{
				ID: u.ID, Name: u.Input.Name, Kind: u.Input.Kind, AmountVnd: u.Input.AmountVND, Unit: u.Input.Unit,
				Note: u.Input.Note, DisplayOrder: int16(u.Order), //nolint:gosec // at most 30 items
			})
			if err != nil {
				return false, err
			}
		}
	}
	if len(deletes) > 0 {
		if err := q.SoftDeleteCharges(ctx, deletes); err != nil {
			return false, err
		}
	}
	return changed, insert(creates)
}

// syncBuildingCharges makes the building's charges equal to the submitted list; it reports whether they changed.
func syncBuildingCharges(ctx context.Context, q *sqlcgen.Queries, buildingID uuid.UUID, submitted []chargeInput) (bool, error) {
	rows, err := q.ListChargesOfBuildings(ctx, []uuid.UUID{buildingID})
	if err != nil {
		return false, err
	}
	return syncCharges(ctx, q, storedOfBuilding(rows), submitted, func(c []chargeCreate) error {
		return insertBuildingCharges(ctx, q, buildingID, c)
	})
}

// syncRoomCharges makes the room's charges equal to the submitted list; it reports whether they changed.
func syncRoomCharges(ctx context.Context, q *sqlcgen.Queries, roomID uuid.UUID, submitted []chargeInput) (bool, error) {
	rows, err := q.ListChargesOfRooms(ctx, []uuid.UUID{roomID})
	if err != nil {
		return false, err
	}
	return syncCharges(ctx, q, storedOfRoom(rows), submitted, func(c []chargeCreate) error {
		return insertRoomCharges(ctx, q, roomID, c)
	})
}

// followCharges makes the rooms carry exactly the live charges of the building: copies of its templates, updated in
// place when a template changed, dropped when it is gone, added when it is new; a room's own charges go (planFollow
// as three set statements, so any number of rooms costs three queries).
func followCharges(ctx context.Context, q *sqlcgen.Queries, roomIDs []uuid.UUID, buildingID uuid.UUID) error {
	if len(roomIDs) == 0 {
		return nil
	}
	if err := q.UpdateChargeCopiesFromTemplates(ctx, sqlcgen.UpdateChargeCopiesFromTemplatesParams{RoomIds: roomIDs, BuildingID: &buildingID}); err != nil {
		return err
	}
	if err := q.DeleteChargeCopiesWithoutTemplate(ctx, sqlcgen.DeleteChargeCopiesWithoutTemplateParams{RoomIds: roomIDs, BuildingID: &buildingID}); err != nil {
		return err
	}
	return q.InsertMissingChargeCopies(ctx, sqlcgen.InsertMissingChargeCopiesParams{RoomIds: roomIDs, BuildingID: &buildingID})
}

// insertChargesForRooms gives every room the same own charges.
func insertChargesForRooms(ctx context.Context, q *sqlcgen.Queries, roomIDs []uuid.UUID, items []chargeInput) error {
	if len(roomIDs) == 0 || len(items) == 0 {
		return nil
	}
	p := sqlcgen.InsertChargesForRoomsParams{RoomIds: roomIDs}
	for i, c := range items {
		p.Names = append(p.Names, c.Name)
		p.Kinds = append(p.Kinds, c.Kind)
		p.Amounts = append(p.Amounts, c.AmountVND)
		p.Units = append(p.Units, c.Unit)
		note := ""
		if c.Note != nil {
			note = *c.Note
		}
		p.Notes = append(p.Notes, note)
		p.Orders = append(p.Orders, int16(i+1)) //nolint:gosec // at most 30 items
	}
	return q.InsertChargesForRooms(ctx, p)
}

// chargesToCreate lists a whole submitted list as creations (a new owner has nothing to update).
func chargesToCreate(items []chargeInput) []chargeCreate {
	out := make([]chargeCreate, len(items))
	for i, item := range items {
		item.ID = nil
		out[i] = chargeCreate{Input: item, Order: i + 1}
	}
	return out
}
