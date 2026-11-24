-- name: InsertAnnouncement :one
INSERT INTO announcements (
    created_by_id, sender_role, landlord_id, audience, targeting, type, title, body, send_notification, show_popup,
    starts_at, ends_at, frequency, frequency_days, max_per_period, max_total, priority, popup_size, cta_label, cta_url, revision
) VALUES (
    sqlc.narg(created_by_id)::uuid, @sender_role::text, sqlc.narg(landlord_id)::uuid, @audience::text, sqlc.narg(targeting)::jsonb,
    @type::text, @title::jsonb, @body::jsonb, @send_notification::boolean, @show_popup::boolean,
    sqlc.narg(starts_at)::timestamptz, sqlc.narg(ends_at)::timestamptz, @frequency::text, sqlc.narg(frequency_days)::smallint,
    sqlc.narg(max_per_period)::smallint, sqlc.narg(max_total)::smallint, @priority::smallint, @popup_size::text,
    sqlc.narg(cta_label)::jsonb, sqlc.narg(cta_url)::text, @revision::integer
)
RETURNING id;

-- name: UpdateAnnouncement :execrows
-- The content, the revision and the sender role; the dates of publishing, the count and the status are not touched.
UPDATE announcements SET
    sender_role = @sender_role::text, audience = @audience::text, targeting = sqlc.narg(targeting)::jsonb, type = @type::text,
    title = @title::jsonb, body = @body::jsonb, send_notification = @send_notification::boolean, show_popup = @show_popup::boolean,
    starts_at = sqlc.narg(starts_at)::timestamptz, ends_at = sqlc.narg(ends_at)::timestamptz, frequency = @frequency::text,
    frequency_days = sqlc.narg(frequency_days)::smallint, max_per_period = sqlc.narg(max_per_period)::smallint,
    max_total = sqlc.narg(max_total)::smallint, priority = @priority::smallint, popup_size = @popup_size::text,
    cta_label = sqlc.narg(cta_label)::jsonb, cta_url = sqlc.narg(cta_url)::text, revision = @revision::integer, updated_at = now()
WHERE id = @id;

-- name: GetAnnouncement :one
SELECT * FROM announcements WHERE id = @id;

-- name: LockAnnouncement :one
SELECT * FROM announcements WHERE id = @id FOR UPDATE;

-- name: ListAnnouncements :many
-- With @landlord_id the landlord's messages, without it the platform's. The status conditions are the dates read at @now.
SELECT a.* FROM announcements a
WHERE CASE WHEN sqlc.narg(landlord_id)::uuid IS NULL THEN a.sender_role <> 'landlord' ELSE a.landlord_id = sqlc.narg(landlord_id)::uuid END
  AND (sqlc.narg(status)::text IS NULL OR CASE sqlc.narg(status)::text
        WHEN 'recalled'   THEN a.recalled_at IS NOT NULL
        WHEN 'draft'      THEN a.recalled_at IS NULL AND a.published_at IS NULL
        WHEN 'scheduled'  THEN a.recalled_at IS NULL AND a.published_at IS NOT NULL AND a.starts_at > @now::timestamptz
        WHEN 'ended'      THEN a.recalled_at IS NULL AND a.published_at IS NOT NULL AND a.ends_at <= @now::timestamptz
        WHEN 'active'     THEN a.recalled_at IS NULL AND a.published_at IS NOT NULL
                               AND (a.starts_at IS NULL OR a.starts_at <= @now::timestamptz) AND (a.ends_at IS NULL OR a.ends_at > @now::timestamptz)
        ELSE false END)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR a.published_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR a.published_at <= sqlc.narg(to_at)::timestamptz)
  AND (sqlc.narg(type)::text IS NULL OR a.type = sqlc.narg(type)::text)
  AND (sqlc.narg(channel)::text IS NULL
       OR (sqlc.narg(channel)::text = 'popup' AND a.show_popup)
       OR (sqlc.narg(channel)::text = 'notification' AND a.send_notification))
ORDER BY a.created_at DESC, a.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountAnnouncements :one
SELECT count(*) FROM announcements a
WHERE CASE WHEN sqlc.narg(landlord_id)::uuid IS NULL THEN a.sender_role <> 'landlord' ELSE a.landlord_id = sqlc.narg(landlord_id)::uuid END
  AND (sqlc.narg(status)::text IS NULL OR CASE sqlc.narg(status)::text
        WHEN 'recalled'   THEN a.recalled_at IS NOT NULL
        WHEN 'draft'      THEN a.recalled_at IS NULL AND a.published_at IS NULL
        WHEN 'scheduled'  THEN a.recalled_at IS NULL AND a.published_at IS NOT NULL AND a.starts_at > @now::timestamptz
        WHEN 'ended'      THEN a.recalled_at IS NULL AND a.published_at IS NOT NULL AND a.ends_at <= @now::timestamptz
        WHEN 'active'     THEN a.recalled_at IS NULL AND a.published_at IS NOT NULL
                               AND (a.starts_at IS NULL OR a.starts_at <= @now::timestamptz) AND (a.ends_at IS NULL OR a.ends_at > @now::timestamptz)
        ELSE false END)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR a.published_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR a.published_at <= sqlc.narg(to_at)::timestamptz)
  AND (sqlc.narg(type)::text IS NULL OR a.type = sqlc.narg(type)::text)
  AND (sqlc.narg(channel)::text IS NULL
       OR (sqlc.narg(channel)::text = 'popup' AND a.show_popup)
       OR (sqlc.narg(channel)::text = 'notification' AND a.send_notification));

-- name: AnnouncementReadCounts :many
-- How many recipients read each announcement.
SELECT announcement_id, count(*) AS read_count
FROM notifications
WHERE announcement_id = ANY(@ids::uuid[]) AND read_at IS NOT NULL
GROUP BY announcement_id;

-- name: AnnouncementImpressionCounts :many
-- How many people saw, closed and pressed the button of each popup.
SELECT announcement_id, count(*) AS seen, count(dismissed_at) AS dismissed, count(clicked_at) AS clicked
FROM announcement_impressions
WHERE announcement_id = ANY(@ids::uuid[])
GROUP BY announcement_id;

-- name: ListAnnouncementCreators :many
SELECT id, full_name, email FROM users WHERE id = ANY(@ids::uuid[]);

-- name: DeleteAnnouncementRecipients :exec
DELETE FROM announcement_recipients WHERE announcement_id = @announcement_id;

-- name: InsertAnnouncementRecipients :exec
INSERT INTO announcement_recipients (announcement_id, user_id)
SELECT @announcement_id::uuid, unnest(@user_ids::uuid[])
ON CONFLICT DO NOTHING;

-- name: ListAnnouncementRecipientIDs :many
SELECT user_id FROM announcement_recipients WHERE announcement_id = @announcement_id ORDER BY user_id;

-- name: ListAnnouncementRecipientPeople :many
SELECT u.id, u.full_name, u.email
FROM announcement_recipients r JOIN users u ON u.id = r.user_id
WHERE r.announcement_id = @announcement_id
ORDER BY u.full_name, u.id;

-- name: SetAnnouncementPublished :execrows
UPDATE announcements SET published_at = sqlc.narg(published_at)::timestamptz, updated_at = now() WHERE id = @id;

-- name: RecallAnnouncement :execrows
UPDATE announcements SET recalled_at = @recalled_at::timestamptz, updated_at = now() WHERE id = @id AND recalled_at IS NULL;

-- name: DeleteAnnouncementNotifications :execrows
DELETE FROM notifications WHERE announcement_id = @announcement_id;

-- name: SetAnnouncementRecipientCount :exec
UPDATE announcements SET recipient_count = @recipient_count::integer, updated_at = now() WHERE id = @id;

-- name: ClaimAnnouncementDelivery :one
-- Marks the notifications as sent when they are due and not sent yet; no row means somebody else did (or not due).
UPDATE announcements SET sent_at = @at::timestamptz, updated_at = now()
WHERE id = @id AND sent_at IS NULL AND published_at IS NOT NULL AND recalled_at IS NULL AND send_notification
  AND (starts_at IS NULL OR starts_at <= @at::timestamptz)
RETURNING id;

-- name: ListDueAnnouncementIDs :many
SELECT id FROM announcements
WHERE send_notification AND published_at IS NOT NULL AND sent_at IS NULL AND recalled_at IS NULL
  AND (starts_at IS NULL OR starts_at <= @now::timestamptz)
ORDER BY starts_at NULLS FIRST, id
LIMIT @page_limit;

-- name: DeliverAnnouncementNotifications :many
-- One notification per person of the audience, once each (the unique index and ON CONFLICT make a repeat harmless).
INSERT INTO notifications (user_id, kind, params, url, audience_role, announcement_id)
SELECT a.user_id, 'announcement', @params::jsonb, '/notifications', sqlc.narg(audience_role)::text, @announcement_id::uuid
FROM announcement_audience(@scope::text, sqlc.narg(landlord)::uuid, sqlc.narg(picked)::uuid[], sqlc.narg(roles)::text[], @excluded_roles::text[], sqlc.narg(province_codes)::integer[], sqlc.narg(ward_codes)::integer[], sqlc.narg(plan_codes)::text[], @plan_none::boolean, sqlc.narg(created_from)::timestamptz, sqlc.narg(created_until)::timestamptz, sqlc.narg(created_before)::timestamptz, sqlc.narg(active_since)::timestamptz, sqlc.narg(user_ids)::uuid[], @now::timestamptz) AS a(user_id)
ON CONFLICT (announcement_id, user_id) WHERE announcement_id IS NOT NULL DO NOTHING
RETURNING user_id;

-- name: CountAnnouncementAudience :one
SELECT count(*) FROM announcement_audience(@scope::text, sqlc.narg(landlord)::uuid, sqlc.narg(picked)::uuid[], sqlc.narg(roles)::text[], @excluded_roles::text[], sqlc.narg(province_codes)::integer[], sqlc.narg(ward_codes)::integer[], sqlc.narg(plan_codes)::text[], @plan_none::boolean, sqlc.narg(created_from)::timestamptz, sqlc.narg(created_until)::timestamptz, sqlc.narg(created_before)::timestamptz, sqlc.narg(active_since)::timestamptz, sqlc.narg(user_ids)::uuid[], @now::timestamptz);

-- name: FilterAnnouncementAudience :many
-- The ones of @ids that belong to the audience.
SELECT a.user_id::uuid AS user_id FROM announcement_audience(@scope::text, sqlc.narg(landlord)::uuid, sqlc.narg(picked)::uuid[], sqlc.narg(roles)::text[], @excluded_roles::text[], sqlc.narg(province_codes)::integer[], sqlc.narg(ward_codes)::integer[], sqlc.narg(plan_codes)::text[], @plan_none::boolean, sqlc.narg(created_from)::timestamptz, sqlc.narg(created_until)::timestamptz, sqlc.narg(created_before)::timestamptz, sqlc.narg(active_since)::timestamptz, sqlc.narg(user_ids)::uuid[], @now::timestamptz) AS a(user_id) WHERE a.user_id = ANY(@ids::uuid[]);

-- name: SearchLandlordPeople :many
-- The running tenants of a landlord whose name or email contains @search (lowercase, % and _ escaped).
SELECT u.id, u.full_name, u.email
FROM users u
WHERE u.status = 'active'
  AND EXISTS (SELECT 1 FROM contracts c WHERE c.tenant_id = u.id AND c.landlord_id = @landlord_id AND c.status = 'active'
              AND c.confirmed_at IS NOT NULL AND c.deleted_at IS NULL)
  AND (@search::text = '' OR lower(u.full_name) LIKE '%' || @search::text || '%' ESCAPE '\' OR lower(u.email) LIKE '%' || @search::text || '%' ESCAPE '\')
ORDER BY u.full_name, u.id
LIMIT @page_limit OFFSET @page_offset;

-- name: CountLandlordPeople :one
SELECT count(*)
FROM users u
WHERE u.status = 'active'
  AND EXISTS (SELECT 1 FROM contracts c WHERE c.tenant_id = u.id AND c.landlord_id = @landlord_id AND c.status = 'active'
              AND c.confirmed_at IS NOT NULL AND c.deleted_at IS NULL)
  AND (@search::text = '' OR lower(u.full_name) LIKE '%' || @search::text || '%' ESCAPE '\' OR lower(u.email) LIKE '%' || @search::text || '%' ESCAPE '\');

-- name: WelcomeAnnouncementExists :one
SELECT EXISTS (SELECT 1 FROM announcements WHERE type = 'welcome' AND sender_role <> 'landlord');

-- name: ListPopupCandidates :many
-- The popups that are out at @now, most important first; the use case keeps the ones this person may see.
SELECT * FROM announcements
WHERE show_popup AND published_at IS NOT NULL AND recalled_at IS NULL
  AND (starts_at IS NULL OR starts_at <= @now::timestamptz) AND (ends_at IS NULL OR ends_at > @now::timestamptz)
ORDER BY priority DESC, published_at DESC, id
LIMIT @page_limit;
