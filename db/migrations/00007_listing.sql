-- +goose Up
-- A landlord's post that offers one room on find rooms. The price, pictures and address are the room's own (nothing is
-- copied here): the listing adds the headline and the text and carries the admins' decision. Only an approved listing
-- of a vacant room is shown to renters.
CREATE TABLE listings (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    room_id         uuid        NOT NULL REFERENCES rooms (id) ON DELETE RESTRICT,
    -- The owner of the room, copied from it so the landlord's lists need no join to find their rows.
    landlord_id     uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    title           text        NOT NULL CHECK (char_length(title) BETWEEN 1 AND 150),
    description     text        CHECK (char_length(description) <= 5000),
    status          text        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'pending', 'approved', 'rejected', 'unpublished')),
    submitted_at    timestamptz,
    reviewed_by_id  uuid        REFERENCES users (id) ON DELETE SET NULL,
    reviewed_at     timestamptz,
    review_note     text        CHECK (char_length(review_note) <= 1000),
    published_at    timestamptz,
    unpublished_at  timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
-- A room is offered by one listing at a time: at most one that is waiting for review or live.
CREATE UNIQUE INDEX listings_room_open_key ON listings (room_id) WHERE status IN ('pending', 'approved');
CREATE INDEX listings_room_idx ON listings (room_id, status);
CREATE INDEX listings_landlord_status_idx ON listings (landlord_id, status, id DESC);
CREATE INDEX listings_status_idx ON listings (status, submitted_at, id);

-- +goose Down
DROP TABLE listings;
