-- Tag many-to-many transaksi (PRD F9).
CREATE TABLE tags (
    id         UUID        PRIMARY KEY,
    user_id    UUID        NOT NULL,
    name       CITEXT      NOT NULL CHECK (char_length(name) BETWEEN 1 AND 30),
    color      TEXT        NOT NULL DEFAULT '' CHECK (color = '' OR color ~ '^#[0-9A-F]{6}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT tags_user_name_uq UNIQUE (user_id, name)
);
CREATE TABLE transaction_tags (
    transaction_id UUID NOT NULL REFERENCES transactions (id) ON DELETE CASCADE,
    tag_id         UUID NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (transaction_id, tag_id)
);
CREATE INDEX transaction_tags_tag_idx ON transaction_tags (tag_id);
