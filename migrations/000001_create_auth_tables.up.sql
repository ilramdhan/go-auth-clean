CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id                    UUID PRIMARY KEY,
    email                 CITEXT       NOT NULL,
    password_hash         TEXT         NOT NULL,
    full_name             VARCHAR(100) NOT NULL,
    status                VARCHAR(30)  NOT NULL DEFAULT 'active'
        CHECK (status IN ('pending_verification', 'active', 'suspended', 'deleted')),
    failed_login_attempts INT          NOT NULL DEFAULT 0 CHECK (failed_login_attempts >= 0),
    locked_until          TIMESTAMPTZ  NULL,
    email_verified_at     TIMESTAMPTZ  NULL,
    password_changed_at   TIMESTAMPTZ  NULL,
    created_at            TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at            TIMESTAMPTZ  NULL
);

-- Email unik hanya untuk user yang belum di-soft-delete.
CREATE UNIQUE INDEX users_email_key ON users (email) WHERE deleted_at IS NULL;

CREATE TABLE sessions (
    id                 UUID PRIMARY KEY,
    user_id            UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id          UUID        NOT NULL,
    refresh_token_hash BYTEA       NOT NULL,
    client_ip          VARCHAR(45) NOT NULL,
    user_agent         TEXT        NOT NULL,
    expires_at         TIMESTAMPTZ NOT NULL,
    rotated_at         TIMESTAMPTZ NULL,
    revoked_at         TIMESTAMPTZ NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX sessions_refresh_token_hash_key ON sessions (refresh_token_hash);
CREATE INDEX sessions_user_id_idx ON sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_family_id_idx ON sessions (family_id);
