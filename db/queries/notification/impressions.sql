-- name: ListAnnouncementImpressions :many
-- What one person did with each of the popups.
SELECT * FROM announcement_impressions WHERE user_id = @user_id AND announcement_id = ANY(@announcement_ids::uuid[]);

-- name: UpsertAnnouncementImpression :exec
INSERT INTO announcement_impressions (
    announcement_id, user_id, revision, view_count, period_view_count, period_started_at, session_id,
    first_seen_at, last_seen_at, dismissed_at, clicked_at
) VALUES (
    @announcement_id::uuid, @user_id::uuid, @revision::integer, @view_count::integer, @period_view_count::integer,
    sqlc.narg(period_started_at)::timestamptz, sqlc.narg(session_id)::uuid, @first_seen_at::timestamptz, @last_seen_at::timestamptz,
    sqlc.narg(dismissed_at)::timestamptz, sqlc.narg(clicked_at)::timestamptz
)
ON CONFLICT (announcement_id, user_id) DO UPDATE SET
    revision = EXCLUDED.revision, view_count = EXCLUDED.view_count, period_view_count = EXCLUDED.period_view_count,
    period_started_at = EXCLUDED.period_started_at, session_id = EXCLUDED.session_id, last_seen_at = EXCLUDED.last_seen_at,
    dismissed_at = EXCLUDED.dismissed_at, clicked_at = EXCLUDED.clicked_at;
