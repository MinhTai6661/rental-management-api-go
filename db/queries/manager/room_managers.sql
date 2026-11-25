-- name: LockLandlordRoomsForManagers :many
-- The landlord's live rooms among the ids, locked in a fixed order (a room that is not theirs or is gone is not here).
SELECT r.id, r.name FROM rooms r
WHERE r.landlord_id = @landlord_id AND r.id = ANY(@room_ids::uuid[]) AND r.deleted_at IS NULL
ORDER BY r.id
FOR UPDATE;

-- name: ListActiveManagedRoomNames :many
-- Which of the rooms already have a manager.
SELECT rm.room_id, r.name AS room_name FROM room_managers rm JOIN rooms r ON r.id = rm.room_id
WHERE rm.status = 'active' AND rm.room_id = ANY(@room_ids::uuid[]) ORDER BY lower(r.name), r.id;

-- name: InsertRoomManagers :many
INSERT INTO room_managers (landlord_id, room_id, manager_id, notify_landlord, assigned_by_id, accepted_at)
SELECT @landlord_id::uuid, unnest(@room_ids::uuid[]), @manager_id::uuid, @notify_landlord::boolean, @landlord_id::uuid, @accepted_at::timestamptz
RETURNING id, room_id;

-- name: InsertRoomManagerCapabilities :exec
INSERT INTO room_manager_capabilities (room_manager_id, capability)
SELECT m, c FROM unnest(@assignment_ids::uuid[]) m CROSS JOIN unnest(@capabilities::text[]) c;

-- name: DeleteRoomManagerCapabilities :exec
DELETE FROM room_manager_capabilities WHERE room_manager_id = ANY(@assignment_ids::uuid[]);

-- name: ListRoomManagerCapabilities :many
SELECT room_manager_id, capability FROM room_manager_capabilities
WHERE room_manager_id = ANY(@assignment_ids::uuid[]) ORDER BY room_manager_id, capability;

-- name: SearchRoomManagers :many
-- The landlord's active hand-overs, newest first; a NULL manager_id is every manager.
SELECT sqlc.embed(rm), r.name AS room_name, b.name AS building_name,
       m.full_name AS manager_full_name, m.email AS manager_email
FROM room_managers rm
JOIN rooms r ON r.id = rm.room_id
JOIN users m ON m.id = rm.manager_id
LEFT JOIN buildings b ON b.id = r.building_id
WHERE rm.landlord_id = @landlord_id AND rm.status = 'active'
  AND (sqlc.narg('manager_id')::uuid IS NULL OR rm.manager_id = sqlc.narg('manager_id')::uuid)
  AND (sqlc.narg('id')::uuid IS NULL OR rm.id = sqlc.narg('id')::uuid)
ORDER BY rm.accepted_at DESC, rm.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountRoomManagers :one
SELECT count(*)::integer FROM room_managers rm
WHERE rm.landlord_id = @landlord_id AND rm.status = 'active'
  AND (sqlc.narg('manager_id')::uuid IS NULL OR rm.manager_id = sqlc.narg('manager_id')::uuid);

-- name: ListRoomManagerGroups :many
-- One row per manager of the landlord, the most recently given first.
SELECT rm.manager_id, m.full_name AS manager_full_name, m.email AS manager_email,
       count(*)::integer AS room_count,
       array_agg(rm.id ORDER BY rm.accepted_at DESC, rm.id DESC)::uuid[] AS assignment_ids,
       array_agg(rm.room_id ORDER BY rm.accepted_at DESC, rm.id DESC)::uuid[] AS room_ids,
       count(*) FILTER (WHERE rm.notify_landlord)::integer AS notify_count,
       max(rm.accepted_at)::timestamptz AS latest_at
FROM room_managers rm JOIN users m ON m.id = rm.manager_id
WHERE rm.landlord_id = @landlord_id AND rm.status = 'active'
GROUP BY rm.manager_id, m.full_name, m.email
ORDER BY max(rm.accepted_at) DESC, rm.manager_id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountRoomManagerGroups :one
SELECT count(DISTINCT rm.manager_id)::integer FROM room_managers rm WHERE rm.landlord_id = @landlord_id AND rm.status = 'active';

-- name: ListRoomManagerGroupCapabilities :many
-- How many of each manager's hand-overs hold each capability.
SELECT rm.manager_id, c.capability, count(*)::integer AS held
FROM room_manager_capabilities c JOIN room_managers rm ON rm.id = c.room_manager_id
WHERE rm.landlord_id = @landlord_id AND rm.status = 'active' AND rm.manager_id = ANY(@manager_ids::uuid[])
GROUP BY rm.manager_id, c.capability;

-- name: ListBusyRoomIDs :many
-- Every room of the landlord that has a manager.
SELECT room_id FROM room_managers WHERE landlord_id = @landlord_id AND status = 'active' ORDER BY room_id;

-- name: LockActiveRoomManagers :many
-- The landlord's active hand-overs among the ids, locked in a fixed order; others are not here.
SELECT * FROM room_managers
WHERE landlord_id = @landlord_id AND status = 'active' AND id = ANY(@ids::uuid[])
ORDER BY id
FOR UPDATE;

-- name: SaveRoomManager :exec
UPDATE room_managers SET notify_landlord = @notify_landlord, updated_at = now() WHERE id = @id;

-- name: RevokeRoomManagers :exec
UPDATE room_managers SET status = 'revoked', revoked_at = @at, updated_at = now()
WHERE id = ANY(@ids::uuid[]) AND status = 'active';

-- name: ListRoomNamesByIDs :many
SELECT id, name FROM rooms WHERE id = ANY(@ids::uuid[]);

-- name: FindActiveRoomManager :one
-- The manager's hand-over of one room, when it is in force.
SELECT * FROM room_managers WHERE room_id = @room_id AND manager_id = @manager_id AND status = 'active';

-- name: CountActiveRoomManagersOf :one
SELECT count(*)::integer FROM room_managers WHERE manager_id = @manager_id AND status = 'active';

-- name: ListActiveRoomManagersOf :many
SELECT * FROM room_managers WHERE manager_id = @manager_id AND status = 'active' ORDER BY id;

-- name: SearchManagedRooms :many
-- The rooms handed to a manager; a NULL landlord_id is every landlord.
SELECT sqlc.embed(rm), r.name AS room_name, b.name AS building_name, l.full_name AS landlord_full_name,
       lp.business_name AS landlord_business_name
FROM room_managers rm
JOIN rooms r ON r.id = rm.room_id AND r.deleted_at IS NULL
JOIN users l ON l.id = rm.landlord_id
LEFT JOIN landlord_profiles lp ON lp.user_id = rm.landlord_id
LEFT JOIN buildings b ON b.id = r.building_id
WHERE rm.manager_id = @manager_id AND rm.status = 'active'
  AND (sqlc.narg('landlord_id')::uuid IS NULL OR rm.landlord_id = sqlc.narg('landlord_id')::uuid)
ORDER BY rm.accepted_at DESC, rm.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountManagedRooms :one
SELECT count(*)::integer FROM room_managers rm JOIN rooms r ON r.id = rm.room_id AND r.deleted_at IS NULL
WHERE rm.manager_id = @manager_id AND rm.status = 'active'
  AND (sqlc.narg('landlord_id')::uuid IS NULL OR rm.landlord_id = sqlc.narg('landlord_id')::uuid);

-- name: ListManagedGroups :many
-- One row per landlord that handed rooms to the manager.
SELECT rm.landlord_id, l.full_name AS landlord_full_name, lp.business_name AS landlord_business_name,
       count(*)::integer AS room_count,
       array_agg(rm.room_id ORDER BY rm.accepted_at DESC, rm.id DESC)::uuid[] AS room_ids,
       max(rm.accepted_at)::timestamptz AS latest_at
FROM room_managers rm
JOIN rooms r ON r.id = rm.room_id AND r.deleted_at IS NULL
JOIN users l ON l.id = rm.landlord_id
LEFT JOIN landlord_profiles lp ON lp.user_id = rm.landlord_id
WHERE rm.manager_id = @manager_id AND rm.status = 'active'
GROUP BY rm.landlord_id, l.full_name, lp.business_name
ORDER BY max(rm.accepted_at) DESC, rm.landlord_id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountManagedGroups :one
SELECT count(DISTINCT rm.landlord_id)::integer FROM room_managers rm JOIN rooms r ON r.id = rm.room_id AND r.deleted_at IS NULL
WHERE rm.manager_id = @manager_id AND rm.status = 'active';

-- name: GrantManagerRole :exec
INSERT INTO user_roles (user_id, role_code) VALUES (@user_id, 'manager') ON CONFLICT (user_id, role_code) DO NOTHING;

-- name: RemoveManagerRole :exec
DELETE FROM user_roles WHERE user_id = @user_id AND role_code = 'manager';

-- name: ClearLastManagerRole :exec
UPDATE users SET last_active_role = NULL, updated_at = now() WHERE id = @user_id AND last_active_role = 'manager';

-- name: ListManagerNames :many
SELECT id, full_name FROM users WHERE id = ANY(@ids::uuid[]);
