-- +goose Up
-- A rental contract between a landlord's room and a tenant. tenant_id is NULL for the "blank" contract the system opens
-- when a room is marked rented without anybody with an account behind it (bills carry tenant_name/tenant_phone then).
CREATE TABLE contracts (
    id                         uuid        PRIMARY KEY DEFAULT uuidv7(),
    room_id                    uuid        NOT NULL REFERENCES rooms (id) ON DELETE RESTRICT,
    -- The owner of the room, copied from it so the landlord's lists and their order need no join to find their rows.
    landlord_id                uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    tenant_id                  uuid        REFERENCES users (id) ON DELETE RESTRICT,
    tenant_name                text        CHECK (char_length(tenant_name) <= 100),
    tenant_phone               text        CHECK (char_length(tenant_phone) <= 30),
    status                     text        NOT NULL CHECK (status IN ('pending', 'active', 'ended', 'rejected')),
    -- Rent of one period of the room's rental_period_unit, agreed at signing (later changes to the room do not rewrite it).
    price_vnd                  bigint      NOT NULL CHECK (price_vnd BETWEEN 0 AND 1000000000000),
    deposit_vnd                bigint      CHECK (deposit_vnd BETWEEN 0 AND 1000000000000),
    occupant_count             smallint    NOT NULL DEFAULT 1 CHECK (occupant_count BETWEEN 1 AND 50),
    starts_on                  date        NOT NULL,
    ends_on                    date,
    notes                      text        CHECK (char_length(notes) <= 1000),
    -- Set when the landlord can see the contract; registrations are visible at once, so it is set on creation.
    confirmed_at               timestamptz,
    -- The landlord's own order of the contracts list (drag and drop): smaller first, new ones go on top.
    display_order              integer     NOT NULL,
    -- A tenant's request to end a running contract; NULL = never asked.
    cancellation_status        text        CHECK (cancellation_status IN ('requested', 'approved', 'declined')),
    cancellation_requested_at  timestamptz,
    cancellation_reason        text        CHECK (char_length(cancellation_reason) <= 1000),
    cancellation_response_note text        CHECK (char_length(cancellation_response_note) <= 1000),
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    deleted_at                 timestamptz,
    -- Nobody signs up for a blank contract, so it is never a registration waiting for an answer or a refused one.
    CHECK (tenant_id IS NOT NULL OR status IN ('active', 'ended'))
);
-- One running contract with a tenant per room, and one running blank contract per room.
CREATE UNIQUE INDEX contracts_room_active_tenant_key ON contracts (room_id)
    WHERE status = 'active' AND tenant_id IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX contracts_room_active_blank_key ON contracts (room_id)
    WHERE status = 'active' AND tenant_id IS NULL AND deleted_at IS NULL;
-- One open (pending or running) registration per tenant and room.
CREATE UNIQUE INDEX contracts_room_tenant_open_key ON contracts (room_id, tenant_id)
    WHERE status IN ('pending', 'active') AND tenant_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX contracts_room_status_idx ON contracts (room_id, status) WHERE deleted_at IS NULL;
-- The landlord's list in its stored order, and the tenant's list by start date.
CREATE INDEX contracts_landlord_order_idx ON contracts (landlord_id, display_order, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX contracts_tenant_idx ON contracts (tenant_id, status, starts_on DESC) WHERE deleted_at IS NULL AND tenant_id IS NOT NULL;
CREATE INDEX contracts_cancellation_idx ON contracts (landlord_id) WHERE cancellation_status = 'requested' AND deleted_at IS NULL;

-- +goose Down
DROP TABLE contracts;
