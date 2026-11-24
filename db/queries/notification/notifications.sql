-- name: InsertNotifications :many
-- One message per user in one statement; returns the ids of the new rows.
INSERT INTO notifications (user_id, kind, params, url, audience_role)
SELECT unnest(@user_ids::uuid[]), @kind::text, @params::jsonb, sqlc.narg(url)::text, sqlc.narg(audience_role)::text
RETURNING id;

-- name: ListNotifications :many
-- @filtered keeps only the messages for @audience and the ones for every side (audience_role NULL).
SELECT id, user_id, kind, params, url, audience_role, read_at, created_at, announcement_id
FROM notifications
WHERE user_id = @user_id
  AND (NOT @filtered::bool OR audience_role IS NULL OR audience_role = @audience::text)
  AND (NOT @unread_only::bool OR read_at IS NULL)
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
ORDER BY created_at DESC, id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountUnreadNotifications :one
SELECT count(*)
FROM notifications
WHERE user_id = @user_id
  AND read_at IS NULL
  AND (NOT @filtered::bool OR audience_role IS NULL OR audience_role = @audience::text);

-- name: MarkNotificationRead :one
-- The first read time is kept when the message was already read.
UPDATE notifications
SET read_at = COALESCE(read_at, @read_at)
WHERE id = @id AND user_id = @user_id
RETURNING id, user_id, kind, params, url, audience_role, read_at, created_at, announcement_id;

-- name: MarkAllNotificationsRead :execrows
UPDATE notifications
SET read_at = @read_at
WHERE user_id = @user_id
  AND read_at IS NULL
  AND (NOT @filtered::bool OR audience_role IS NULL OR audience_role = @audience::text);

-- name: MarkNotificationsRead :execrows
UPDATE notifications
SET read_at = @read_at
WHERE user_id = @user_id AND read_at IS NULL AND id = ANY(@ids::uuid[]);

-- name: DeleteNotification :execrows
DELETE FROM notifications WHERE id = @id AND user_id = @user_id;

-- name: DeleteNotifications :execrows
DELETE FROM notifications WHERE user_id = @user_id AND id = ANY(@ids::uuid[]);

-- name: DeleteNotificationsOfUser :execrows
-- Clears a person's inbox of every role; @read_only keeps what is unread.
DELETE FROM notifications WHERE user_id = @user_id AND (NOT @read_only::bool OR read_at IS NOT NULL);

-- name: PruneNotifications :execrows
-- Deletes one batch of the oldest messages older than @before.
DELETE FROM notifications
WHERE id IN (SELECT n.id FROM notifications n WHERE n.created_at < @before ORDER BY n.created_at LIMIT @batch_size);

-- name: ListAdminUserIDs :many
-- The active accounts holding the admin role (super admins are not admins here).
SELECT u.id
FROM users u
JOIN user_roles r ON r.user_id = u.id
WHERE r.role_code = 'admin' AND u.status = 'active'
ORDER BY u.id;

-- name: UserHasNotificationOfKind :one
SELECT EXISTS (SELECT 1 FROM notifications WHERE user_id = @user_id AND kind = @kind::text);
