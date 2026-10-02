-- Audit log append-only untuk event keamanan. metadata tidak boleh berisi
-- password/token/OTP. user_id nullable (login gagal untuk email tak dikenal
-- hanya menyimpan email_hash).
CREATE TABLE audit_logs (
    id          UUID PRIMARY KEY,
    user_id     UUID         NULL REFERENCES users (id) ON DELETE SET NULL,
    event_type  VARCHAR(40)  NOT NULL,
    outcome     VARCHAR(10)  NOT NULL DEFAULT 'success',
    email_hash  BYTEA        NULL,
    session_id  UUID         NULL,
    ip_address  INET         NULL,
    user_agent  TEXT         NULL,
    request_id  VARCHAR(128) NULL,
    metadata    JSONB        NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT audit_event_type_chk CHECK (event_type IN (
        'user_registered', 'email_verified', 'otp_failed',
        'login_succeeded', 'login_failed', 'account_locked',
        'refresh_token_reuse_detected',
        'logout', 'logout_all', 'session_revoked',
        'password_changed', 'password_reset_requested', 'password_reset',
        'profile_updated', 'account_deleted'
    )),
    CONSTRAINT audit_outcome_chk CHECK (outcome IN ('success', 'failure')),
    CONSTRAINT audit_metadata_obj_chk CHECK (jsonb_typeof(metadata) = 'object')
);

-- Keyset pagination: (occurred_at, id) DESC per user.
CREATE INDEX audit_logs_user_time_idx ON audit_logs (user_id, occurred_at DESC, id DESC);
CREATE INDEX audit_logs_event_time_idx ON audit_logs (event_type, occurred_at DESC);
CREATE INDEX audit_logs_ip_time_idx ON audit_logs (ip_address, occurred_at DESC) WHERE outcome = 'failure';
