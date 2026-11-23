-- Pictures of a room or of a building. The "owner" queries take both ids and one of them NULL: the other column
-- then decides (a NULL comparison is never true).

-- name: ListMediaOfBuildings :many
SELECT * FROM media WHERE building_id = ANY (@building_ids::uuid[]) ORDER BY display_order, id;

-- name: ListMediaOfRooms :many
SELECT * FROM media WHERE room_id = ANY (@room_ids::uuid[]) ORDER BY display_order, id;

-- name: DeleteMediaOfBuildings :many
-- Removes the pictures of the buildings and returns their file keys, so the files can be deleted after commit.
DELETE FROM media WHERE building_id = ANY (@building_ids::uuid[])
RETURNING file_key, thumb_key;

-- name: DeleteMediaOfRooms :many
-- Removes the pictures of the rooms and returns their file keys, so the files can be deleted after commit.
DELETE FROM media WHERE room_id = ANY (@room_ids::uuid[])
RETURNING file_key, thumb_key;

-- name: OwnsRoom :one
SELECT EXISTS (SELECT 1 FROM rooms WHERE id = @id AND landlord_id = @landlord_id AND deleted_at IS NULL);

-- name: OwnsBuilding :one
SELECT EXISTS (SELECT 1 FROM buildings WHERE id = @id AND landlord_id = @landlord_id AND deleted_at IS NULL);

-- name: InsertMedia :one
INSERT INTO media (room_id, building_id, kind, file_key, thumb_key, content_type, size_bytes, width, height,
                   original_name, display_order)
VALUES (sqlc.narg('room_id'), sqlc.narg('building_id'), @kind, @file_key, @thumb_key, @content_type, @size_bytes,
        @width, @height, @original_name, @display_order)
RETURNING *;

-- name: DemoteMediaCovers :exec
UPDATE media SET kind = 'photo', updated_at = now()
WHERE kind = 'cover' AND (room_id = sqlc.narg('room_id')::uuid OR building_id = sqlc.narg('building_id')::uuid);

-- name: ReplaceMediaFile :one
UPDATE media
SET file_key = @file_key, thumb_key = @thumb_key, content_type = @content_type, size_bytes = @size_bytes,
    width = @width, height = @height, original_name = @original_name, updated_at = now()
WHERE id = @id AND (room_id = sqlc.narg('room_id')::uuid OR building_id = sqlc.narg('building_id')::uuid)
RETURNING *;

-- name: DeleteMedia :one
DELETE FROM media
WHERE id = @id AND (room_id = sqlc.narg('room_id')::uuid OR building_id = sqlc.narg('building_id')::uuid)
RETURNING file_key, thumb_key;

-- name: RenumberMedia :exec
-- Closes the gaps a deleted picture leaves: positions become 1..n in the current order.
UPDATE media m SET display_order = n.pos
FROM (SELECT id, row_number() OVER (ORDER BY display_order, id)::smallint AS pos
      FROM media WHERE room_id = sqlc.narg('room_id')::uuid OR building_id = sqlc.narg('building_id')::uuid) n
WHERE m.id = n.id AND m.display_order <> n.pos;

-- name: SetMediaOrder :exec
-- Applies an arrangement: position and kind per picture. Demote the old cover first (one cover per owner).
UPDATE media m SET kind = o.kind, display_order = o.pos, updated_at = now()
FROM (SELECT unnest(@ids::uuid[]) AS id, unnest(@kinds::text[]) AS kind, unnest(@positions::smallint[]) AS pos) o
WHERE m.id = o.id AND (m.room_id = sqlc.narg('room_id')::uuid OR m.building_id = sqlc.narg('building_id')::uuid);

-- name: SetBuildingCover :exec
UPDATE buildings SET cover_key = @cover_key, cover_thumb_key = @cover_thumb_key, updated_at = now() WHERE id = @id;

-- name: PushBuildingCover :many
-- The rooms that follow the building's pictures show its cover. Returns the rooms it changed.
UPDATE rooms r SET cover_key = b.cover_key, cover_thumb_key = b.cover_thumb_key
FROM buildings b
WHERE b.id = @building_id AND r.building_id = b.id AND r.inherits_media AND r.deleted_at IS NULL
RETURNING r.id;

-- name: StopFollowingBuildingMedia :exec
-- A room that gets a picture of its own no longer shows the building's.
UPDATE rooms SET inherits_media = false, updated_at = now() WHERE id = @id;

-- name: CopyBuildingMedia :exec
-- Duplicates pictures onto a building under new keys (the files were copied first).
INSERT INTO media (building_id, kind, file_key, thumb_key, content_type, size_bytes, width, height, original_name,
                   display_order)
SELECT @building_id::uuid, m.kind, n.file_key, n.thumb_key, m.content_type, m.size_bytes, m.width, m.height,
       m.original_name, m.display_order
FROM (SELECT unnest(@source_ids::uuid[]) AS source_id, unnest(@file_keys::text[]) AS file_key,
             unnest(@thumb_keys::text[]) AS thumb_key) n
JOIN media m ON m.id = n.source_id;

-- name: CopyRoomMedia :exec
-- Duplicates pictures onto rooms (room_ids[i] receives source_ids[i]) under new keys.
INSERT INTO media (room_id, kind, file_key, thumb_key, content_type, size_bytes, width, height, original_name,
                   display_order)
SELECT n.room_id, m.kind, n.file_key, n.thumb_key, m.content_type, m.size_bytes, m.width, m.height,
       m.original_name, m.display_order
FROM (SELECT unnest(@room_ids::uuid[]) AS room_id, unnest(@source_ids::uuid[]) AS source_id,
             unnest(@file_keys::text[]) AS file_key, unnest(@thumb_keys::text[]) AS thumb_key) n
JOIN media m ON m.id = n.source_id;
