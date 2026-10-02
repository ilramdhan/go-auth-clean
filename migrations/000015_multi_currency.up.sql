-- Multi currency (PRD F12): kurs manual per user + transfer lintas currency.
CREATE TABLE exchange_rates (
    id         UUID           PRIMARY KEY,
    user_id    UUID           NOT NULL,
    base       CHAR(3)        NOT NULL REFERENCES currencies (code),
    quote      CHAR(3)        NOT NULL REFERENCES currencies (code),
    rate       NUMERIC(20,10) NOT NULL CHECK (rate > 0),
    as_of      DATE           NOT NULL,
    created_at TIMESTAMPTZ    NOT NULL DEFAULT now(),
    CONSTRAINT exchange_rates_pair CHECK (base <> quote),
    CONSTRAINT exchange_rates_uq UNIQUE (user_id, base, quote, as_of)
);
CREATE INDEX exchange_rates_lookup_idx ON exchange_rates (user_id, base, quote, as_of DESC);

ALTER TABLE transfers
    ADD COLUMN to_amount   BIGINT,
    ADD COLUMN to_currency CHAR(3) REFERENCES currencies (code);
UPDATE transfers SET to_amount = amount, to_currency = currency;
ALTER TABLE transfers
    ALTER COLUMN to_amount SET NOT NULL,
    ALTER COLUMN to_currency SET NOT NULL,
    ADD CONSTRAINT tr_to_amount_positive CHECK (to_amount > 0),
    ADD CONSTRAINT tr_same_currency_amount CHECK (to_currency <> currency OR to_amount = amount);
