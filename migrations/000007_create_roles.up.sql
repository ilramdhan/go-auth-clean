-- RBAC. Role 'user' implisit untuk semua user; user_roles hanya menyimpan
-- grant eksplisit (mis. admin).
CREATE TABLE roles (
    id          SMALLINT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT,
    CONSTRAINT roles_name_chk CHECK (name ~ '^[a-z][a-z0-9_]{1,31}$')
);

INSERT INTO roles (id, name, description) VALUES
    (1, 'user', 'Pengguna biasa'),
    (2, 'admin', 'Administrator');

CREATE TABLE user_roles (
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role_id    SMALLINT    NOT NULL REFERENCES roles (id) ON DELETE RESTRICT,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_by UUID        NULL REFERENCES users (id) ON DELETE SET NULL,
    PRIMARY KEY (user_id, role_id)
);
CREATE INDEX user_roles_role_idx ON user_roles (role_id);
