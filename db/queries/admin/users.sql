-- Queries of the admin's user management.

-- name: AdminListUsers :many
-- Staff accounts show only when include_staff is set (a super admin); the viewer never sees their own account.
SELECT u.id, u.email, u.full_name, u.phone_number, u.status, u.ward_code, u.created_at,
       COALESCE((SELECT array_agg(ur.role_code ORDER BY r.seniority) FROM user_roles ur JOIN roles r ON r.code = ur.role_code
                 WHERE ur.user_id = u.id), '{}')::text[] AS roles
FROM users u
WHERE u.deleted_at IS NULL
  AND u.id <> @viewer_id
  AND (@include_staff::boolean OR NOT EXISTS (SELECT 1 FROM user_roles ur JOIN roles r ON r.code = ur.role_code
                                              WHERE ur.user_id = u.id AND r.is_staff))
  AND (sqlc.narg('status')::text IS NULL OR u.status = sqlc.narg('status')::text)
  AND (sqlc.narg('role_name')::text IS NULL OR EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = u.id AND ur.role_code = sqlc.narg('role_name')::text))
  AND (sqlc.narg('search')::text IS NULL OR lower(u.full_name) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\'
       OR lower(COALESCE(u.email, '')) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\')
  AND (sqlc.narg('ward_code')::int IS NULL OR u.ward_code = sqlc.narg('ward_code')::int)
  AND (sqlc.narg('province_code')::int IS NULL OR u.ward_code IN (SELECT code FROM wards WHERE province_code = sqlc.narg('province_code')::int))
ORDER BY
  CASE WHEN @sort::text = 'name' THEN u.full_name END ASC,
  CASE WHEN @sort::text = 'email' THEN u.email END ASC,
  u.created_at DESC, u.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: AdminCountUsers :one
SELECT count(*)::bigint
FROM users u
WHERE u.deleted_at IS NULL
  AND u.id <> @viewer_id
  AND (@include_staff::boolean OR NOT EXISTS (SELECT 1 FROM user_roles ur JOIN roles r ON r.code = ur.role_code
                                              WHERE ur.user_id = u.id AND r.is_staff))
  AND (sqlc.narg('status')::text IS NULL OR u.status = sqlc.narg('status')::text)
  AND (sqlc.narg('role_name')::text IS NULL OR EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = u.id AND ur.role_code = sqlc.narg('role_name')::text))
  AND (sqlc.narg('search')::text IS NULL OR lower(u.full_name) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\'
       OR lower(COALESCE(u.email, '')) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\')
  AND (sqlc.narg('ward_code')::int IS NULL OR u.ward_code = sqlc.narg('ward_code')::int)
  AND (sqlc.narg('province_code')::int IS NULL OR u.ward_code IN (SELECT code FROM wards WHERE province_code = sqlc.narg('province_code')::int));

-- name: AdminLockUser :one
-- The account and its roles; the row stays locked until the transaction ends.
SELECT u.id, u.email, u.full_name, u.phone_number, u.status, u.last_active_role, u.ward_code, u.address_detail, u.created_at,
       COALESCE((SELECT array_agg(ur.role_code ORDER BY r.seniority) FROM user_roles ur JOIN roles r ON r.code = ur.role_code
                 WHERE ur.user_id = u.id), '{}')::text[] AS roles
FROM users u
WHERE u.id = $1 AND u.deleted_at IS NULL
FOR UPDATE OF u;

-- name: AdminGetUser :one
SELECT u.id, u.email, u.full_name, u.phone_number, u.status, u.last_active_role, u.ward_code, u.address_detail, u.created_at,
       COALESCE((SELECT array_agg(ur.role_code ORDER BY r.seniority) FROM user_roles ur JOIN roles r ON r.code = ur.role_code
                 WHERE ur.user_id = u.id), '{}')::text[] AS roles
FROM users u
WHERE u.id = $1 AND u.deleted_at IS NULL;

-- name: AdminSoftDeleteUser :exec
UPDATE users SET deleted_at = @now::timestamptz, updated_at = @now::timestamptz WHERE id = @id AND deleted_at IS NULL;

-- name: AdminDeleteUserRoles :exec
DELETE FROM user_roles WHERE user_id = $1;

-- name: AdminInsertUserRoles :exec
INSERT INTO user_roles (user_id, role_code, assigned_at)
SELECT @user_id::uuid, unnest(@role_codes::text[]), @now::timestamptz
ON CONFLICT (user_id, role_code) DO NOTHING;

-- name: AdminDeleteUserRole :exec
DELETE FROM user_roles WHERE user_id = @user_id AND role_code = @role_code;

-- name: AdminSetLastActiveRole :exec
UPDATE users SET last_active_role = sqlc.narg('role')::text, updated_at = @now::timestamptz WHERE id = @id;

-- name: AdminRevokeUserSessions :many
UPDATE user_sessions SET revoked_at = @now::timestamptz, revoked_reason = @reason
WHERE user_id = @user_id AND revoked_at IS NULL
RETURNING id;

-- name: AdminGetUserContact :one
SELECT full_name, phone_number, ward_code, address_detail FROM users WHERE id = $1 AND deleted_at IS NULL;
