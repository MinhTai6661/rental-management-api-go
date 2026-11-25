-- Figures of the whole platform for the staff: counts and averages only, nothing about a person.

-- name: StatsUserCounts :one
SELECT count(*)::int AS total,
       count(*) FILTER (WHERE status = 'active')::int AS active,
       count(*) FILTER (WHERE created_at >= @month_start::timestamptz)::int AS new_this_month,
       count(*) FILTER (WHERE created_at >= @previous_start::timestamptz AND created_at < @month_start::timestamptz)::int AS new_previous_month
FROM users
WHERE deleted_at IS NULL;

-- name: StatsUsersByRole :many
-- A person with two roles counts under each.
SELECT ur.role_code, count(*)::int AS users
FROM user_roles ur
JOIN users u ON u.id = ur.user_id AND u.deleted_at IS NULL
GROUP BY ur.role_code;

-- name: StatsApplicationsByStatus :many
SELECT status, count(*)::int AS applications FROM landlord_applications GROUP BY status;

-- name: StatsApplicationReview :one
-- The mean wait (hours) over the latest 200 applications approved recently, and how long the oldest one still
-- waiting has waited (whole days). The counts say whether the two means anything (a mean of nothing is 0 here).
SELECT (SELECT count(*) FROM (SELECT 1 FROM landlord_applications WHERE status = 'approved' AND reviewed_at >= sqlc.arg(since)::timestamptz
                              ORDER BY reviewed_at DESC LIMIT 200) counted)::int AS reviewed,
       COALESCE((SELECT round(avg(extract(epoch FROM (reviewed_at - created_at)) / 3600)::numeric, 1)
                 FROM (SELECT reviewed_at, created_at FROM landlord_applications
                       WHERE status = 'approved' AND reviewed_at >= sqlc.arg(since)::timestamptz
                       ORDER BY reviewed_at DESC LIMIT 200) recent), 0)::float8 AS average_review_hours,
       (SELECT count(*) FROM landlord_applications WHERE status = 'pending_review')::int AS waiting,
       COALESCE((SELECT floor(extract(epoch FROM (sqlc.arg(now)::timestamptz - min(created_at))) / 86400)
                 FROM landlord_applications WHERE status = 'pending_review'), 0)::int AS oldest_waiting_days;

-- name: StatsPendingSubscriptions :one
SELECT count(*)::int FROM subscriptions WHERE status = 'pending';

-- name: StatsAnnouncementsSent :one
SELECT count(*)::int FROM announcements WHERE sent_at >= @month_start::timestamptz AND recalled_at IS NULL;

-- name: StatsAnnouncementReads :one
-- Of the 5 latest announcements that went out and were not recalled: people reached and notifications read.
WITH recent AS (
    SELECT id, recipient_count FROM announcements
    WHERE sent_at IS NOT NULL AND recalled_at IS NULL
    ORDER BY sent_at DESC, id DESC LIMIT 5
)
SELECT COALESCE((SELECT sum(recipient_count) FROM recent), 0)::bigint AS reached,
       (SELECT count(*) FROM notifications n WHERE n.announcement_id IN (SELECT id FROM recent) AND n.read_at IS NOT NULL)::bigint AS read;

-- name: StatsUsersByMonth :many
SELECT to_char(created_at AT TIME ZONE 'Asia/Ho_Chi_Minh', 'YYYY-MM')::text AS month, count(*)::int AS total
FROM users
WHERE deleted_at IS NULL AND created_at >= @from_at::timestamptz AND created_at < @before_at::timestamptz
GROUP BY 1;

-- name: StatsApplicationsByMonth :many
SELECT to_char(created_at AT TIME ZONE 'Asia/Ho_Chi_Minh', 'YYYY-MM')::text AS month, count(*)::int AS total
FROM landlord_applications
WHERE created_at >= @from_at::timestamptz AND created_at < @before_at::timestamptz
GROUP BY 1;

-- name: StatsStaffCounts :one
SELECT count(*) FILTER (WHERE ur.role_code = 'admin')::int AS admins,
       count(*) FILTER (WHERE ur.role_code = 'super_admin')::int AS super_admins
FROM user_roles ur
JOIN users u ON u.id = ur.user_id AND u.deleted_at IS NULL
WHERE ur.role_code IN ('admin', 'super_admin');

-- name: StatsPlanSubscribers :many
SELECT p.code, p.name, p.audience, p.price_vnd, p.billing_interval,
       (SELECT count(*) FROM subscriptions s WHERE s.plan_id = p.id AND s.status = 'active')::int AS subscribers
FROM plans p
ORDER BY p.display_order, p.code;

-- name: StatsRecentSettingChanges :many
SELECT created_at, actor_id, changes
FROM audit_logs
WHERE action = 'setting.changed'
ORDER BY created_at DESC, id DESC
LIMIT 5;

-- name: StatsAuditCounts :one
SELECT (SELECT count(*) FROM audit_logs WHERE created_at >= sqlc.arg(since)::timestamptz)::bigint AS important,
       (SELECT count(*) FROM audit_activity WHERE created_at >= sqlc.arg(since)::timestamptz)::bigint AS routine;
