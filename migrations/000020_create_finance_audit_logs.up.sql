-- Audit trail perubahan data finance (PRD F18). Append-only.
CREATE TABLE finance_audit_logs (
    id         UUID        PRIMARY KEY,
    user_id    UUID        NOT NULL,
    actor_id   UUID        NOT NULL,
    entity     TEXT        NOT NULL CHECK (char_length(entity) BETWEEN 1 AND 40),
    entity_id  UUID        NOT NULL,
    action     TEXT        NOT NULL CHECK (action IN ('create', 'update', 'delete')),
    before     JSONB,
    after      JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX finance_audit_user_idx ON finance_audit_logs (user_id, created_at DESC, id DESC);
CREATE INDEX finance_audit_entity_idx ON finance_audit_logs (user_id, entity, entity_id, created_at DESC);
