-- +goose Up
-- Problems people report. A room report (from a tenant) goes to the landlord of the room; a feature report (from a
-- tenant or a landlord) goes to the admins. Whoever it goes to answers it. The author deleting a report only hides it
-- from them (deleted_at): the recipient keeps it as read-only history.
CREATE TABLE reports (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    reporter_id uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    kind        text        NOT NULL CHECK (kind IN ('room', 'feature')),
    room_id     uuid        REFERENCES rooms (id) ON DELETE RESTRICT,
    subject     text        NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 150),
    body        text        NOT NULL CHECK (char_length(body) BETWEEN 1 AND 5000),
    status      text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'in_progress', 'resolved')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz,
    CHECK ((kind = 'room') = (room_id IS NOT NULL))
);
-- The author's list (their own, not deleted), the landlord's inbox (by room) and the admins' inbox (feature reports).
CREATE INDEX reports_reporter_idx ON reports (reporter_id, created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX reports_room_idx ON reports (room_id, created_at DESC, id DESC) WHERE kind = 'room';
CREATE INDEX reports_feature_idx ON reports (status, created_at DESC, id DESC) WHERE kind = 'feature';

-- The conversation under a report: the author and the recipient write as many messages as they like.
CREATE TABLE report_replies (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    report_id  uuid        NOT NULL REFERENCES reports (id) ON DELETE CASCADE,
    author_id  uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    body       text        NOT NULL CHECK (char_length(body) BETWEEN 1 AND 5000),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX report_replies_report_idx ON report_replies (report_id, created_at DESC, id DESC);

-- A report as a reader sees it: with its room and owner, its author (and which side they write from) and how many
-- messages follow. The two lists and the detail read this one shape.
CREATE VIEW report_details AS
SELECT rep.id, rep.reporter_id, rep.kind, rep.room_id, rm.landlord_id AS room_landlord_id, rm.name AS room_name,
       rep.subject, rep.body, rep.status, rep.created_at, rep.deleted_at,
       u.full_name AS reporter_name,
       EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = u.id AND ur.role_code IN ('admin', 'super_admin')) AS reporter_is_staff,
       EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = u.id AND ur.role_code = 'landlord') AS reporter_is_landlord,
       (SELECT count(*) FROM report_replies x WHERE x.report_id = rep.id)::integer AS replies_count
FROM reports rep
JOIN users u ON u.id = rep.reporter_id
LEFT JOIN rooms rm ON rm.id = rep.room_id;

-- One message of a conversation with its author, as the screen shows it.
CREATE VIEW report_reply_details AS
SELECT x.id, x.report_id, x.body, x.created_at, x.author_id, u.full_name AS author_name,
       EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = u.id AND ur.role_code IN ('admin', 'super_admin')) AS author_is_staff,
       EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = u.id AND ur.role_code = 'landlord') AS author_is_landlord
FROM report_replies x
JOIN users u ON u.id = x.author_id;

-- +goose Down
DROP VIEW report_reply_details;
DROP VIEW report_details;
DROP TABLE report_replies;
DROP TABLE reports;
