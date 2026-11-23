-- name: SearchContractFactsForBilling :many
-- The facts about contracts that billing needs. scoped = false is for the system (the daily commands, a lock already
-- taken). A landlord (as_landlord) sees the confirmed contracts of their own live rooms, a tenant their own contracts.
-- running_only keeps the contracts that run; after_id is the keyset of a scan.
SELECT c.id, c.room_id, c.landlord_id, c.tenant_id, c.tenant_name, c.tenant_phone, c.status, c.price_vnd, c.occupant_count,
       c.starts_on, c.ends_on, r.name AS room_name, r.rental_period_unit AS room_rental_period_unit,
       l.full_name AS landlord_full_name, t.full_name AS tenant_full_name, t.email AS tenant_email
FROM contracts c
JOIN rooms r ON r.id = c.room_id
JOIN users l ON l.id = c.landlord_id
LEFT JOIN users t ON t.id = c.tenant_id
WHERE c.deleted_at IS NULL
  AND (NOT @scoped::boolean
       OR (@as_landlord::boolean AND c.landlord_id = @viewer_id AND c.confirmed_at IS NOT NULL AND r.deleted_at IS NULL)
       OR (NOT @as_landlord::boolean AND c.tenant_id = @viewer_id))
  AND (NOT @running_only::boolean OR (c.status = 'active' AND c.confirmed_at IS NOT NULL AND r.deleted_at IS NULL))
  AND (sqlc.narg('id')::uuid IS NULL OR c.id = sqlc.narg('id')::uuid)
  AND c.id > @after_id
ORDER BY c.id
LIMIT @page_limit;

-- name: LockContractForBilling :one
SELECT id FROM contracts WHERE id = @id AND deleted_at IS NULL FOR UPDATE;

-- name: FindContractIDByRoomName :one
-- The latest contract of a live room of the landlord, by the room's name (any case): for the demo seed.
SELECT c.id
FROM contracts c
JOIN rooms r ON r.id = c.room_id
WHERE c.landlord_id = @landlord_id AND lower(r.name) = lower(@name::text) AND c.deleted_at IS NULL AND r.deleted_at IS NULL
ORDER BY c.created_at DESC, c.id DESC
LIMIT 1;

-- name: ListChargesOfRoomForBilling :many
SELECT id, name, kind, unit, amount_vnd
FROM charges
WHERE room_id = @room_id AND deleted_at IS NULL
ORDER BY display_order, id;

-- name: GetRoomNameOfLandlord :one
SELECT name FROM rooms WHERE id = @id AND landlord_id = @landlord_id AND deleted_at IS NULL;
