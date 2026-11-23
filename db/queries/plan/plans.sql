-- name: SearchPlans :many
-- A page of the catalog: by audience, display order and price. A NULL audience lists both; active_only hides the
-- plans that are no longer offered.
SELECT id, code, name, description, audience, price_vnd, billing_interval, max_rooms, max_buildings,
       max_images_per_room, max_active_contracts, max_pending_registrations, search_priority, features, is_default,
       is_active, display_order, created_at, updated_at
FROM plans
WHERE (sqlc.narg('audience')::text IS NULL OR audience = sqlc.narg('audience')::text)
  AND (NOT @active_only::boolean OR is_active)
ORDER BY audience, display_order, price_vnd, id
LIMIT @page_limit OFFSET @page_offset;

-- name: CountPlans :one
SELECT count(*)::integer
FROM plans
WHERE (sqlc.narg('audience')::text IS NULL OR audience = sqlc.narg('audience')::text)
  AND (NOT @active_only::boolean OR is_active);

-- name: GetPlan :one
SELECT id, code, name, description, audience, price_vnd, billing_interval, max_rooms, max_buildings,
       max_images_per_room, max_active_contracts, max_pending_registrations, search_priority, features, is_default,
       is_active, display_order, created_at, updated_at
FROM plans
WHERE id = @id;

-- name: GetPlanByCode :one
SELECT id, code, name, description, audience, price_vnd, billing_interval, max_rooms, max_buildings,
       max_images_per_room, max_active_contracts, max_pending_registrations, search_priority, features, is_default,
       is_active, display_order, created_at, updated_at
FROM plans
WHERE code = @code;

-- name: GetDefaultPlan :one
-- The plan an account of the audience is on while it has no subscription (an inactive default does not count).
SELECT id, code, name, description, audience, price_vnd, billing_interval, max_rooms, max_buildings,
       max_images_per_room, max_active_contracts, max_pending_registrations, search_priority, features, is_default,
       is_active, display_order, created_at, updated_at
FROM plans
WHERE audience = @audience AND is_default AND is_active
LIMIT 1;

-- name: InsertPlan :one
INSERT INTO plans (code, name, description, audience, price_vnd, billing_interval, max_rooms, max_buildings,
                   max_images_per_room, max_active_contracts, max_pending_registrations, search_priority, features,
                   is_default, is_active, display_order)
VALUES (@code, @name, @description, @audience, @price_vnd, @billing_interval, @max_rooms, @max_buildings,
        @max_images_per_room, @max_active_contracts, @max_pending_registrations, @search_priority, @features,
        @is_default, @is_active, @display_order)
RETURNING id;

-- name: UpdatePlan :exec
-- Every field a super admin can change (the audience cannot: subscriptions carry it).
UPDATE plans
SET code = @code, name = @name, description = @description, price_vnd = @price_vnd,
    billing_interval = @billing_interval, max_rooms = @max_rooms, max_buildings = @max_buildings,
    max_images_per_room = @max_images_per_room, max_active_contracts = @max_active_contracts,
    max_pending_registrations = @max_pending_registrations, search_priority = @search_priority,
    features = @features, is_default = @is_default, is_active = @is_active, display_order = @display_order,
    updated_at = now()
WHERE id = @id;

-- name: ClearDefaultPlan :exec
-- Takes the default flag off the plans of the audience but one (the nil uuid spares none).
UPDATE plans SET is_default = false, updated_at = now()
WHERE audience = @audience AND is_default AND id <> @except_id;
