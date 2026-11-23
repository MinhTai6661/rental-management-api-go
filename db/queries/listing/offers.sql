-- The rooms find rooms shows ("offered" rooms), in every query below: a live room that is available, with no running
-- contract that a tenant holds (a blank contract exists only while the room is rented), whose landlord still holds the
-- landlord role, and with an approved listing. domain/listing RoomState.Offered says the same in words.

-- name: SearchOffers :many
-- One page of offered rooms. Filters are optional; a NULL filter is off. house_like is a lower-case LIKE pattern body
-- already escaped with "\": it matches the building of the room, or the landlord's name for a room outside any building.
-- sort is "" for the default order (the landlord's plan priority from priority_ids/priority_values, 0 for a landlord
-- left out, then newest first), or "price", "-price", "area", "-area", "created_at", "-created_at".
SELECT r.id AS room_id, r.name, r.building_id, COALESCE(b.name, u.full_name)::text AS house_name,
       r.area_square_meters, r.price_vnd, r.minimum_rental_period, r.rental_period_unit, r.description,
       r.address_detail, r.ward_code, w.name AS ward_name, w.province_code AS province_code, p.name AS province_name,
       r.cover_key, r.cover_thumb_key, li.id AS listing_id, li.title AS listing_title,
       li.description AS listing_description, r.landlord_id, u.full_name AS landlord_full_name, r.created_at
FROM rooms r
JOIN listings li ON li.room_id = r.id AND li.status = 'approved'
JOIN users u ON u.id = r.landlord_id
LEFT JOIN buildings b ON b.id = r.building_id
LEFT JOIN wards w ON w.code = r.ward_code
LEFT JOIN provinces p ON p.code = w.province_code
WHERE r.deleted_at IS NULL AND r.status = 'available'
  AND EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = r.landlord_id AND ur.role_code = 'landlord')
  AND NOT EXISTS (SELECT 1 FROM contracts c
                  WHERE c.room_id = r.id AND c.status = 'active' AND c.tenant_id IS NOT NULL AND c.deleted_at IS NULL)
  AND (sqlc.narg('id')::uuid IS NULL OR r.id = sqlc.narg('id')::uuid)
  AND (sqlc.narg('landlord_id')::uuid IS NULL OR r.landlord_id = sqlc.narg('landlord_id')::uuid)
  AND (sqlc.narg('house_like')::text IS NULL
       OR lower(b.name) LIKE '%' || sqlc.narg('house_like')::text || '%' ESCAPE '\'
       OR (r.building_id IS NULL AND lower(u.full_name) LIKE '%' || sqlc.narg('house_like')::text || '%' ESCAPE '\'))
  AND (sqlc.narg('ward_code')::integer IS NULL OR r.ward_code = sqlc.narg('ward_code')::integer)
  AND (sqlc.narg('province_code')::integer IS NULL OR sqlc.narg('ward_code')::integer IS NOT NULL
       OR w.province_code = sqlc.narg('province_code')::integer)
  AND (sqlc.narg('price_from')::bigint IS NULL OR r.price_vnd >= sqlc.narg('price_from')::bigint)
  AND (sqlc.narg('price_to')::bigint IS NULL OR r.price_vnd <= sqlc.narg('price_to')::bigint)
  AND (sqlc.narg('area_from')::numeric IS NULL OR r.area_square_meters >= sqlc.narg('area_from')::numeric)
  AND (sqlc.narg('area_to')::numeric IS NULL OR r.area_square_meters <= sqlc.narg('area_to')::numeric)
ORDER BY
  CASE WHEN @sort::text = '' THEN
      COALESCE((@priority_values::integer[])[array_position(@priority_ids::uuid[], r.landlord_id)], 0)
  END DESC,
  CASE WHEN @sort::text IN ('', '-created_at') THEN r.created_at END DESC,
  CASE WHEN @sort::text = 'created_at' THEN r.created_at END ASC,
  CASE WHEN @sort::text = 'price' THEN r.price_vnd END ASC,
  CASE WHEN @sort::text = '-price' THEN r.price_vnd END DESC,
  CASE WHEN @sort::text = 'area' THEN r.area_square_meters END ASC NULLS LAST,
  CASE WHEN @sort::text = '-area' THEN r.area_square_meters END DESC NULLS LAST,
  r.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountOffers :one
SELECT count(*)::integer
FROM rooms r
JOIN listings li ON li.room_id = r.id AND li.status = 'approved'
JOIN users u ON u.id = r.landlord_id
LEFT JOIN buildings b ON b.id = r.building_id
LEFT JOIN wards w ON w.code = r.ward_code
WHERE r.deleted_at IS NULL AND r.status = 'available'
  AND EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = r.landlord_id AND ur.role_code = 'landlord')
  AND NOT EXISTS (SELECT 1 FROM contracts c
                  WHERE c.room_id = r.id AND c.status = 'active' AND c.tenant_id IS NOT NULL AND c.deleted_at IS NULL)
  AND (sqlc.narg('landlord_id')::uuid IS NULL OR r.landlord_id = sqlc.narg('landlord_id')::uuid)
  AND (sqlc.narg('house_like')::text IS NULL
       OR lower(b.name) LIKE '%' || sqlc.narg('house_like')::text || '%' ESCAPE '\'
       OR (r.building_id IS NULL AND lower(u.full_name) LIKE '%' || sqlc.narg('house_like')::text || '%' ESCAPE '\'))
  AND (sqlc.narg('ward_code')::integer IS NULL OR r.ward_code = sqlc.narg('ward_code')::integer)
  AND (sqlc.narg('province_code')::integer IS NULL OR sqlc.narg('ward_code')::integer IS NOT NULL
       OR w.province_code = sqlc.narg('province_code')::integer)
  AND (sqlc.narg('price_from')::bigint IS NULL OR r.price_vnd >= sqlc.narg('price_from')::bigint)
  AND (sqlc.narg('price_to')::bigint IS NULL OR r.price_vnd <= sqlc.narg('price_to')::bigint)
  AND (sqlc.narg('area_from')::numeric IS NULL OR r.area_square_meters >= sqlc.narg('area_from')::numeric)
  AND (sqlc.narg('area_to')::numeric IS NULL OR r.area_square_meters <= sqlc.narg('area_to')::numeric);

-- name: ListOfferLandlords :many
-- The landlords with at least one offered room that matches the filters (the search asks the plans about them).
SELECT DISTINCT r.landlord_id
FROM rooms r
JOIN listings li ON li.room_id = r.id AND li.status = 'approved'
JOIN users u ON u.id = r.landlord_id
LEFT JOIN buildings b ON b.id = r.building_id
LEFT JOIN wards w ON w.code = r.ward_code
WHERE r.deleted_at IS NULL AND r.status = 'available'
  AND EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = r.landlord_id AND ur.role_code = 'landlord')
  AND NOT EXISTS (SELECT 1 FROM contracts c
                  WHERE c.room_id = r.id AND c.status = 'active' AND c.tenant_id IS NOT NULL AND c.deleted_at IS NULL)
  AND (sqlc.narg('landlord_id')::uuid IS NULL OR r.landlord_id = sqlc.narg('landlord_id')::uuid)
  AND (sqlc.narg('house_like')::text IS NULL
       OR lower(b.name) LIKE '%' || sqlc.narg('house_like')::text || '%' ESCAPE '\'
       OR (r.building_id IS NULL AND lower(u.full_name) LIKE '%' || sqlc.narg('house_like')::text || '%' ESCAPE '\'))
  AND (sqlc.narg('ward_code')::integer IS NULL OR r.ward_code = sqlc.narg('ward_code')::integer)
  AND (sqlc.narg('province_code')::integer IS NULL OR sqlc.narg('ward_code')::integer IS NOT NULL
       OR w.province_code = sqlc.narg('province_code')::integer)
  AND (sqlc.narg('price_from')::bigint IS NULL OR r.price_vnd >= sqlc.narg('price_from')::bigint)
  AND (sqlc.narg('price_to')::bigint IS NULL OR r.price_vnd <= sqlc.narg('price_to')::bigint)
  AND (sqlc.narg('area_from')::numeric IS NULL OR r.area_square_meters >= sqlc.narg('area_from')::numeric)
  AND (sqlc.narg('area_to')::numeric IS NULL OR r.area_square_meters <= sqlc.narg('area_to')::numeric);

-- name: GetOfferLandlord :one
-- An active account that holds the landlord role, as a guest may see it: the name only.
SELECT u.id, u.full_name
FROM users u
WHERE u.id = @id AND u.status = 'active'
  AND EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = u.id AND ur.role_code = 'landlord');

-- name: ListOfferedHousesOfLandlord :many
-- The names of the buildings of the landlord that have an offered room.
SELECT DISTINCT b.name
FROM rooms r
JOIN listings li ON li.room_id = r.id AND li.status = 'approved'
JOIN buildings b ON b.id = r.building_id
WHERE r.landlord_id = @landlord_id AND r.deleted_at IS NULL AND r.status = 'available'
  AND NOT EXISTS (SELECT 1 FROM contracts c
                  WHERE c.room_id = r.id AND c.status = 'active' AND c.tenant_id IS NOT NULL AND c.deleted_at IS NULL)
ORDER BY b.name;
