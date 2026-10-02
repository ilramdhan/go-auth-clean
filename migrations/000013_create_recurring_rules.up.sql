-- Transaksi berulang (PRD F8). Worker membuat transaksi idempotent per
-- (recurring_rule_id, occurrence_date).
CREATE TABLE recurring_rules (
    id            UUID        PRIMARY KEY,
    user_id       UUID        NOT NULL,
    account_id    UUID        NOT NULL,
    category_id   UUID        NOT NULL REFERENCES categories (id),
    type          TEXT        NOT NULL CHECK (type IN ('income', 'expense')),
    amount        BIGINT      NOT NULL CHECK (amount > 0),
    currency      CHAR(3)     NOT NULL REFERENCES currencies (code),
    note          TEXT        NOT NULL DEFAULT '' CHECK (char_length(note) <= 255),
    frequency     TEXT        NOT NULL CHECK (frequency IN ('daily', 'weekly', 'monthly', 'yearly')),
    interval_n    SMALLINT    NOT NULL DEFAULT 1 CHECK (interval_n BETWEEN 1 AND 365),
    by_month_day  SMALLINT    NOT NULL CHECK (by_month_day BETWEEN 1 AND 31),
    start_date    DATE        NOT NULL,
    end_date      DATE        CHECK (end_date IS NULL OR end_date >= start_date),
    next_run_date DATE        NOT NULL,
    last_run_date DATE,
    status        TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'ended')),
    pause_reason  TEXT        NOT NULL DEFAULT '' CHECK (char_length(pause_reason) <= 255),
    version       INTEGER     NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,
    CONSTRAINT rr_account_fk FOREIGN KEY (account_id, user_id) REFERENCES accounts (id, user_id)
);
CREATE INDEX recurring_due_idx ON recurring_rules (next_run_date, id) WHERE status = 'active' AND deleted_at IS NULL;
CREATE INDEX recurring_user_idx ON recurring_rules (user_id, created_at DESC) WHERE deleted_at IS NULL;

ALTER TABLE transactions
    ADD COLUMN recurring_rule_id UUID REFERENCES recurring_rules (id),
    ADD COLUMN occurrence_date   DATE,
    ADD COLUMN import_hash       BYTEA,
    ADD CONSTRAINT tx_recurring_pair CHECK ((recurring_rule_id IS NULL) = (occurrence_date IS NULL));
ALTER TABLE transactions DROP CONSTRAINT transactions_source_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_source_check
    CHECK (source IN ('manual', 'transfer_fee', 'recurring', 'import'));
-- idempotency worker: satu transaksi per rule per tanggal (termasuk yang sudah dihapus user)
CREATE UNIQUE INDEX tx_recurring_occurrence_uq ON transactions (recurring_rule_id, occurrence_date)
    WHERE recurring_rule_id IS NOT NULL;
-- dedupe import CSV per user
CREATE UNIQUE INDEX tx_import_hash_uq ON transactions (user_id, import_hash)
    WHERE import_hash IS NOT NULL AND deleted_at IS NULL;
