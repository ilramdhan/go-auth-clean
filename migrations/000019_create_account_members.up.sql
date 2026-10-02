-- Shared wallet (PRD F16): owner membagikan akun ke user lain.
CREATE TABLE account_members (
    account_id UUID        NOT NULL,
    owner_id   UUID        NOT NULL,
    member_id  UUID        NOT NULL,
    role       TEXT        NOT NULL CHECK (role IN ('viewer', 'editor')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, member_id),
    CONSTRAINT account_members_not_owner CHECK (owner_id <> member_id),
    CONSTRAINT account_members_account_fk FOREIGN KEY (account_id, owner_id) REFERENCES accounts (id, user_id) ON DELETE CASCADE
);
CREATE INDEX account_members_member_idx ON account_members (member_id);
