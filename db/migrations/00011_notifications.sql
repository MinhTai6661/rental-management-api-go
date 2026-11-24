-- +goose Up
-- In-app messages for one user. What a message is about is a kind: the web translates it with params, so it reads in the
-- language the user picks when they look at it. Announcements (a later migration) add announcement_id, so recalling one
-- or counting who read it never queries JSON.
CREATE TABLE notifications (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- A closed set kept in the application (internal/domain/notification); this only stops garbage.
    kind          text        NOT NULL CHECK (kind ~ '^[a-z_]{1,50}$'),
    params        jsonb       NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(params) = 'object'),
    url           text        CHECK (char_length(url) <= 500),
    -- The side of the account the message is for; NULL shows in every role.
    audience_role text        CHECK (audience_role IN ('landlord', 'tenant', 'manager')),
    read_at       timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
-- The inbox and the bell, newest first (the audience filter is applied on these rows).
CREATE INDEX notifications_inbox_idx ON notifications (user_id, created_at DESC, id DESC);
-- The unread count.
CREATE INDEX notifications_unread_idx ON notifications (user_id, audience_role) WHERE read_at IS NULL;
-- The daily prune.
CREATE INDEX notifications_created_idx ON notifications (created_at);

-- +goose Down
DROP TABLE notifications;
