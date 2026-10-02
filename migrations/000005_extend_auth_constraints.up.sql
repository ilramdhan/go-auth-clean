-- P2: event audit & alasan revoke baru (2FA, OAuth, RBAC, API key).
ALTER TABLE audit_logs DROP CONSTRAINT audit_event_type_chk;
ALTER TABLE audit_logs ADD CONSTRAINT audit_event_type_chk CHECK (event_type IN (
    'user_registered', 'email_verified', 'otp_failed',
    'login_succeeded', 'login_failed', 'account_locked',
    'refresh_token_reuse_detected',
    'logout', 'logout_all', 'session_revoked',
    'password_changed', 'password_reset_requested', 'password_reset',
    'profile_updated', 'account_deleted',
    'mfa_enabled', 'mfa_disabled', 'mfa_failed', 'mfa_recovery_code_used', 'mfa_recovery_codes_regenerated',
    'oauth_login', 'oauth_linked',
    'user_suspended', 'user_activated', 'role_granted', 'role_revoked',
    'api_key_created', 'api_key_revoked'
));

ALTER TABLE sessions DROP CONSTRAINT sessions_revoked_reason_chk;
ALTER TABLE sessions ADD CONSTRAINT sessions_revoked_reason_chk CHECK (
    revoked_reason IS NULL OR revoked_reason IN (
        'logout', 'logout_all', 'password_changed', 'password_reset',
        'reuse_detected', 'session_revoked', 'account_deleted', 'session_limit',
        'user_suspended', 'mfa_changed'
    )
);

-- Admin list user: keyset (created_at, id) DESC + pencarian substring email/nama.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX users_created_id_idx ON users (created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX users_email_trgm_idx ON users USING gin ((email::text) gin_trgm_ops) WHERE deleted_at IS NULL;
CREATE INDEX users_full_name_trgm_idx ON users USING gin (full_name gin_trgm_ops) WHERE deleted_at IS NULL;
