-- Create Shared Spaces table for anonymous collaboration
CREATE TABLE IF NOT EXISTS shared_spaces (
    code TEXT PRIMARY KEY, -- unique identifier (8 chars; older 6-char codes still valid)
    payload JSONB NOT NULL, -- Full book + records snapshot
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Index for faster lookups (though code is PK, explicitly stated for clarity)
CREATE INDEX IF NOT EXISTS idx_shared_spaces_code ON shared_spaces(code);
