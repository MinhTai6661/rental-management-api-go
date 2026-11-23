package property

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"rental-management/internal/modules/property/sqlcgen"
	"rental-management/pkg/apperr"
	"rental-management/pkg/db"
)

// RoomInput is the body of a room: a full replacement of its fields. An absent value (nil) means the room has none
// of its own for that part; a room inside a building then follows the building (see resolveInheritance). A nil
// Charges keeps (update) or follows (create) the charges; an empty list means none. A nil Status keeps the current
// status on update and is "available" on create.
type RoomInput struct {
	BuildingID    *uuid.UUID
	Name          string
	WardCode      *int32
	AddressDetail *string
	PriceVND      *int64
	Area          *float64
	MinPeriod     *int32
	PeriodUnit    *string
	Description   *string
	Status        *string
	Charges       []ChargeInput
	Inherit       *[]Group
}

// RoomFilter narrows the list of rooms.
type RoomFilter struct {
	Name         string
	Status       *string
	BuildingID   *uuid.UUID
	NoBuilding   bool
	WardCode     *int32
	ProvinceCode *int32
	PriceFrom    *int64
	PriceTo      *int64
	AreaFrom     *float64
	AreaTo       *float64
	// Sort is "", "name", "-name", "price", "-price", "area", "-area", "created_at" or "-created_at"; the default is
	// the landlord's own order.
	Sort string
}

// RunInput asks for a run of rooms in one building: NamePrefix + number From..To (zero padded to Padding digits).
// The values it carries are the rooms' own; those left out follow the building.
type RunInput struct {
	NamePrefix  string
	From, To    int
	Padding     int
	Status      *string
	PriceVND    *int64
	Area        *float64
	MinPeriod   *int32
	PeriodUnit  *string
	Description *string
	Charges     []ChargeInput
}

// RunResult tells which rooms a run created, in the order of their names.
type RunResult struct {
	IDs   []uuid.UUID `json:"ids"`
	Count int         `json:"count"`
}

// normalized cleans the text and rounds the area. It returns the charges in the form the rules use.
func (in RoomInput) normalized() (RoomInput, []chargeInput, error) {
	var fields []apperr.Field
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		fields = append(fields, apperr.Field{Name: "name", Rule: apperr.RuleRequired})
	}
	in.AddressDetail = blankToNil(in.AddressDetail)
	in.Description = blankToNil(in.Description)
	if in.Area != nil {
		area := roundArea(*in.Area)
		in.Area = &area
	}
	charges, chargeFields := normalizeChargeInputs(in.Charges)
	fields = append(fields, chargeFields...)
	if len(fields) > 0 {
		return in, nil, apperr.Invalid(fields...)
	}
	return in, charges, nil
}

// normalizeChargeInputs converts the charges of a request to the rule form, trimmed; nil stays nil.
func normalizeChargeInputs(items []ChargeInput) ([]chargeInput, []apperr.Field) {
	if items == nil {
		return nil, nil
	}
	raw := make([]chargeInput, len(items))
	for i, c := range items {
		raw[i] = c.rule()
	}
	return normalizeCharges(raw)
}

func (in RoomInput) ruleInput(charges []chargeInput) roomInput {
	return roomInput{
		WardCode: in.WardCode, AddressDetail: in.AddressDetail, PriceVND: in.PriceVND, Area: in.Area,
		MinPeriod: in.MinPeriod, PeriodUnit: in.PeriodUnit, Description: in.Description, Charges: charges,
	}
}

// flags are the inherits_* columns of a room.
type flags struct {
	price, area, rentalPeriod, description, charges, media bool
}

func flagsOf(groups []Group) flags {
	return flags{
		price: slices.Contains(groups, GroupPrice), area: slices.Contains(groups, GroupArea),
		rentalPeriod: slices.Contains(groups, GroupRentalPeriod), description: slices.Contains(groups, GroupDescription),
		charges: slices.Contains(groups, GroupCharges), media: slices.Contains(groups, GroupMedia),
	}
}

// groupsOfRow lists the groups a stored room follows, in the fixed order.
func groupsOfRow(r sqlcgen.GetRoomForUpdateRow) []Group {
	var out []Group
	for _, g := range Groups() {
		follows := map[Group]bool{
			GroupPrice: r.InheritsPrice, GroupArea: r.InheritsArea, GroupRentalPeriod: r.InheritsRentalPeriod,
			GroupDescription: r.InheritsDescription, GroupCharges: r.InheritsCharges, GroupMedia: r.InheritsMedia,
		}[g]
		if follows {
			out = append(out, g)
		}
	}
	return out
}

func statusOrDefault(s *string, fallback Status) Status {
	if s == nil {
		return fallback
	}
	return Status(*s)
}

// loadBuildingDefaults locks a live building of the landlord and returns what it offers its rooms; a building that
// is not theirs is a 422 on building_id.
func loadBuildingDefaults(ctx context.Context, q *sqlcgen.Queries, landlordID, id uuid.UUID) (buildingDefaults, error) {
	row, err := q.GetBuildingForUpdate(ctx, sqlcgen.GetBuildingForUpdateParams{ID: id, LandlordID: landlordID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return buildingDefaults{}, apperr.Invalid(apperr.Field{Name: "building_id", Rule: apperr.RuleExists})
		}
		return buildingDefaults{}, err
	}
	return defaultsOfRow(row), nil
}

// checkRoomLimit refuses adding n rooms past the plan's limit (the caller holds the landlord lock).
func (s *Service) checkRoomLimit(ctx context.Context, q *sqlcgen.Queries, landlordID uuid.UUID, adding int) error {
	limit, err := s.plans.MaxRooms(ctx, landlordID)
	if err != nil || limit == nil {
		return err
	}
	count, err := q.CountLiveRooms(ctx, landlordID)
	if err != nil {
		return err
	}
	if limitReached(limit, int(count), adding) {
		return apperr.Conflict(apperr.CodeRoomLimit).WithParams(map[string]string{"limit": strconv.Itoa(*limit)})
	}
	return nil
}

// checkRoomName refuses a name another live room in the same building (or outside any building) already uses.
func checkRoomName(ctx context.Context, q *sqlcgen.Queries, landlordID uuid.UUID, buildingID *uuid.UUID, name string, except *uuid.UUID) error {
	taken, err := q.RoomNameTaken(ctx, sqlcgen.RoomNameTakenParams{LandlordID: landlordID, Name: name, BuildingID: buildingID, ExceptID: except})
	if err != nil {
		return err
	}
	if taken {
		return apperr.Invalid(apperr.Field{Name: "name", Rule: apperr.RuleUnique})
	}
	return nil
}

// roomNameTaken turns the unique index refusing a duplicate name (a race the pre-check cannot close) into the same
// field error as the pre-check.
func roomNameTaken(err error) error {
	if db.IsUniqueViolation(err) {
		return apperr.Invalid(apperr.Field{Name: "name", Rule: apperr.RuleUnique})
	}
	return err
}

// ListRooms returns a page of the landlord's rooms and the total.
func (s *Service) ListRooms(ctx context.Context, landlordID uuid.UUID, f RoomFilter, limit, offset int) ([]RoomSummary, int, error) {
	q := s.store.Queries()
	var nameLike *string
	if name := strings.ToLower(strings.TrimSpace(f.Name)); name != "" {
		like := escapeLike(name)
		nameLike = &like
	}
	rows, err := q.SearchRooms(ctx, sqlcgen.SearchRoomsParams{
		LandlordID: landlordID, NameLike: nameLike, Status: f.Status, BuildingID: f.BuildingID, NoBuilding: f.NoBuilding,
		WardCode: f.WardCode, ProvinceCode: f.ProvinceCode, PriceFrom: f.PriceFrom, PriceTo: f.PriceTo,
		AreaFrom: f.AreaFrom, AreaTo: f.AreaTo, Sort: f.Sort,
		PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := q.CountRooms(ctx, sqlcgen.CountRoomsParams{
		LandlordID: landlordID, NameLike: nameLike, Status: f.Status, BuildingID: f.BuildingID, NoBuilding: f.NoBuilding,
		WardCode: f.WardCode, ProvinceCode: f.ProvinceCode, PriceFrom: f.PriceFrom, PriceTo: f.PriceTo,
		AreaFrom: f.AreaFrom, AreaTo: f.AreaTo,
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]RoomSummary, len(rows))
	for i, r := range rows {
		out[i] = roomSummary(r, s.storage)
	}
	return out, int(total), nil
}

// GetRoom returns one room with its charges and pictures; 404 when it is not the landlord's.
func (s *Service) GetRoom(ctx context.Context, landlordID, id uuid.UUID) (RoomDetail, error) {
	return s.roomDetail(ctx, s.store.Queries(), landlordID, id)
}

func (s *Service) roomDetail(ctx context.Context, q *sqlcgen.Queries, landlordID, id uuid.UUID) (RoomDetail, error) {
	rows, err := q.SearchRooms(ctx, sqlcgen.SearchRoomsParams{LandlordID: landlordID, ID: &id, PageLimit: 1})
	if err != nil {
		return RoomDetail{}, err
	}
	if len(rows) == 0 {
		return RoomDetail{}, apperr.NotFound()
	}
	row := rows[0]
	charges, err := q.ListChargesOfRooms(ctx, []uuid.UUID{id})
	if err != nil {
		return RoomDetail{}, err
	}
	detail := RoomDetail{RoomSummary: roomSummary(row, s.storage), Description: row.Description, Charges: roomChargeViews(charges), Media: []MediaView{}}
	// A room that follows its building's pictures shows the building's.
	if row.InheritsMedia && row.BuildingID != nil {
		media, err := q.ListMediaOfBuildings(ctx, []uuid.UUID{*row.BuildingID})
		if err != nil {
			return RoomDetail{}, err
		}
		detail.Media = mediaViews(media, s.storage)
		return detail, nil
	}
	media, err := q.ListMediaOfRooms(ctx, []uuid.UUID{id})
	if err != nil {
		return RoomDetail{}, err
	}
	detail.Media = mediaViews(media, s.storage)
	return detail, nil
}

// CreateRoom adds a room to the landlord, within the limit of their plan.
func (s *Service) CreateRoom(ctx context.Context, landlordID uuid.UUID, in RoomInput) (RoomDetail, error) {
	in, charges, err := in.normalized()
	if err != nil {
		return RoomDetail{}, err
	}
	status := statusOrDefault(in.Status, StatusAvailable)
	var id uuid.UUID
	err = s.store.InTx(ctx, func(tx pgx.Tx, q *sqlcgen.Queries) error {
		if err := lockLandlord(ctx, q, landlordID); err != nil {
			return err
		}
		var building *buildingDefaults
		if in.BuildingID != nil {
			d, err := loadBuildingDefaults(ctx, q, landlordID, *in.BuildingID)
			if err != nil {
				return err
			}
			building = &d
		}
		ri := in.ruleInput(charges)
		groups := resolveInheritance(in.Inherit, carriedGroups(ri, nil), building != nil)
		values, fields := effectiveValues(building, groups, ri)
		if len(fields) > 0 {
			return apperr.Invalid(fields...)
		}
		if err := checkWard(ctx, q, values.WardCode); err != nil {
			return err
		}
		if err := s.checkRoomLimit(ctx, q, landlordID, 1); err != nil {
			return err
		}
		if err := checkRoomName(ctx, q, landlordID, in.BuildingID, in.Name, nil); err != nil {
			return err
		}
		last, err := q.MaxRoomOrder(ctx, landlordID)
		if err != nil {
			return err
		}
		fl := flagsOf(groups)
		id, err = q.InsertRoom(ctx, sqlcgen.InsertRoomParams{
			LandlordID: landlordID, BuildingID: in.BuildingID, Name: in.Name, WardCode: values.WardCode,
			AddressDetail: values.AddressDetail, AreaSquareMeters: values.Area, PriceVnd: values.PriceVND,
			MinimumRentalPeriod: int16(values.MinPeriod), RentalPeriodUnit: values.PeriodUnit, //nolint:gosec // validated to 1..3650
			Description: values.Description, Status: string(status), InheritsPrice: fl.price, InheritsArea: fl.area,
			InheritsRentalPeriod: fl.rentalPeriod, InheritsDescription: fl.description, InheritsCharges: fl.charges,
			InheritsMedia: fl.media, DisplayOrder: last + 1,
		})
		if err != nil {
			return err
		}
		if fl.charges {
			err = followCharges(ctx, q, []uuid.UUID{id}, *in.BuildingID)
		} else {
			err = insertRoomCharges(ctx, q, id, chargesToCreate(charges))
		}
		if err != nil {
			return err
		}
		if fl.media {
			if err := q.SetRoomCoversFromBuilding(ctx, []uuid.UUID{id}); err != nil {
				return err
			}
		}
		if status == StatusRented {
			return s.observer.StatusChanged(ctx, tx, []StatusChange{{RoomID: id, To: status}})
		}
		return nil
	})
	if err != nil {
		return RoomDetail{}, roomNameTaken(err)
	}
	return s.GetRoom(ctx, landlordID, id)
}

// UpdateRoom replaces the fields of a room.
func (s *Service) UpdateRoom(ctx context.Context, landlordID, id uuid.UUID, in RoomInput) (RoomDetail, error) {
	in, charges, err := in.normalized()
	if err != nil {
		return RoomDetail{}, err
	}
	var staleFiles []string
	err = s.store.InTx(ctx, func(tx pgx.Tx, q *sqlcgen.Queries) error {
		cur, err := q.GetRoomForUpdate(ctx, sqlcgen.GetRoomForUpdateParams{ID: id, LandlordID: landlordID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.NotFound()
			}
			return err
		}
		var building *buildingDefaults
		if in.BuildingID != nil {
			d, err := loadBuildingDefaults(ctx, q, landlordID, *in.BuildingID)
			if err != nil {
				return err
			}
			building = &d
		}
		ri := in.ruleInput(charges)
		state := roomState{InheritsCharges: cur.InheritsCharges, InheritsMedia: cur.InheritsMedia}
		groups := resolveInheritance(in.Inherit, carriedGroups(ri, &state), building != nil)
		values, fields := effectiveValues(building, groups, ri)
		if len(fields) > 0 {
			return apperr.Invalid(fields...)
		}
		if err := checkWard(ctx, q, values.WardCode); err != nil {
			return err
		}
		status := statusOrDefault(in.Status, Status(cur.Status))
		if status != Status(cur.Status) {
			blockers, err := s.guards.Blockers(ctx, tx, []uuid.UUID{id})
			if err != nil {
				return err
			}
			if code := checkStatusChange(status, blockers[id]); code != "" {
				return apperr.Conflict(code)
			}
		}
		if err := checkRoomName(ctx, q, landlordID, in.BuildingID, in.Name, &id); err != nil {
			return err
		}
		fl := flagsOf(groups)
		err = q.UpdateRoom(ctx, sqlcgen.UpdateRoomParams{
			ID: id, LandlordID: landlordID, BuildingID: in.BuildingID, Name: in.Name, WardCode: values.WardCode,
			AddressDetail: values.AddressDetail, AreaSquareMeters: values.Area, PriceVnd: values.PriceVND,
			MinimumRentalPeriod: int16(values.MinPeriod), RentalPeriodUnit: values.PeriodUnit, //nolint:gosec // validated to 1..3650
			Description: values.Description, Status: string(status), InheritsPrice: fl.price, InheritsArea: fl.area,
			InheritsRentalPeriod: fl.rentalPeriod, InheritsDescription: fl.description, InheritsCharges: fl.charges,
			InheritsMedia: fl.media,
		})
		if err != nil {
			return err
		}
		buildingMoved := !equalPtr(cur.BuildingID, in.BuildingID)
		changed := cur.Name != in.Name || buildingMoved || !slices.Equal(groupsOfRow(cur), groups) ||
			!equalPtr(cur.WardCode, values.WardCode) || cur.AddressDetail != values.AddressDetail ||
			!equalPtr(cur.AreaSquareMeters, values.Area) || cur.PriceVnd != values.PriceVND ||
			int32(cur.MinimumRentalPeriod) != values.MinPeriod || cur.RentalPeriodUnit != values.PeriodUnit ||
			!equalPtr(cur.Description, values.Description)

		chargesChanged, err := s.updateRoomCharges(ctx, q, id, cur, fl.charges, buildingMoved, in, charges)
		if err != nil {
			return err
		}
		mediaChanged, files, err := s.updateRoomMedia(ctx, q, id, cur, fl.media, buildingMoved)
		if err != nil {
			return err
		}
		staleFiles = files

		if status != Status(cur.Status) {
			if err := s.observer.StatusChanged(ctx, tx, []StatusChange{{RoomID: id, From: Status(cur.Status), To: status}}); err != nil {
				return err
			}
		}
		if changed || chargesChanged || mediaChanged {
			return s.contentChanged(ctx, tx, []uuid.UUID{id})
		}
		return nil
	})
	if err != nil {
		return RoomDetail{}, roomNameTaken(err)
	}
	s.deleteFiles(ctx, staleFiles)
	return s.GetRoom(ctx, landlordID, id)
}

// updateRoomCharges brings the charges of an updated room in line with its new inheritance: a room that follows
// its building takes the building's, one that stopped following keeps the copies as its own, and a list in the
// request replaces the own charges. It reports whether the charges a listing shows changed.
func (s *Service) updateRoomCharges(ctx context.Context, q *sqlcgen.Queries, id uuid.UUID, cur sqlcgen.GetRoomForUpdateRow,
	follows, buildingMoved bool, in RoomInput, charges []chargeInput) (bool, error) {
	if follows {
		if err := followCharges(ctx, q, []uuid.UUID{id}, *in.BuildingID); err != nil {
			return false, err
		}
		return !cur.InheritsCharges || buildingMoved, nil
	}
	changed := false
	if cur.InheritsCharges {
		if err := q.DetachRoomCharges(ctx, []uuid.UUID{id}); err != nil {
			return false, err
		}
		changed = true
	}
	if in.Charges != nil {
		replaced, err := syncRoomCharges(ctx, q, id, charges)
		if err != nil {
			return false, err
		}
		changed = changed || replaced
	}
	return changed, nil
}

// updateRoomMedia keeps the cover of an updated room in line with whether it follows its building's pictures: a
// room that starts following drops its own pictures (their files are returned, to delete after commit) and shows the
// building's cover; one that stops following shows its own cover (none when it has no pictures). It reports whether
// the pictures shown changed.
func (s *Service) updateRoomMedia(ctx context.Context, q *sqlcgen.Queries, id uuid.UUID, cur sqlcgen.GetRoomForUpdateRow,
	follows, buildingMoved bool) (bool, []string, error) {
	ids := []uuid.UUID{id}
	switch {
	case follows && !cur.InheritsMedia:
		dropped, err := q.DeleteMediaOfRooms(ctx, ids)
		if err != nil {
			return false, nil, err
		}
		var files []string
		for _, f := range dropped {
			files = append(files, f.FileKey, f.ThumbKey)
		}
		return true, files, q.SetRoomCoversFromBuilding(ctx, ids)
	case follows && buildingMoved:
		return true, nil, q.SetRoomCoversFromBuilding(ctx, ids)
	case !follows && cur.InheritsMedia:
		_, err := refreshCover(ctx, q, RoomMedia(id))
		return true, nil, err
	}
	return false, nil, nil
}

// DeleteRoom deletes a room; 409 while something (a running contract, an open listing) blocks it.
func (s *Service) DeleteRoom(ctx context.Context, landlordID, id uuid.UUID) error {
	result, err := s.deleteRooms(ctx, landlordID, []uuid.UUID{id}, true)
	if err != nil {
		return err
	}
	if len(result.Failed) > 0 {
		return apperr.Conflict(result.Failed[0].Code)
	}
	return nil
}

// BulkDeleteRooms deletes the rooms among ids that nothing blocks; the others are reported as failed. An id that is
// not the landlord's room at all is a 422 on ids.
func (s *Service) BulkDeleteRooms(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) (BulkResult, error) {
	return s.deleteRooms(ctx, landlordID, ids, false)
}

// deleteRooms deletes what nothing blocks; single says the ids are one path id (not found is then a 404, otherwise a
// 422 on ids).
func (s *Service) deleteRooms(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID, single bool) (BulkResult, error) {
	var result BulkResult
	err := s.store.InTx(ctx, func(tx pgx.Tx, q *sqlcgen.Queries) error {
		rows, err := q.ListRoomsForUpdate(ctx, sqlcgen.ListRoomsForUpdateParams{LandlordID: landlordID, Ids: ids})
		if err != nil {
			return err
		}
		if len(rows) != len(ids) {
			return missingRooms(single)
		}
		blockers, err := s.guards.Blockers(ctx, tx, ids)
		if err != nil {
			return err
		}
		var deletable []uuid.UUID
		for _, r := range rows {
			if code := checkDelete(blockers[r.ID]); code != "" {
				result.Failed = append(result.Failed, BulkFailure{ID: r.ID, Name: r.Name, Code: code})
				continue
			}
			deletable = append(deletable, r.ID)
		}
		result.Done = deletable
		if len(deletable) == 0 {
			return nil
		}
		return q.SoftDeleteRooms(ctx, sqlcgen.SoftDeleteRoomsParams{LandlordID: landlordID, Ids: deletable})
	})
	if err != nil {
		return BulkResult{}, err
	}
	if result.Done == nil {
		result.Done = []uuid.UUID{}
	}
	return result, nil
}

// missingRooms is the answer for ids that are not all the landlord's: a 404 for a path id, a 422 on ids for a list.
func missingRooms(single bool) error {
	if single {
		return apperr.NotFound()
	}
	return apperr.Invalid(apperr.Field{Name: "ids", Rule: apperr.RuleExists})
}

// BulkSetRoomStatus moves the rooms among ids to a status; rooms a running contract keeps rented are reported as
// failed, rooms already in that status count as done.
func (s *Service) BulkSetRoomStatus(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID, to Status) (BulkResult, error) {
	var result BulkResult
	err := s.store.InTx(ctx, func(tx pgx.Tx, q *sqlcgen.Queries) error {
		rows, err := q.ListRoomsForUpdate(ctx, sqlcgen.ListRoomsForUpdateParams{LandlordID: landlordID, Ids: ids})
		if err != nil {
			return err
		}
		if len(rows) != len(ids) {
			return missingRooms(false)
		}
		blockers, err := s.guards.Blockers(ctx, tx, ids)
		if err != nil {
			return err
		}
		var moving []uuid.UUID
		var changes []StatusChange
		for _, r := range rows {
			if Status(r.Status) == to {
				result.Done = append(result.Done, r.ID)
				continue
			}
			if code := checkStatusChange(to, blockers[r.ID]); code != "" {
				result.Failed = append(result.Failed, BulkFailure{ID: r.ID, Name: r.Name, Code: code})
				continue
			}
			moving = append(moving, r.ID)
			changes = append(changes, StatusChange{RoomID: r.ID, From: Status(r.Status), To: to})
		}
		if len(moving) == 0 {
			return nil
		}
		result.Done = append(result.Done, moving...)
		if err := q.SetRoomStatus(ctx, sqlcgen.SetRoomStatusParams{Status: string(to), LandlordID: landlordID, Ids: moving}); err != nil {
			return err
		}
		return s.observer.StatusChanged(ctx, tx, changes)
	})
	if err != nil {
		return BulkResult{}, err
	}
	if result.Done == nil {
		result.Done = []uuid.UUID{}
	}
	return result, nil
}

// ReorderRooms applies a drag and drop: ids are the rooms that moved, in their new relative order.
func (s *Service) ReorderRooms(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) error {
	return s.store.InTx(ctx, func(_ pgx.Tx, q *sqlcgen.Queries) error {
		rows, err := q.ListRoomOrder(ctx, landlordID)
		if err != nil {
			return err
		}
		current := make([]orderedID, len(rows))
		have := make(map[uuid.UUID]bool, len(rows))
		for i, r := range rows {
			current[i] = orderedID{ID: r.ID, Order: int(r.DisplayOrder)}
			have[r.ID] = true
		}
		for _, id := range ids {
			if !have[id] {
				return apperr.Invalid(apperr.Field{Name: "ids", Rule: apperr.RuleExists})
			}
		}
		moves := reorder(current, ids)
		if len(moves) == 0 {
			return nil
		}
		params := sqlcgen.SetRoomOrderParams{LandlordID: landlordID}
		for _, m := range moves {
			params.Ids = append(params.Ids, m.ID)
			params.Orders = append(params.Orders, int32(m.Order)) //nolint:gosec // a list position
		}
		return q.SetRoomOrder(ctx, params)
	})
}

// CreateRoomsInBuilding adds a run of rooms (prefix + number range) to a building in one go. Values the request
// leaves out follow the building, and every new room follows the building's pictures.
func (s *Service) CreateRoomsInBuilding(ctx context.Context, landlordID, buildingID uuid.UUID, in RunInput) (RunResult, error) {
	names, failure := roomNames(in.NamePrefix, in.From, in.To, in.Padding)
	if failure != "" {
		params := map[string]string{}
		switch failure {
		case rangeTooMany:
			params["max"] = strconv.Itoa(maxRoomsPerRun)
		case rangeNameTooLong:
			params["max"] = strconv.Itoa(maxRoomNameLength)
		}
		return RunResult{}, apperr.Invalid(apperr.Field{Name: "name_prefix", Rule: failure, Params: params})
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
	status := statusOrDefault(in.Status, StatusAvailable)

	var ids []uuid.UUID
	err = s.store.InTx(ctx, func(_ pgx.Tx, q *sqlcgen.Queries) error {
		if err := lockLandlord(ctx, q, landlordID); err != nil {
			return err
		}
		row, err := q.GetBuildingForUpdate(ctx, sqlcgen.GetBuildingForUpdateParams{ID: buildingID, LandlordID: landlordID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.NotFound()
			}
			return err
		}
		building := defaultsOfRow(row)
		ri := own.ruleInput(charges)
		groups := resolveInheritance(nil, carriedGroups(ri, nil), true)
		values, fields := effectiveValues(&building, groups, ri)
		if len(fields) > 0 {
			return apperr.Invalid(fields...)
		}
		if err := s.checkRoomLimit(ctx, q, landlordID, len(names)); err != nil {
			return err
		}
		if err := checkRunNames(ctx, q, buildingID, names); err != nil {
			return err
		}
		last, err := q.MaxRoomOrder(ctx, landlordID)
		if err != nil {
			return err
		}
		fl := flagsOf(groups)
		params := sqlcgen.InsertRoomRunParams{
			LandlordID: landlordID, BuildingID: buildingID, WardCode: values.WardCode, AddressDetail: values.AddressDetail,
			AreaSquareMeters: values.Area, PriceVnd: values.PriceVND, MinimumRentalPeriod: int16(values.MinPeriod), //nolint:gosec // validated
			RentalPeriodUnit: values.PeriodUnit, Description: values.Description, Status: string(status),
			InheritsPrice: fl.price, InheritsArea: fl.area, InheritsRentalPeriod: fl.rentalPeriod,
			InheritsDescription: fl.description, InheritsCharges: fl.charges, InheritsMedia: fl.media, Names: names,
		}
		for i := range names {
			params.Orders = append(params.Orders, last+int32(i)+1) //nolint:gosec // at most 100 names
		}
		rows, err := q.InsertRoomRun(ctx, params)
		if err != nil {
			return err
		}
		slices.SortFunc(rows, func(a, b sqlcgen.InsertRoomRunRow) int { return int(a.DisplayOrder - b.DisplayOrder) })
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		if fl.charges {
			return followCharges(ctx, q, ids, buildingID)
		}
		return insertChargesForRooms(ctx, q, ids, charges)
	})
	if err != nil {
		return RunResult{}, roomNameTaken(err)
	}
	return RunResult{IDs: ids, Count: len(ids)}, nil
}

// maxNamesShown is how many taken names an error lists.
const maxNamesShown = 5

// checkRunNames refuses a run when any of its names is already used by a live room of the building.
func checkRunNames(ctx context.Context, q *sqlcgen.Queries, buildingID uuid.UUID, names []string) error {
	lower := make([]string, len(names))
	for i, n := range names {
		lower[i] = strings.ToLower(n)
	}
	taken, err := q.RoomNamesTakenInBuilding(ctx, sqlcgen.RoomNamesTakenInBuildingParams{BuildingID: &buildingID, Names: lower})
	if err != nil || len(taken) == 0 {
		return err
	}
	if len(taken) > maxNamesShown {
		taken = taken[:maxNamesShown]
	}
	return apperr.Invalid(apperr.Field{Name: "name_prefix", Rule: apperr.RuleNamesTaken, Params: map[string]string{"names": strings.Join(taken, ", ")}})
}
