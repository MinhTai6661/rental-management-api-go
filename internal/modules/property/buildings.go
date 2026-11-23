package property

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"rental-management/internal/modules/property/sqlcgen"
	"rental-management/internal/platform/apperr"
	"rental-management/internal/platform/db"
)

// BuildingInput is the body of a building: a full replacement of its fields. A nil Charges keeps the current ones,
// an empty list removes them all.
type BuildingInput struct {
	Name          string
	WardCode      *int32
	AddressDetail string
	Description   *string
	PriceVND      *int64
	Area          *float64
	MinPeriod     *int32
	PeriodUnit    *string
	Charges       []ChargeInput
}

// ChargeInput is one charge of a request; ID names a charge already stored (for an update).
type ChargeInput struct {
	ID        *uuid.UUID
	Name      string
	Kind      string
	AmountVND int64
	Unit      string
	Note      *string
}

func (c ChargeInput) rule() chargeInput {
	return chargeInput(c)
}

// BuildingFilter narrows the list of buildings.
type BuildingFilter struct {
	Name         string
	WardCode     *int32
	ProvinceCode *int32
	// Sort is "", "name", "-name", "created_at" or "-created_at"; the default is the landlord's own order.
	Sort string
}

// BulkFailure is an item a bulk operation refused.
type BulkFailure struct {
	ID     uuid.UUID
	Name   string
	Code   string
	Params map[string]string
}

// BulkResult tells which items a bulk operation did and which it refused.
type BulkResult struct {
	Done   []uuid.UUID
	Failed []BulkFailure
}

// FirstBuilding is the building a new landlord starts with.
type FirstBuilding struct {
	Name          string
	WardCode      *int32
	AddressDetail string
}

// normalized checks what the struct tags cannot and cleans the text: the name and address must not be blank, the
// rental period is given whole or not at all, the area keeps two decimals and the charges are trimmed.
func (in BuildingInput) normalized() (BuildingInput, []chargeInput, error) {
	var fields []apperr.Field
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		fields = append(fields, apperr.Field{Name: "name", Rule: apperr.RuleRequired})
	}
	in.AddressDetail = strings.TrimSpace(in.AddressDetail)
	if in.AddressDetail == "" {
		fields = append(fields, apperr.Field{Name: "address_detail", Rule: apperr.RuleRequired})
	}
	in.Description = blankToNil(in.Description)
	if (in.MinPeriod == nil) != (in.PeriodUnit == nil) {
		fields = append(fields, apperr.Field{Name: "minimum_rental_period", Rule: apperr.RuleRentalPeriodPair})
	}
	if in.Area != nil {
		area := roundArea(*in.Area)
		in.Area = &area
	}
	var charges []chargeInput
	if in.Charges != nil {
		raw := make([]chargeInput, len(in.Charges))
		for i, c := range in.Charges {
			raw[i] = c.rule()
		}
		var chargeFields []apperr.Field
		charges, chargeFields = normalizeCharges(raw)
		fields = append(fields, chargeFields...)
	}
	if len(fields) > 0 {
		return in, nil, apperr.Invalid(fields...)
	}
	return in, charges, nil
}

func int16Ptr(v *int32) *int16 {
	if v == nil {
		return nil
	}
	n := int16(*v) //nolint:gosec // validated to 1..3650
	return &n
}

// ListBuildings returns a page of the landlord's buildings and the total.
func (s *Service) ListBuildings(ctx context.Context, landlordID uuid.UUID, f BuildingFilter, limit, offset int) ([]BuildingSummary, int, error) {
	q := s.store.Queries()
	var nameLike *string
	if name := strings.ToLower(strings.TrimSpace(f.Name)); name != "" {
		like := escapeLike(name)
		nameLike = &like
	}
	rows, err := q.SearchBuildings(ctx, sqlcgen.SearchBuildingsParams{
		LandlordID: landlordID, NameLike: nameLike, WardCode: f.WardCode, ProvinceCode: f.ProvinceCode, Sort: f.Sort,
		PageLimit: int32(limit), PageOffset: int32(offset), //nolint:gosec // bounded by httpx.ParsePage
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := q.CountBuildings(ctx, sqlcgen.CountBuildingsParams{
		LandlordID: landlordID, NameLike: nameLike, WardCode: f.WardCode, ProvinceCode: f.ProvinceCode,
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]BuildingSummary, len(rows))
	for i, r := range rows {
		out[i] = buildingSummary(r, s.storage)
	}
	return out, int(total), nil
}

// GetBuilding returns one building with its charges and pictures; 404 when it is not the landlord's.
func (s *Service) GetBuilding(ctx context.Context, landlordID, id uuid.UUID) (BuildingDetail, error) {
	return s.buildingDetail(ctx, s.store.Queries(), landlordID, id)
}

func (s *Service) buildingDetail(ctx context.Context, q *sqlcgen.Queries, landlordID, id uuid.UUID) (BuildingDetail, error) {
	rows, err := q.SearchBuildings(ctx, sqlcgen.SearchBuildingsParams{LandlordID: landlordID, ID: &id, PageLimit: 1})
	if err != nil {
		return BuildingDetail{}, err
	}
	if len(rows) == 0 {
		return BuildingDetail{}, apperr.NotFound()
	}
	charges, err := q.ListChargesOfBuildings(ctx, []uuid.UUID{id})
	if err != nil {
		return BuildingDetail{}, err
	}
	media, err := q.ListMediaOfBuildings(ctx, []uuid.UUID{id})
	if err != nil {
		return BuildingDetail{}, err
	}
	return BuildingDetail{
		BuildingSummary: buildingSummary(rows[0], s.storage),
		Charges:         chargeViews(charges),
		Media:           mediaViews(media, s.storage),
	}, nil
}

// CreateBuilding adds a building to the landlord, within the limit of their plan.
func (s *Service) CreateBuilding(ctx context.Context, landlordID uuid.UUID, in BuildingInput) (BuildingDetail, error) {
	in, charges, err := in.normalized()
	if err != nil {
		return BuildingDetail{}, err
	}
	var id uuid.UUID
	err = s.store.InTx(ctx, func(_ pgx.Tx, q *sqlcgen.Queries) error {
		if err := lockLandlord(ctx, q, landlordID); err != nil {
			return err
		}
		if err := s.checkBuildingLimit(ctx, q, landlordID); err != nil {
			return err
		}
		id, err = s.insertBuilding(ctx, q, landlordID, in, chargesToCreate(charges))
		return err
	})
	if err != nil {
		return BuildingDetail{}, nameTaken(err)
	}
	return s.GetBuilding(ctx, landlordID, id)
}

// checkBuildingLimit refuses a new building past the plan's limit (the caller holds the landlord lock).
func (s *Service) checkBuildingLimit(ctx context.Context, q *sqlcgen.Queries, landlordID uuid.UUID) error {
	limit, err := s.plans.MaxBuildings(ctx, landlordID)
	if err != nil || limit == nil {
		return err
	}
	count, err := q.CountLiveBuildings(ctx, landlordID)
	if err != nil {
		return err
	}
	if limitReached(limit, int(count), 1) {
		return apperr.Conflict(apperr.CodeBuildingLimit).WithParams(map[string]string{"limit": strconv.Itoa(*limit)})
	}
	return nil
}

// insertBuilding checks the ward and the name, takes the next position and stores the building with its charges.
func (s *Service) insertBuilding(ctx context.Context, q *sqlcgen.Queries, landlordID uuid.UUID, in BuildingInput, charges []chargeCreate) (uuid.UUID, error) {
	if err := checkWard(ctx, q, in.WardCode); err != nil {
		return uuid.Nil, err
	}
	if err := checkBuildingName(ctx, q, landlordID, in.Name, nil); err != nil {
		return uuid.Nil, err
	}
	last, err := q.MaxBuildingOrder(ctx, landlordID)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := q.InsertBuilding(ctx, sqlcgen.InsertBuildingParams{
		LandlordID: landlordID, Name: in.Name, WardCode: in.WardCode, AddressDetail: in.AddressDetail,
		Description: in.Description, PriceVnd: in.PriceVND, AreaSquareMeters: in.Area,
		MinimumRentalPeriod: int16Ptr(in.MinPeriod), RentalPeriodUnit: in.PeriodUnit, DisplayOrder: last + 1,
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, insertBuildingCharges(ctx, q, id, charges)
}

func checkWard(ctx context.Context, q *sqlcgen.Queries, code *int32) error {
	if code == nil {
		return nil
	}
	ok, err := q.WardExists(ctx, *code)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.Invalid(apperr.Field{Name: "ward_code", Rule: apperr.RuleExists})
	}
	return nil
}

// checkBuildingName refuses a name another live building of the landlord already uses (any case).
func checkBuildingName(ctx context.Context, q *sqlcgen.Queries, landlordID uuid.UUID, name string, except *uuid.UUID) error {
	taken, err := q.BuildingNameTaken(ctx, sqlcgen.BuildingNameTakenParams{LandlordID: landlordID, Name: name, ExceptID: except})
	if err != nil {
		return err
	}
	if taken {
		return apperr.Invalid(apperr.Field{Name: "name", Rule: apperr.RuleUnique})
	}
	return nil
}

// nameTaken turns the unique index refusing a duplicate name (a race the pre-check cannot close) into the same
// field error as the pre-check.
func nameTaken(err error) error {
	if db.IsUniqueViolation(err) {
		return apperr.Invalid(apperr.Field{Name: "name", Rule: apperr.RuleUnique})
	}
	return err
}

// UpdateBuilding replaces the fields of a building and pushes what changed into the rooms that follow it, all in one
// transaction.
func (s *Service) UpdateBuilding(ctx context.Context, landlordID, id uuid.UUID, in BuildingInput) (BuildingDetail, error) {
	in, charges, err := in.normalized()
	if err != nil {
		return BuildingDetail{}, err
	}
	err = s.store.InTx(ctx, func(tx pgx.Tx, q *sqlcgen.Queries) error {
		current, err := q.GetBuildingForUpdate(ctx, sqlcgen.GetBuildingForUpdateParams{ID: id, LandlordID: landlordID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.NotFound()
			}
			return err
		}
		if err := checkWard(ctx, q, in.WardCode); err != nil {
			return err
		}
		if err := checkBuildingName(ctx, q, landlordID, in.Name, &id); err != nil {
			return err
		}
		if in.PriceVND == nil {
			following, err := q.CountRoomsFollowingPrice(ctx, &id)
			if err != nil {
				return err
			}
			if following > 0 {
				return apperr.Invalid(apperr.Field{
					Name: "price_vnd", Rule: apperr.RuleBuildingPriceInUse, Params: map[string]string{"count": strconv.Itoa(int(following))},
				})
			}
		}
		err = q.UpdateBuilding(ctx, sqlcgen.UpdateBuildingParams{
			ID: id, LandlordID: landlordID, Name: in.Name, WardCode: in.WardCode, AddressDetail: in.AddressDetail,
			Description: in.Description, PriceVnd: in.PriceVND, AreaSquareMeters: in.Area,
			MinimumRentalPeriod: int16Ptr(in.MinPeriod), RentalPeriodUnit: in.PeriodUnit,
		})
		if err != nil {
			return err
		}
		reached, err := pushToRooms(ctx, q, id, defaultsOfRow(current), in.defaults())
		if err != nil {
			return err
		}
		if in.Charges != nil {
			changed, err := syncBuildingCharges(ctx, q, id, charges)
			if err != nil {
				return err
			}
			if changed {
				followers, err := q.ListRoomsFollowingCharges(ctx, &id)
				if err != nil {
					return err
				}
				if err := followCharges(ctx, q, followers, id); err != nil {
					return err
				}
				reached = append(reached, followers...)
			}
		}
		return s.contentChanged(ctx, tx, reached)
	})
	if err != nil {
		return BuildingDetail{}, nameTaken(err)
	}
	return s.GetBuilding(ctx, landlordID, id)
}

// defaults is what the building offers its rooms.
func (in BuildingInput) defaults() buildingDefaults {
	return buildingDefaults{
		WardCode: in.WardCode, AddressDetail: in.AddressDetail, PriceVND: in.PriceVND, Area: in.Area,
		MinPeriod: in.MinPeriod, PeriodUnit: in.PeriodUnit, Description: in.Description,
	}
}

func defaultsOfRow(r sqlcgen.GetBuildingForUpdateRow) buildingDefaults {
	d := buildingDefaults{
		WardCode: r.WardCode, AddressDetail: r.AddressDetail, PriceVND: r.PriceVnd, Area: r.AreaSquareMeters,
		PeriodUnit: r.RentalPeriodUnit, Description: r.Description,
	}
	if r.MinimumRentalPeriod != nil {
		n := int32(*r.MinimumRentalPeriod)
		d.MinPeriod = &n
	}
	return d
}

// pushToRooms copies what changed in a building into the rooms that follow it (the address into all of them) and
// returns the rooms it touched (see propagation).
func pushToRooms(ctx context.Context, q *sqlcgen.Queries, id uuid.UUID, before, after buildingDefaults) ([]uuid.UUID, error) {
	var reached []uuid.UUID
	collect := func(ids []uuid.UUID, err error) error {
		reached = append(reached, ids...)
		return err
	}
	if addressChanged(before, after) {
		err := collect(q.PushBuildingAddress(ctx, sqlcgen.PushBuildingAddressParams{WardCode: after.WardCode, AddressDetail: after.AddressDetail, BuildingID: &id}))
		if err != nil {
			return nil, err
		}
	}
	groups := propagation(before, after)
	if groups[GroupPrice] {
		if err := collect(q.PushBuildingPrice(ctx, sqlcgen.PushBuildingPriceParams{PriceVnd: *after.PriceVND, BuildingID: &id})); err != nil {
			return nil, err
		}
	}
	if groups[GroupArea] {
		if err := collect(q.PushBuildingArea(ctx, sqlcgen.PushBuildingAreaParams{AreaSquareMeters: after.Area, BuildingID: &id})); err != nil {
			return nil, err
		}
	}
	if groups[GroupRentalPeriod] {
		err := collect(q.PushBuildingRentalPeriod(ctx, sqlcgen.PushBuildingRentalPeriodParams{
			MinimumRentalPeriod: *int16Ptr(after.MinPeriod), RentalPeriodUnit: *after.PeriodUnit, BuildingID: &id,
		}))
		if err != nil {
			return nil, err
		}
	}
	if groups[GroupDescription] {
		if err := collect(q.PushBuildingDescription(ctx, sqlcgen.PushBuildingDescriptionParams{Description: after.Description, BuildingID: &id})); err != nil {
			return nil, err
		}
	}
	return reached, nil
}

// contentChanged tells the observer which rooms changed in a way a listing shows (each room once).
func (s *Service) contentChanged(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) error {
	ids = distinct(ids)
	if len(ids) == 0 {
		return nil
	}
	return s.observer.ContentChanged(ctx, tx, ids)
}

// distinct drops repeated ids, keeping the first occurrence.
func distinct(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := ids[:0:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// DeleteBuilding deletes an empty building; 409 while it holds rooms.
func (s *Service) DeleteBuilding(ctx context.Context, landlordID, id uuid.UUID) error {
	result, err := s.deleteBuildings(ctx, landlordID, []uuid.UUID{id}, true)
	if err != nil {
		return err
	}
	if len(result.Failed) > 0 {
		f := result.Failed[0]
		return apperr.Conflict(f.Code).WithParams(f.Params)
	}
	return nil
}

// BulkDeleteBuildings deletes the empty buildings among ids; the others are reported as failed. An id that is not
// the landlord's building at all is a 422 on ids.
func (s *Service) BulkDeleteBuildings(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) (BulkResult, error) {
	return s.deleteBuildings(ctx, landlordID, ids, false)
}

// deleteBuildings deletes the empty ones; single says the ids are one path id (not found is then a 404, otherwise a
// 422 on ids).
func (s *Service) deleteBuildings(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID, single bool) (BulkResult, error) {
	var result BulkResult
	var keys []string
	err := s.store.InTx(ctx, func(_ pgx.Tx, q *sqlcgen.Queries) error {
		rows, err := q.ListBuildingsToDelete(ctx, sqlcgen.ListBuildingsToDeleteParams{LandlordID: landlordID, Ids: ids})
		if err != nil {
			return err
		}
		if len(rows) != len(ids) {
			if single {
				return apperr.NotFound()
			}
			return apperr.Invalid(apperr.Field{Name: "ids", Rule: apperr.RuleExists})
		}
		var deletable []uuid.UUID
		for _, r := range rows {
			if r.RoomsCount > 0 {
				result.Failed = append(result.Failed, BulkFailure{
					ID: r.ID, Name: r.Name, Code: apperr.CodeBuildingHasRooms,
					Params: map[string]string{"count": strconv.Itoa(int(r.RoomsCount))},
				})
				continue
			}
			deletable = append(deletable, r.ID)
		}
		result.Done = deletable
		if len(deletable) == 0 {
			return nil
		}
		if err := q.SoftDeleteBuildings(ctx, sqlcgen.SoftDeleteBuildingsParams{LandlordID: landlordID, Ids: deletable}); err != nil {
			return err
		}
		if err := q.SoftDeleteChargesOfBuildings(ctx, deletable); err != nil {
			return err
		}
		files, err := q.DeleteMediaOfBuildings(ctx, deletable)
		for _, f := range files {
			keys = append(keys, f.FileKey, f.ThumbKey)
		}
		return err
	})
	if err != nil {
		return BulkResult{}, err
	}
	s.deleteFiles(ctx, keys)
	if result.Done == nil {
		result.Done = []uuid.UUID{}
	}
	return result, nil
}

// deleteFiles removes stored files after the transaction that dropped their rows committed. A failure is logged:
// the rows are gone, so a leftover file is only wasted space.
func (s *Service) deleteFiles(ctx context.Context, keys []string) {
	if len(keys) == 0 {
		return
	}
	// The request may be over already (the client left); the cleanup must still happen.
	if err := s.storage.Delete(context.WithoutCancel(ctx), keys...); err != nil {
		s.log.Warn("delete stored files", "error", err, "count", len(keys))
	}
}

// ReorderBuildings applies a drag and drop: ids are the buildings that moved, in their new relative order.
func (s *Service) ReorderBuildings(ctx context.Context, landlordID uuid.UUID, ids []uuid.UUID) error {
	return s.store.InTx(ctx, func(_ pgx.Tx, q *sqlcgen.Queries) error {
		rows, err := q.ListBuildingOrder(ctx, landlordID)
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
		params := sqlcgen.SetBuildingOrderParams{LandlordID: landlordID}
		for _, m := range moves {
			params.Ids = append(params.Ids, m.ID)
			params.Orders = append(params.Orders, int32(m.Order)) //nolint:gosec // a list position
		}
		return q.SetBuildingOrder(ctx, params)
	})
}

// CopyBuilding makes a new building with the details, charges and (with includeRooms) rooms of an existing one. The
// name must be free. Copied rooms keep their name, values and inheritance but are empty (available, or maintenance
// as before): no tenant, contract, listing or manager comes along. Pictures are duplicated (new files, so deleting
// one copy never touches the other): the building's own, and those of the rooms that have their own; a room that
// follows the building's pictures shows the new building's.
func (s *Service) CopyBuilding(ctx context.Context, landlordID, sourceID uuid.UUID, name string, includeRooms bool) (CopyResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return CopyResult{}, apperr.Invalid(apperr.Field{Name: "name", Rule: apperr.RuleRequired})
	}
	if err := s.owns(ctx, landlordID, BuildingMedia(sourceID)); err != nil {
		return CopyResult{}, err
	}
	// The files are duplicated before the transaction (like an upload); they are deleted again if it fails.
	copies, err := s.stageMediaCopies(ctx, landlordID, sourceID, includeRooms)
	if err != nil {
		return CopyResult{}, err
	}
	var id uuid.UUID
	copied := 0
	err = s.store.InTx(ctx, func(_ pgx.Tx, q *sqlcgen.Queries) error {
		if err := lockLandlord(ctx, q, landlordID); err != nil {
			return err
		}
		source, err := q.GetBuildingForUpdate(ctx, sqlcgen.GetBuildingForUpdateParams{ID: sourceID, LandlordID: landlordID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.NotFound()
			}
			return err
		}
		if err := s.checkBuildingLimit(ctx, q, landlordID); err != nil {
			return err
		}
		charges, err := q.ListChargesOfBuildings(ctx, []uuid.UUID{sourceID})
		if err != nil {
			return err
		}
		templates := make([]chargeCreate, len(charges))
		for i, c := range charges {
			templates[i] = chargeCreate{Input: chargeInput{Name: c.Name, Kind: c.Kind, AmountVND: c.AmountVnd, Unit: c.Unit, Note: c.Note}, Order: i + 1}
		}
		in := BuildingInput{
			Name: name, WardCode: source.WardCode, AddressDetail: source.AddressDetail, Description: source.Description,
			PriceVND: source.PriceVnd, Area: source.AreaSquareMeters, PeriodUnit: source.RentalPeriodUnit,
		}
		if source.MinimumRentalPeriod != nil {
			n := int32(*source.MinimumRentalPeriod)
			in.MinPeriod = &n
		}
		if id, err = s.insertBuilding(ctx, q, landlordID, in, templates); err != nil {
			return err
		}
		// The building's cover must exist before its rooms are copied: the rooms that follow it show that cover.
		if err := insertBuildingMediaCopies(ctx, q, id, copies); err != nil {
			return err
		}
		if includeRooms {
			copied, err = s.copyRooms(ctx, q, landlordID, sourceID, id, copies)
		}
		return err
	})
	if err != nil {
		s.deleteFiles(ctx, keysOf(copies.all()))
		return CopyResult{}, nameTaken(err)
	}
	s.deleteFiles(ctx, copies.unusedKeys())
	detail, err := s.GetBuilding(ctx, landlordID, id)
	return CopyResult{Building: detail, RoomsCopied: copied}, err
}

// copyRooms copies the live rooms of one building into another, inside the caller's transaction; it returns how many.
func (s *Service) copyRooms(ctx context.Context, q *sqlcgen.Queries, landlordID, from, to uuid.UUID, copies *mediaCopies) (int, error) {
	rooms, err := q.ListRoomsOfBuildingForCopy(ctx, sqlcgen.ListRoomsOfBuildingForCopyParams{BuildingID: &from, LandlordID: landlordID})
	if err != nil || len(rooms) == 0 {
		return 0, err
	}
	if err := s.checkRoomLimit(ctx, q, landlordID, len(rooms)); err != nil {
		return 0, err
	}
	base, err := q.MaxRoomOrder(ctx, landlordID)
	if err != nil {
		return 0, err
	}
	params := sqlcgen.CopyRoomsParams{BuildingID: to, BaseOrder: base}
	var followCharge, followMedia, ownCharge, ownSource []uuid.UUID
	ownMedia := map[uuid.UUID]uuid.UUID{} // source room -> its copy, for the rooms with pictures of their own
	for i, r := range rooms {
		fresh, err := uuid.NewV7()
		if err != nil {
			return 0, err
		}
		params.DestIds = append(params.DestIds, fresh)
		params.SrcIds = append(params.SrcIds, r.ID)
		params.Positions = append(params.Positions, int32(i+1)) //nolint:gosec // at most the plan's room limit
		if r.InheritsCharges {
			followCharge = append(followCharge, fresh)
		} else {
			ownCharge, ownSource = append(ownCharge, fresh), append(ownSource, r.ID)
		}
		if r.InheritsMedia {
			followMedia = append(followMedia, fresh)
		} else {
			ownMedia[r.ID] = fresh
		}
	}
	if err := q.CopyRooms(ctx, params); err != nil {
		return 0, err
	}
	if err := followCharges(ctx, q, followCharge, to); err != nil {
		return 0, err
	}
	if len(ownCharge) > 0 {
		if err := q.CopyOwnCharges(ctx, sqlcgen.CopyOwnChargesParams{NewIds: ownCharge, SourceIds: ownSource}); err != nil {
			return 0, err
		}
	}
	if len(followMedia) > 0 {
		if err := q.SetRoomCoversFromBuilding(ctx, followMedia); err != nil {
			return 0, err
		}
	}
	if err := insertRoomMediaCopies(ctx, q, copies, ownMedia); err != nil {
		return 0, err
	}
	return len(rooms), nil
}

// EnsureFirstBuilding gives a landlord who never had a building their first one, with the position 1. A landlord
// who deleted all their buildings does not get another. It reports whether it created one.
func (s *Service) EnsureFirstBuilding(ctx context.Context, landlordID uuid.UUID, first FirstBuilding) (bool, error) {
	created := false
	err := s.store.InTx(ctx, func(_ pgx.Tx, q *sqlcgen.Queries) error {
		if err := lockLandlord(ctx, q, landlordID); err != nil {
			return err
		}
		ever, err := q.AnyBuildingEver(ctx, landlordID)
		if err != nil || ever {
			return err
		}
		in, _, err := BuildingInput{Name: first.Name, WardCode: first.WardCode, AddressDetail: first.AddressDetail}.normalized()
		if err != nil {
			return err
		}
		if _, err := s.insertBuilding(ctx, q, landlordID, in, nil); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, nameTaken(err)
}
