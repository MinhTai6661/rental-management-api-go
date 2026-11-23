-- +goose Up
CREATE TABLE roles (
    code      text     PRIMARY KEY CHECK (code ~ '^[a-z_]+$'),
    seniority smallint NOT NULL UNIQUE,
    is_staff  boolean  NOT NULL
);

CREATE TABLE permissions (
    code        text PRIMARY KEY CHECK (code ~ '^[a-z_]+\.[a-z_]+$'),
    description text NOT NULL
);

CREATE TABLE role_permissions (
    role_code       text NOT NULL REFERENCES roles (code) ON DELETE CASCADE,
    permission_code text NOT NULL REFERENCES permissions (code) ON DELETE CASCADE,
    PRIMARY KEY (role_code, permission_code)
);

CREATE TABLE users (
    id                 uuid        PRIMARY KEY DEFAULT uuidv7(),
    -- Nullable so accounts of services that give no email (Zalo) fit later.
    email              text        UNIQUE CHECK (email = lower(email)),
    password_hash      text,
    full_name          text        NOT NULL CHECK (length(full_name) BETWEEN 1 AND 255),
    phone_number       text        CHECK (length(phone_number) <= 20),
    status             text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    ward_code          integer     REFERENCES wards (code) ON DELETE SET NULL,
    address_detail     text        CHECK (length(address_detail) <= 500),
    last_active_role   text        REFERENCES roles (code),
    email_verified_at  timestamptz,
    terms_version      text,
    terms_accepted_at  timestamptz,
    accepts_invitations boolean    NOT NULL DEFAULT true,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX users_ward_code_idx ON users (ward_code);
CREATE INDEX users_status_idx ON users (status);

CREATE TABLE user_roles (
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role_code   text        NOT NULL REFERENCES roles (code) ON DELETE RESTRICT,
    assigned_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role_code)
);
CREATE INDEX user_roles_role_code_idx ON user_roles (role_code);

-- One row per login: it is the family of refresh tokens, and its id is the `sid` claim of the access token.
CREATE TABLE user_sessions (
    id             uuid        PRIMARY KEY,
    user_id        uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    active_role    text        NOT NULL REFERENCES roles (code),
    user_agent     text,
    ip_address     inet,
    created_at     timestamptz NOT NULL,
    last_used_at   timestamptz NOT NULL,
    revoked_at     timestamptz,
    revoked_reason text        CHECK (revoked_reason IN
        ('logout', 'logout_all', 'role_switch', 'password_change', 'reuse_detected', 'account_inactive'))
);
CREATE INDEX user_sessions_open_idx ON user_sessions (user_id) WHERE revoked_at IS NULL;

-- Only the SHA-256 of the token is stored. A token is used once: rotating it sets rotated_at and adds a child.
CREATE TABLE refresh_tokens (
    id         uuid        PRIMARY KEY,
    session_id uuid        NOT NULL REFERENCES user_sessions (id) ON DELETE CASCADE,
    token_hash bytea       NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    parent_id  uuid        REFERENCES refresh_tokens (id) ON DELETE SET NULL,
    expires_at timestamptz NOT NULL,
    rotated_at timestamptz,
    created_at timestamptz NOT NULL
);
CREATE INDEX refresh_tokens_session_id_idx ON refresh_tokens (session_id);
CREATE INDEX refresh_tokens_expires_at_idx ON refresh_tokens (expires_at);

-- +goose Down
DROP TABLE refresh_tokens;
DROP TABLE user_sessions;
DROP TABLE user_roles;
DROP TABLE users;
DROP TABLE role_permissions;
DROP TABLE permissions;
DROP TABLE roles;
