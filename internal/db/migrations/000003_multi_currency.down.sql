ALTER TABLE users
    DROP COLUMN IF EXISTS base_currency;

ALTER TABLE record_templates
    DROP COLUMN IF EXISTS currency;

ALTER TABLE books
    DROP COLUMN IF EXISTS currency;

ALTER TABLE personal_records
    DROP COLUMN IF EXISTS fx,
    DROP COLUMN IF EXISTS booked,
    DROP COLUMN IF EXISTS original,
    DROP COLUMN IF EXISTS amount_currency;

ALTER TABLE records
    DROP COLUMN IF EXISTS split_custom_amounts,
    DROP COLUMN IF EXISTS fx,
    DROP COLUMN IF EXISTS booked,
    DROP COLUMN IF EXISTS original,
    DROP COLUMN IF EXISTS amount_currency;
