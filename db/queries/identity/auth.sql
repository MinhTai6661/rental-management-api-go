-- Queries of accounts, login sessions, refresh tokens and email verification.

-- name: CreateUser :one
INSERT INTO users (id, email, password_hash, full_name, phone_number, ward_code, address_detail,
                   last_active_role, terms_version, terms_accepted_at, email_verified_at, created_at, updated_at)
VALUES (@id, @email, @password_hash, @full_name, @phone_number, @ward_code, @address_detail,
        @last_active_role, @terms_version, @terms_accepted_at, @email_verified_at, @now, @now)
RETURNING id;

-- name: UpsertDemoUser :one
-- The demo accounts are recreated safely: a second seed refreshes the password and keeps the account.
INSERT INTO users (id, email, password_hash, full_name, ward_code, last_active_role,
                   terms_version, terms_accepted_at, email_verified_at, created_at, updated_at)
VALUES (@id, @email, @password_hash, @full_name, @ward_code, @last_active_role,
        @terms_version, @now::timestamptz, @now::timestamptz, @now::timestamptz, @now::timestamptz)
ON CONFLICT (email) DO UPDATE
SET password_hash = EXCLUDED.password_hash,
    full_name = EXCLUDED.full_name,
    ward_code = EXCLUDED.ward_code,
    last_active_role = EXCLUDED.last_active_role,
    status = 'active',
    email_verified_at = COALESCE(users.email_verified_at, EXCLUDED.email_verified_at),
    updated_at = EXCLUDED.updated_at
RETURNING id;

-- name: FirstWardCode :one
SELECT code FROM wards ORDER BY code LIMIT 1;

-- name: GetUserByEmail :one
SELECT id, email, password_hash, status, last_active_role, email_verified_at, full_name
FROM users WHERE email = @email::text;

-- name: GetUserProfile :one
-- The account with its ward and province, for the user object of the API.
SELECT u.id, u.email, u.full_name, u.phone_number, u.status, u.address_detail, u.last_active_role,
       u.terms_version, u.created_at,
       w.code AS ward_code, w.name AS ward_name,
       p.code AS province_code, p.name AS province_name
FROM users u
LEFT JOIN wards w ON w.code = u.ward_code
LEFT JOIN provinces p ON p.code = w.province_code
WHERE u.id = $1;

-- name: GetAuthUser :one
-- Everything a request needs to know about its caller, in one query.
SELECT u.status, u.last_active_role,
       COALESCE(array_agg(ur.role_code ORDER BY r.seniority) FILTER (WHERE ur.role_code IS NOT NULL), '{}')::text[] AS roles
FROM users u
LEFT JOIN user_roles ur ON ur.user_id = u.id
LEFT JOIN roles r ON r.code = ur.role_code
WHERE u.id = $1
GROUP BY u.id;

-- name: GetPasswordHash :one
SELECT password_hash FROM users WHERE id = $1;

-- name: UpdatePassword :exec
UPDATE users SET password_hash = @password_hash, updated_at = @now WHERE id = @id;

-- name: SetLastActiveRole :exec
UPDATE users SET last_active_role = @role, updated_at = @now WHERE id = @id;

-- name: MarkEmailVerified :exec
UPDATE users SET email_verified_at = COALESCE(email_verified_at, @now::timestamptz), updated_at = @now::timestamptz WHERE id = @id;

-- name: AddUserRole :exec
INSERT INTO user_roles (user_id, role_code, assigned_at)
VALUES (@user_id, @role_code, @now)
ON CONFLICT (user_id, role_code) DO NOTHING;

-- name: ListPermissionCodesOfRole :many
SELECT permission_code FROM role_permissions WHERE role_code = $1 ORDER BY permission_code;

-- name: CreateSession :exec
INSERT INTO user_sessions (id, user_id, active_role, user_agent, ip_address, created_at, last_used_at)
VALUES (@id, @user_id, @active_role, @user_agent, @ip_address, @now, @now);

-- name: RevokeSession :exec
UPDATE user_sessions SET revoked_at = @now::timestamptz, revoked_reason = @reason
WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeUserSessions :many
-- Closes every open session of the user, except the one named (use a null except_id to close them all).
UPDATE user_sessions SET revoked_at = @now::timestamptz, revoked_reason = @reason
WHERE user_id = @user_id AND revoked_at IS NULL
  AND (sqlc.narg('except_id')::uuid IS NULL OR id <> sqlc.narg('except_id')::uuid)
RETURNING id;

-- name: TouchSession :exec
UPDATE user_sessions
SET last_used_at = @now, active_role = @active_role, user_agent = @user_agent, ip_address = @ip_address
WHERE id = @id;

-- name: InsertRefreshToken :exec
INSERT INTO refresh_tokens (id, session_id, token_hash, parent_id, expires_at, created_at)
VALUES (@id, @session_id, @token_hash, @parent_id, @expires_at, @now);

-- name: GetRefreshTokenForUpdate :one
-- Locks the token row so two requests with the same token are handled one after the other.
SELECT rt.id, rt.session_id, rt.expires_at, rt.rotated_at,
       s.user_id, s.active_role, s.revoked_at AS session_revoked_at
FROM refresh_tokens rt
JOIN user_sessions s ON s.id = rt.session_id
WHERE rt.token_hash = $1
FOR UPDATE OF rt;

-- name: MarkRefreshTokenRotated :exec
UPDATE refresh_tokens SET rotated_at = @now::timestamptz WHERE id = @id;

-- name: GetRefreshTokenSession :one
SELECT s.id AS session_id, s.user_id, s.revoked_at
FROM refresh_tokens rt
JOIN user_sessions s ON s.id = rt.session_id
WHERE rt.token_hash = $1;

-- name: InsertVerificationToken :exec
INSERT INTO email_verification_tokens (id, user_id, token_hash, expires_at, created_at)
VALUES (@id, @user_id, @token_hash, @expires_at, @now);

-- name: InvalidateVerificationTokens :exec
UPDATE email_verification_tokens SET used_at = @now::timestamptz WHERE user_id = @user_id AND used_at IS NULL;

-- name: GetVerificationTokenForUpdate :one
SELECT id, user_id, expires_at, used_at FROM email_verification_tokens WHERE token_hash = $1 FOR UPDATE;

-- name: MarkVerificationTokenUsed :exec
UPDATE email_verification_tokens SET used_at = @now::timestamptz WHERE id = @id;

-- name: LatestVerificationTokenAt :one
SELECT created_at FROM email_verification_tokens WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1;
