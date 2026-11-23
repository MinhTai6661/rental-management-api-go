-- +goose Up
-- Money paid on a bill: a tenant sends a payment (all or part of the bill) and the landlord confirms or rejects it, or
-- the landlord writes down money that came in outside the app. Money is whole VND.
CREATE TABLE payments (
    id                 uuid        PRIMARY KEY DEFAULT uuidv7(),
    invoice_id         uuid        NOT NULL REFERENCES invoices (id) ON DELETE RESTRICT,
    -- The tenant's account; NULL for a payment the landlord wrote down (the person has no account).
    payer_id           uuid        REFERENCES users (id) ON DELETE SET NULL,
    -- manual_proof, momo, recorded... the code of the payment method (a driver in the application), so a new way of
    -- paying needs no schema change.
    method             text        NOT NULL CHECK (char_length(method) BETWEEN 1 AND 30),
    amount_vnd         bigint      NOT NULL CHECK (amount_vnd BETWEEN 1 AND 1000000000000),
    -- pending: waiting for the landlord; confirmed: counts towards the bill; rejected; voided: was confirmed, then the
    -- landlord reopened the bill (kept for the history, no longer counts).
    status             text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'confirmed', 'rejected', 'voided')),
    note               text        CHECK (char_length(note) <= 500),
    -- Storage keys of the proof pictures (private files: they are only reached through signed links).
    proof_keys         text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(proof_keys) <= 10),
    -- The id of the payment at an online gateway, when there is one.
    external_reference text        CHECK (char_length(external_reference) <= 100),
    reviewed_by_id     uuid        REFERENCES users (id) ON DELETE SET NULL,
    reviewed_at        timestamptz,
    review_note        text        CHECK (char_length(review_note) <= 500),
    -- The landlord who wrote the payment down; NULL for a payment a tenant sent.
    recorded_by_id     uuid        REFERENCES users (id) ON DELETE SET NULL,
    paid_at            timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'pending') = (reviewed_at IS NULL)),
    CHECK (status NOT IN ('confirmed', 'voided') OR paid_at IS NOT NULL),
    CHECK (status <> 'rejected' OR paid_at IS NULL)
);
-- What is paid and what waits for review, per bill (the totals and the lists of a bill).
CREATE INDEX payments_invoice_status_idx ON payments (invoice_id, status);
CREATE INDEX payments_payer_idx ON payments (payer_id, created_at DESC) WHERE payer_id IS NOT NULL;
-- The landlord's list of payments by status, newest first.
CREATE INDEX payments_status_created_idx ON payments (status, created_at DESC, id DESC);
CREATE INDEX payments_reviewed_by_idx ON payments (reviewed_by_id) WHERE reviewed_by_id IS NOT NULL;
CREATE INDEX payments_recorded_by_idx ON payments (recorded_by_id) WHERE recorded_by_id IS NOT NULL;

-- How a landlord wants to be paid: one row per landlord and payment method. The settings (the account to pay into)
-- are encrypted in the application before they are stored.
CREATE TABLE landlord_payment_methods (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    landlord_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    method        text        NOT NULL CHECK (char_length(method) BETWEEN 1 AND 30),
    config_sealed text        NOT NULL,
    is_active     boolean     NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (landlord_id, method)
);

-- +goose Down
DROP TABLE landlord_payment_methods;
DROP TABLE payments;
