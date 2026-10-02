-- Target tabungan (PRD F13).
CREATE TABLE savings_goals (
    id            UUID        PRIMARY KEY,
    user_id       UUID        NOT NULL,
    name          CITEXT      NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),
    target_amount BIGINT      NOT NULL CHECK (target_amount > 0),
    currency      CHAR(3)     NOT NULL REFERENCES currencies (code),
    target_date   DATE,
    account_id    UUID,
    status        TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'achieved', 'archived')),
    version       INTEGER     NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,
    CONSTRAINT goals_account_fk FOREIGN KEY (account_id, user_id) REFERENCES accounts (id, user_id)
);
CREATE UNIQUE INDEX savings_goals_user_name_uq ON savings_goals (user_id, name) WHERE deleted_at IS NULL;

CREATE TABLE goal_contributions (
    id                UUID        PRIMARY KEY,
    goal_id           UUID        NOT NULL REFERENCES savings_goals (id) ON DELETE CASCADE,
    user_id           UUID        NOT NULL,
    amount            BIGINT      NOT NULL CHECK (amount <> 0),
    contribution_date DATE        NOT NULL,
    transfer_id       UUID        REFERENCES transfers (id),
    note              TEXT        NOT NULL DEFAULT '' CHECK (char_length(note) <= 255),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX goal_contributions_goal_idx ON goal_contributions (goal_id, contribution_date DESC, id DESC);
CREATE UNIQUE INDEX goal_contributions_transfer_uq ON goal_contributions (goal_id, transfer_id) WHERE transfer_id IS NOT NULL;
