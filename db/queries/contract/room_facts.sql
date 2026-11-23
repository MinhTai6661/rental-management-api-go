-- name: LockRoomForContract :one
-- The facts about a room that contracts need. Locks the row until the transaction ends, so contracts and the room
-- change one at a time. Includes a deleted room (deleted_at tells). listed: an admin approved a listing of the room.
SELECT r.id, r.landlord_id, r.name, r.status, r.price_vnd, r.minimum_rental_period, r.rental_period_unit, r.deleted_at,
       EXISTS (SELECT 1 FROM listings l WHERE l.room_id = r.id AND l.status = 'approved') AS listed
FROM rooms r
WHERE r.id = @id
FOR UPDATE;

-- name: FindRoomByNameForContract :one
-- A live room of the landlord by name (any case): for the demo seed.
SELECT r.id, r.landlord_id, r.name, r.status, r.price_vnd, r.minimum_rental_period, r.rental_period_unit, r.deleted_at,
       EXISTS (SELECT 1 FROM listings l WHERE l.room_id = r.id AND l.status = 'approved') AS listed
FROM rooms r
WHERE r.landlord_id = @landlord_id AND lower(r.name) = lower(@name::text) AND r.deleted_at IS NULL
ORDER BY r.display_order, r.id
LIMIT 1;

-- name: ListChargesOfRoomForRental :many
-- The costs on top of the rent a renter sees in the detail of a rented room.
SELECT id, name, kind, amount_vnd, unit, note
FROM charges
WHERE room_id = @room_id AND deleted_at IS NULL
ORDER BY display_order, id;

-- name: ListPicturesOfRoomForRental :many
-- The pictures a renter sees: the room's own, or the building's while the room follows them.
SELECT m.id, m.kind, m.file_key, m.thumb_key, m.width, m.height
FROM media m
JOIN rooms r ON (r.inherits_media AND m.building_id = r.building_id) OR (NOT r.inherits_media AND m.room_id = r.id)
WHERE r.id = @room_id
ORDER BY m.display_order, m.id;
