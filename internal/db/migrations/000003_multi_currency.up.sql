-- Multi-currency support. Every column is nullable with no default so that a
-- field the client omitted stays absent (NULL) and comes back absent on pull.
-- original / booked / fx / split_custom_amounts are opaque JSON owned by the
-- frontend and are returned verbatim.
ALTER TABLE records
    ADD COLUMN IF NOT EXISTS amount_currency TEXT,
    ADD COLUMN IF NOT EXISTS original JSONB,
    ADD COLUMN IF NOT EXISTS booked JSONB,
    ADD COLUMN IF NOT EXISTS fx JSONB,
    ADD COLUMN IF NOT EXISTS split_custom_amounts JSONB;

ALTER TABLE personal_records
    ADD COLUMN IF NOT EXISTS amount_currency TEXT,
    ADD COLUMN IF NOT EXISTS original JSONB,
    ADD COLUMN IF NOT EXISTS booked JSONB,
    ADD COLUMN IF NOT EXISTS fx JSONB;

ALTER TABLE books
    ADD COLUMN IF NOT EXISTS currency TEXT;

ALTER TABLE record_templates
    ADD COLUMN IF NOT EXISTS currency TEXT;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS base_currency TEXT;
