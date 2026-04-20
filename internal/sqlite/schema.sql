-- schema.sql — applied by sqlite.Open on every call (idempotent via
-- CREATE ... IF NOT EXISTS). Kept as a single file per the pitch's
-- no-migration-library stance: if the schema ever changes before v1.0,
-- users re-run `sense scan` and the index rebuilds from source.

CREATE TABLE IF NOT EXISTS sense_files (
    id          INTEGER PRIMARY KEY,
    path        TEXT    NOT NULL UNIQUE,
    language    TEXT    NOT NULL,
    hash        TEXT    NOT NULL,
    symbols     INTEGER NOT NULL DEFAULT 0,
    indexed_at  TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sense_files_path     ON sense_files (path);
CREATE INDEX IF NOT EXISTS idx_sense_files_language ON sense_files (language);

CREATE TABLE IF NOT EXISTS sense_symbols (
    id          INTEGER PRIMARY KEY,
    file_id     INTEGER NOT NULL REFERENCES sense_files(id) ON DELETE CASCADE,
    name        TEXT    NOT NULL,
    qualified   TEXT    NOT NULL,
    kind        TEXT    NOT NULL,
    visibility  TEXT    DEFAULT 'public',
    parent_id   INTEGER REFERENCES sense_symbols(id),
    line_start  INTEGER NOT NULL,
    line_end    INTEGER NOT NULL,
    docstring   TEXT,
    complexity  INTEGER,
    snippet     TEXT,
    -- (file_id, qualified) is the Index contract's natural key for Symbol
    -- upserts. Not in the definition doc but required for ON CONFLICT.
    UNIQUE (file_id, qualified)
);

CREATE INDEX IF NOT EXISTS idx_sense_symbols_name      ON sense_symbols (name);
CREATE INDEX IF NOT EXISTS idx_sense_symbols_qualified ON sense_symbols (qualified);
CREATE INDEX IF NOT EXISTS idx_sense_symbols_kind      ON sense_symbols (kind);
CREATE INDEX IF NOT EXISTS idx_sense_symbols_file_id   ON sense_symbols (file_id);

CREATE TABLE IF NOT EXISTS sense_edges (
    id          INTEGER PRIMARY KEY,
    source_id   INTEGER REFERENCES sense_symbols(id) ON DELETE CASCADE,
    target_id   INTEGER NOT NULL REFERENCES sense_symbols(id) ON DELETE CASCADE,
    kind        TEXT    NOT NULL,
    file_id     INTEGER NOT NULL REFERENCES sense_files(id) ON DELETE CASCADE,
    line        INTEGER,
    confidence  REAL    DEFAULT 1.0
);

CREATE INDEX        IF NOT EXISTS idx_sense_edges_source ON sense_edges (source_id, kind);

-- idx_sense_edges_target is the reverse-edge index the blast BFS
-- reads against: "given these target_ids, find source_ids for
-- kind='calls'." Including source_id makes the index COVERING for
-- that exact query shape so BFS walks with zero row fetches per
-- hop — `EXPLAIN QUERY PLAN` reports `USING COVERING INDEX` and
-- `internal/sqlite/plan_test.go` pins that guarantee. The DROP
-- below is an in-place upgrade path for databases that were created
-- before the index gained its third column; on a fresh DB it is a
-- no-op.
DROP   INDEX        IF EXISTS     idx_sense_edges_target;
CREATE INDEX        IF NOT EXISTS idx_sense_edges_target ON sense_edges (target_id, kind, source_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_sense_edges_unique ON sense_edges (source_id, target_id, kind, file_id);

CREATE TABLE IF NOT EXISTS sense_metrics (
    key   TEXT PRIMARY KEY,
    value INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sense_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- sense_embeddings is created empty now. Cycle 2's embedding pitch will
-- populate it; having the table upfront means that pitch needs no schema
-- change, just inserts.
CREATE TABLE IF NOT EXISTS sense_embeddings (
    symbol_id   INTEGER PRIMARY KEY REFERENCES sense_symbols(id) ON DELETE CASCADE,
    vector      BLOB    NOT NULL
);
