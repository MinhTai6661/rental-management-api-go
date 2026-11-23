-- name: LockRoomForListing :one
-- The facts about a room that listings need. Locks the row until the transaction ends, so listings and the room
-- change one at a time. Includes a deleted room (deleted_at tells).
SELECT id, landlord_id, name, deleted_at
FROM rooms
WHERE id = @id
FOR UPDATE;

-- name: ListChargesOfRoomForListing :many
-- The costs on top of the rent that a listing shows.
SELECT id, name, kind, amount_vnd, unit, note
FROM charges
WHERE room_id = @room_id AND deleted_at IS NULL
ORDER BY display_order, id;

-- name: ListPicturesOfRoomForListing :many
-- The pictures a listing shows: the room's own, or the building's while the room follows them.
SELECT m.id, m.kind, m.file_key, m.thumb_key, m.width, m.height
FROM media m
JOIN rooms r ON (r.inherits_media AND m.building_id = r.building_id) OR (NOT r.inherits_media AND m.room_id = r.id)
WHERE r.id = @room_id
ORDER BY m.display_order, m.id;

-- name: ListRoomsWithoutListing :many
-- The available rooms of a landlord that have no listing at all yet, in display order (the demo seed writes them one).
SELECT r.id, r.name, r.description
FROM rooms r
WHERE r.landlord_id = @landlord_id AND r.deleted_at IS NULL AND r.status = 'available'
  AND NOT EXISTS (SELECT 1 FROM listings l WHERE l.room_id = r.id)
ORDER BY r.display_order, r.id;
