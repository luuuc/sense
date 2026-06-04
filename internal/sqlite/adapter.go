// Package sqlite is the SQLite-backed implementation of index.Index. It
// uses modernc.org/sqlite (pure Go, CGO_ENABLED=0) so the single-binary
// story in 02-architecture.md holds.
package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/luuuc/sense/internal/index"
)

//go:embed schema.sql
var schemaSQL string

//go:embed schema_fts.sql
var schemaFTSSQL string

// SchemaVersion is stamped into the database's PRAGMA user_version.
// Bump when schema.sql changes incompatibly — a mismatch triggers
// auto-rebuild (drop all tables, fresh schema, full scan). Never set
// before a scan completes successfully; see StampSchemaVersion.
const SchemaVersion = 5

// metricsPreserve is the preserve-set every reset path honors: the lifetime
// counters in sense_metrics (total queries, estimated tokens saved) survive
// both an automatic schema-bump rebuild and an explicit `sense scan
// --rebuild`. They are the only data in the index not re-derivable from source
// (03-data-model.md), so a reset must not zero them. The shape-stability
// invariant for excluding a table from the drop lives on resetSenseTables.
var metricsPreserve = map[string]bool{"sense_metrics": true}

// maxOpenConns is the connection-pool size Open applies. InTx relies on
// this being 1 — its raw BEGIN/COMMIT approach shares a transaction
// across Adapter calls by sharing the single pooled connection. If this
// ever changes to allow concurrent writes, InTx must be reworked to use
// database/sql's proper *sql.Tx threading. The constant is checked at
// InTx entry so a future config change fails loudly instead of silently
// corrupting transactional semantics.
const maxOpenConns = 1

// ftsTriggerStatements keep the FTS5 content-sync table in sync with
// sense_symbols. Each trigger is executed individually because
// modernc.org/sqlite's multi-statement handling silently drops trigger
// DDL after virtual-table statements.
var ftsTriggerStatements = []string{
	`CREATE TRIGGER IF NOT EXISTS sense_symbols_fts_insert
	 AFTER INSERT ON sense_symbols BEGIN
	     INSERT INTO sense_symbols_fts(rowid, name, qualified, docstring, snippet, name_parts)
	     VALUES (new.id, new.name, new.qualified, new.docstring, new.snippet, new.name_parts);
	 END`,

	`CREATE TRIGGER IF NOT EXISTS sense_symbols_fts_delete
	 BEFORE DELETE ON sense_symbols BEGIN
	     INSERT INTO sense_symbols_fts(sense_symbols_fts, rowid, name, qualified, docstring, snippet, name_parts)
	     VALUES ('delete', old.id, old.name, old.qualified, old.docstring, old.snippet, old.name_parts);
	 END`,

	`CREATE TRIGGER IF NOT EXISTS sense_symbols_fts_update
	 BEFORE UPDATE ON sense_symbols BEGIN
	     INSERT INTO sense_symbols_fts(sense_symbols_fts, rowid, name, qualified, docstring, snippet, name_parts)
	     VALUES ('delete', old.id, old.name, old.qualified, old.docstring, old.snippet, old.name_parts);
	 END`,

	`CREATE TRIGGER IF NOT EXISTS sense_symbols_fts_update_after
	 AFTER UPDATE ON sense_symbols BEGIN
	     INSERT INTO sense_symbols_fts(rowid, name, qualified, docstring, snippet, name_parts)
	     VALUES (new.id, new.name, new.qualified, new.docstring, new.snippet, new.name_parts);
	 END`,
}

// Adapter is the SQLite implementation of index.Index.
type Adapter struct {
	db *sql.DB
	// Rebuilt is true when Open detected a schema version mismatch and
	// dropped all tables to recreate a fresh schema. Callers that need
	// a populated index (e.g. the MCP server) should trigger a full
	// scan before serving.
	Rebuilt bool
	// FTSMigrated is true when Open detected a stale FTS5 table
	// (missing columns) and dropped+recreated it. Keyword search
	// results will be incomplete until the next scan repopulates them.
	FTSMigrated bool
}

// compile-time contract check.
var _ index.Index = (*Adapter)(nil)

// Open opens or creates a SQLite index at path. The parent directory must
// already exist. On every open the schema is reapplied (idempotent) and
// WAL + foreign-key pragmas are set via the DSN so they apply to every
// connection in the pool, not just the first.
func Open(ctx context.Context, path string) (*Adapter, error) {
	// modernc.org/sqlite reads _pragma parameters on every new connection,
	// which is what foreign_keys (a per-connection setting) requires.
	// synchronous=NORMAL is durable under WAL for crash recovery; it can
	// lose the last few seconds under a power cut. That's the right
	// trade-off for a derived index — `sense scan` rebuilds from source.
	mmapSize := "0"
	if strconv.IntSize == 64 {
		mmapSize = "134217728" // 128MB on 64-bit platforms
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(wal)" +
		"&_pragma=synchronous(normal)" +
		"&_pragma=temp_store(memory)" +
		"&_pragma=cache_size(-8000)" +
		// busy_timeout makes a writer wait-and-retry rather than fail
		// instantly with SQLITE_BUSY. With the embedded watcher, brief
		// write overlap is possible across processes during single-writer
		// lock handoff (stale-lock reclaim) and against a one-shot
		// `sense scan`; this turns those rare races into a short wait.
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=mmap_size(" + mmapSize + ")"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}

	// Single connection serializes writes and reads. Scan is sequential
	// today; relax when concurrent MCP reads warrant the contention cost.
	// Load-bearing for InTx — see maxOpenConns constant.
	db.SetMaxOpenConns(maxOpenConns)

	// Check stored schema version. A mismatch (stored > 0 but !=
	// expected) means the binary was upgraded — drop everything and
	// rebuild from source. Version 0 means fresh DB (no prior scan).
	// If PRAGMA user_version itself fails (corrupt file), storedVersion
	// stays 0 and we proceed as fresh — the schema apply below will
	// likely fail with a more descriptive error.
	var storedVersion int
	_ = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&storedVersion)
	rebuilt := false
	ftsMigrated := false
	if storedVersion != 0 && storedVersion != SchemaVersion {
		// Binary upgraded against an older schema: drop and recreate from
		// source via the one reset primitive. resetSenseTables resets
		// user_version to 0 and re-applies the full schema, so the apply
		// branch below is skipped — the state is indistinguishable from a
		// fresh DB until StampSchemaVersion is called after scan. The
		// preserve-set keeps lifetime metrics across the rebuild; everything
		// else is re-derived by the scan.
		rebuilt = true
		if err := resetSenseTables(ctx, db, metricsPreserve); err != nil {
			_ = db.Close()
			return nil, err
		}
	} else {
		// Fresh or current DB: apply the schema in place. CREATE ... IF NOT
		// EXISTS makes this a no-op on an already-current index, and a stale
		// FTS table is migrated in passing.
		var err error
		ftsMigrated, err = applySchema(ctx, db)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
	}

	return &Adapter{db: db, Rebuilt: rebuilt, FTSMigrated: ftsMigrated}, nil
}

// applySchema applies the base schema, then the FTS5 virtual table, then its
// triggers — each as a separate ExecContext because modernc.org/sqlite
// silently drops virtual-table and trigger DDL embedded after other
// statements in one multi-statement string. It returns whether a stale FTS
// table was migrated (dropped and recreated), so the caller can warn that
// keyword search will repopulate on the next scan.
//
// CREATE ... IF NOT EXISTS makes this safe on a fresh, current, or
// just-reset database alike. The migration path only fires on an in-place
// upgrade where the FTS table survived but lost columns; after a full reset
// the table is gone, so ftsMigrated is false.
func applySchema(ctx context.Context, db *sql.DB) (ftsMigrated bool, err error) {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return false, fmt.Errorf("sqlite schema: %w", err)
	}

	// Migration: if the FTS table exists but is missing the snippet column
	// (added in the search-quality pitch), drop it and its triggers so the
	// CREATE IF NOT EXISTS below picks up the new schema.
	ftsMigrated = ftsNeedsMigration(ctx, db)
	if ftsMigrated {
		for _, name := range []string{
			"sense_symbols_fts_insert", "sense_symbols_fts_delete",
			"sense_symbols_fts_update", "sense_symbols_fts_update_after",
		} {
			_, _ = db.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+name)
		}
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS sense_symbols_fts")
	}

	if _, err := db.ExecContext(ctx, schemaFTSSQL); err != nil {
		return ftsMigrated, fmt.Errorf("sqlite fts schema: %w", err)
	}
	for _, stmt := range ftsTriggerStatements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return ftsMigrated, fmt.Errorf("sqlite fts trigger: %w", err)
		}
	}
	return ftsMigrated, nil
}

// Rebuild drops and recreates every source-derived sense_% table, preserving
// the lifetime metrics, then leaves an empty current-schema index. It is the
// explicit counterpart to Open's automatic schema-mismatch rebuild: `sense
// scan --rebuild` calls it after Open and before the walk, so the emptied
// sense_files makes the scan's hash-skip miss on every file and re-parse and
// re-resolve the whole tree (and re-embed, inline with --embed or deferred to
// the MCP server otherwise). user_version is left at 0 until the scan
// completes and StampSchemaVersion runs, so a crash mid-rebuild reopens as a
// fresh index and rescans.
func (a *Adapter) Rebuild(ctx context.Context) error {
	return resetSenseTables(ctx, a.db, metricsPreserve)
}

// ftsNeedsMigration returns true if the FTS5 table exists but is
// missing the snippet column. This happens when upgrading from a
// database created before the search-quality pitch added snippet
// to the FTS index.
func ftsNeedsMigration(ctx context.Context, db *sql.DB) bool {
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type='table' AND name='sense_symbols_fts'`).Scan(&count)
	if err != nil || count == 0 {
		return false
	}
	// FTS5 content tables expose column names via a zero-row SELECT.
	rows, err := db.QueryContext(ctx, "SELECT * FROM sense_symbols_fts LIMIT 0")
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return false
	}
	has := map[string]bool{}
	for _, c := range cols {
		has[c] = true
	}
	return !has["snippet"] || !has["name_parts"]
}

// StampSchemaVersion writes the current SchemaVersion into PRAGMA
// user_version. Call ONLY after a successful scan completes — this is
// the invariant that prevents a crash mid-rebuild from leaving an
// apparently-current but incomplete index.
func (a *Adapter) StampSchemaVersion(ctx context.Context) error {
	_, err := a.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion))
	if err != nil {
		return fmt.Errorf("sqlite stamp schema version: %w", err)
	}
	return nil
}

// Close releases the underlying database handle and flushes the WAL.
func (a *Adapter) Close() error {
	return a.db.Close()
}

// DB exposes the underlying *sql.DB for read-path consumers that
// speak plain database/sql — the blast BFS in 01-03 and the CLI
// symbol lookup in 01-04. Writers must continue to go through the
// named WriteFile / WriteSymbol / WriteEdge methods so the upsert
// contract stays in one place.
func (a *Adapter) DB() *sql.DB {
	return a.db
}

// InTx runs fn inside a SQLite transaction, committing on success and
// rolling back on error. Callers keep using the same Adapter inside fn —
// there is no transaction-scoped handle to thread through, which avoids
// an interface-wide refactor.
//
// This relies on MaxOpenConns being 1: with a single pooled connection,
// the BEGIN/COMMIT/ROLLBACK statements issued here share the same
// connection as every a.db.ExecContext / QueryRowContext call inside
// fn, so SQLite treats them as one transaction. If the pool size ever
// changes, this helper must be reworked to use database/sql's proper
// BeginTx + *sql.Tx plumbing. The runtime check below fails loudly
// rather than silently corrupting transactional semantics.
func (a *Adapter) InTx(ctx context.Context, fn func() error) (err error) {
	if got := a.db.Stats().MaxOpenConnections; got != maxOpenConns {
		panic(fmt.Sprintf(
			"sqlite.InTx: MaxOpenConnections = %d, want %d — the single-conn "+
				"transaction trick no longer applies; switch InTx to *sql.Tx "+
				"plumbing before raising the pool size",
			got, maxOpenConns))
	}
	if _, err := a.db.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("sqlite begin: %w", err)
	}
	defer func() {
		if err != nil {
			// Best-effort rollback; the returned error is the real one.
			// A rollback failure after a primary error is almost always
			// a consequence of the primary error, not new information.
			_, _ = a.db.ExecContext(ctx, "ROLLBACK")
			return
		}
		if _, commitErr := a.db.ExecContext(ctx, "COMMIT"); commitErr != nil {
			err = fmt.Errorf("sqlite commit: %w", commitErr)
			_, _ = a.db.ExecContext(ctx, "ROLLBACK")
		}
	}()
	return fn()
}

// resetSenseTables is the single index-reset primitive. It drops every
// sense_% table/view NOT named in preserve, resets PRAGMA user_version to 0,
// and re-applies the full schema (base tables, FTS virtual table, triggers)
// via applySchema. Both reset callers share it: Open's schema-mismatch branch
// (automatic, on a binary upgrade) and Adapter.Rebuild (explicit, behind
// `sense scan --rebuild`). They differ only in the preserve-set.
//
// DROP (not DELETE) is deliberate. A schema bump changes table *shape*, so the
// structure must be recreated — DELETE would leave the stale shape. An
// explicit same-binary rebuild uses the same drop path because it is strictly
// more thorough (it also heals any structural drift) and repopulates
// immediately, so index.db self-levels via SQLite page reuse.
//
// Preserve-via-exclude invariant: a preserved table keeps both its rows and
// its existing shape, because it is never dropped and CREATE TABLE IF NOT
// EXISTS is a no-op on it. This is correct only while the preserved table's
// shape is stable across schema versions. If a future SchemaVersion ever
// changes a preserved table (e.g. sense_metrics) itself, that release must
// ship a hand-written migration for that one table, since it is no longer
// auto-dropped here.
//
// Discovering tables via sqlite_master keeps this future-proof: tables added
// in later schema versions are dropped automatically without maintaining a
// parallel list.
func resetSenseTables(ctx context.Context, db *sql.DB, preserve map[string]bool) error {
	rows, err := db.QueryContext(ctx,
		"SELECT type, name FROM sqlite_master WHERE type IN ('table','view') AND name LIKE 'sense_%'")
	if err != nil {
		return fmt.Errorf("sqlite list tables: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type entry struct{ typ, name string }
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.typ, &e.name); err != nil {
			return fmt.Errorf("sqlite list tables scan: %w", err)
		}
		if preserve[e.name] {
			continue
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite list tables iterate: %w", err)
	}

	// Drop with foreign keys disabled so order is irrelevant: sqlite_master
	// lists tables in creation order (parents first), but ON DELETE CASCADE
	// makes a FK-enforced DROP of a parent fail once its children's rows are
	// gone. Every dropped table is recreated by applySchema below, and the
	// only preserved table (sense_metrics) participates in no FK, so disabling
	// enforcement for the structural reset is safe. Restored to ON before
	// returning — the single pooled connection (maxOpenConns) keeps the DSN's
	// foreign_keys(1) default otherwise. modernc applies PRAGMA foreign_keys
	// only outside a transaction, which holds here (each Exec autocommits).
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("sqlite disable foreign_keys: %w", err)
	}
	// Restore on a non-cancellable context: re-enabling enforcement is a
	// safety invariant, and with one pooled connection a missed restore would
	// leave FK checks off for the rest of the process. A cancelled ctx must
	// not skip it.
	defer func() {
		_, _ = db.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON")
	}()

	for _, e := range entries {
		stmt := fmt.Sprintf("DROP %s IF EXISTS %s", strings.ToUpper(e.typ), e.name)
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("sqlite drop %s %s: %w", e.typ, e.name, err)
		}
	}

	// Reset user_version so the state is indistinguishable from a fresh DB
	// until StampSchemaVersion is called after the scan completes.
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 0"); err != nil {
		return fmt.Errorf("sqlite reset user_version: %w", err)
	}

	// ftsMigrated is intentionally discarded: after a full drop the FTS table
	// is gone, so applySchema recreates it fresh and never reports a migration.
	if _, err := applySchema(ctx, db); err != nil {
		return err
	}
	return nil
}
