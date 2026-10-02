# How to make migrations
- Make sure already install ```go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest```
- All file migrations create in dir ```mkdir -p database/migrations```
- To create migrations use this command ```migrate create -ext sql -dir database/migrations -seq create_users_and_sessions_tables```
- It will create 2 file migrations up and down for example 
> 000001_create_users_and_sessions_tables.up.sql
```sql
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    full_name VARCHAR(255) NOT NULL,
    is_active BOOLEAN DEFAULT TRUE,
    failed_login_attempts INT DEFAULT 0,
    locked_until TIMESTAMP WITH TIME ZONE NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE sessions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token VARCHAR(255) UNIQUE NOT NULL,
    is_revoked BOOLEAN DEFAULT FALSE,
    client_ip VARCHAR(45) NOT NULL,
    user_agent TEXT NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
```
> 000001_create_users_and_sessions_tables.down.sql
```sql
-- Hapus tabel sessions dulu karena dia bergantung pada users (Foreign Key)
DROP TABLE IF EXISTS sessions;

-- Baru hapus tabel users
DROP TABLE IF EXISTS users;

DROP EXTENSION IF EXISTS "uuid-ossp";
```