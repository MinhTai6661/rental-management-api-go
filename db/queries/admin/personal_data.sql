-- Queries of the data subject's rights: export and erasure.

-- name: AdminErasureBlockers :one
SELECT
  EXISTS (SELECT 1 FROM contracts ct WHERE ct.tenant_id = @user_id AND ct.status IN ('active', 'pending') AND ct.deleted_at IS NULL) AS as_tenant,
  EXISTS (SELECT 1 FROM contracts cl WHERE cl.landlord_id = @user_id AND cl.status IN ('active', 'pending') AND cl.deleted_at IS NULL) AS as_landlord;

-- name: AdminExportAccount :one
SELECT u.id, u.email, u.full_name, u.phone_number, u.status, u.address_detail, u.ward_code, u.terms_version,
       u.terms_accepted_at, u.accepts_invitations, u.created_at,
       COALESCE((SELECT array_agg(ur.role_code ORDER BY r.seniority) FROM user_roles ur JOIN roles r ON r.code = ur.role_code
                 WHERE ur.user_id = u.id), '{}')::text[] AS roles
FROM users u WHERE u.id = $1 AND u.deleted_at IS NULL;

-- name: AdminExportRooms :many
SELECT id, name, address_detail, price_vnd, status FROM rooms WHERE landlord_id = $1 AND deleted_at IS NULL ORDER BY created_at, id LIMIT 1000;

-- name: AdminExportRenterContracts :many
SELECT c.id, c.room_id, r.name AS room_name, c.status, c.price_vnd, c.starts_on, c.ends_on
FROM contracts c JOIN rooms r ON r.id = c.room_id
WHERE c.tenant_id = $1 AND c.deleted_at IS NULL ORDER BY c.created_at, c.id LIMIT 1000;

-- name: AdminExportPayments :many
SELECT id, invoice_id, method, amount_vnd, status, note, paid_at, created_at FROM payments WHERE payer_id = $1 ORDER BY created_at, id LIMIT 1000;

-- name: AdminRevokeOpenInvitationsOfLandlord :exec
UPDATE contract_invitations
SET status = 'revoked', answered_at = @now::timestamptz, link_token_sealed = NULL, updated_at = @now::timestamptz
WHERE landlord_id = @user_id AND status IN ('pending', 'awaiting_approval');

-- name: AdminCancelOpenSubscriptions :exec
UPDATE subscriptions
SET status = 'canceled', canceled_at = @now::timestamptz, ends_at = @now::timestamptz, updated_at = @now::timestamptz
WHERE user_id = @user_id AND status IN ('active', 'pending');

-- name: AdminDeleteLandlordPaymentMethods :exec
DELETE FROM landlord_payment_methods WHERE landlord_id = $1;

-- name: AdminDeleteLandlordApplicationsOfUser :exec
DELETE FROM landlord_applications WHERE user_id = $1;

-- name: AdminDeleteLandlordProfile :exec
DELETE FROM landlord_profiles WHERE user_id = $1;

-- name: AdminDeleteNotificationsOfUser :exec
DELETE FROM notifications WHERE user_id = $1;

-- name: AdminAnonymizeUser :exec
UPDATE users
SET full_name = @display_name, email = NULL, phone_number = NULL, address_detail = NULL, ward_code = NULL,
    password_hash = NULL, status = 'inactive', accepts_invitations = false, last_active_role = NULL,
    erased_at = @now::timestamptz, deleted_at = @now::timestamptz, updated_at = @now::timestamptz
WHERE id = @id;
