-- 2FA TOTP. Secret DIENKRIPSI (AES-256-GCM, AAD = user id), bukan di-hash,
-- karena perlu dibaca untuk verifikasi. last_used_step mencegah replay kode.
CREATE TABLE user_mfa (
    user_id          UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    secret_encrypted TEXT        NOT NULL,
    status           VARCHAR(10) NOT NULL CHECK (status IN ('pending', 'enabled')),
    last_used_step   BIGINT      NOT NULL DEFAULT 0 CHECK (last_used_step >= 0),
    enabled_at       TIMESTAMPTZ NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_mfa_enabled_chk CHECK ((status = 'enabled') = (enabled_at IS NOT NULL))
);

-- Recovery code sekali pakai: hanya HMAC-SHA256(pepper, code) yang disimpan.
CREATE TABLE mfa_recovery_codes (
    id         UUID PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  BYTEA       NOT NULL CHECK (octet_length(code_hash) = 32),
    used_at    TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX mfa_recovery_codes_user_hash_uq ON mfa_recovery_codes (user_id, code_hash);
