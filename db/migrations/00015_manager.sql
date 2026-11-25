-- +goose Up
-- Rooms handed over to managers. A hand-over exists only after the person asked accepted an invitation (see below); the
-- landlord is the only one who ends it. A room has at most one active hand-over.
CREATE TABLE room_managers (
    id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    landlord_id    uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    room_id        uuid        NOT NULL REFERENCES rooms (id) ON DELETE RESTRICT,
    manager_id     uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    status         text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    -- Tell the landlord when the manager sends or edits a bill.
    notify_landlord boolean    NOT NULL DEFAULT true,
    assigned_by_id uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    accepted_at    timestamptz NOT NULL,
    revoked_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL)),
    CHECK (manager_id <> landlord_id)
);
CREATE UNIQUE INDEX room_managers_room_active_key ON room_managers (room_id) WHERE status = 'active';
CREATE INDEX room_managers_manager_idx ON room_managers (manager_id, status);
CREATE INDEX room_managers_landlord_idx ON room_managers (landlord_id, status, created_at DESC, id DESC);

-- What a manager may do on top of the basics, room by room. A capability is a code of internal/domain/manager (the
-- CHECK is the last defence; the domain refuses an unknown code first and ignores one it no longer knows when reading).
CREATE TABLE room_manager_capabilities (
    room_manager_id uuid NOT NULL REFERENCES room_managers (id) ON DELETE CASCADE,
    capability      text NOT NULL CHECK (capability IN
        ('payments.review', 'contracts.review_registrations', 'contracts.propose', 'contracts.create')),
    PRIMARY KEY (room_manager_id, capability)
);

-- A landlord's invitation to manage some rooms, sent as a link (the same mould as contract_invitations). Only the hash
-- of the secret can be looked up; while the invitation waits it is also kept encrypted so the landlord can copy the
-- same link again. There is no "awaiting approval" state here.
CREATE TABLE manager_invitations (
    id                uuid        PRIMARY KEY DEFAULT uuidv7(),
    landlord_id       uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    status            text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'declined', 'revoked')),
    token_hash        text        NOT NULL UNIQUE CHECK (char_length(token_hash) = 64),
    link_token_sealed text,
    expires_at        timestamptz NOT NULL,
    notify_landlord   boolean     NOT NULL DEFAULT true,
    recipient_id      uuid        REFERENCES users (id) ON DELETE SET NULL,
    recipient_email   text        CHECK (recipient_email = lower(recipient_email) AND char_length(recipient_email) <= 190),
    answered_by_id    uuid        REFERENCES users (id) ON DELETE SET NULL,
    answered_at       timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CHECK (status = 'pending' OR link_token_sealed IS NULL),
    CHECK ((status <> 'pending') = (answered_at IS NOT NULL))
);
CREATE INDEX manager_invitations_landlord_idx ON manager_invitations (landlord_id, created_at DESC, id DESC);
CREATE INDEX manager_invitations_recipient_idx ON manager_invitations (recipient_id, created_at DESC, id DESC)
    WHERE status = 'pending' AND recipient_id IS NOT NULL;

CREATE TABLE manager_invitation_rooms (
    invitation_id uuid NOT NULL REFERENCES manager_invitations (id) ON DELETE CASCADE,
    room_id       uuid NOT NULL REFERENCES rooms (id) ON DELETE RESTRICT,
    PRIMARY KEY (invitation_id, room_id)
);
CREATE INDEX manager_invitation_rooms_room_idx ON manager_invitation_rooms (room_id);

CREATE TABLE manager_invitation_capabilities (
    invitation_id uuid NOT NULL REFERENCES manager_invitations (id) ON DELETE CASCADE,
    capability    text NOT NULL CHECK (capability IN
        ('payments.review', 'contracts.review_registrations', 'contracts.propose', 'contracts.create')),
    PRIMARY KEY (invitation_id, capability)
);

-- The landlord's answer to an offer of a room that a manager wrote (see contract_invitations.created_by_id).
ALTER TABLE contract_invitations
    ADD COLUMN approved_by_id uuid REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN approved_at    timestamptz,
    ADD COLUMN approval_note  text CHECK (char_length(approval_note) <= 1000);
CREATE INDEX contract_invitations_author_idx ON contract_invitations (created_by_id, created_at DESC, id DESC)
    WHERE created_by_id IS NOT NULL;

-- +goose Down
DROP INDEX contract_invitations_author_idx;
ALTER TABLE contract_invitations DROP COLUMN approval_note, DROP COLUMN approved_at, DROP COLUMN approved_by_id;
DROP TABLE manager_invitation_capabilities;
DROP TABLE manager_invitation_rooms;
DROP TABLE manager_invitations;
DROP TABLE room_manager_capabilities;
DROP TABLE room_managers;
