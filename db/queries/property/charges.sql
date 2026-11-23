-- name: ListChargesOfBuildings :many
SELECT id, building_id, name, kind, amount_vnd, unit, note, display_order, building_charge_id
FROM charges
WHERE building_id = ANY (@building_ids::uuid[]) AND deleted_at IS NULL
ORDER BY display_order, id;

-- name: InsertBuildingCharge :exec
INSERT INTO charges (building_id, name, kind, amount_vnd, unit, note, display_order)
VALUES (@building_id, @name, @kind, @amount_vnd, @unit, @note, @display_order);

-- name: UpdateCharge :exec
UPDATE charges
SET name = @name, kind = @kind, amount_vnd = @amount_vnd, unit = @unit, note = @note,
    display_order = @display_order, updated_at = now()
WHERE id = @id AND deleted_at IS NULL;

-- name: SoftDeleteCharges :exec
UPDATE charges SET deleted_at = now(), updated_at = now()
WHERE id = ANY (@ids::uuid[]) AND deleted_at IS NULL;

-- name: SoftDeleteChargesOfBuildings :exec
UPDATE charges SET deleted_at = now(), updated_at = now()
WHERE building_id = ANY (@building_ids::uuid[]) AND deleted_at IS NULL;

-- name: ListChargesOfRooms :many
SELECT id, room_id, name, kind, amount_vnd, unit, note, display_order, building_charge_id
FROM charges
WHERE room_id = ANY (@room_ids::uuid[]) AND deleted_at IS NULL
ORDER BY display_order, id;

-- name: InsertRoomCharge :exec
INSERT INTO charges (room_id, name, kind, amount_vnd, unit, note, display_order)
VALUES (@room_id, @name, @kind, @amount_vnd, @unit, @note, @display_order);

-- name: InsertChargesForRooms :exec
-- The same list of own charges for every room of a run. notes use '' for none.
INSERT INTO charges (room_id, name, kind, amount_vnd, unit, note, display_order)
SELECT r.id, c.name, c.kind, c.amount_vnd, c.unit, nullif(c.note, ''), c.display_order
FROM unnest(@room_ids::uuid[]) AS r(id)
CROSS JOIN (SELECT unnest(@names::text[]) AS name, unnest(@kinds::text[]) AS kind,
                   unnest(@amounts::bigint[]) AS amount_vnd, unnest(@units::text[]) AS unit,
                   unnest(@notes::text[]) AS note, unnest(@orders::smallint[]) AS display_order) AS c;

-- name: DetachRoomCharges :exec
-- A room that stops following its building keeps its charges as its own.
UPDATE charges SET building_charge_id = NULL, updated_at = now()
WHERE room_id = ANY (@room_ids::uuid[]) AND building_charge_id IS NOT NULL AND deleted_at IS NULL;

-- name: UpdateChargeCopiesFromTemplates :exec
-- Step 1 of making rooms follow a building's charges: a copy of a live template of the building is updated in
-- place (it keeps its id), so what hangs on it keeps pointing at it.
UPDATE charges c
SET name = t.name, kind = t.kind, amount_vnd = t.amount_vnd, unit = t.unit, note = t.note,
    display_order = t.display_order, updated_at = now()
FROM charges t
WHERE c.room_id = ANY (@room_ids::uuid[]) AND c.deleted_at IS NULL
  AND t.id = c.building_charge_id AND t.building_id = @building_id AND t.deleted_at IS NULL;

-- name: DeleteChargeCopiesWithoutTemplate :exec
-- Step 2: the room's own charges and the copies of templates that are gone (or belong to another building).
UPDATE charges c SET deleted_at = now(), updated_at = now()
WHERE c.room_id = ANY (@room_ids::uuid[]) AND c.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM charges t
                  WHERE t.id = c.building_charge_id AND t.building_id = @building_id AND t.deleted_at IS NULL);

-- name: InsertMissingChargeCopies :exec
-- Step 3: a live template with no copy on the room is copied onto it.
INSERT INTO charges (room_id, building_charge_id, name, kind, amount_vnd, unit, note, display_order)
SELECT r.id, t.id, t.name, t.kind, t.amount_vnd, t.unit, t.note, t.display_order
FROM unnest(@room_ids::uuid[]) AS r(id)
JOIN charges t ON t.building_id = @building_id AND t.deleted_at IS NULL
WHERE NOT EXISTS (SELECT 1 FROM charges c
                  WHERE c.room_id = r.id AND c.building_charge_id = t.id AND c.deleted_at IS NULL);

-- name: CopyOwnCharges :exec
-- Copies the own charges of rooms (source_ids) onto their copies (new_ids, same order).
INSERT INTO charges (room_id, name, kind, amount_vnd, unit, note, display_order)
SELECT n.new_id, c.name, c.kind, c.amount_vnd, c.unit, c.note, c.display_order
FROM (SELECT unnest(@new_ids::uuid[]) AS new_id, unnest(@source_ids::uuid[]) AS source_id) AS n
JOIN charges c ON c.room_id = n.source_id AND c.deleted_at IS NULL;
