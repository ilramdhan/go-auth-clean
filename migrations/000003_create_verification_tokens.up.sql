-- OTP generik per purpose. Kode tidak pernah disimpan: hanya HMAC-SHA256(pepper, code).
CREATE TABLE verification_tokens (
    id             UUID PRIMARY KEY,
    user_id        UUID         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose        VARCHAR(30)  NOT NULL
        CHECK (purpose IN ('email_verification', 'password_reset')),
    target         CITEXT       NOT NULL,
    code_hash      BYTEA        NOT NULL CHECK (octet_length(code_hash) = 32),
    attempts       INT          NOT NULL DEFAULT 0,
    max_attempts   INT          NOT NULL DEFAULT 5,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at     TIMESTAMPTZ  NOT NULL,
    consumed_at    TIMESTAMPTZ  NULL,
    invalidated_at TIMESTAMPTZ  NULL,

    CONSTRAINT vt_attempts_chk CHECK (attempts >= 0 AND max_attempts > 0 AND attempts <= max_attempts),
    CONSTRAINT vt_expiry_chk CHECK (expires_at > created_at)
);

-- Hanya satu token aktif per (user, purpose). now() tidak boleh dipakai di
-- predicate index, jadi app wajib meng-invalidate token lama saat membuat yang baru.
CREATE UNIQUE INDEX vt_one_active_per_purpose_uq
    ON verification_tokens (user_id, purpose)
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;

-- Untuk cooldown & kuota harian resend.
CREATE INDEX vt_user_purpose_created_idx ON verification_tokens (user_id, purpose, created_at DESC);
CREATE INDEX vt_expires_at_idx ON verification_tokens (expires_at);
