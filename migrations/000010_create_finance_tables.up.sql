-- Finance core (PRD-02 MVP). user_id adalah referensi logis ke users milik
-- bounded context auth: sengaja TANPA foreign key agar finance bisa dipisah.
-- ID = UUIDv7 dibuat di app layer (tidak ada DEFAULT gen_random_uuid()).
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS citext;

-- ===== Reference data (harus sama dengan registry shared/money) =====
CREATE TABLE currencies (
    code       CHAR(3)  PRIMARY KEY CHECK (code ~ '^[A-Z]{3}$'),
    name       TEXT     NOT NULL,
    minor_unit SMALLINT NOT NULL CHECK (minor_unit BETWEEN 0 AND 4),
    symbol     TEXT     NOT NULL
);
INSERT INTO currencies (code, name, minor_unit, symbol) VALUES
    ('IDR', 'Indonesian Rupiah', 0, 'Rp'),
    ('USD', 'US Dollar', 2, '$'),
    ('SGD', 'Singapore Dollar', 2, 'S$'),
    ('JPY', 'Japanese Yen', 0, '¥'),
    ('EUR', 'Euro', 2, '€');

-- ===== User settings (dibuat lazily) =====
CREATE TABLE user_settings (
    user_id       UUID        PRIMARY KEY,
    base_currency CHAR(3)     NOT NULL DEFAULT 'IDR' REFERENCES currencies (code),
    timezone      TEXT        NOT NULL DEFAULT 'Asia/Jakarta' CHECK (char_length(timezone) BETWEEN 1 AND 64),
    week_start    SMALLINT    NOT NULL DEFAULT 1 CHECK (week_start BETWEEN 0 AND 6),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===== Accounts =====
CREATE TABLE accounts (
    id              UUID        PRIMARY KEY,
    user_id         UUID        NOT NULL,
    name            CITEXT      NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),
    type            TEXT        NOT NULL CHECK (type IN ('cash', 'bank', 'ewallet', 'credit_card')),
    currency        CHAR(3)     NOT NULL REFERENCES currencies (code),
    initial_balance BIGINT      NOT NULL DEFAULT 0,
    current_balance BIGINT      NOT NULL DEFAULT 0,
    allow_negative  BOOLEAN     NOT NULL DEFAULT false,
    version         INTEGER     NOT NULL DEFAULT 1 CHECK (version > 0),
    archived_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,
    -- defense in depth: invariant saldo juga dijaga DB
    CONSTRAINT accounts_non_negative CHECK (allow_negative OR current_balance >= 0),
    -- target FK komposit (id, user_id): DB menolak referensi lintas user
    CONSTRAINT accounts_id_user_uk UNIQUE (id, user_id)
);
CREATE UNIQUE INDEX accounts_user_name_uq ON accounts (user_id, name) WHERE deleted_at IS NULL;
CREATE INDEX accounts_user_idx ON accounts (user_id) WHERE deleted_at IS NULL;

-- ===== Categories (user_id NULL = kategori system) =====
CREATE TABLE categories (
    id         UUID        PRIMARY KEY,
    user_id    UUID,
    parent_id  UUID        REFERENCES categories (id),
    type       TEXT        NOT NULL CHECK (type IN ('income', 'expense')),
    name       CITEXT      NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),
    icon       TEXT        NOT NULL DEFAULT '' CHECK (char_length(icon) <= 30),
    color      TEXT        NOT NULL DEFAULT '' CHECK (color = '' OR color ~ '^#[0-9A-F]{6}$'),
    is_system  BOOLEAN     NOT NULL GENERATED ALWAYS AS (user_id IS NULL) STORED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT categories_no_self_parent CHECK (parent_id IS NULL OR parent_id <> id)
);
-- NULLS NOT DISTINCT (PG15+) supaya kategori root & system juga unik
CREATE UNIQUE INDEX categories_uq ON categories (user_id, type, parent_id, name) NULLS NOT DISTINCT
    WHERE deleted_at IS NULL;
CREATE INDEX categories_user_idx ON categories (user_id) WHERE deleted_at IS NULL;
CREATE INDEX categories_parent_idx ON categories (parent_id) WHERE deleted_at IS NULL;

-- ===== Transactions (income/expense; amount selalu positif, arah dari type) =====
CREATE TABLE transactions (
    id               UUID        PRIMARY KEY,
    user_id          UUID        NOT NULL,
    account_id       UUID        NOT NULL,
    category_id      UUID        NOT NULL REFERENCES categories (id),
    type             TEXT        NOT NULL CHECK (type IN ('income', 'expense')),
    amount           BIGINT      NOT NULL CHECK (amount > 0),
    currency         CHAR(3)     NOT NULL REFERENCES currencies (code),
    transaction_date DATE        NOT NULL CHECK (transaction_date >= DATE '1970-01-01'),
    note             TEXT        NOT NULL DEFAULT '' CHECK (char_length(note) <= 255),
    source           TEXT        NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'transfer_fee')),
    version          INTEGER     NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ,
    CONSTRAINT tx_account_fk FOREIGN KEY (account_id, user_id) REFERENCES accounts (id, user_id)
);
CREATE INDEX tx_user_date_idx ON transactions (user_id, transaction_date DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX tx_user_account_date_idx ON transactions (user_id, account_id, transaction_date DESC) WHERE deleted_at IS NULL;
CREATE INDEX tx_user_category_date_idx ON transactions (user_id, category_id, transaction_date) WHERE deleted_at IS NULL;
CREATE INDEX tx_note_trgm_idx ON transactions USING gin (note gin_trgm_ops);

-- ===== Transfers (satu baris, dua leg; tidak masuk laporan income/expense) =====
CREATE TABLE transfers (
    id                 UUID        PRIMARY KEY,
    user_id            UUID        NOT NULL,
    from_account_id    UUID        NOT NULL,
    to_account_id      UUID        NOT NULL,
    amount             BIGINT      NOT NULL CHECK (amount > 0),
    fee_amount         BIGINT      NOT NULL DEFAULT 0 CHECK (fee_amount >= 0),
    currency           CHAR(3)     NOT NULL REFERENCES currencies (code),
    fee_transaction_id UUID        UNIQUE REFERENCES transactions (id),
    transfer_date      DATE        NOT NULL CHECK (transfer_date >= DATE '1970-01-01'),
    note               TEXT        NOT NULL DEFAULT '' CHECK (char_length(note) <= 255),
    version            INTEGER     NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,
    CONSTRAINT tr_from_fk FOREIGN KEY (from_account_id, user_id) REFERENCES accounts (id, user_id),
    CONSTRAINT tr_to_fk FOREIGN KEY (to_account_id, user_id) REFERENCES accounts (id, user_id),
    CONSTRAINT tr_distinct_accounts CHECK (from_account_id <> to_account_id),
    CONSTRAINT tr_fee_pair CHECK ((fee_amount = 0) = (fee_transaction_id IS NULL))
);
CREATE INDEX transfers_user_date_idx ON transfers (user_id, transfer_date DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX transfers_from_idx ON transfers (from_account_id) WHERE deleted_at IS NULL;
CREATE INDEX transfers_to_idx ON transfers (to_account_id) WHERE deleted_at IS NULL;

-- ===== Idempotency keys (POST /transactions, /transfers) =====
CREATE TABLE idempotency_keys (
    user_id         UUID        NOT NULL,
    key             TEXT        NOT NULL CHECK (char_length(key) BETWEEN 8 AND 128),
    request_method  TEXT        NOT NULL,
    request_path    TEXT        NOT NULL,
    request_hash    BYTEA       NOT NULL,
    status          TEXT        NOT NULL CHECK (status IN ('processing', 'completed')),
    response_status SMALLINT,
    response_body   BYTEA,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, key)
);
CREATE INDEX idempotency_expires_idx ON idempotency_keys (expires_at);
