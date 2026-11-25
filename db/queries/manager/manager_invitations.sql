-- name: InsertManagerInvitation :one
INSERT INTO manager_invitations (landlord_id, token_hash, link_token_sealed, expires_at, notify_landlord, recipient_id, recipient_email)
VALUES (@landlord_id, @token_hash, @link_token_sealed, @expires_at, @notify_landlord, @recipient_id, @recipient_email)
RETURNING id;

-- name: InsertManagerInvitationRooms :exec
INSERT INTO manager_invitation_rooms (invitation_id, room_id)
SELECT @invitation_id::uuid, unnest(@room_ids::uuid[]);

-- name: InsertManagerInvitationCapabilities :exec
INSERT INTO manager_invitation_capabilities (invitation_id, capability)
SELECT @invitation_id::uuid, unnest(@capabilities::text[]);

-- name: SearchManagerInvitations :many
-- The landlord's invitations with the people named. Open ones first, then the newest. A NULL id or status is off.
SELECT sqlc.embed(i),
       rc.full_name AS recipient_full_name, rc.email AS recipient_account_email
FROM manager_invitations i
LEFT JOIN users rc ON rc.id = i.recipient_id
WHERE i.landlord_id = @landlord_id
  AND (sqlc.narg('id')::uuid IS NULL OR i.id = sqlc.narg('id')::uuid)
  AND (sqlc.narg('status')::text IS NULL OR i.status = sqlc.narg('status')::text)
ORDER BY CASE i.status WHEN 'pending' THEN 0 ELSE 1 END, i.created_at DESC, i.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountManagerInvitations :one
SELECT count(*)::integer
FROM manager_invitations i
WHERE i.landlord_id = @landlord_id
  AND (sqlc.narg('status')::text IS NULL OR i.status = sqlc.narg('status')::text);

-- name: LockManagerInvitation :one
SELECT * FROM manager_invitations WHERE id = @id FOR UPDATE;

-- name: SaveManagerInvitation :exec
UPDATE manager_invitations
SET status = @status, token_hash = @token_hash, link_token_sealed = @link_token_sealed, expires_at = @expires_at,
    answered_by_id = @answered_by_id, answered_at = @answered_at, updated_at = now()
WHERE id = @id;

-- name: FindManagerInvitationByTokenHash :one
SELECT * FROM manager_invitations WHERE token_hash = @token_hash;

-- name: FindManagerInvitationForRecipient :one
SELECT * FROM manager_invitations WHERE id = @id AND recipient_id = @recipient_id;

-- name: GetManagerInvitationLandlordName :one
SELECT l.full_name FROM manager_invitations i JOIN users l ON l.id = i.landlord_id WHERE i.id = @id;

-- name: ListManagerInvitationInbox :many
-- The open invitations addressed to an account, newest first.
SELECT sqlc.embed(i), l.full_name AS landlord_name
FROM manager_invitations i
JOIN users l ON l.id = i.landlord_id
WHERE i.recipient_id = @recipient_id AND i.status = 'pending' AND i.expires_at > @now
ORDER BY i.created_at DESC, i.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountManagerInvitationInbox :one
SELECT count(*)::integer FROM manager_invitations i
WHERE i.recipient_id = @recipient_id AND i.status = 'pending' AND i.expires_at > @now;

-- name: ListManagerInvitationRooms :many
-- The rooms of some invitations, with the room names the landlord knows.
SELECT mr.invitation_id, r.id AS room_id, r.name AS room_name, b.name AS building_name
FROM manager_invitation_rooms mr
JOIN rooms r ON r.id = mr.room_id
LEFT JOIN buildings b ON b.id = r.building_id
WHERE mr.invitation_id = ANY(@invitation_ids::uuid[])
ORDER BY mr.invitation_id, lower(r.name), r.id;

-- name: ListManagerInvitationCapabilities :many
SELECT invitation_id, capability FROM manager_invitation_capabilities
WHERE invitation_id = ANY(@invitation_ids::uuid[]) ORDER BY invitation_id, capability;

-- name: LockOpenManagerInvitationsOfRooms :many
-- The invitations still waiting (not expired) that hold any of the rooms, locked in a fixed order.
SELECT i.* FROM manager_invitations i
WHERE i.status = 'pending' AND i.expires_at > @now
  AND EXISTS (SELECT 1 FROM manager_invitation_rooms mr WHERE mr.invitation_id = i.id AND mr.room_id = ANY(@room_ids::uuid[]))
ORDER BY i.id
FOR UPDATE OF i;

-- name: DeleteManagerInvitationRooms :exec
DELETE FROM manager_invitation_rooms WHERE invitation_id = @invitation_id AND room_id = ANY(@room_ids::uuid[]);

-- name: CountManagerInvitationRooms :one
SELECT count(*)::integer FROM manager_invitation_rooms WHERE invitation_id = @invitation_id;

-- name: RevokeOpenManagerInvitationsOfRooms :many
-- Withdraws the waiting invitations of rooms that are gone (the secret is wiped with the status).
UPDATE manager_invitations
SET status = 'revoked', link_token_sealed = NULL, answered_at = @at, updated_at = now()
WHERE status = 'pending'
  AND id IN (SELECT mr.invitation_id FROM manager_invitation_rooms mr WHERE mr.room_id = ANY(@room_ids::uuid[]))
RETURNING *;

-- name: GetManagerInvitationByID :one
SELECT * FROM manager_invitations WHERE id = @id;
