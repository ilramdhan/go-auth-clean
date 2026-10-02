-- Lifecycle user: register sekarang mulai dari pending_verification.
-- User lama (status active) tidak diubah (grandfathered).
ALTER TABLE users ALTER COLUMN status SET DEFAULT 'pending_verification';
ALTER TABLE users ADD COLUMN last_login_at TIMESTAMPTZ NULL;
ALTER TABLE users ADD CONSTRAINT users_deleted_consistency_chk
    CHECK ((status = 'deleted') = (deleted_at IS NOT NULL));
ALTER TABLE users ADD CONSTRAINT users_full_name_len_chk
    CHECK (char_length(full_name) BETWEEN 1 AND 100);

-- Alasan revoke untuk audit/forensik.
ALTER TABLE sessions ADD COLUMN revoked_reason VARCHAR(30) NULL;
ALTER TABLE sessions ADD CONSTRAINT sessions_revoked_reason_chk CHECK (
    revoked_reason IS NULL OR revoked_reason IN (
        'logout', 'logout_all', 'password_changed', 'password_reset',
        'reuse_detected', 'session_revoked', 'account_deleted', 'session_limit'
    )
);

-- Dipakai cleanup job (hapus session expired) dan list session aktif.
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
CREATE INDEX sessions_user_active_idx ON sessions (user_id, created_at DESC)
    WHERE revoked_at IS NULL AND rotated_at IS NULL;
