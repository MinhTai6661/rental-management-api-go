-- Queries of landlord applications, landlord profiles and the impact of taking the landlord role away.

-- name: AdminInsertLandlordApplication :one
INSERT INTO landlord_applications (id, user_id, business_name, phone, ward_code, address_detail, note, created_at, updated_at)
VALUES (@id, @user_id, @business_name, @phone, @ward_code, @address_detail, @note, @now, @now)
RETURNING id;

-- name: AdminGetLandlordApplication :one
SELECT a.id, a.user_id, a.business_name, a.phone, a.ward_code, a.address_detail, a.note, a.status,
       a.reviewed_by_id, a.reviewed_at, a.review_note, a.created_at, u.full_name AS applicant_name, u.email AS applicant_email
FROM landlord_applications a JOIN users u ON u.id = a.user_id
WHERE a.id = $1;

-- name: AdminLockLandlordApplication :one
SELECT a.id, a.user_id, a.business_name, a.phone, a.ward_code, a.address_detail, a.note, a.status,
       a.reviewed_by_id, a.reviewed_at, a.review_note, a.created_at, u.full_name AS applicant_name, u.email AS applicant_email
FROM landlord_applications a JOIN users u ON u.id = a.user_id
WHERE a.id = $1
FOR UPDATE OF a;

-- name: AdminLatestLandlordApplicationOfUser :one
SELECT a.id, a.user_id, a.business_name, a.phone, a.ward_code, a.address_detail, a.note, a.status,
       a.reviewed_by_id, a.reviewed_at, a.review_note, a.created_at, u.full_name AS applicant_name, u.email AS applicant_email
FROM landlord_applications a JOIN users u ON u.id = a.user_id
WHERE a.user_id = $1
ORDER BY a.created_at DESC, a.id DESC
LIMIT 1;

-- name: AdminHasOpenLandlordApplication :one
SELECT EXISTS (SELECT 1 FROM landlord_applications WHERE user_id = $1 AND status = 'pending_review');

-- name: AdminDeleteOpenLandlordApplication :execrows
DELETE FROM landlord_applications WHERE user_id = $1 AND status = 'pending_review';

-- name: AdminListLandlordApplications :many
SELECT a.id, a.user_id, a.business_name, a.phone, a.ward_code, a.address_detail, a.note, a.status,
       a.reviewed_by_id, a.reviewed_at, a.review_note, a.created_at, u.full_name AS applicant_name, u.email AS applicant_email
FROM landlord_applications a JOIN users u ON u.id = a.user_id
WHERE a.status = @status
  AND (sqlc.narg('search')::text IS NULL OR lower(a.business_name) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\'
       OR a.phone LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\'
       OR lower(u.full_name) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\'
       OR lower(COALESCE(u.email, '')) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\')
ORDER BY
  CASE WHEN @sort::text = 'oldest' THEN a.created_at END ASC,
  CASE WHEN @sort::text = 'name' THEN lower(a.business_name) END ASC,
  a.created_at DESC, a.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: AdminCountLandlordApplications :one
SELECT count(*)::bigint
FROM landlord_applications a JOIN users u ON u.id = a.user_id
WHERE a.status = @status
  AND (sqlc.narg('search')::text IS NULL OR lower(a.business_name) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\'
       OR a.phone LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\'
       OR lower(u.full_name) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\'
       OR lower(COALESCE(u.email, '')) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\');

-- name: AdminCountLandlordApplicationsByStatus :many
SELECT status, count(*)::bigint AS total FROM landlord_applications GROUP BY status;

-- name: AdminListLandlordApplicationHistory :many
-- The other applications of the same person, newest first.
SELECT a.id, a.user_id, a.business_name, a.phone, a.ward_code, a.address_detail, a.note, a.status,
       a.reviewed_by_id, a.reviewed_at, a.review_note, a.created_at, ''::text AS applicant_name, NULL::text AS applicant_email
FROM landlord_applications a
WHERE a.user_id = @user_id AND a.id <> @except_id
ORDER BY a.created_at DESC, a.id DESC
LIMIT 50;

-- name: AdminReviewLandlordApplication :exec
UPDATE landlord_applications
SET status = @status, reviewed_by_id = @reviewer_id, reviewed_at = @now::timestamptz, review_note = @review_note,
    updated_at = @now::timestamptz
WHERE id = @id;

-- name: AdminGetLandlordProfile :one
SELECT user_id, business_name, phone, ward_code, address_detail, description, created_at, updated_at
FROM landlord_profiles WHERE user_id = $1;

-- name: AdminUpsertLandlordProfile :exec
INSERT INTO landlord_profiles (user_id, business_name, phone, ward_code, address_detail, description, created_at, updated_at)
VALUES (@user_id, @business_name, @phone, @ward_code, @address_detail, @description, @now, @now)
ON CONFLICT (user_id) DO UPDATE
SET business_name = EXCLUDED.business_name, phone = EXCLUDED.phone, ward_code = EXCLUDED.ward_code,
    address_detail = EXCLUDED.address_detail, description = EXCLUDED.description, updated_at = EXCLUDED.updated_at;

-- name: AdminInsertLandlordProfileIfAbsent :execrows
INSERT INTO landlord_profiles (user_id, business_name, phone, ward_code, address_detail, created_at, updated_at)
VALUES (@user_id, @business_name, @phone, @ward_code, @address_detail, @now, @now)
ON CONFLICT (user_id) DO NOTHING;

-- name: AdminLandlordImpact :one
-- What taking the landlord role away touches: rooms, running contracts, registrations waiting and unpaid bills.
SELECT
  (SELECT count(*) FROM rooms rm WHERE rm.landlord_id = @landlord_id AND rm.deleted_at IS NULL)::bigint AS room_count,
  (SELECT count(*) FROM contracts ca WHERE ca.landlord_id = @landlord_id AND ca.status = 'active' AND ca.deleted_at IS NULL)::bigint AS active_contracts,
  (SELECT count(*) FROM contracts cp WHERE cp.landlord_id = @landlord_id AND cp.status = 'pending' AND cp.deleted_at IS NULL)::bigint AS pending_registrations,
  (SELECT count(*) FROM invoices i JOIN contracts c ON c.id = i.contract_id
    WHERE c.landlord_id = @landlord_id AND i.status = 'sent')::bigint AS unpaid_invoices;
