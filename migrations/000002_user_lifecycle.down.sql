DROP INDEX IF EXISTS sessions_user_active_idx;
DROP INDEX IF EXISTS sessions_expires_at_idx;
ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_revoked_reason_chk;
ALTER TABLE sessions DROP COLUMN IF EXISTS revoked_reason;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_full_name_len_chk;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_deleted_consistency_chk;
ALTER TABLE users DROP COLUMN IF EXISTS last_login_at;
ALTER TABLE users ALTER COLUMN status SET DEFAULT 'active';
