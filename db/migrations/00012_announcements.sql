-- +goose Up
-- A message from the platform (admins) or from a landlord (to their own tenants): a notification in the inbox, a popup,
-- or both. Its status is not stored: it follows from published_at, starts_at, ends_at and recalled_at.
CREATE TABLE announcements (
    id                uuid        PRIMARY KEY DEFAULT uuidv7(),
    created_by_id     uuid        REFERENCES users (id) ON DELETE SET NULL,
    -- The role the sender acted in when writing it: decides who owns it (platform = any admin or super admin, landlord =
    -- that landlord) and which staff it may reach. Stored, so a later role change of the creator changes nothing.
    sender_role       text        NOT NULL CHECK (sender_role IN ('super_admin', 'admin', 'landlord')),
    -- The landlord whose people a landlord's message is for (NULL for the platform's).
    landlord_id       uuid        REFERENCES users (id) ON DELETE CASCADE,
    audience          text        NOT NULL CHECK (audience IN
        ('everyone', 'landlords', 'tenants', 'custom', 'my_tenants', 'my_people', 'selected_people')),
    -- The normalized criteria (see domain/notification Criteria); NULL when none.
    targeting         jsonb       CHECK (targeting IS NULL OR jsonb_typeof(targeting) = 'object'),
    type              text        NOT NULL DEFAULT 'notice' CHECK (type IN ('notice', 'warning', 'promotion', 'whats_new', 'welcome')),
    -- One text per language: {"vi": "...", "en": "..."}.
    title             jsonb       NOT NULL CHECK (jsonb_typeof(title) = 'object'),
    body              jsonb       NOT NULL CHECK (jsonb_typeof(body) = 'object'),
    send_notification boolean     NOT NULL,
    show_popup        boolean     NOT NULL,
    published_at      timestamptz,
    starts_at         timestamptz,
    ends_at           timestamptz,
    frequency         text        NOT NULL DEFAULT 'once' CHECK (frequency IN ('once', 'every_session', 'every_days')),
    frequency_days    smallint    CHECK (frequency_days BETWEEN 1 AND 365),
    max_per_period    smallint    CHECK (max_per_period BETWEEN 1 AND 100),
    max_total         smallint    CHECK (max_total BETWEEN 1 AND 1000),
    priority          smallint    NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 10),
    popup_size        text        NOT NULL DEFAULT 'medium' CHECK (popup_size IN ('small', 'medium', 'large', 'full')),
    cta_label         jsonb       CHECK (cta_label IS NULL OR jsonb_typeof(cta_label) = 'object'),
    cta_url           text        CHECK (char_length(cta_url) <= 500),
    -- Raised by an edit that asks to show the popup again to the people who closed it.
    revision          integer     NOT NULL DEFAULT 1 CHECK (revision >= 1),
    recipient_count   integer     NOT NULL DEFAULT 0 CHECK (recipient_count >= 0),
    -- When the notifications went out (NULL: not yet, or none to send).
    sent_at           timestamptz,
    recalled_at       timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CHECK (send_notification OR show_popup),
    CHECK ((sender_role = 'landlord') = (landlord_id IS NOT NULL)),
    CHECK ((frequency = 'every_days') = (frequency_days IS NOT NULL)),
    CHECK ((cta_label IS NULL) = (cta_url IS NULL)),
    CHECK (starts_at IS NULL OR ends_at IS NULL OR ends_at > starts_at)
);
CREATE INDEX announcements_platform_list_idx ON announcements (created_at DESC, id DESC) WHERE sender_role <> 'landlord';
CREATE INDEX announcements_landlord_list_idx ON announcements (landlord_id, created_at DESC, id DESC) WHERE landlord_id IS NOT NULL;
CREATE INDEX announcements_popup_idx ON announcements (priority DESC, published_at DESC)
    WHERE show_popup AND published_at IS NOT NULL AND recalled_at IS NULL;
CREATE INDEX announcements_due_idx ON announcements (starts_at)
    WHERE send_notification AND published_at IS NOT NULL AND sent_at IS NULL AND recalled_at IS NULL;
CREATE INDEX announcements_created_by_idx ON announcements (created_by_id) WHERE created_by_id IS NOT NULL;

-- The people a landlord picked one by one (selected_people).
CREATE TABLE announcement_recipients (
    announcement_id uuid NOT NULL REFERENCES announcements (id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (announcement_id, user_id)
);
CREATE INDEX announcement_recipients_user_idx ON announcement_recipients (user_id);

-- What each person did with a popup: one row per person and announcement (written by the popup routes).
CREATE TABLE announcement_impressions (
    announcement_id   uuid        NOT NULL REFERENCES announcements (id) ON DELETE CASCADE,
    user_id           uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    revision          integer     NOT NULL CHECK (revision >= 1),
    view_count        integer     NOT NULL DEFAULT 0 CHECK (view_count >= 0),
    period_view_count integer     NOT NULL DEFAULT 0 CHECK (period_view_count >= 0),
    period_started_at timestamptz,
    session_id        uuid,
    first_seen_at     timestamptz NOT NULL,
    last_seen_at      timestamptz NOT NULL,
    dismissed_at      timestamptz,
    clicked_at        timestamptz,
    PRIMARY KEY (announcement_id, user_id)
);
CREATE INDEX announcement_impressions_user_idx ON announcement_impressions (user_id);

-- The pictures of a popup (a slide), in order.
CREATE TABLE announcement_images (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    announcement_id uuid        NOT NULL REFERENCES announcements (id) ON DELETE CASCADE,
    file_key        text        NOT NULL UNIQUE CHECK (file_key ~ '^announcements/'),
    content_type    text        NOT NULL,
    width           integer     NOT NULL CHECK (width > 0),
    height          integer     NOT NULL CHECK (height > 0),
    size_bytes      integer     NOT NULL CHECK (size_bytes > 0),
    -- Where pressing the picture goes (platform messages only): an app path or an https address.
    link_url        text        CHECK (char_length(link_url) <= 2048),
    display_order   smallint    NOT NULL CHECK (display_order >= 0),
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX announcement_images_order_idx ON announcement_images (announcement_id, display_order, id);

ALTER TABLE notifications ADD COLUMN announcement_id uuid REFERENCES announcements (id) ON DELETE CASCADE;
-- One notification per person and announcement (delivery is ON CONFLICT DO NOTHING); also the recall and read counts.
CREATE UNIQUE INDEX notifications_announcement_user_key ON notifications (announcement_id, user_id) WHERE announcement_id IS NOT NULL;

-- Who an announcement goes to: the ONE definition the count, the delivery and the popup check share. Every parameter is
-- resolved in Go (domain/notification ResolveFilter); a NULL array means "no such criterion". A single STABLE SELECT:
-- Postgres inlines it, so "WHERE user_id = $x" on the result stays an index lookup.
-- p_scope: 'all' (no role scope), 'landlords', 'tenants', 'my_tenants' (running confirmed contracts of p_landlord),
--          'picked' (my_tenants restricted to p_picked).
-- +goose StatementBegin
CREATE FUNCTION announcement_audience(
    p_scope text, p_landlord uuid, p_picked uuid[],
    p_roles text[], p_excluded_roles text[], p_province_codes integer[], p_ward_codes integer[],
    p_plan_codes text[], p_plan_none boolean,
    p_created_from timestamptz, p_created_until timestamptz, p_created_before timestamptz,
    p_active_since timestamptz, p_user_ids uuid[], p_now timestamptz
) RETURNS TABLE (user_id uuid) LANGUAGE sql STABLE AS $$
    SELECT u.id
    FROM users u
    WHERE u.status = 'active'
      AND NOT EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role_code = ANY (p_excluded_roles))
      AND (p_scope <> 'landlords' OR EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role_code = 'landlord'))
      AND (p_scope <> 'tenants'   OR EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role_code = 'tenant'))
      AND (p_scope NOT IN ('my_tenants', 'picked') OR EXISTS (
            SELECT 1 FROM contracts c
            WHERE c.tenant_id = u.id AND c.landlord_id = p_landlord AND c.status = 'active'
              AND c.confirmed_at IS NOT NULL AND c.deleted_at IS NULL))
      AND (p_scope <> 'picked' OR u.id = ANY (p_picked))
      AND (p_roles IS NULL OR EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role_code = ANY (p_roles)))
      AND ((p_province_codes IS NULL AND p_ward_codes IS NULL) OR EXISTS (
            SELECT 1 FROM wards w
            WHERE (w.code = ANY (p_ward_codes) OR w.province_code = ANY (p_province_codes))
              AND (w.code = u.ward_code
                   OR EXISTS (SELECT 1 FROM rooms ro WHERE ro.landlord_id = u.id AND ro.deleted_at IS NULL AND ro.ward_code = w.code)
                   OR EXISTS (SELECT 1 FROM buildings b WHERE b.landlord_id = u.id AND b.deleted_at IS NULL AND b.ward_code = w.code)
                   OR EXISTS (SELECT 1 FROM contracts c JOIN rooms ro ON ro.id = c.room_id
                              WHERE c.tenant_id = u.id AND c.status = 'active' AND c.deleted_at IS NULL AND ro.ward_code = w.code))))
      AND ((p_plan_codes IS NULL AND NOT p_plan_none)
           OR (p_plan_codes IS NOT NULL AND EXISTS (
                SELECT 1 FROM subscriptions s JOIN plans p ON p.id = s.plan_id
                WHERE s.user_id = u.id AND s.status = 'active' AND s.starts_at <= p_now
                  AND (s.ends_at IS NULL OR s.ends_at > p_now) AND p.code = ANY (p_plan_codes)))
           OR (p_plan_none AND NOT EXISTS (
                SELECT 1 FROM subscriptions s
                WHERE s.user_id = u.id AND s.status = 'active' AND s.starts_at <= p_now
                  AND (s.ends_at IS NULL OR s.ends_at > p_now))))
      AND (p_created_from   IS NULL OR u.created_at >= p_created_from)
      AND (p_created_until  IS NULL OR u.created_at <= p_created_until)
      AND (p_created_before IS NULL OR u.created_at <  p_created_before)
      AND (p_active_since   IS NULL OR EXISTS (
            SELECT 1 FROM user_sessions us WHERE us.user_id = u.id AND us.last_used_at >= p_active_since))
      AND (p_user_ids IS NULL OR u.id = ANY (p_user_ids))
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION announcement_audience;
DROP INDEX notifications_announcement_user_key;
ALTER TABLE notifications DROP COLUMN announcement_id;
DROP TABLE announcement_images;
DROP TABLE announcement_impressions;
DROP TABLE announcement_recipients;
DROP TABLE announcements;
