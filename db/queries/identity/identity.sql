-- Queries of the catalog seed (roles, permissions and which role has which permission).

-- name: UpsertRole :exec
INSERT INTO roles (code, seniority, is_staff)
VALUES ($1, $2, $3)
ON CONFLICT (code) DO UPDATE
SET seniority = EXCLUDED.seniority,
    is_staff = EXCLUDED.is_staff;

-- name: UpsertPermission :exec
INSERT INTO permissions (code, description)
VALUES ($1, $2)
ON CONFLICT (code) DO UPDATE
SET description = EXCLUDED.description;

-- name: DeleteStalePermissions :exec
DELETE FROM permissions WHERE code <> ALL(@keep::text[]);

-- name: DeleteRolePermissions :exec
DELETE FROM role_permissions WHERE role_code = $1;

-- name: InsertRolePermissions :exec
INSERT INTO role_permissions (role_code, permission_code)
SELECT @role_code::text, unnest(@permission_codes::text[]);

-- name: ListRoles :many
SELECT code, seniority, is_staff FROM roles ORDER BY seniority;

-- name: ListPermissions :many
SELECT code, description FROM permissions ORDER BY code;

-- name: ListRolePermissions :many
SELECT role_code, permission_code FROM role_permissions ORDER BY role_code, permission_code;
