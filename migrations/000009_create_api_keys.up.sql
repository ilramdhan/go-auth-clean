-- API key: format gac_<env>_<prefix8>_<secret32>. prefix untuk lookup,
-- secret hanya disimpan sebagai SHA-256 (entropi tinggi, tidak perlu bcrypt).
CREATE TABLE api_keys (
    id           UUID PRIMARY KEY,
    user_id      UUID         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         VARCHAR(100) NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    prefix       CHAR(8)      NOT NULL,
    secret_hash  BYTEA        NOT NULL CHECK (octet_length(secret_hash) = 32),
    scopes       TEXT[]       NOT NULL CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY['read', 'write']::text[]),
    expires_at   TIMESTAMPTZ  NULL,
    last_used_at TIMESTAMPTZ  NULL,
    revoked_at   TIMESTAMPTZ  NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT api_keys_prefix_key UNIQUE (prefix)
);
CREATE INDEX api_keys_user_idx ON api_keys (user_id, created_at DESC);
