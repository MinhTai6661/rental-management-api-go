-- The accounts an invitation can name, read-only (the identity context owns users), and the addresses that asked
-- for no more invitation emails.

-- name: GetInviteeByID :one
SELECT u.id, u.full_name, u.email, u.email_verified_at, u.status, u.accepts_invitations,
       array(SELECT ur.role_code FROM user_roles ur WHERE ur.user_id = u.id ORDER BY ur.role_code)::text[] AS roles
FROM users u
WHERE u.id = @id;

-- name: GetInviteeByEmail :one
-- Addresses are stored in lower case: the caller passes a normalized one.
SELECT u.id, u.full_name, u.email, u.email_verified_at, u.status, u.accepts_invitations,
       array(SELECT ur.role_code FROM user_roles ur WHERE ur.user_id = u.id ORDER BY ur.role_code)::text[] AS roles
FROM users u
WHERE u.email = @email::text;

-- name: InviteeEmailOptedOut :one
SELECT EXISTS (SELECT 1 FROM users WHERE email = @email::text AND NOT accepts_invitations);

-- name: AddInvitationEmailBlock :exec
INSERT INTO invitation_email_blocks (email_hash) VALUES (@email_hash::text)
ON CONFLICT (email_hash) DO NOTHING;

-- name: InvitationEmailBlockExists :one
SELECT EXISTS (SELECT 1 FROM invitation_email_blocks WHERE email_hash = @email_hash::text);
