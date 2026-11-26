-- +goose Up
-- The accounts of other services (Google, Zalo) that open an account here. A row is never deleted when the person
-- unlinks it: unlinked_at stays set so that provider cannot sign in again by itself (only from a password session).
CREATE TABLE oauth_accounts (
    id               uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id          uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider         text        NOT NULL CHECK (provider IN ('google', 'zalo')),
    provider_user_id text        NOT NULL CHECK (length(provider_user_id) BETWEEN 1 AND 255),
    linked_at        timestamptz NOT NULL,
    unlinked_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    -- A provider account opens one account here, and an account has one account of each provider.
    CONSTRAINT oauth_accounts_provider_account_key UNIQUE (provider, provider_user_id),
    CONSTRAINT oauth_accounts_user_provider_key UNIQUE (user_id, provider)
);

-- +goose Down
DROP TABLE oauth_accounts;
