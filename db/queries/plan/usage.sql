-- name: LockAccountForPlan :one
-- The account whose plans change. Locked until the transaction ends (NO KEY UPDATE does not block the foreign keys
-- that point at the user), so two changes of one account's plans run one at a time.
SELECT id, full_name FROM users WHERE id = @id FOR NO KEY UPDATE;

-- name: ListRoleCodesOfUser :many
SELECT role_code FROM user_roles WHERE user_id = @user_id ORDER BY role_code;

-- name: CountRoomsOfLandlordForPlan :one
SELECT count(*)::integer FROM rooms WHERE landlord_id = @landlord_id AND deleted_at IS NULL;

-- name: CountBuildingsOfLandlordForPlan :one
SELECT count(*)::integer FROM buildings WHERE landlord_id = @landlord_id AND deleted_at IS NULL;

-- name: CountRunningContractsOfLandlordForPlan :one
-- Running contracts with a tenant: a blank contract is bookkeeping, not a tenant the plan limits.
SELECT count(*)::integer FROM contracts
WHERE landlord_id = @landlord_id AND status = 'active' AND tenant_id IS NOT NULL AND deleted_at IS NULL;

-- name: CountPendingRegistrationsOfTenantForPlan :one
SELECT count(*)::integer FROM contracts
WHERE tenant_id = @tenant_id AND status = 'pending' AND deleted_at IS NULL;
