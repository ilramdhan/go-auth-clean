-- Hutang (payable) & piutang (receivable) beserta cicilan (PRD F14).
CREATE TABLE debts (
    id           UUID        PRIMARY KEY,
    user_id      UUID        NOT NULL,
    direction    TEXT        NOT NULL CHECK (direction IN ('payable', 'receivable')),
    counterparty TEXT        NOT NULL CHECK (char_length(counterparty) BETWEEN 1 AND 100),
    principal    BIGINT      NOT NULL CHECK (principal > 0),
    currency     CHAR(3)     NOT NULL REFERENCES currencies (code),
    start_date   DATE        NOT NULL,
    due_date     DATE,
    note         TEXT        NOT NULL DEFAULT '' CHECK (char_length(note) <= 255),
    status       TEXT        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'settled')),
    version      INTEGER     NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ
);
CREATE INDEX debts_user_idx ON debts (user_id, status, created_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE debt_payments (
    id             UUID        PRIMARY KEY,
    debt_id        UUID        NOT NULL REFERENCES debts (id) ON DELETE CASCADE,
    user_id        UUID        NOT NULL,
    amount         BIGINT      NOT NULL CHECK (amount > 0),
    payment_date   DATE        NOT NULL,
    transaction_id UUID        UNIQUE REFERENCES transactions (id),
    note           TEXT        NOT NULL DEFAULT '' CHECK (char_length(note) <= 255),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX debt_payments_debt_idx ON debt_payments (debt_id, payment_date DESC, id DESC);
