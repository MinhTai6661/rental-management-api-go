-- name: SearchBuildings :many
-- The landlord's live buildings with their ward, province and room counts. Filters are optional; a NULL filter is
-- off. name_like is a lower-case LIKE pattern body already escaped with "\".
SELECT b.id, b.name, b.ward_code, w.name AS ward_name, w.province_code, p.name AS province_name,
       b.address_detail, b.description, b.price_vnd, b.area_square_meters, b.minimum_rental_period,
       b.rental_period_unit, b.cover_key, b.cover_thumb_key, b.display_order, b.created_at, b.updated_at,
       (SELECT count(*) FROM rooms r WHERE r.building_id = b.id AND r.deleted_at IS NULL)::integer AS rooms_count,
       (SELECT count(*) FROM rooms r
         WHERE r.building_id = b.id AND r.deleted_at IS NULL AND r.status = 'available')::integer AS available_rooms_count
FROM buildings b
LEFT JOIN wards w ON w.code = b.ward_code
LEFT JOIN provinces p ON p.code = w.province_code
WHERE b.landlord_id = @landlord_id
  AND b.deleted_at IS NULL
  AND (sqlc.narg('id')::uuid IS NULL OR b.id = sqlc.narg('id')::uuid)
  AND (sqlc.narg('name_like')::text IS NULL OR lower(b.name) LIKE '%' || sqlc.narg('name_like')::text || '%' ESCAPE '\')
  AND (sqlc.narg('ward_code')::integer IS NULL OR b.ward_code = sqlc.narg('ward_code')::integer)
  AND (sqlc.narg('province_code')::integer IS NULL OR w.province_code = sqlc.narg('province_code')::integer)
ORDER BY
  CASE WHEN @sort::text = 'name' THEN lower(b.name) END ASC,
  CASE WHEN @sort::text = '-name' THEN lower(b.name) END DESC,
  CASE WHEN @sort::text = 'created_at' THEN b.created_at END ASC,
  CASE WHEN @sort::text = '-created_at' THEN b.created_at END DESC,
  b.display_order, b.id
LIMIT @page_limit OFFSET @page_offset;

-- name: CountBuildings :one
SELECT count(*)::integer
FROM buildings b
LEFT JOIN wards w ON w.code = b.ward_code
WHERE b.landlord_id = @landlord_id
  AND b.deleted_at IS NULL
  AND (sqlc.narg('name_like')::text IS NULL OR lower(b.name) LIKE '%' || sqlc.narg('name_like')::text || '%' ESCAPE '\')
  AND (sqlc.narg('ward_code')::integer IS NULL OR b.ward_code = sqlc.narg('ward_code')::integer)
  AND (sqlc.narg('province_code')::integer IS NULL OR w.province_code = sqlc.narg('province_code')::integer);

-- name: GetBuildingForUpdate :one
SELECT id, name, ward_code, address_detail, description, price_vnd, area_square_meters, minimum_rental_period,
       rental_period_unit
FROM buildings
WHERE id = @id AND landlord_id = @landlord_id AND deleted_at IS NULL
FOR UPDATE;

-- name: InsertBuilding :one
INSERT INTO buildings (landlord_id, name, ward_code, address_detail, description, price_vnd, area_square_meters,
                       minimum_rental_period, rental_period_unit, display_order)
VALUES (@landlord_id, @name, @ward_code, @address_detail, @description, @price_vnd, @area_square_meters,
        @minimum_rental_period, @rental_period_unit, @display_order)
RETURNING id;

-- name: UpdateBuilding :exec
UPDATE buildings
SET name = @name, ward_code = @ward_code, address_detail = @address_detail, description = @description,
    price_vnd = @price_vnd, area_square_meters = @area_square_meters, minimum_rental_period = @minimum_rental_period,
    rental_period_unit = @rental_period_unit, updated_at = now()
WHERE id = @id AND landlord_id = @landlord_id AND deleted_at IS NULL;

-- name: BuildingNameTaken :one
-- Whether a live building of the landlord already has the name (any case), ignoring the building except_id.
SELECT EXISTS (
    SELECT 1 FROM buildings
    WHERE landlord_id = @landlord_id AND deleted_at IS NULL AND lower(name) = lower(@name::text)
      AND (sqlc.narg('except_id')::uuid IS NULL OR id <> sqlc.narg('except_id')::uuid)
);

-- name: CountLiveBuildings :one
SELECT count(*)::integer FROM buildings WHERE landlord_id = @landlord_id AND deleted_at IS NULL;

-- name: MaxBuildingOrder :one
-- Deleted buildings count, so a new building never takes a position a deleted one had.
SELECT coalesce(max(display_order), 0)::integer FROM buildings WHERE landlord_id = @landlord_id;

-- name: AnyBuildingEver :one
SELECT EXISTS (SELECT 1 FROM buildings WHERE landlord_id = @landlord_id);

-- name: WardExists :one
SELECT EXISTS (SELECT 1 FROM wards WHERE code = $1);

-- name: PropertyFirstWardCode :one
SELECT code FROM wards ORDER BY code LIMIT 1;

-- name: CountRoomsFollowingPrice :one
SELECT count(*)::integer FROM rooms WHERE building_id = $1 AND inherits_price AND deleted_at IS NULL;

-- name: ListBuildingsToDelete :many
-- The landlord's live buildings among ids, locked, with how many live rooms each still holds.
SELECT b.id, b.name,
       (SELECT count(*) FROM rooms r WHERE r.building_id = b.id AND r.deleted_at IS NULL)::integer AS rooms_count
FROM buildings b
WHERE b.landlord_id = @landlord_id AND b.deleted_at IS NULL AND b.id = ANY (@ids::uuid[])
ORDER BY b.display_order, b.id
FOR UPDATE OF b;

-- name: SoftDeleteBuildings :exec
UPDATE buildings SET deleted_at = now(), updated_at = now()
WHERE landlord_id = @landlord_id AND deleted_at IS NULL AND id = ANY (@ids::uuid[]);

-- name: ListBuildingOrder :many
-- Every live building of the landlord in display order, locked while the order is rewritten.
SELECT id, display_order FROM buildings
WHERE landlord_id = $1 AND deleted_at IS NULL
ORDER BY display_order, id
FOR UPDATE;

-- name: SetBuildingOrder :exec
UPDATE buildings b SET display_order = o.display_order, updated_at = now()
FROM (SELECT unnest(@ids::uuid[]) AS id, unnest(@orders::integer[]) AS display_order) AS o
WHERE b.id = o.id AND b.landlord_id = @landlord_id;
