-- Identitas eksternal (OAuth/OIDC). Satu (provider, subject) = satu user.
CREATE TABLE user_identities (
    id         UUID PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider   VARCHAR(30) NOT NULL CHECK (provider ~ '^[a-z][a-z0-9_]{1,29}$'),
    subject    TEXT        NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 255),
    email      CITEXT      NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_identities_provider_subject_key UNIQUE (provider, subject),
    CONSTRAINT user_identities_user_provider_key UNIQUE (user_id, provider)
);

-- Kode sekali pakai (60 detik) dari callback OAuth ke frontend; ditukar
-- dengan token lewat POST /auth/oauth/exchange. Hanya hash yang disimpan.
CREATE TABLE oauth_login_codes (
    code_hash  BYTEA PRIMARY KEY CHECK (octet_length(code_hash) = 32),
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider   VARCHAR(30) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX oauth_login_codes_expires_idx ON oauth_login_codes (expires_at);
