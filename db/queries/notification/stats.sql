-- name: StatsCohortMessages :one
-- The messages a sender put out (and did not recall) within [@from_at, @to_at): the platform's, or a landlord's.
SELECT count(*) AS total,
       count(*) FILTER (WHERE send_notification) AS with_notification,
       count(*) FILTER (WHERE show_popup) AS with_popup,
       COALESCE(sum(recipient_count) FILTER (WHERE send_notification), 0)::bigint AS recipients
FROM announcements a
WHERE a.published_at >= @from_at::timestamptz AND a.published_at < @to_at::timestamptz AND a.recalled_at IS NULL
  AND CASE WHEN sqlc.narg(landlord_id)::uuid IS NULL THEN a.sender_role <> 'landlord' ELSE a.landlord_id = sqlc.narg(landlord_id)::uuid END;

-- name: StatsNotificationRead :one
-- How many recipients read the notifications of the scope: one message (@announcement_id), or the cohort of a sender.
SELECT count(*) FROM notifications n
WHERE n.read_at IS NOT NULL AND n.announcement_id IN (
    SELECT a.id FROM announcements a
    WHERE CASE WHEN sqlc.narg(announcement_id)::uuid IS NOT NULL THEN a.id = sqlc.narg(announcement_id)::uuid
          ELSE a.published_at >= @from_at::timestamptz AND a.published_at < @to_at::timestamptz AND a.recalled_at IS NULL
               AND CASE WHEN sqlc.narg(landlord_id)::uuid IS NULL THEN a.sender_role <> 'landlord' ELSE a.landlord_id = sqlc.narg(landlord_id)::uuid END
          END);

-- name: StatsPopupTotals :one
-- What people did with the popups of the scope (same scope as StatsNotificationRead).
SELECT count(*) AS viewers, count(i.dismissed_at) AS closed, count(i.clicked_at) AS clicked,
       COALESCE(sum(i.view_count), 0)::bigint AS views
FROM announcement_impressions i
WHERE i.announcement_id IN (
    SELECT a.id FROM announcements a
    WHERE CASE WHEN sqlc.narg(announcement_id)::uuid IS NOT NULL THEN a.id = sqlc.narg(announcement_id)::uuid
          ELSE a.published_at >= @from_at::timestamptz AND a.published_at < @to_at::timestamptz AND a.recalled_at IS NULL
               AND CASE WHEN sqlc.narg(landlord_id)::uuid IS NULL THEN a.sender_role <> 'landlord' ELSE a.landlord_id = sqlc.narg(landlord_id)::uuid END
          END);

-- name: StatsDailyEvents :many
-- Per day (ICT) from @first_day to @last_day: people who first saw the popup, closed it, pressed its button, and
-- notifications read, over every message the sender put out (or the one message). A channel that is not asked for is 0.
WITH scope AS (
    SELECT a.id FROM announcements a
    WHERE CASE WHEN sqlc.narg(announcement_id)::uuid IS NOT NULL THEN a.id = sqlc.narg(announcement_id)::uuid
          ELSE a.published_at IS NOT NULL AND a.recalled_at IS NULL
               AND CASE WHEN sqlc.narg(landlord_id)::uuid IS NULL THEN a.sender_role <> 'landlord' ELSE a.landlord_id = sqlc.narg(landlord_id)::uuid END
          END
), seen AS (
    SELECT (i.first_seen_at AT TIME ZONE 'Asia/Ho_Chi_Minh')::date AS day, count(*) AS total FROM announcement_impressions i
    WHERE @with_popup::boolean AND i.announcement_id IN (SELECT id FROM scope)
      AND i.first_seen_at >= @from_at::timestamptz AND i.first_seen_at < @to_at::timestamptz GROUP BY 1
), closed AS (
    SELECT (i.dismissed_at AT TIME ZONE 'Asia/Ho_Chi_Minh')::date AS day, count(*) AS total FROM announcement_impressions i
    WHERE @with_popup::boolean AND i.announcement_id IN (SELECT id FROM scope)
      AND i.dismissed_at >= @from_at::timestamptz AND i.dismissed_at < @to_at::timestamptz GROUP BY 1
), clicked AS (
    SELECT (i.clicked_at AT TIME ZONE 'Asia/Ho_Chi_Minh')::date AS day, count(*) AS total FROM announcement_impressions i
    WHERE @with_popup::boolean AND i.announcement_id IN (SELECT id FROM scope)
      AND i.clicked_at >= @from_at::timestamptz AND i.clicked_at < @to_at::timestamptz GROUP BY 1
), reads AS (
    SELECT (n.read_at AT TIME ZONE 'Asia/Ho_Chi_Minh')::date AS day, count(*) AS total FROM notifications n
    WHERE @with_notification::boolean AND n.announcement_id IN (SELECT id FROM scope)
      AND n.read_at >= @from_at::timestamptz AND n.read_at < @to_at::timestamptz GROUP BY 1
)
SELECT d.day::date AS day,
       COALESCE(seen.total, 0)::bigint AS viewers, COALESCE(closed.total, 0)::bigint AS closed,
       COALESCE(clicked.total, 0)::bigint AS clicked, COALESCE(reads.total, 0)::bigint AS read
FROM generate_series(@first_day::date, @last_day::date, interval '1 day') AS d(day)
LEFT JOIN seen ON seen.day = d.day::date LEFT JOIN closed ON closed.day = d.day::date
LEFT JOIN clicked ON clicked.day = d.day::date LEFT JOIN reads ON reads.day = d.day::date
ORDER BY d.day;
