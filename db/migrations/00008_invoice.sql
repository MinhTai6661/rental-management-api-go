-- +goose Up
-- A bill of a rental contract for one billing period. The landlord writes it as a draft (nobody else sees it), sends
-- it, and it is paid or not. Money is whole VND. A contract may have no tenant account (the blank contract), so a bill
-- can also be passed on by a link that needs no sign in.
CREATE TABLE invoices (
    id                      uuid        PRIMARY KEY DEFAULT uuidv7(),
    contract_id             uuid        NOT NULL REFERENCES contracts (id) ON DELETE RESTRICT,
    -- Who wrote it: the landlord (NULL for the bill the plan's automatic invoicing sent).
    created_by_id           uuid        REFERENCES users (id) ON DELETE SET NULL,
    invoice_number          text        NOT NULL UNIQUE CHECK (char_length(invoice_number) BETWEEN 1 AND 30),
    -- What people call it: "04/2026 - P101" unless the landlord wrote another name.
    name                    text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80 AND name = btrim(name)),
    -- The words of the bank transfer text (plain characters, at most 14) and the whole text: the words, a space and a
    -- random tail, so every transfer text is unique and a payment can be matched to its bill.
    payment_label           text        NOT NULL CHECK (char_length(payment_label) BETWEEN 1 AND 14 AND payment_label ~ '^[A-Za-z0-9 ._/-]+$'),
    payment_code            text        NOT NULL UNIQUE CHECK (char_length(payment_code) BETWEEN 1 AND 20),
    -- First and last day (inclusive) of the period the bill covers.
    period_start            date        NOT NULL,
    period_end              date        NOT NULL,
    total_amount_vnd        bigint      NOT NULL CHECK (total_amount_vnd BETWEEN 0 AND 1000000000000),
    due_at                  timestamptz NOT NULL,
    status                  text        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'sent', 'paid')),
    -- NULL while it is a draft.
    sent_at                 timestamptz,
    -- manual (the landlord pressed send) or automatic (the plan's automatic invoicing); NULL for a draft.
    sent_via                text        CHECK (sent_via IN ('manual', 'automatic')),
    sent_by_id              uuid        REFERENCES users (id) ON DELETE SET NULL,
    paid_at                 timestamptz,
    note                    text        CHECK (char_length(note) <= 500),
    -- app: a notice and an email to the tenant's account; link: the landlord passes on the link that shows the bill.
    delivery                text        NOT NULL DEFAULT 'app' CHECK (delivery IN ('app', 'link')),
    -- The link that shows the bill without signing in: only the hash can be looked up, the secret is kept encrypted
    -- (share_token_sealed) so the landlord can copy the same link again.
    share_token_hash        text        UNIQUE CHECK (char_length(share_token_hash) = 64),
    share_token_sealed      text,
    share_expires_at        timestamptz,
    -- Reminders already sent (a reminder goes out once, so the daily command can run again safely).
    due_soon_reminded_on    date,
    overdue_reminded_on     date,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    CHECK (period_end >= period_start),
    CHECK ((status = 'draft') = (sent_at IS NULL)),
    CHECK ((status = 'draft') = (sent_via IS NULL)),
    CHECK ((status = 'paid') = (paid_at IS NOT NULL)),
    CHECK ((share_token_hash IS NULL) = (share_expires_at IS NULL))
);
-- One bill per contract and period.
CREATE UNIQUE INDEX invoices_contract_period_key ON invoices (contract_id, period_start);
CREATE INDEX invoices_contract_list_idx ON invoices (contract_id, period_start DESC, id DESC);
-- The reminders look for sent bills by due date.
CREATE INDEX invoices_status_due_idx ON invoices (status, due_at);
CREATE INDEX invoices_created_by_idx ON invoices (created_by_id) WHERE created_by_id IS NOT NULL;

-- The lines of a bill, written when it is made so that later price changes never rewrite an old bill.
CREATE TABLE invoice_items (
    id             uuid         PRIMARY KEY DEFAULT uuidv7(),
    invoice_id     uuid         NOT NULL REFERENCES invoices (id) ON DELETE CASCADE,
    -- rent, a charge kind (service, electricity, water...) or other.
    kind           text         NOT NULL CHECK (kind IN ('rent', 'service', 'electricity', 'water', 'internet', 'parking', 'cleaning', 'other')),
    description    text         NOT NULL CHECK (char_length(description) BETWEEN 1 AND 200),
    quantity       numeric(12,2) NOT NULL CHECK (quantity >= 0),
    unit_price_vnd bigint       NOT NULL CHECK (unit_price_vnd BETWEEN 0 AND 1000000000000),
    amount_vnd     bigint       NOT NULL CHECK (amount_vnd BETWEEN 0 AND 1000000000000),
    display_order  smallint     NOT NULL
);
CREATE INDEX invoice_items_invoice_idx ON invoice_items (invoice_id, display_order);

-- History of the electricity and water meters of a room: what the landlord read when a bill was made. reading_value is
-- the index of the meter; consumption is what was used since the reading before, which the bill charges for.
CREATE TABLE meter_readings (
    id             uuid         PRIMARY KEY DEFAULT uuidv7(),
    -- The room's charge the meter belongs to (a charge that was removed stays here, soft deleted).
    charge_id      uuid         NOT NULL REFERENCES charges (id) ON DELETE CASCADE,
    invoice_id     uuid         NOT NULL REFERENCES invoices (id) ON DELETE CASCADE,
    read_on        date         NOT NULL,
    reading_value  numeric(12,2) NOT NULL CHECK (reading_value >= 0),
    consumption    numeric(12,2) NOT NULL CHECK (consumption >= 0),
    -- Price per kWh or cubic meter charged for it on that bill.
    unit_price_vnd bigint       NOT NULL CHECK (unit_price_vnd BETWEEN 0 AND 1000000000000),
    recorded_by_id uuid         REFERENCES users (id) ON DELETE SET NULL,
    created_at     timestamptz  NOT NULL DEFAULT now(),
    UNIQUE (invoice_id, charge_id)
);
CREATE INDEX meter_readings_charge_idx ON meter_readings (charge_id, read_on DESC, id DESC);

-- +goose Down
DROP TABLE meter_readings;
DROP TABLE invoice_items;
DROP TABLE invoices;
