-- +goose Up
-- Admin: soft-deleted and erased accounts, landlord applications and profiles, published legal documents and the
-- overrides of the system settings.
ALTER TABLE users ADD COLUMN erased_at timestamptz;
ALTER TABLE users ADD COLUMN deleted_at timestamptz;
CREATE INDEX users_deleted_at_idx ON users (deleted_at) WHERE deleted_at IS NOT NULL;

-- A removed account closes its sessions for a reason of its own.
ALTER TABLE user_sessions DROP CONSTRAINT user_sessions_revoked_reason_check;
ALTER TABLE user_sessions ADD CONSTRAINT user_sessions_revoked_reason_check CHECK (revoked_reason IN
    ('logout', 'logout_all', 'role_switch', 'password_change', 'reuse_detected', 'account_inactive',
     'account_deleted', 'account_erased', 'roles_changed'));

-- A tenant's request to become a landlord. It waits for an admin right away (no email step).
CREATE TABLE landlord_applications (
    id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id        uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    business_name  text        NOT NULL CHECK (char_length(business_name) BETWEEN 1 AND 150),
    phone          text        NOT NULL CHECK (char_length(phone) BETWEEN 1 AND 20),
    ward_code      integer     REFERENCES wards (code) ON DELETE SET NULL,
    address_detail text        CHECK (char_length(address_detail) <= 500),
    note           text        CHECK (char_length(note) <= 1000),
    status         text        NOT NULL DEFAULT 'pending_review' CHECK (status IN ('pending_review', 'approved', 'rejected')),
    reviewed_by_id uuid        REFERENCES users (id) ON DELETE SET NULL,
    reviewed_at    timestamptz,
    review_note    text        CHECK (char_length(review_note) <= 1000),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'pending_review') = (reviewed_at IS NULL))
);
-- One open application per account.
CREATE UNIQUE INDEX landlord_applications_open_key ON landlord_applications (user_id) WHERE status = 'pending_review';
CREATE INDEX landlord_applications_status_idx ON landlord_applications (status, created_at DESC, id DESC);
CREATE INDEX landlord_applications_user_idx ON landlord_applications (user_id, created_at DESC);

-- The business side of a landlord, apart from the personal profile on users.
CREATE TABLE landlord_profiles (
    user_id        uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    business_name  text        NOT NULL CHECK (char_length(business_name) BETWEEN 1 AND 150),
    phone          text        CHECK (char_length(phone) <= 20),
    ward_code      integer     REFERENCES wards (code) ON DELETE SET NULL,
    address_detail text        CHECK (char_length(address_detail) <= 500),
    description    text        CHECK (char_length(description) <= 2000),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- Every publication is a new row; a row is never changed or deleted.
CREATE TABLE legal_documents (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    kind            text        NOT NULL CHECK (kind IN ('terms', 'privacy')),
    version         text        NOT NULL CHECK (char_length(version) BETWEEN 1 AND 40),
    content         jsonb       NOT NULL CHECK (jsonb_typeof(content) = 'object'),
    change_summary  jsonb       NOT NULL CHECK (jsonb_typeof(change_summary) = 'object'),
    published_by_id uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kind, version)
);
CREATE INDEX legal_documents_kind_idx ON legal_documents (kind, created_at DESC, id DESC);
CREATE INDEX legal_documents_created_idx ON legal_documents (created_at DESC, id DESC);

-- Overrides of the defaults of the settings a super admin may change (the keys live in the application).
CREATE TABLE app_settings (
    key           text        PRIMARY KEY CHECK (char_length(key) BETWEEN 1 AND 100),
    value         jsonb       NOT NULL,
    updated_by_id uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE app_settings;
DROP TABLE legal_documents;
DROP TABLE landlord_profiles;
DROP TABLE landlord_applications;
ALTER TABLE user_sessions DROP CONSTRAINT user_sessions_revoked_reason_check;
ALTER TABLE user_sessions ADD CONSTRAINT user_sessions_revoked_reason_check CHECK (revoked_reason IN
    ('logout', 'logout_all', 'role_switch', 'password_change', 'reuse_detected', 'account_inactive'));
DROP INDEX users_deleted_at_idx;
ALTER TABLE users DROP COLUMN deleted_at;
ALTER TABLE users DROP COLUMN erased_at;
