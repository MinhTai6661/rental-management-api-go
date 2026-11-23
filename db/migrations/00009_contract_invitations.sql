-- +goose Up
-- A landlord's offer of a room on set terms, sent as a link. Whoever may answer it (see the audience) and accepts
-- becomes the tenant: the contract is created then. Only the hash of the link's secret can be looked up; while the
-- offer waits, the secret is also kept encrypted (link_token_sealed) so the landlord can copy the same link again.
CREATE TABLE contract_invitations (
    id                uuid        PRIMARY KEY DEFAULT uuidv7(),
    room_id           uuid        NOT NULL REFERENCES rooms (id) ON DELETE RESTRICT,
    -- The owner of the room, copied from it so the landlord's list needs no join to find their rows.
    landlord_id       uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    -- A manager who wrote it for the landlord (manager phase); NULL = the landlord wrote it.
    created_by_id     uuid        REFERENCES users (id) ON DELETE SET NULL,
    status            text        NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('awaiting_approval', 'pending', 'accepted', 'declined', 'revoked')),
    token_hash        text        NOT NULL UNIQUE CHECK (char_length(token_hash) = 64),
    link_token_sealed text,
    expires_at        timestamptz NOT NULL,
    -- The terms the contract is made of on acceptance.
    price_vnd         bigint      NOT NULL CHECK (price_vnd BETWEEN 0 AND 1000000000000),
    deposit_vnd       bigint      CHECK (deposit_vnd BETWEEN 0 AND 1000000000000),
    occupant_count    smallint    NOT NULL DEFAULT 1 CHECK (occupant_count BETWEEN 1 AND 50),
    starts_on         date        NOT NULL,
    ends_on           date,
    contract_status   text        NOT NULL DEFAULT 'active' CHECK (contract_status IN ('pending', 'active')),
    note              text        CHECK (char_length(note) <= 1000),
    -- Who it is for: an account (only it may answer), else an address (only a verified account with it), else anybody.
    recipient_id      uuid        REFERENCES users (id) ON DELETE SET NULL,
    recipient_email   text        CHECK (recipient_email = lower(recipient_email) AND char_length(recipient_email) <= 190),
    answered_by_id    uuid        REFERENCES users (id) ON DELETE SET NULL,
    answered_at       timestamptz,
    contract_id       uuid        REFERENCES contracts (id) ON DELETE RESTRICT,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_on IS NULL OR ends_on > starts_on),
    -- The secret is kept only while the offer waits for an answer.
    CHECK (status = 'pending' OR link_token_sealed IS NULL),
    CHECK ((status IN ('accepted', 'declined', 'revoked')) = (answered_at IS NOT NULL)),
    CHECK ((status = 'accepted') = (contract_id IS NOT NULL))
);
CREATE INDEX contract_invitations_landlord_idx ON contract_invitations (landlord_id, created_at DESC, id DESC);
CREATE INDEX contract_invitations_recipient_idx ON contract_invitations (recipient_id, created_at DESC, id DESC)
    WHERE status = 'pending' AND recipient_id IS NOT NULL;
CREATE INDEX contract_invitations_room_open_idx ON contract_invitations (room_id)
    WHERE status IN ('pending', 'awaiting_approval');

-- Addresses that asked, from the link of an invitation email, never to be mailed an invitation again. Only the SHA-256
-- of the lower-case address is kept.
CREATE TABLE invitation_email_blocks (
    email_hash text        PRIMARY KEY CHECK (char_length(email_hash) = 64),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE invitation_email_blocks;
DROP TABLE contract_invitations;
