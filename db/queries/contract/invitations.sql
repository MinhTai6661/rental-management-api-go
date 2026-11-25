-- name: InsertContractInvitation :one
INSERT INTO contract_invitations (room_id, landlord_id, created_by_id, status, token_hash, link_token_sealed, expires_at,
                                  price_vnd, deposit_vnd, occupant_count, starts_on, ends_on, contract_status, note,
                                  recipient_id, recipient_email)
VALUES (@room_id, @landlord_id, @created_by_id, @status, @token_hash, @link_token_sealed, @expires_at,
        @price_vnd, @deposit_vnd, @occupant_count, @starts_on, @ends_on, @contract_status, @note,
        @recipient_id, @recipient_email)
RETURNING id;

-- name: SearchContractInvitations :many
-- The landlord's invitations with the room, the building, the landlord's name and the people named. Open ones first,
-- then the newest. A NULL id or status is off.
SELECT sqlc.embed(i),
       r.name AS room_name, b.name AS building_name, l.full_name AS landlord_name,
       rc.full_name AS recipient_full_name, rc.email AS recipient_account_email,
       ab.full_name AS answered_by_full_name, ab.email AS answered_by_email
FROM contract_invitations i
JOIN rooms r ON r.id = i.room_id
JOIN users l ON l.id = i.landlord_id
LEFT JOIN buildings b ON b.id = r.building_id
LEFT JOIN users rc ON rc.id = i.recipient_id
LEFT JOIN users ab ON ab.id = i.answered_by_id
WHERE (sqlc.narg('landlord_id')::uuid IS NULL OR i.landlord_id = sqlc.narg('landlord_id')::uuid)
  -- A manager sees only what they wrote, and only while they still manage the room (the repository sets exactly one
  -- of landlord_id and author_id).
  AND (sqlc.narg('author_id')::uuid IS NULL OR (i.created_by_id = sqlc.narg('author_id')::uuid AND EXISTS (
        SELECT 1 FROM room_managers rm WHERE rm.room_id = i.room_id AND rm.manager_id = sqlc.narg('author_id')::uuid AND rm.status = 'active')))
  AND (sqlc.narg('id')::uuid IS NULL OR i.id = sqlc.narg('id')::uuid)
  AND (sqlc.narg('status')::text IS NULL OR i.status = sqlc.narg('status')::text)
ORDER BY CASE i.status WHEN 'awaiting_approval' THEN 0 WHEN 'pending' THEN 1 ELSE 2 END, i.created_at DESC, i.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountContractInvitations :one
SELECT count(*)::integer
FROM contract_invitations i
WHERE (sqlc.narg('landlord_id')::uuid IS NULL OR i.landlord_id = sqlc.narg('landlord_id')::uuid)
  AND (sqlc.narg('author_id')::uuid IS NULL OR (i.created_by_id = sqlc.narg('author_id')::uuid AND EXISTS (
        SELECT 1 FROM room_managers rm WHERE rm.room_id = i.room_id AND rm.manager_id = sqlc.narg('author_id')::uuid AND rm.status = 'active')))
  AND (sqlc.narg('status')::text IS NULL OR i.status = sqlc.narg('status')::text);

-- name: LockContractInvitation :one
SELECT * FROM contract_invitations WHERE id = @id FOR UPDATE;

-- name: SaveContractInvitation :exec
-- The fields an invitation's life changes: its answer, its link secret and its end.
UPDATE contract_invitations
SET status = @status, token_hash = @token_hash, link_token_sealed = @link_token_sealed, expires_at = @expires_at,
    answered_by_id = @answered_by_id, answered_at = @answered_at, contract_id = @contract_id,
    approved_by_id = @approved_by_id, approved_at = @approved_at, approval_note = @approval_note, updated_at = now()
WHERE id = @id;

-- name: FindContractInvitationByTokenHash :one
-- The invitation a link opens (any status: the use case decides what can still be done with it).
SELECT * FROM contract_invitations WHERE token_hash = @token_hash;

-- name: FindContractInvitationForRecipient :one
-- The invitation with that id when it is addressed to the account (an offer to an address or to anybody has no
-- recipient id, so it is only reached by its link).
SELECT * FROM contract_invitations WHERE id = @id AND recipient_id = @recipient_id;

-- name: GetContractInvitationOffer :one
-- An invitation as the invited person sees it: the room with its address, the building and the landlord.
SELECT sqlc.embed(i),
       r.name AS room_name, r.status AS room_status, r.price_vnd AS room_price_vnd,
       r.minimum_rental_period AS room_minimum_rental_period, r.rental_period_unit AS room_rental_period_unit,
       r.address_detail AS room_address_detail, r.description AS room_description,
       r.area_square_meters AS room_area_square_meters, r.cover_key AS room_cover_key,
       r.cover_thumb_key AS room_cover_thumb_key, r.building_id AS room_building_id,
       b.name AS building_name,
       r.ward_code AS ward_code, w.name AS ward_name, w.province_code AS province_code, p.name AS province_name,
       l.full_name AS landlord_full_name
FROM contract_invitations i
JOIN rooms r ON r.id = i.room_id
JOIN users l ON l.id = i.landlord_id
LEFT JOIN buildings b ON b.id = r.building_id
LEFT JOIN wards w ON w.code = r.ward_code
LEFT JOIN provinces p ON p.code = w.province_code
WHERE i.id = @id;

-- name: ListContractInvitationInbox :many
-- The open offers addressed to an account, newest first.
SELECT sqlc.embed(i),
       r.name AS room_name, r.status AS room_status, r.price_vnd AS room_price_vnd,
       r.minimum_rental_period AS room_minimum_rental_period, r.rental_period_unit AS room_rental_period_unit,
       r.address_detail AS room_address_detail, r.description AS room_description,
       r.area_square_meters AS room_area_square_meters, r.cover_key AS room_cover_key,
       r.cover_thumb_key AS room_cover_thumb_key, r.building_id AS room_building_id,
       b.name AS building_name,
       r.ward_code AS ward_code, w.name AS ward_name, w.province_code AS province_code, p.name AS province_name,
       l.full_name AS landlord_full_name
FROM contract_invitations i
JOIN rooms r ON r.id = i.room_id AND r.deleted_at IS NULL
JOIN users l ON l.id = i.landlord_id
LEFT JOIN buildings b ON b.id = r.building_id
LEFT JOIN wards w ON w.code = r.ward_code
LEFT JOIN provinces p ON p.code = w.province_code
WHERE i.recipient_id = @recipient_id AND i.status = 'pending' AND i.expires_at > @now
ORDER BY i.created_at DESC, i.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountContractInvitationInbox :one
SELECT count(*)::integer
FROM contract_invitations i
JOIN rooms r ON r.id = i.room_id AND r.deleted_at IS NULL
WHERE i.recipient_id = @recipient_id AND i.status = 'pending' AND i.expires_at > @now;

-- name: RevokeOpenContractInvitationsOfRooms :many
-- Withdraws the offers of rooms that are gone: the secret is wiped with the status.
UPDATE contract_invitations
SET status = 'revoked', link_token_sealed = NULL, answered_at = @at, updated_at = now()
WHERE room_id = ANY(@room_ids::uuid[]) AND status IN ('pending', 'awaiting_approval')
RETURNING *;

-- name: RevokeAwaitingContractInvitationsOfAuthor :many
-- Withdraws the offers a manager wrote that still wait for the landlord (the manager lost their rooms).
UPDATE contract_invitations
SET status = 'revoked', link_token_sealed = NULL, answered_at = @at, updated_at = now()
WHERE created_by_id = @author_id AND status = 'awaiting_approval'
RETURNING *;
