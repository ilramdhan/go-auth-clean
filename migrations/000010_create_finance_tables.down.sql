DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS transfers;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS categories;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS user_settings;
DROP TABLE IF EXISTS currencies;
-- citext & pg_trgm tetap dipertahankan: dipakai tabel auth (000001) dan index
-- trigram users (000005); pg_trgm di-drop oleh 000005.down.
