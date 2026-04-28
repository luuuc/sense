-- schema_fts.sql — FTS5 full-text search index and sync triggers.
-- Executed separately from schema.sql because modernc.org/sqlite's
-- ExecContext silently drops virtual-table statements embedded in a
-- long multi-statement string. Each statement here is executed
-- individually by Open.

CREATE VIRTUAL TABLE IF NOT EXISTS sense_symbols_fts USING fts5(
    name,
    qualified,
    docstring,
    snippet,
    name_parts,
    content='sense_symbols',
    content_rowid='id'
);
