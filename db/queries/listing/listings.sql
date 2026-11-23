-- name: SearchListings :many
-- The listings in a scope, each with its room, its landlord and the admin who reviewed it. landlord_id is the
-- landlord's own scope (NULL: every landlord's, for the admins); exclude_drafts hides the drafts. Filters are
-- optional; a NULL filter is off. search_like is a lower-case LIKE pattern body already escaped with "\". The
-- province filter only applies when no ward is given. sort is "" or "-created_at" (newest first), "created_at",
-- "submitted_at" (oldest sent first), "title", "price", "-price", "area" or "-area" (the room's own values).
SELECT l.id, l.room_id, l.landlord_id, l.title, l.description, l.status, l.submitted_at, l.reviewed_by_id,
       l.reviewed_at, l.review_note, l.published_at, l.unpublished_at, l.created_at, l.updated_at,
       r.name AS room_name, r.status AS room_status, r.price_vnd AS room_price_vnd,
       r.minimum_rental_period AS room_minimum_rental_period, r.rental_period_unit AS room_rental_period_unit,
       r.address_detail AS room_address_detail, r.description AS room_description,
       r.area_square_meters AS room_area_square_meters, r.cover_key AS room_cover_key,
       r.cover_thumb_key AS room_cover_thumb_key, r.building_id AS room_building_id,
       (r.deleted_at IS NULL)::boolean AS room_live,
       r.ward_code AS ward_code, w.name AS ward_name, w.province_code AS province_code, p.name AS province_name,
       u.full_name AS landlord_full_name, u.email AS landlord_email, u.phone_number AS landlord_phone_number,
       rv.full_name AS reviewer_full_name, rv.email AS reviewer_email, rv.phone_number AS reviewer_phone_number
FROM listings l
JOIN rooms r ON r.id = l.room_id
JOIN users u ON u.id = l.landlord_id
LEFT JOIN users rv ON rv.id = l.reviewed_by_id
LEFT JOIN wards w ON w.code = r.ward_code
LEFT JOIN provinces p ON p.code = w.province_code
WHERE (sqlc.narg('landlord_id')::uuid IS NULL OR l.landlord_id = sqlc.narg('landlord_id')::uuid)
  AND (sqlc.narg('id')::uuid IS NULL OR l.id = sqlc.narg('id')::uuid)
  AND (sqlc.narg('status')::text IS NULL OR l.status = sqlc.narg('status')::text)
  AND (NOT @exclude_drafts::boolean OR l.status <> 'draft')
  AND (sqlc.narg('search_like')::text IS NULL
       OR lower(l.title) LIKE '%' || sqlc.narg('search_like')::text || '%' ESCAPE '\'
       OR lower(r.name) LIKE '%' || sqlc.narg('search_like')::text || '%' ESCAPE '\')
  AND (sqlc.narg('ward_code')::integer IS NULL OR r.ward_code = sqlc.narg('ward_code')::integer)
  AND (sqlc.narg('province_code')::integer IS NULL OR sqlc.narg('ward_code')::integer IS NOT NULL
       OR w.province_code = sqlc.narg('province_code')::integer)
  AND (sqlc.narg('price_from')::bigint IS NULL OR r.price_vnd >= sqlc.narg('price_from')::bigint)
  AND (sqlc.narg('price_to')::bigint IS NULL OR r.price_vnd <= sqlc.narg('price_to')::bigint)
  AND (sqlc.narg('area_from')::numeric IS NULL OR r.area_square_meters >= sqlc.narg('area_from')::numeric)
  AND (sqlc.narg('area_to')::numeric IS NULL OR r.area_square_meters <= sqlc.narg('area_to')::numeric)
ORDER BY
  CASE WHEN @sort::text IN ('', '-created_at') THEN l.created_at END DESC,
  CASE WHEN @sort::text = 'created_at' THEN l.created_at END ASC,
  CASE WHEN @sort::text = 'submitted_at' THEN l.submitted_at END ASC NULLS LAST,
  CASE WHEN @sort::text = 'title' THEN l.title END ASC,
  CASE WHEN @sort::text = 'price' THEN r.price_vnd END ASC,
  CASE WHEN @sort::text = '-price' THEN r.price_vnd END DESC,
  CASE WHEN @sort::text = 'area' THEN r.area_square_meters END ASC NULLS LAST,
  CASE WHEN @sort::text = '-area' THEN r.area_square_meters END DESC NULLS LAST,
  l.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountListings :one
SELECT count(*)::integer
FROM listings l
JOIN rooms r ON r.id = l.room_id
LEFT JOIN wards w ON w.code = r.ward_code
WHERE (sqlc.narg('landlord_id')::uuid IS NULL OR l.landlord_id = sqlc.narg('landlord_id')::uuid)
  AND (sqlc.narg('status')::text IS NULL OR l.status = sqlc.narg('status')::text)
  AND (NOT @exclude_drafts::boolean OR l.status <> 'draft')
  AND (sqlc.narg('search_like')::text IS NULL
       OR lower(l.title) LIKE '%' || sqlc.narg('search_like')::text || '%' ESCAPE '\'
       OR lower(r.name) LIKE '%' || sqlc.narg('search_like')::text || '%' ESCAPE '\')
  AND (sqlc.narg('ward_code')::integer IS NULL OR r.ward_code = sqlc.narg('ward_code')::integer)
  AND (sqlc.narg('province_code')::integer IS NULL OR sqlc.narg('ward_code')::integer IS NOT NULL
       OR w.province_code = sqlc.narg('province_code')::integer)
  AND (sqlc.narg('price_from')::bigint IS NULL OR r.price_vnd >= sqlc.narg('price_from')::bigint)
  AND (sqlc.narg('price_to')::bigint IS NULL OR r.price_vnd <= sqlc.narg('price_to')::bigint)
  AND (sqlc.narg('area_from')::numeric IS NULL OR r.area_square_meters >= sqlc.narg('area_from')::numeric)
  AND (sqlc.narg('area_to')::numeric IS NULL OR r.area_square_meters <= sqlc.narg('area_to')::numeric);

-- name: CountListingsByStatus :many
-- How many listings there are in each status (the counters of the tabs), in a scope.
SELECT status, count(*)::integer AS total
FROM listings
WHERE (sqlc.narg('landlord_id')::uuid IS NULL OR landlord_id = sqlc.narg('landlord_id')::uuid)
  AND (NOT @exclude_drafts::boolean OR status <> 'draft')
GROUP BY status;

-- name: ListingsByIDs :many
SELECT id, room_id, landlord_id, title, description, status, submitted_at, reviewed_by_id, reviewed_at, review_note,
       published_at, unpublished_at, created_at, updated_at
FROM listings
WHERE id = ANY(@ids::uuid[]);

-- name: GetListingForUpdate :one
SELECT id, room_id, landlord_id, title, description, status, submitted_at, reviewed_by_id, reviewed_at, review_note,
       published_at, unpublished_at, created_at, updated_at
FROM listings
WHERE id = @id
FOR UPDATE;

-- name: InsertListing :one
INSERT INTO listings (room_id, landlord_id, title, description, status, submitted_at, reviewed_by_id, reviewed_at,
                      review_note, published_at, unpublished_at)
VALUES (@room_id, @landlord_id, @title, @description, @status, @submitted_at, @reviewed_by_id, @reviewed_at,
        @review_note, @published_at, @unpublished_at)
RETURNING id;

-- name: UpdateListing :exec
UPDATE listings
SET title = @title, description = @description, status = @status, submitted_at = @submitted_at,
    reviewed_by_id = @reviewed_by_id, reviewed_at = @reviewed_at, review_note = @review_note,
    published_at = @published_at, unpublished_at = @unpublished_at, updated_at = now()
WHERE id = @id;

-- name: DeleteListing :exec
DELETE FROM listings WHERE id = @id;

-- name: OpenListingOfRoomExists :one
-- Whether the room has a waiting or live listing other than except_id.
SELECT EXISTS (
    SELECT 1 FROM listings
    WHERE room_id = @room_id AND status IN ('pending', 'approved')
      AND (sqlc.narg('except_id')::uuid IS NULL OR id <> sqlc.narg('except_id')::uuid)
);

-- name: RoomsWithOpenListing :many
SELECT DISTINCT room_id
FROM listings
WHERE room_id = ANY(@room_ids::uuid[]) AND status IN ('pending', 'approved');

-- name: LockOpenListingsOfRooms :many
SELECT id, room_id, landlord_id, title, description, status, submitted_at, reviewed_by_id, reviewed_at, review_note,
       published_at, unpublished_at, created_at, updated_at
FROM listings
WHERE room_id = ANY(@room_ids::uuid[]) AND status IN ('pending', 'approved')
ORDER BY id
FOR UPDATE;

-- name: CountOpenListingsOfLandlord :one
SELECT count(*)::integer FROM listings WHERE landlord_id = @landlord_id AND status IN ('pending', 'approved');

-- name: LockLandlordForListings :exec
-- Serialises the changes that count a landlord's open listings against a plan limit.
SELECT pg_advisory_xact_lock(hashtextextended('listing:' || @landlord_id::text, 0));
