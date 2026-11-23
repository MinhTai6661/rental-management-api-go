-- +goose Up
-- The plan catalog. A limit that is NULL means unlimited; a free plan costs 0. Plans are never deleted: a plan that is
-- no longer offered is switched off (is_active), so the subscriptions to it keep working.
CREATE TABLE plans (
    id                        uuid        PRIMARY KEY DEFAULT uuidv7(),
    code                      text        NOT NULL CONSTRAINT plans_code_key UNIQUE CHECK (code ~ '^[a-z0-9_]{1,50}$'),
    name                      text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    description               text        CHECK (char_length(description) <= 1000),
    audience                  text        NOT NULL CHECK (audience IN ('landlord', 'tenant')),
    -- The price of one billing_interval in whole VND.
    price_vnd                 bigint      NOT NULL DEFAULT 0 CHECK (price_vnd BETWEEN 0 AND 1000000000000),
    billing_interval          text        NOT NULL DEFAULT 'month' CHECK (billing_interval IN ('month', 'year')),
    max_rooms                 integer     CHECK (max_rooms >= 0),
    max_buildings             integer     CHECK (max_buildings >= 0),
    -- Pictures one room (or one building) may have.
    max_images_per_room       integer     CHECK (max_images_per_room BETWEEN 1 AND 100),
    -- Running contracts with a tenant per landlord.
    max_active_contracts      integer     CHECK (max_active_contracts >= 0),
    -- Registrations waiting for an answer per tenant.
    max_pending_registrations integer     CHECK (max_pending_registrations >= 0),
    -- Higher comes first in find rooms.
    search_priority           integer     NOT NULL DEFAULT 0 CHECK (search_priority BETWEEN 0 AND 65535),
    features                  text[]      NOT NULL DEFAULT '{}'
                                          CHECK (features <@ ARRAY['auto_invoicing', 'vip_badge', 'priority_support']),
    -- The plan an account of the audience is on while it has no subscription.
    is_default                boolean     NOT NULL DEFAULT false,
    is_active                 boolean     NOT NULL DEFAULT true,
    display_order             integer     NOT NULL DEFAULT 0 CHECK (display_order BETWEEN 0 AND 65535),
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now()
);
-- One default plan per audience.
CREATE UNIQUE INDEX plans_audience_default_key ON plans (audience) WHERE is_default;
CREATE INDEX plans_audience_listing_idx ON plans (audience, is_active, display_order, price_vnd);

-- A user's plan. A pending row is a request for a paid plan waiting for an admin (there is no payment gateway yet).
CREATE TABLE subscriptions (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    plan_id       uuid        NOT NULL REFERENCES plans (id) ON DELETE RESTRICT,
    -- The audience of the plan, copied so "one per audience" can be a constraint. A person who is both landlord and
    -- tenant has a plan on each side.
    audience      text        NOT NULL CHECK (audience IN ('landlord', 'tenant')),
    status        text        NOT NULL CHECK (status IN ('pending', 'active', 'canceled', 'expired')),
    starts_at     timestamptz NOT NULL,
    -- NULL = never lapses.
    ends_at       timestamptz,
    canceled_at   timestamptz,
    -- The admin who handed the plan out; NULL when the user took it themselves.
    granted_by_id uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX subscriptions_user_audience_active_key ON subscriptions (user_id, audience) WHERE status = 'active';
CREATE UNIQUE INDEX subscriptions_user_audience_pending_key ON subscriptions (user_id, audience) WHERE status = 'pending';
-- The admin's list by status, newest first.
CREATE INDEX subscriptions_status_created_idx ON subscriptions (status, created_at DESC, id DESC);
-- The daily expiry looks only at running subscriptions that have an end.
CREATE INDEX subscriptions_expiry_idx ON subscriptions (ends_at) WHERE status = 'active' AND ends_at IS NOT NULL;
CREATE INDEX subscriptions_plan_idx ON subscriptions (plan_id);

-- +goose Down
DROP TABLE subscriptions;
DROP TABLE plans;
