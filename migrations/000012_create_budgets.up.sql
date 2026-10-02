-- Budget per kategori expense per bulan (PRD F7). spent dihitung saat dibaca.
CREATE TABLE budgets (
    id                  UUID        PRIMARY KEY,
    user_id             UUID        NOT NULL,
    category_id         UUID        NOT NULL REFERENCES categories (id),
    period_month        DATE        NOT NULL CHECK (EXTRACT(DAY FROM period_month) = 1),
    amount              BIGINT      NOT NULL CHECK (amount > 0),
    currency            CHAR(3)     NOT NULL REFERENCES currencies (code),
    alert_threshold_pct SMALLINT    NOT NULL DEFAULT 80 CHECK (alert_threshold_pct BETWEEN 1 AND 100),
    last_alert_level    TEXT        CHECK (last_alert_level IN ('warning', 'exceeded')),
    version             INTEGER     NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at          TIMESTAMPTZ
);
CREATE UNIQUE INDEX budgets_user_cat_month_uq ON budgets (user_id, category_id, period_month) WHERE deleted_at IS NULL;
CREATE INDEX budgets_month_idx ON budgets (period_month, id) WHERE deleted_at IS NULL;
