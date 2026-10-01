ALTER TABLE book_members
    DROP COLUMN IF EXISTS archived;

ALTER TABLE shared_spaces
    DROP COLUMN IF EXISTS version,
    DROP COLUMN IF EXISTS doc;
