-- Transfer lintas currency tidak bisa direpresentasikan di skema lama.
DELETE FROM transfers WHERE to_currency <> currency;
ALTER TABLE transfers
    DROP CONSTRAINT IF EXISTS tr_same_currency_amount,
    DROP CONSTRAINT IF EXISTS tr_to_amount_positive,
    DROP COLUMN IF EXISTS to_currency,
    DROP COLUMN IF EXISTS to_amount;
DROP TABLE IF EXISTS exchange_rates;
