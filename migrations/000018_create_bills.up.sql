-- Pengingat tagihan (PRD F15).
CREATE TABLE bills (
    id                 UUID        PRIMARY KEY,
    user_id            UUID        NOT NULL,
    name               CITEXT      NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),
    amount             BIGINT      NOT NULL CHECK (amount > 0),
    currency           CHAR(3)     NOT NULL REFERENCES currencies (code),
    account_id         UUID,
    category_id        UUID        REFERENCES categories (id),
    frequency          TEXT        NOT NULL CHECK (frequency IN ('once', 'weekly', 'monthly', 'yearly')),
    by_month_day       SMALLINT    NOT NULL CHECK (by_month_day BETWEEN 1 AND 31),
    next_due_date      DATE        NOT NULL,
    remind_days_before SMALLINT    NOT NULL DEFAULT 3 CHECK (remind_days_before BETWEEN 0 AND 30),
    last_reminded_for  DATE,
    status             TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'done')),
    version            INTEGER     NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,
    CONSTRAINT bills_account_fk FOREIGN KEY (account_id, user_id) REFERENCES accounts (id, user_id)
);
CREATE INDEX bills_user_due_idx ON bills (user_id, next_due_date) WHERE deleted_at IS NULL;
CREATE INDEX bills_remind_idx ON bills (next_due_date, id) WHERE status = 'active' AND deleted_at IS NULL;
