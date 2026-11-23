-- name: SearchRooms :many
-- The landlord's live rooms with their building, ward and province. Filters are optional; a NULL filter is off.
-- name_like is a lower-case LIKE pattern body already escaped with "\". no_building keeps only rooms outside any
-- building. Rooms follow their building's address, so ward and province come from the room row itself.
SELECT r.id, r.building_id, b.name AS building_name, r.name, r.ward_code, w.name AS ward_name, w.province_code,
       p.name AS province_name, r.address_detail, r.area_square_meters, r.price_vnd, r.minimum_rental_period,
       r.rental_period_unit, r.description, r.status, r.inherits_price, r.inherits_area, r.inherits_rental_period,
       r.inherits_description, r.inherits_charges, r.inherits_media, r.cover_key, r.cover_thumb_key,
       r.display_order, r.created_at, r.updated_at
FROM rooms r
LEFT JOIN buildings b ON b.id = r.building_id
LEFT JOIN wards w ON w.code = r.ward_code
LEFT JOIN provinces p ON p.code = w.province_code
WHERE r.landlord_id = @landlord_id
  AND r.deleted_at IS NULL
  AND (sqlc.narg('id')::uuid IS NULL OR r.id = sqlc.narg('id')::uuid)
  AND (sqlc.narg('name_like')::text IS NULL OR lower(r.name) LIKE '%' || sqlc.narg('name_like')::text || '%' ESCAPE '\')
  AND (sqlc.narg('status')::text IS NULL OR r.status = sqlc.narg('status')::text)
  AND (sqlc.narg('building_id')::uuid IS NULL OR r.building_id = sqlc.narg('building_id')::uuid)
  AND (NOT sqlc.arg('no_building')::boolean OR r.building_id IS NULL)
  AND (sqlc.narg('ward_code')::integer IS NULL OR r.ward_code = sqlc.narg('ward_code')::integer)
  AND (sqlc.narg('province_code')::integer IS NULL OR w.province_code = sqlc.narg('province_code')::integer)
  AND (sqlc.narg('price_from')::bigint IS NULL OR r.price_vnd >= sqlc.narg('price_from')::bigint)
  AND (sqlc.narg('price_to')::bigint IS NULL OR r.price_vnd <= sqlc.narg('price_to')::bigint)
  AND (sqlc.narg('area_from')::numeric IS NULL OR r.area_square_meters >= sqlc.narg('area_from')::numeric)
  AND (sqlc.narg('area_to')::numeric IS NULL OR r.area_square_meters <= sqlc.narg('area_to')::numeric)
ORDER BY
  CASE WHEN @sort::text = 'name' THEN lower(r.name) END ASC,
  CASE WHEN @sort::text = '-name' THEN lower(r.name) END DESC,
  CASE WHEN @sort::text = 'price' THEN r.price_vnd END ASC,
  CASE WHEN @sort::text = '-price' THEN r.price_vnd END DESC,
  CASE WHEN @sort::text = 'area' THEN r.area_square_meters END ASC NULLS LAST,
  CASE WHEN @sort::text = '-area' THEN r.area_square_meters END DESC NULLS LAST,
  CASE WHEN @sort::text = 'created_at' THEN r.created_at END ASC,
  CASE WHEN @sort::text = '-created_at' THEN r.created_at END DESC,
  r.display_order, r.id
LIMIT @page_limit OFFSET @page_offset;

-- name: CountRooms :one
SELECT count(*)::integer
FROM rooms r
LEFT JOIN wards w ON w.code = r.ward_code
WHERE r.landlord_id = @landlord_id
  AND r.deleted_at IS NULL
  AND (sqlc.narg('name_like')::text IS NULL OR lower(r.name) LIKE '%' || sqlc.narg('name_like')::text || '%' ESCAPE '\')
  AND (sqlc.narg('status')::text IS NULL OR r.status = sqlc.narg('status')::text)
  AND (sqlc.narg('building_id')::uuid IS NULL OR r.building_id = sqlc.narg('building_id')::uuid)
  AND (NOT sqlc.arg('no_building')::boolean OR r.building_id IS NULL)
  AND (sqlc.narg('ward_code')::integer IS NULL OR r.ward_code = sqlc.narg('ward_code')::integer)
  AND (sqlc.narg('province_code')::integer IS NULL OR w.province_code = sqlc.narg('province_code')::integer)
  AND (sqlc.narg('price_from')::bigint IS NULL OR r.price_vnd >= sqlc.narg('price_from')::bigint)
  AND (sqlc.narg('price_to')::bigint IS NULL OR r.price_vnd <= sqlc.narg('price_to')::bigint)
  AND (sqlc.narg('area_from')::numeric IS NULL OR r.area_square_meters >= sqlc.narg('area_from')::numeric)
  AND (sqlc.narg('area_to')::numeric IS NULL OR r.area_square_meters <= sqlc.narg('area_to')::numeric);

-- name: GetRoomForUpdate :one
SELECT id, building_id, name, ward_code, address_detail, area_square_meters, price_vnd, minimum_rental_period,
       rental_period_unit, description, status, inherits_price, inherits_area, inherits_rental_period,
       inherits_description, inherits_charges, inherits_media
FROM rooms
WHERE id = @id AND landlord_id = @landlord_id AND deleted_at IS NULL
FOR UPDATE;

-- name: InsertRoom :one
INSERT INTO rooms (landlord_id, building_id, name, ward_code, address_detail, area_square_meters, price_vnd,
                   minimum_rental_period, rental_period_unit, description, status, inherits_price, inherits_area,
                   inherits_rental_period, inherits_description, inherits_charges, inherits_media, display_order)
VALUES (@landlord_id, @building_id, @name, @ward_code, @address_detail, @area_square_meters, @price_vnd,
        @minimum_rental_period, @rental_period_unit, @description, @status, @inherits_price, @inherits_area,
        @inherits_rental_period, @inherits_description, @inherits_charges, @inherits_media, @display_order)
RETURNING id;

-- name: UpdateRoom :exec
UPDATE rooms
SET building_id = @building_id, name = @name, ward_code = @ward_code, address_detail = @address_detail,
    area_square_meters = @area_square_meters, price_vnd = @price_vnd, minimum_rental_period = @minimum_rental_period,
    rental_period_unit = @rental_period_unit, description = @description, status = @status,
    inherits_price = @inherits_price, inherits_area = @inherits_area, inherits_rental_period = @inherits_rental_period,
    inherits_description = @inherits_description, inherits_charges = @inherits_charges,
    inherits_media = @inherits_media, updated_at = now()
WHERE id = @id AND landlord_id = @landlord_id AND deleted_at IS NULL;

-- name: InsertRoomRun :many
-- One run of rooms in a building: the same values for every name, the building's cover on each, consecutive
-- positions. Returns the new ids in the order of the names.
INSERT INTO rooms (landlord_id, building_id, name, ward_code, address_detail, area_square_meters, price_vnd,
                   minimum_rental_period, rental_period_unit, description, status, inherits_price, inherits_area,
                   inherits_rental_period, inherits_description, inherits_charges, inherits_media, cover_key,
                   cover_thumb_key, display_order)
SELECT @landlord_id::uuid, @building_id::uuid, n.name, sqlc.narg('ward_code')::integer, @address_detail::text,
       sqlc.narg('area_square_meters')::numeric, @price_vnd::bigint, @minimum_rental_period::smallint,
       @rental_period_unit::text, sqlc.narg('description')::text, @status::text, @inherits_price::boolean,
       @inherits_area::boolean, @inherits_rental_period::boolean, @inherits_description::boolean,
       @inherits_charges::boolean, @inherits_media::boolean,
       CASE WHEN @inherits_media::boolean THEN b.cover_key END,
       CASE WHEN @inherits_media::boolean THEN b.cover_thumb_key END, n.display_order
FROM (SELECT unnest(@names::text[]) AS name, unnest(@orders::integer[]) AS display_order) AS n
JOIN buildings b ON b.id = @building_id::uuid
RETURNING id, display_order;

-- name: ListRoomsOfBuildingForCopy :many
-- The live rooms of a building in display order, with what a copy needs to know.
SELECT id, inherits_charges, inherits_media
FROM rooms
WHERE building_id = @building_id AND landlord_id = @landlord_id AND deleted_at IS NULL
ORDER BY display_order, id;

-- name: CopyRooms :exec
-- Copies rooms into a new building under new ids, same order: same name, values and inheritance, the address of
-- the new building, never rented. Positions continue after base_order.
INSERT INTO rooms (id, landlord_id, building_id, name, ward_code, address_detail, area_square_meters, price_vnd,
                   minimum_rental_period, rental_period_unit, description, status, inherits_price, inherits_area,
                   inherits_rental_period, inherits_description, inherits_charges, inherits_media, display_order)
SELECT n.dest_id, r.landlord_id, nb.id, r.name, nb.ward_code, nb.address_detail, r.area_square_meters, r.price_vnd,
       r.minimum_rental_period, r.rental_period_unit, r.description,
       CASE WHEN r.status = 'maintenance' THEN 'maintenance' ELSE 'available' END, r.inherits_price,
       r.inherits_area, r.inherits_rental_period, r.inherits_description, r.inherits_charges, r.inherits_media,
       sqlc.arg('base_order')::integer + n.pos
FROM (SELECT unnest(@dest_ids::uuid[]) AS dest_id, unnest(@src_ids::uuid[]) AS src_id, unnest(@positions::integer[]) AS pos) AS n
JOIN rooms r ON r.id = n.src_id
JOIN buildings nb ON nb.id = @building_id::uuid;

-- name: CountLiveRooms :one
SELECT count(*)::integer FROM rooms WHERE landlord_id = @landlord_id AND deleted_at IS NULL;

-- name: MaxRoomOrder :one
-- Deleted rooms count, so a new room never takes a position a deleted one had.
SELECT coalesce(max(display_order), 0)::integer FROM rooms WHERE landlord_id = @landlord_id;

-- name: RoomNameTaken :one
-- Whether a live room of the landlord in the same building (or outside any building) has the name (any case),
-- ignoring the room except_id.
SELECT EXISTS (
    SELECT 1 FROM rooms
    WHERE landlord_id = @landlord_id AND deleted_at IS NULL AND lower(name) = lower(@name::text)
      AND building_id IS NOT DISTINCT FROM sqlc.narg('building_id')::uuid
      AND (sqlc.narg('except_id')::uuid IS NULL OR id <> sqlc.narg('except_id')::uuid)
);

-- name: RoomNamesTakenInBuilding :many
-- The names among names a live room of the building already has (any case), in the stored spelling.
SELECT name FROM rooms
WHERE building_id = @building_id AND deleted_at IS NULL AND lower(name) = ANY (@names::text[])
ORDER BY display_order, id;

-- name: ListRoomsForUpdate :many
-- The landlord's live rooms among ids, locked.
SELECT id, name, status
FROM rooms
WHERE landlord_id = @landlord_id AND deleted_at IS NULL AND id = ANY (@ids::uuid[])
ORDER BY display_order, id
FOR UPDATE;

-- name: SoftDeleteRooms :exec
UPDATE rooms SET deleted_at = now(), updated_at = now()
WHERE landlord_id = @landlord_id AND deleted_at IS NULL AND id = ANY (@ids::uuid[]);

-- name: SetRoomStatus :exec
UPDATE rooms SET status = @status, updated_at = now()
WHERE landlord_id = @landlord_id AND deleted_at IS NULL AND id = ANY (@ids::uuid[]);

-- name: ListRoomOrder :many
-- Every live room of the landlord in display order, locked while the order is rewritten.
SELECT id, display_order FROM rooms
WHERE landlord_id = $1 AND deleted_at IS NULL
ORDER BY display_order, id
FOR UPDATE;

-- name: SetRoomOrder :exec
UPDATE rooms r SET display_order = o.display_order, updated_at = now()
FROM (SELECT unnest(@ids::uuid[]) AS id, unnest(@orders::integer[]) AS display_order) AS o
WHERE r.id = o.id AND r.landlord_id = @landlord_id;

-- name: SetRoomCoversFromBuilding :exec
-- Rooms that follow their building's pictures show its cover.
UPDATE rooms r SET cover_key = b.cover_key, cover_thumb_key = b.cover_thumb_key
FROM buildings b
WHERE r.id = ANY (@ids::uuid[]) AND b.id = r.building_id;

-- name: SetRoomCover :exec
UPDATE rooms SET cover_key = @cover_key, cover_thumb_key = @cover_thumb_key WHERE id = @id;

-- The pushes of a building edit into the rooms that follow it. Each returns the rooms it changed.

-- name: PushBuildingPrice :many
UPDATE rooms SET price_vnd = @price_vnd, updated_at = now()
WHERE building_id = @building_id AND inherits_price AND deleted_at IS NULL
RETURNING id;

-- name: PushBuildingArea :many
UPDATE rooms SET area_square_meters = @area_square_meters, updated_at = now()
WHERE building_id = @building_id AND inherits_area AND deleted_at IS NULL
RETURNING id;

-- name: PushBuildingRentalPeriod :many
UPDATE rooms SET minimum_rental_period = @minimum_rental_period, rental_period_unit = @rental_period_unit, updated_at = now()
WHERE building_id = @building_id AND inherits_rental_period AND deleted_at IS NULL
RETURNING id;

-- name: PushBuildingDescription :many
UPDATE rooms SET description = @description, updated_at = now()
WHERE building_id = @building_id AND inherits_description AND deleted_at IS NULL
RETURNING id;

-- name: PushBuildingAddress :many
UPDATE rooms SET ward_code = @ward_code, address_detail = @address_detail, updated_at = now()
WHERE building_id = @building_id AND deleted_at IS NULL
RETURNING id;

-- name: ListRoomsFollowingCharges :many
SELECT id FROM rooms WHERE building_id = @building_id AND inherits_charges AND deleted_at IS NULL;
