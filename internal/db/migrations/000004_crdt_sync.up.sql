-- Shared-book sync v2 (state-based CRDT). doc holds the CRDT document
-- ({book, members, records} of {f, r, _v} entities); NULL means the space is
-- still legacy v1 (payload only). version is the space's monotonic change
-- counter; every entity's _v is the version at which it last changed.
-- payload keeps a flattened v1 rendering of doc so old clients can still read.
ALTER TABLE shared_spaces
    ADD COLUMN IF NOT EXISTS doc JSONB,
    ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 0;

-- Archived members (v2 never hard-deletes a member). Nullable with no default so
-- that "absent" on backup push stays NULL and comes back absent on pull.
ALTER TABLE book_members
    ADD COLUMN IF NOT EXISTS archived BOOLEAN;
