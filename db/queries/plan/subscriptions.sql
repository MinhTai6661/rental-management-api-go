-- name: InsertSubscription :one
INSERT INTO subscriptions (user_id, plan_id, audience, status, starts_at, ends_at, granted_by_id)
VALUES (@user_id, @plan_id, @audience, @status, @starts_at, @ends_at, @granted_by_id)
RETURNING id;

-- name: GetSubscriptionDetail :one
-- A subscription with its plan and its owner.
SELECT sqlc.embed(s), sqlc.embed(p), u.full_name AS user_full_name, u.email AS user_email
FROM subscriptions s
JOIN plans p ON p.id = s.plan_id
JOIN users u ON u.id = s.user_id
WHERE s.id = @id;

-- name: LockSubscription :one
SELECT id, user_id, plan_id, audience, status, starts_at, ends_at, canceled_at, granted_by_id, created_at
FROM subscriptions
WHERE id = @id
FOR UPDATE;

-- name: GetRunningSubscription :one
-- The subscription that counts at now: active, started and not lapsed.
SELECT sqlc.embed(s), sqlc.embed(p)
FROM subscriptions s
JOIN plans p ON p.id = s.plan_id
WHERE s.user_id = @user_id AND s.audience = @audience AND s.status = 'active'
  AND s.starts_at <= @now AND (s.ends_at IS NULL OR s.ends_at > @now)
ORDER BY s.starts_at DESC
LIMIT 1;

-- name: GetPendingSubscription :one
SELECT sqlc.embed(s), sqlc.embed(p)
FROM subscriptions s
JOIN plans p ON p.id = s.plan_id
WHERE s.user_id = @user_id AND s.audience = @audience AND s.status = 'pending'
ORDER BY s.created_at DESC
LIMIT 1;

-- name: CancelPendingSubscriptions :exec
UPDATE subscriptions SET status = 'canceled', canceled_at = @now, updated_at = now()
WHERE user_id = @user_id AND audience = @audience AND status = 'pending';

-- name: EndOpenSubscriptions :execrows
-- Cancels the active and pending subscriptions of a user (of one audience, or of every one when it is NULL) but one
-- (the nil uuid spares none).
UPDATE subscriptions SET status = 'canceled', canceled_at = @now, ends_at = @now, updated_at = now()
WHERE user_id = @user_id AND status IN ('active', 'pending') AND id <> @except_id
  AND (sqlc.narg('audience')::text IS NULL OR audience = sqlc.narg('audience')::text);

-- name: ActivateSubscription :exec
UPDATE subscriptions SET status = 'active', starts_at = @starts_at, ends_at = @ends_at, granted_by_id = @granted_by_id,
                         updated_at = now()
WHERE id = @id AND status = 'pending';

-- name: SearchSubscriptions :many
-- A page of subscriptions, newest first. A NULL filter is off.
SELECT sqlc.embed(s), sqlc.embed(p), u.full_name AS user_full_name, u.email AS user_email
FROM subscriptions s
JOIN plans p ON p.id = s.plan_id
JOIN users u ON u.id = s.user_id
WHERE (sqlc.narg('status')::text IS NULL OR s.status = sqlc.narg('status')::text)
  AND (sqlc.narg('audience')::text IS NULL OR s.audience = sqlc.narg('audience')::text)
  AND (sqlc.narg('user_id')::uuid IS NULL OR s.user_id = sqlc.narg('user_id')::uuid)
ORDER BY s.created_at DESC, s.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountSubscriptions :one
SELECT count(*)::integer
FROM subscriptions s
WHERE (sqlc.narg('status')::text IS NULL OR s.status = sqlc.narg('status')::text)
  AND (sqlc.narg('audience')::text IS NULL OR s.audience = sqlc.narg('audience')::text)
  AND (sqlc.narg('user_id')::uuid IS NULL OR s.user_id = sqlc.narg('user_id')::uuid);

-- name: ExpireDueSubscriptions :many
-- Marks up to batch_size active subscriptions past their end as expired. SKIP LOCKED lets two runs at once share the work.
UPDATE subscriptions SET status = 'expired', updated_at = now()
WHERE id IN (
    SELECT due.id FROM subscriptions due
    WHERE due.status = 'active' AND due.ends_at IS NOT NULL AND due.ends_at <= @now
    ORDER BY due.ends_at, due.id
    LIMIT @batch_size
    FOR UPDATE SKIP LOCKED
)
RETURNING id;

-- name: ListRunningPriorities :many
-- The search priority of the plan each of the users is running on the audience's side at now (users with no running
-- subscription are left out: they are on the default plan).
SELECT DISTINCT ON (s.user_id) s.user_id, p.search_priority
FROM subscriptions s
JOIN plans p ON p.id = s.plan_id
WHERE s.user_id = ANY(@user_ids::uuid[]) AND s.audience = @audience AND s.status = 'active'
  AND s.starts_at <= @now AND (s.ends_at IS NULL OR s.ends_at > @now)
ORDER BY s.user_id, s.starts_at DESC;
