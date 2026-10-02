DELETE FROM transactions WHERE source IN ('recurring', 'import');
DROP INDEX IF EXISTS tx_import_hash_uq;
DROP INDEX IF EXISTS tx_recurring_occurrence_uq;
ALTER TABLE transactions DROP CONSTRAINT transactions_source_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_source_check CHECK (source IN ('manual', 'transfer_fee'));
ALTER TABLE transactions
    DROP CONSTRAINT IF EXISTS tx_recurring_pair,
    DROP COLUMN IF EXISTS import_hash,
    DROP COLUMN IF EXISTS occurrence_date,
    DROP COLUMN IF EXISTS recurring_rule_id;
DROP TABLE IF EXISTS recurring_rules;
