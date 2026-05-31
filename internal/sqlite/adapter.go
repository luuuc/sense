// Package sqlite is the SQLite-backed implementation of index.Index. It
// uses modernc.org/sqlite (pure Go, CGO_ENABLED=0) so the single-binary
// story in 02-architecture.md holds.
package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
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
	if storedVersion != 0 && storedVersion != SchemaVersion {
		rebuilt = true
		if err := dropAllSenseTables(ctx, db); err != nil {
			_ = db.Close()
			return nil, err
		}
		// Reset user_version so the state is indistinguishable from a
		// fresh DB until StampSchemaVersion is called after scan.
		if _, err := db.ExecContext(ctx, "PRAGMA user_version = 0"); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlite reset user_version: %w", err)
		}
	}

	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite schema: %w", err)
	}

	// FTS5 virtual table and triggers are applied separately because
	// modernc.org/sqlite's ExecContext silently drops virtual-table
	// statements embedded in a long multi-statement string.
	//
	// Migration: if the FTS table exists but is missing the snippet
	// column (added in search-quality pitch), drop it and its triggers
	// so the CREATE IF NOT EXISTS below picks up the new schema.
	ftsMigrated := ftsNeedsMigration(ctx, db)
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
		_ = db.Close()
		return nil, fmt.Errorf("sqlite fts schema: %w", err)
	}
	for _, stmt := range ftsTriggerStatements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlite fts trigger: %w", err)
		}
	}

	return &Adapter{db: db, Rebuilt: rebuilt, FTSMigrated: ftsMigrated}, nil
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

// FileMeta returns the id and hash for the given relative path.
// Returns (0, "", nil) if the file is not in the index.
func (a *Adapter) FileMeta(ctx context.Context, path string) (int64, string, error) {
	var id int64
	var hash string
	err := a.db.QueryRowContext(ctx,
		"SELECT id, hash FROM sense_files WHERE path = ?", path,
	).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("sqlite FileMeta: %w", err)
	}
	return id, hash, nil
}

// CachedFile holds pre-loaded file metadata for bulk hash comparison.
type CachedFile struct {
	ID   int64
	Hash string
}

// FileHashMap loads all file paths and hashes into a map for bulk
// incremental comparison. One query replaces N per-file FileMeta calls.
func (a *Adapter) FileHashMap(ctx context.Context) (map[string]CachedFile, error) {
	rows, err := a.db.QueryContext(ctx, "SELECT id, path, hash FROM sense_files")
	if err != nil {
		return nil, fmt.Errorf("sqlite FileHashMap: %w", err)
	}
	defer func() { _ = rows.Close() }()

	m := make(map[string]CachedFile)
	for rows.Next() {
		var id int64
		var path, hash string
		if err := rows.Scan(&id, &path, &hash); err != nil {
			return nil, fmt.Errorf("sqlite FileHashMap scan: %w", err)
		}
		m[path] = CachedFile{ID: id, Hash: hash}
	}
	return m, rows.Err()
}

// FilePaths returns every path currently tracked in sense_files.
func (a *Adapter) FilePaths(ctx context.Context) ([]string, error) {
	rows, err := a.db.QueryContext(ctx, "SELECT path FROM sense_files")
	if err != nil {
		return nil, fmt.Errorf("sqlite FilePaths: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("sqlite FilePaths scan: %w", err)
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// DeleteFile removes a file and (via FK CASCADE) its symbols from the index.
func (a *Adapter) DeleteFile(ctx context.Context, path string) error {
	_, err := a.db.ExecContext(ctx, "DELETE FROM sense_files WHERE path = ?", path)
	if err != nil {
		return fmt.Errorf("sqlite DeleteFile: %w", err)
	}
	return nil
}

// -------------------- writes --------------------

func (a *Adapter) WriteFile(ctx context.Context, f *model.File) (int64, error) {
	const q = `
		INSERT INTO sense_files (path, language, hash, symbols, indexed_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			language   = excluded.language,
			hash       = excluded.hash,
			symbols    = excluded.symbols,
			indexed_at = excluded.indexed_at
		RETURNING id`

	var id int64
	err := a.db.QueryRowContext(ctx, q,
		f.Path, f.Language, f.Hash, f.Symbols,
		f.IndexedAt.UTC().Format(time.RFC3339Nano),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("sqlite WriteFile: %w", err)
	}
	return id, nil
}

func (a *Adapter) WriteSymbol(ctx context.Context, s *model.Symbol) (int64, error) {
	const q = `
		INSERT INTO sense_symbols
			(file_id, name, qualified, kind, visibility, receiver, parent_id,
			 line_start, line_end, docstring, complexity, snippet, name_parts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(file_id, qualified) DO UPDATE SET
			name       = excluded.name,
			kind       = excluded.kind,
			visibility = excluded.visibility,
			receiver   = excluded.receiver,
			parent_id  = excluded.parent_id,
			line_start = excluded.line_start,
			line_end   = excluded.line_end,
			docstring  = excluded.docstring,
			complexity = excluded.complexity,
			snippet    = excluded.snippet,
			name_parts = excluded.name_parts
		RETURNING id`

	nameParts := symbolNameParts(s.Name, s.Qualified)
	var id int64
	err := a.db.QueryRowContext(ctx, q,
		s.FileID, s.Name, s.Qualified, string(s.Kind), s.Visibility, s.Receiver,
		nullableInt64(s.ParentID), s.LineStart, s.LineEnd,
		s.Docstring, nullableInt(s.Complexity), s.Snippet, nameParts,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("sqlite WriteSymbol: %w", err)
	}
	return id, nil
}

func symbolNameParts(name, qualified string) string {
	parts := Decompose(name)
	qParts := Decompose(qualified)
	if qParts != parts {
		parts = parts + " " + qParts
	}
	return parts
}

func (a *Adapter) WriteEdge(ctx context.Context, e *model.Edge) (int64, error) {
	var (
		id  int64
		err error
	)
	if e.SourceID != nil {
		const q = `
			INSERT INTO sense_edges (source_id, target_id, kind, file_id, line, confidence)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(source_id, target_id, kind, file_id) DO UPDATE SET
				line       = excluded.line,
				confidence = excluded.confidence
			RETURNING id`
		err = a.db.QueryRowContext(ctx, q,
			*e.SourceID, e.TargetID, string(e.Kind), e.FileID,
			nullableInt(e.Line), e.Confidence,
		).Scan(&id)
	} else {
		// File-level edge: source_id is NULL. No unique constraint
		// covers this case (SQLite treats NULLs as distinct), so use
		// a plain INSERT. Idempotency is handled by the scan harness
		// deleting stale edges for changed files before re-extracting.
		const q = `
			INSERT INTO sense_edges (source_id, target_id, kind, file_id, line, confidence)
			VALUES (NULL, ?, ?, ?, ?, ?)
			RETURNING id`
		err = a.db.QueryRowContext(ctx, q,
			e.TargetID, string(e.Kind), e.FileID,
			nullableInt(e.Line), e.Confidence,
		).Scan(&id)
	}
	if err != nil {
		return 0, fmt.Errorf("sqlite WriteEdge: %w", err)
	}
	return id, nil
}

// PrepareSymbolStmt returns a prepared statement for batch-writing
// symbols within a transaction. Use ExecSymbolStmt to bind parameters
// and scan the returned id. The caller must close the statement.
func (a *Adapter) PrepareSymbolStmt(ctx context.Context) (*sql.Stmt, error) {
	const q = `
		INSERT INTO sense_symbols
			(file_id, name, qualified, kind, visibility, receiver, parent_id,
			 line_start, line_end, docstring, complexity, snippet, name_parts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(file_id, qualified) DO UPDATE SET
			name       = excluded.name,
			kind       = excluded.kind,
			visibility = excluded.visibility,
			receiver   = excluded.receiver,
			parent_id  = excluded.parent_id,
			line_start = excluded.line_start,
			line_end   = excluded.line_end,
			docstring  = excluded.docstring,
			complexity = excluded.complexity,
			snippet    = excluded.snippet,
			name_parts = excluded.name_parts
		RETURNING id`
	return a.db.PrepareContext(ctx, q)
}

// ExecSymbolStmt writes a symbol using a prepared statement from PrepareSymbolStmt.
func ExecSymbolStmt(ctx context.Context, stmt *sql.Stmt, s *model.Symbol) (int64, error) {
	nameParts := symbolNameParts(s.Name, s.Qualified)
	var id int64
	err := stmt.QueryRowContext(ctx,
		s.FileID, s.Name, s.Qualified, string(s.Kind), s.Visibility, s.Receiver,
		nullableInt64(s.ParentID), s.LineStart, s.LineEnd,
		s.Docstring, nullableInt(s.Complexity), s.Snippet, nameParts,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("sqlite WriteSymbol (prepared): %w", err)
	}
	return id, nil
}

// PrepareEdgeStmt returns a prepared statement for batch-writing edges
// with a non-nil source_id within a transaction. The caller must close
// the statement.
func (a *Adapter) PrepareEdgeStmt(ctx context.Context) (*sql.Stmt, error) {
	const q = `
		INSERT INTO sense_edges (source_id, target_id, kind, file_id, line, confidence)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_id, target_id, kind, file_id) DO UPDATE SET
			line       = excluded.line,
			confidence = excluded.confidence
		RETURNING id`
	return a.db.PrepareContext(ctx, q)
}

// ExecEdgeStmt writes an edge using a prepared statement and returns
// the row id. The statement must have been created by PrepareEdgeStmt.
// Only for edges with a non-nil SourceID.
func ExecEdgeStmt(ctx context.Context, stmt *sql.Stmt, e *model.Edge) (int64, error) {
	var id int64
	err := stmt.QueryRowContext(ctx,
		*e.SourceID, e.TargetID, string(e.Kind), e.FileID,
		nullableInt(e.Line), e.Confidence,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("sqlite WriteEdge (prepared): %w", err)
	}
	return id, nil
}

// -------------------- reads --------------------

func (a *Adapter) ReadSymbol(ctx context.Context, id int64) (*model.SymbolContext, error) {
	const q = `
		SELECT
			s.id, s.file_id, s.name, s.qualified, s.kind, s.visibility, s.parent_id,
			s.line_start, s.line_end, s.docstring, s.complexity, s.snippet,
			f.id, f.path, f.language, f.hash, f.symbols, f.indexed_at
		FROM sense_symbols s
		JOIN sense_files   f ON f.id = s.file_id
		WHERE s.id = ?`

	var (
		sym        model.Symbol
		file       model.File
		parentID   sql.NullInt64
		complexity sql.NullInt64
		visibility sql.NullString
		docstring  sql.NullString
		snippet    sql.NullString
		indexedAt  string
	)
	err := a.db.QueryRowContext(ctx, q, id).Scan(
		&sym.ID, &sym.FileID, &sym.Name, &sym.Qualified, &sym.Kind, &visibility,
		&parentID, &sym.LineStart, &sym.LineEnd, &docstring, &complexity, &snippet,
		&file.ID, &file.Path, &file.Language, &file.Hash, &file.Symbols, &indexedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, index.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite ReadSymbol: %w", err)
	}

	model.HydrateSymbolNullables(&sym, parentID, complexity, visibility, docstring, snippet)
	file.IndexedAt, err = time.Parse(time.RFC3339Nano, indexedAt)
	if err != nil {
		return nil, fmt.Errorf("sqlite ReadSymbol: parse indexed_at: %w", err)
	}

	outbound, err := a.loadEdges(ctx, id, true)
	if err != nil {
		return nil, fmt.Errorf("sqlite ReadSymbol outbound: %w", err)
	}
	inbound, err := a.loadEdges(ctx, id, false)
	if err != nil {
		return nil, fmt.Errorf("sqlite ReadSymbol inbound: %w", err)
	}

	return &model.SymbolContext{
		Symbol:   sym,
		File:     file,
		Outbound: outbound,
		Inbound:  inbound,
	}, nil
}

// ReadSymbolGraph performs a multi-hop BFS from the given symbol.
// Depth 1 behaves identically to ReadSymbol. At depth 2+, the BFS
// expands the frontier in the requested direction, deduplicating
// against already-visited nodes. MaxPerHop caps new symbols per hop;
// zero means unlimited.
func (a *Adapter) ReadSymbolGraph(ctx context.Context, id int64, depth int, direction model.Direction, maxPerHop int) (*model.GraphResult, error) {
	root, err := a.ReadSymbol(ctx, id)
	if err != nil {
		return nil, err
	}
	result := &model.GraphResult{Root: *root}
	// For container symbols a "who calls this" question is best answered by
	// who calls the container's members, not only who names the type. Fold
	// member callers into the root's inbound set so a class/type graph query
	// reflects real usage — the gap that made graph and blast disagree.
	if direction != model.DirectionCallees {
		if err := a.foldMemberCallers(ctx, &result.Root); err != nil {
			return nil, err
		}
	}
	// Symmetric to foldMemberCallers: fold member callees into the container's
	// outbound set so "what does this class call" reflects what its methods
	// reach, instead of returning an empty list that reads as "depends on
	// nothing".
	if direction != model.DirectionCallers {
		if err := a.foldMemberCallees(ctx, &result.Root); err != nil {
			return nil, err
		}
	}
	if depth <= 1 {
		return result, nil
	}

	visited := map[int64]struct{}{id: {}}
	frontier := graphFrontier(&result.Root, direction, visited)

	for hop := 2; hop <= depth; hop++ {
		if len(frontier) == 0 {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("graph depth: cancelled at hop %d: %w", hop, err)
		}

		var layer model.HopEdges
		var nextFrontier []int64
		hopSymbols := 0

		expand := func(symID int64, outbound bool, dest *[]model.EdgeRef) error {
			refs, err := a.loadEdges(ctx, symID, outbound)
			if err != nil {
				return fmt.Errorf("graph depth hop %d: %w", hop, err)
			}
			for _, e := range refs {
				if !expandableEdge(e, visited) {
					continue
				}
				visited[e.Target.ID] = struct{}{}
				*dest = append(*dest, e)
				nextFrontier = append(nextFrontier, e.Target.ID)
				hopSymbols++
				if maxPerHop > 0 && hopSymbols >= maxPerHop {
					result.Truncated = true
					return nil
				}
			}
			return nil
		}

		for _, symID := range frontier {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("graph depth: cancelled at hop %d: %w", hop, err)
			}
			if maxPerHop > 0 && hopSymbols >= maxPerHop {
				result.Truncated = true
				break
			}
			if direction != model.DirectionCallees {
				if err := expand(symID, false, &layer.Inbound); err != nil {
					return nil, err
				}
				if result.Truncated {
					break
				}
			}
			if direction != model.DirectionCallers {
				if err := expand(symID, true, &layer.Outbound); err != nil {
					return nil, err
				}
				if result.Truncated {
					break
				}
			}
		}
		if len(layer.Inbound) > 0 || len(layer.Outbound) > 0 {
			result.Layers = append(result.Layers, layer)
		}
		if result.Truncated {
			break
		}
		frontier = nextFrontier
	}
	return result, nil
}

// expandableEdge returns true if the edge should be followed during
// BFS — filters out zero-ID targets, temporal (co-change) edges, and
// already-visited nodes.
func expandableEdge(e model.EdgeRef, visited map[int64]struct{}) bool {
	if e.Target.ID == 0 || e.Edge.Kind == model.EdgeTemporal {
		return false
	}
	_, seen := visited[e.Target.ID]
	return !seen
}

// graphFrontier extracts the initial BFS frontier from the root's
// edges, filtered by direction. Discovered IDs are added to visited.
func graphFrontier(sc *model.SymbolContext, direction model.Direction, visited map[int64]struct{}) []int64 {
	var frontier []int64
	if direction != model.DirectionCallees {
		for _, e := range sc.Inbound {
			if expandableEdge(e, visited) {
				visited[e.Target.ID] = struct{}{}
				frontier = append(frontier, e.Target.ID)
			}
		}
	}
	if direction != model.DirectionCallers {
		for _, e := range sc.Outbound {
			if expandableEdge(e, visited) {
				visited[e.Target.ID] = struct{}{}
				frontier = append(frontier, e.Target.ID)
			}
		}
	}
	return frontier
}

// isContainerExpandKind reports the symbol kinds whose member callers are
// folded into the symbol's own inbound edges by foldMemberCallers. Matches
// blast's child-expansion set: concrete types whose methods carry the real
// callers (modules/interfaces are excluded — their children are definitions,
// not usage).
func isContainerExpandKind(k model.SymbolKind) bool {
	return k == model.KindClass || k == model.KindType
}

// isUsageEdge reports whether an edge kind represents a symbol being used
// (called or referenced) rather than a structural relationship
// (inherits/composes/includes) or co-change noise (temporal).
func isUsageEdge(k model.EdgeKind) bool {
	return k == model.EdgeCalls || k == model.EdgeReferences
}

// hasUsageEdge reports whether edges contains at least one above-floor
// usage edge — a real call/reference signal, as opposed to a structural
// edge or a low-confidence resolution guess. Used by both fold paths:
// against Inbound it means "has a real caller", against Outbound it means
// "has a real callee".
func hasUsageEdge(edges []model.EdgeRef) bool {
	for _, e := range edges {
		if isUsageEdge(e.Edge.Kind) && e.Edge.Confidence >= extract.ConfidenceUnresolved {
			return true
		}
	}
	return false
}

// foldMemberCallers enriches a container symbol's inbound edges with the
// callers of its own members. Without it, "who calls Order" returns only the
// edges that name the type (references/inherits), missing every caller that
// goes through Order's methods. The container and its own members are
// excluded as callers so internal method-to-method calls don't masquerade as
// external usage; results are deduped against the existing inbound set.
func (a *Adapter) foldMemberCallers(ctx context.Context, sc *model.SymbolContext) error {
	if !isContainerExpandKind(sc.Symbol.Kind) {
		return nil
	}
	// Only enrich when the container has no real direct caller of its own —
	// the "a referenced class looks unused" case (graph returned [] while
	// blast found callers via the class's methods). A structural edge
	// (inherits/composes) or a low-confidence name-collision guess is not a
	// real caller, so those do not suppress enrichment.
	if hasUsageEdge(sc.Inbound) {
		return nil
	}
	childIDs, err := a.childSymbolIDs(ctx, sc.Symbol.ID)
	if err != nil {
		return err
	}
	if len(childIDs) == 0 {
		return nil
	}
	exclude := make(map[int64]struct{}, len(childIDs)+1)
	exclude[sc.Symbol.ID] = struct{}{}
	for _, cid := range childIDs {
		exclude[cid] = struct{}{}
	}
	seen := make(map[int64]struct{}, len(sc.Inbound))
	for _, e := range sc.Inbound {
		seen[e.Target.ID] = struct{}{}
	}
	for _, cid := range childIDs {
		refs, err := a.loadEdges(ctx, cid, false)
		if err != nil {
			return err
		}
		for _, e := range refs {
			if e.Target.ID == 0 || e.Edge.Kind == model.EdgeTemporal {
				continue
			}
			// Don't fold low-confidence guesses (e.g. name-collision
			// fallbacks stamped below the traversal floor) into the class's
			// callers — that would re-admit exactly what blast filters out.
			if e.Edge.Confidence < extract.ConfidenceUnresolved {
				continue
			}
			if _, skip := exclude[e.Target.ID]; skip {
				continue
			}
			if _, dup := seen[e.Target.ID]; dup {
				continue
			}
			seen[e.Target.ID] = struct{}{}
			sc.Inbound = append(sc.Inbound, e)
		}
	}
	return nil
}

// foldMemberCallees enriches a container symbol's outbound edges with the
// callees of its own members. Without it, "what does PriceValue call" returns
// only the edges the type itself names (usually none), so calls renders as
// `[]` — read as "depends on nothing" when the class in fact reaches Money.new,
// money.format, and friends through its methods. Calls back into the container
// or its own siblings are excluded so internal method-to-method traffic doesn't
// masquerade as an external dependency; results are deduped by target against
// the existing outbound set and each other.
func (a *Adapter) foldMemberCallees(ctx context.Context, sc *model.SymbolContext) error {
	if !isContainerExpandKind(sc.Symbol.Kind) {
		return nil
	}
	// Only enrich when the container has no real callee of its own — the
	// "a class's dependencies look empty" case. A structural edge
	// (inherits/composes) or a low-confidence guess is not a real callee, so
	// those do not suppress enrichment.
	if hasUsageEdge(sc.Outbound) {
		return nil
	}
	childIDs, err := a.childSymbolIDs(ctx, sc.Symbol.ID)
	if err != nil {
		return err
	}
	if len(childIDs) == 0 {
		return nil
	}
	exclude := make(map[int64]struct{}, len(childIDs)+1)
	exclude[sc.Symbol.ID] = struct{}{}
	for _, cid := range childIDs {
		exclude[cid] = struct{}{}
	}
	seen := make(map[int64]struct{}, len(sc.Outbound))
	for _, e := range sc.Outbound {
		seen[e.Target.ID] = struct{}{}
	}
	for _, cid := range childIDs {
		refs, err := a.loadEdges(ctx, cid, true)
		if err != nil {
			return err
		}
		for _, e := range refs {
			if e.Target.ID == 0 || e.Edge.Kind == model.EdgeTemporal {
				continue
			}
			// Don't fold low-confidence guesses (e.g. the 0.3 ERB/i18n
			// edges) into the class's callees — that would re-admit exactly
			// what blast and the graph confidence floor filter out.
			if e.Edge.Confidence < extract.ConfidenceUnresolved {
				continue
			}
			if _, skip := exclude[e.Target.ID]; skip {
				continue
			}
			if _, dup := seen[e.Target.ID]; dup {
				continue
			}
			// First member edge to a given target wins; the displayed
			// confidence is first-seen (by edge id), not the max across members.
			seen[e.Target.ID] = struct{}{}
			sc.Outbound = append(sc.Outbound, e)
		}
	}
	return nil
}

// childSymbolIDs returns the ids of symbols whose parent is parentID.
func (a *Adapter) childSymbolIDs(ctx context.Context, parentID int64) ([]int64, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id FROM sense_symbols WHERE parent_id = ?`, parentID)
	if err != nil {
		return nil, fmt.Errorf("sqlite childSymbolIDs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlite childSymbolIDs scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (a *Adapter) Query(ctx context.Context, f index.Filter) ([]model.Symbol, error) {
	limit := int64(f.Limit)
	if limit <= 0 {
		limit = -1 // SQLite convention for "no limit".
	}

	// Each optional filter is expressed as "(sentinel OR column = value)"
	// so a zero Filter matches everything in one prepared statement.
	const q = `
		SELECT id, file_id, name, qualified, kind, visibility, parent_id,
		       line_start, line_end, docstring, complexity, snippet
		FROM sense_symbols
		WHERE (?  = ''  OR name    = ?)
		  AND (?  = ''  OR kind    = ?)
		  AND (?  = 0   OR file_id = ?)
		ORDER BY id
		LIMIT ?`

	rows, err := a.db.QueryContext(ctx, q,
		f.Name, f.Name,
		string(f.Kind), string(f.Kind),
		f.FileID, f.FileID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite Query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []model.Symbol
	for rows.Next() {
		var (
			s          model.Symbol
			parentID   sql.NullInt64
			complexity sql.NullInt64
			visibility sql.NullString
			docstring  sql.NullString
			snippet    sql.NullString
		)
		if err := rows.Scan(
			&s.ID, &s.FileID, &s.Name, &s.Qualified, &s.Kind, &visibility,
			&parentID, &s.LineStart, &s.LineEnd, &docstring, &complexity, &snippet,
		); err != nil {
			return nil, fmt.Errorf("sqlite Query scan: %w", err)
		}
		model.HydrateSymbolNullables(&s, parentID, complexity, visibility, docstring, snippet)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite Query iterate: %w", err)
	}
	return out, nil
}

// SymbolRefs returns every symbol's (id, qualified, file_id, receiver,
// language) ordered by ascending id. It exists so resolution passes that build
// an in-memory qualified-name index can avoid hydrating Snippet / Docstring /
// Visibility fields that they immediately discard — about a 5× reduction in
// bytes loaded on a real-sized repo. The ascending id guarantee lets callers
// build multi-value maps without a follow-up sort: the first id under each key
// is deterministically the earliest written. The language is left-joined from
// sense_files so the resolver can gate cross-language bare-name matches; a
// symbol whose file row is missing returns an empty language.
func (a *Adapter) SymbolRefs(ctx context.Context) ([]model.SymbolRef, error) {
	const q = `SELECT s.id, s.qualified, s.file_id, s.receiver, COALESCE(f.language, ''), COALESCE(f.path, '')
		FROM sense_symbols s
		LEFT JOIN sense_files f ON f.id = s.file_id
		ORDER BY s.id ASC`
	rows, err := a.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite SymbolRefs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var refs []model.SymbolRef
	for rows.Next() {
		var r model.SymbolRef
		if err := rows.Scan(&r.ID, &r.Qualified, &r.FileID, &r.Receiver, &r.Language, &r.Path); err != nil {
			return nil, fmt.Errorf("sqlite SymbolRefs scan: %w", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite SymbolRefs iterate: %w", err)
	}
	return refs, nil
}

// EdgesOfKind returns all edges of a given kind. Used by post-extraction
// passes (e.g. interface satisfaction) that need to query relationship
// data across the entire index.
func (a *Adapter) EdgesOfKind(ctx context.Context, kind model.EdgeKind) ([]model.Edge, error) {
	const q = `SELECT id, source_id, target_id, kind, file_id, line, confidence
		FROM sense_edges WHERE kind = ? ORDER BY id ASC`
	rows, err := a.db.QueryContext(ctx, q, string(kind))
	if err != nil {
		return nil, fmt.Errorf("sqlite EdgesOfKind: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []model.Edge
	for rows.Next() {
		var e model.Edge
		var sourceID, line sql.NullInt64
		if err := rows.Scan(&e.ID, &sourceID, &e.TargetID, &e.Kind, &e.FileID, &line, &e.Confidence); err != nil {
			return nil, fmt.Errorf("sqlite EdgesOfKind scan: %w", err)
		}
		if sourceID.Valid {
			v := sourceID.Int64
			e.SourceID = &v
		}
		if line.Valid {
			v := int(line.Int64)
			e.Line = &v
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite EdgesOfKind iterate: %w", err)
	}
	return out, nil
}

// FileIDsByLanguage returns the IDs of all files with the given language.
func (a *Adapter) FileIDsByLanguage(ctx context.Context, lang string) (map[int64]bool, error) {
	const q = `SELECT id FROM sense_files WHERE language = ?`
	rows, err := a.db.QueryContext(ctx, q, lang)
	if err != nil {
		return nil, fmt.Errorf("sqlite FileIDsByLanguage: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlite FileIDsByLanguage scan: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite FileIDsByLanguage iterate: %w", err)
	}
	return out, nil
}

// symbolsForFilesChunkSize caps the number of placeholders per IN clause
// to stay well within SQLite's variable limit.
const symbolsForFilesChunkSize = 500

// SymbolsForFiles returns the symbols belonging to the given file IDs,
// with the fields needed to construct embedding inputs: id, qualified name,
// kind, parent name (resolved via parent_id), and snippet. Large file ID
// lists are batched into chunks to avoid exceeding SQLite's variable limit.
func (a *Adapter) SymbolsForFiles(ctx context.Context, fileIDs []int64) ([]EmbedSymbol, error) {
	if len(fileIDs) == 0 {
		return nil, nil
	}
	var all []EmbedSymbol
	for i := 0; i < len(fileIDs); i += symbolsForFilesChunkSize {
		end := i + symbolsForFilesChunkSize
		if end > len(fileIDs) {
			end = len(fileIDs)
		}
		chunk, err := a.symbolsForFilesChunk(ctx, fileIDs[i:end])
		if err != nil {
			return nil, err
		}
		all = append(all, chunk...)
	}
	return all, nil
}

func (a *Adapter) symbolsForFilesChunk(ctx context.Context, fileIDs []int64) ([]EmbedSymbol, error) {
	placeholders := make([]byte, 0, len(fileIDs)*2-1)
	args := make([]any, len(fileIDs))
	for i, id := range fileIDs {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args[i] = id
	}
	q := `SELECT s.id, s.file_id, s.qualified, s.kind, COALESCE(p.name, ''),
		       s.snippet, s.line_start, s.line_end
		FROM sense_symbols s
		LEFT JOIN sense_symbols p ON s.parent_id = p.id
		WHERE s.file_id IN (` + string(placeholders) + `)
		ORDER BY s.id ASC`

	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite SymbolsForFiles: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var syms []EmbedSymbol
	for rows.Next() {
		var s EmbedSymbol
		var snippet sql.NullString
		if err := rows.Scan(&s.ID, &s.FileID, &s.Qualified, &s.Kind, &s.ParentName,
			&snippet, &s.LineStart, &s.LineEnd); err != nil {
			return nil, fmt.Errorf("sqlite SymbolsForFiles scan: %w", err)
		}
		s.Snippet = snippet.String
		syms = append(syms, s)
	}
	return syms, rows.Err()
}

// EmbedSymbol is the minimal shape needed to build an embedding input.
type EmbedSymbol struct {
	ID         int64
	FileID     int64
	Qualified  string
	Kind       string
	ParentName string
	Snippet    string
	LineStart  int
	LineEnd    int
}

// SymbolsWithoutEmbeddings returns all symbols that have no embedding yet,
// with the same fields as SymbolsForFiles. Used by the background embedder
// to process embedding debt.
func (a *Adapter) SymbolsWithoutEmbeddings(ctx context.Context) ([]EmbedSymbol, error) {
	const q = `SELECT s.id, s.file_id, s.qualified, s.kind, COALESCE(p.name, ''),
		       s.snippet, s.line_start, s.line_end
		FROM sense_symbols s
		LEFT JOIN sense_symbols p ON s.parent_id = p.id
		LEFT JOIN sense_embeddings e ON e.symbol_id = s.id
		WHERE e.symbol_id IS NULL
		ORDER BY s.id ASC`

	rows, err := a.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite SymbolsWithoutEmbeddings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var syms []EmbedSymbol
	for rows.Next() {
		var s EmbedSymbol
		var snippet sql.NullString
		if err := rows.Scan(&s.ID, &s.FileID, &s.Qualified, &s.Kind, &s.ParentName,
			&snippet, &s.LineStart, &s.LineEnd); err != nil {
			return nil, fmt.Errorf("sqlite SymbolsWithoutEmbeddings scan: %w", err)
		}
		s.Snippet = snippet.String
		syms = append(syms, s)
	}
	return syms, rows.Err()
}

// WriteEmbedding upserts a single embedding vector into sense_embeddings.
func (a *Adapter) WriteEmbedding(ctx context.Context, symbolID int64, vector []byte) error {
	const q = `INSERT INTO sense_embeddings (symbol_id, vector)
		VALUES (?, ?)
		ON CONFLICT(symbol_id) DO UPDATE SET vector = excluded.vector`
	_, err := a.db.ExecContext(ctx, q, symbolID, vector)
	if err != nil {
		return fmt.Errorf("sqlite WriteEmbedding: %w", err)
	}
	return nil
}

// PrepareEmbeddingStmt returns a prepared statement for batch-writing
// embeddings within a transaction. The caller must close the statement.
func (a *Adapter) PrepareEmbeddingStmt(ctx context.Context) (*sql.Stmt, error) {
	const q = `INSERT INTO sense_embeddings (symbol_id, vector)
		VALUES (?, ?)
		ON CONFLICT(symbol_id) DO UPDATE SET vector = excluded.vector`
	return a.db.PrepareContext(ctx, q)
}

// WriteMeta upserts a key-value pair into sense_meta.
func (a *Adapter) WriteMeta(ctx context.Context, key, value string) error {
	const q = `INSERT INTO sense_meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`
	_, err := a.db.ExecContext(ctx, q, key, value)
	if err != nil {
		return fmt.Errorf("sqlite WriteMeta: %w", err)
	}
	return nil
}

// ReadMeta returns the value for a key in sense_meta, or "" if not found.
func (a *Adapter) ReadMeta(ctx context.Context, key string) (string, error) {
	var value string
	err := a.db.QueryRowContext(ctx,
		"SELECT value FROM sense_meta WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sqlite ReadMeta: %w", err)
	}
	return value, nil
}

// EmbeddingDebtCount returns the number of symbols that lack embeddings.
func (a *Adapter) EmbeddingDebtCount(ctx context.Context) (int, error) {
	var count int
	err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sense_symbols s
		 LEFT JOIN sense_embeddings e ON e.symbol_id = s.id
		 WHERE e.symbol_id IS NULL`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("sqlite EmbeddingDebtCount: %w", err)
	}
	return count, nil
}

// ClearEmbeddings deletes all rows from sense_embeddings.
func (a *Adapter) ClearEmbeddings(ctx context.Context) error {
	_, err := a.db.ExecContext(ctx, "DELETE FROM sense_embeddings")
	if err != nil {
		return fmt.Errorf("sqlite ClearEmbeddings: %w", err)
	}
	return nil
}

// DeleteMeta removes a key from sense_meta.
func (a *Adapter) DeleteMeta(ctx context.Context, key string) error {
	_, err := a.db.ExecContext(ctx, "DELETE FROM sense_meta WHERE key = ?", key)
	if err != nil {
		return fmt.Errorf("sqlite DeleteMeta: %w", err)
	}
	return nil
}

func (a *Adapter) Clear(ctx context.Context) error {
	// Order respects foreign keys even if ON DELETE CASCADE would handle
	// it — explicit is clearer than clever.
	for _, tbl := range []string{
		"sense_edges",
		"sense_embeddings",
		"sense_symbols",
		"sense_files",
	} {
		if _, err := a.db.ExecContext(ctx, "DELETE FROM "+tbl); err != nil {
			return fmt.Errorf("sqlite Clear %s: %w", tbl, err)
		}
	}
	return nil
}

// dropAllSenseTables discovers and drops every sense_* table/view in the
// database. Using sqlite_master makes this future-proof — new tables added
// in later schema versions get dropped automatically without maintaining a
// parallel list.
func dropAllSenseTables(ctx context.Context, db *sql.DB) error {
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
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite list tables iterate: %w", err)
	}

	for _, e := range entries {
		stmt := fmt.Sprintf("DROP %s IF EXISTS %s", strings.ToUpper(e.typ), e.name)
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("sqlite drop %s %s: %w", e.typ, e.name, err)
		}
	}
	return nil
}

// -------------------- helpers --------------------

// loadEdges returns adjacency around symbolID. When outbound is true, the
// symbol is the edge source and the joined `other` row is the target;
// when false, the symbol is the edge target and `other` is the source.
// Either way `other` is the non-focal endpoint the EdgeRef exposes.
func (a *Adapter) loadEdges(ctx context.Context, symbolID int64, outbound bool) ([]model.EdgeRef, error) {
	var q string
	if outbound {
		q = `
		SELECT
			e.id, e.source_id, e.target_id, e.kind, e.file_id, e.line, e.confidence,
			other.id, other.file_id, other.name, other.qualified, other.kind,
			other.visibility, other.parent_id, other.line_start, other.line_end,
			other.docstring, other.complexity, other.snippet
		FROM sense_edges   e
		JOIN sense_symbols other ON other.id = e.target_id
		WHERE e.source_id = ?
		ORDER BY e.id`
	} else {
		q = `
		SELECT
			e.id, e.source_id, e.target_id, e.kind, e.file_id, e.line, e.confidence,
			COALESCE(other.id, 0), COALESCE(other.file_id, 0),
			COALESCE(other.name, ''), COALESCE(other.qualified, ''),
			COALESCE(other.kind, ''), other.visibility, other.parent_id,
			COALESCE(other.line_start, 0), COALESCE(other.line_end, 0),
			other.docstring, other.complexity, other.snippet
		FROM sense_edges   e
		LEFT JOIN sense_symbols other ON other.id = e.source_id
		WHERE e.target_id = ?
		ORDER BY e.id`
	}

	rows, err := a.db.QueryContext(ctx, q, symbolID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var refs []model.EdgeRef
	for rows.Next() {
		var (
			e               model.Edge
			other           model.Symbol
			sourceID        sql.NullInt64
			line            sql.NullInt64
			otherParentID   sql.NullInt64
			otherComplexity sql.NullInt64
			otherVisibility sql.NullString
			otherDocstring  sql.NullString
			otherSnippet    sql.NullString
		)
		if err := rows.Scan(
			&e.ID, &sourceID, &e.TargetID, &e.Kind, &e.FileID, &line, &e.Confidence,
			&other.ID, &other.FileID, &other.Name, &other.Qualified, &other.Kind,
			&otherVisibility, &otherParentID, &other.LineStart, &other.LineEnd,
			&otherDocstring, &otherComplexity, &otherSnippet,
		); err != nil {
			return nil, err
		}
		if sourceID.Valid {
			e.SourceID = &sourceID.Int64
		}
		if line.Valid {
			l := int(line.Int64)
			e.Line = &l
		}
		model.HydrateSymbolNullables(&other, otherParentID, otherComplexity, otherVisibility, otherDocstring, otherSnippet)
		refs = append(refs, model.EdgeRef{Edge: e, Target: other})
	}
	return refs, rows.Err()
}

// nullableInt64 returns nil (mapped to SQL NULL) when p is nil, otherwise
// the dereferenced value. database/sql's driver converts a Go nil into a
// SQL NULL; a typed zero (e.g. int64(0)) becomes SQL 0.
func nullableInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
