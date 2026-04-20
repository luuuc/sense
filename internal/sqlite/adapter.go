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
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
)

//go:embed schema.sql
var schemaSQL string

// schemaVersion is stamped into the database's PRAGMA user_version so a
// future adapter can detect a stale on-disk schema and rebuild — the
// "drops the old database, and rebuilds" path described in 04-storage.md.
// Bump this when schema.sql changes shape incompatibly.
const schemaVersion = 1

// maxOpenConns is the connection-pool size Open applies. InTx relies on
// this being 1 — its raw BEGIN/COMMIT approach shares a transaction
// across Adapter calls by sharing the single pooled connection. If this
// ever changes to allow concurrent writes, InTx must be reworked to use
// database/sql's proper *sql.Tx threading. The constant is checked at
// InTx entry so a future config change fails loudly instead of silently
// corrupting transactional semantics.
const maxOpenConns = 1

// Adapter is the SQLite implementation of index.Index.
type Adapter struct {
	db *sql.DB
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
	dsn := "file:" + path + "?_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(wal)" +
		"&_pragma=synchronous(normal)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}

	// Single connection serializes writes and reads. Scan is sequential
	// today; relax when concurrent MCP reads warrant the contention cost.
	// Load-bearing for InTx — see maxOpenConns constant.
	db.SetMaxOpenConns(maxOpenConns)

	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite schema: %w", err)
	}

	// PRAGMA user_version can't be parameterised; the integer is a trusted
	// build-time constant so interpolation is safe.
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite set user_version: %w", err)
	}

	return &Adapter{db: db}, nil
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
			(file_id, name, qualified, kind, visibility, parent_id,
			 line_start, line_end, docstring, complexity, snippet)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(file_id, qualified) DO UPDATE SET
			name       = excluded.name,
			kind       = excluded.kind,
			visibility = excluded.visibility,
			parent_id  = excluded.parent_id,
			line_start = excluded.line_start,
			line_end   = excluded.line_end,
			docstring  = excluded.docstring,
			complexity = excluded.complexity,
			snippet    = excluded.snippet
		RETURNING id`

	var id int64
	err := a.db.QueryRowContext(ctx, q,
		s.FileID, s.Name, s.Qualified, string(s.Kind), s.Visibility,
		nullableInt64(s.ParentID), s.LineStart, s.LineEnd,
		s.Docstring, nullableInt(s.Complexity), s.Snippet,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("sqlite WriteSymbol: %w", err)
	}
	return id, nil
}

func (a *Adapter) WriteEdge(ctx context.Context, e *model.Edge) (int64, error) {
	const q = `
		INSERT INTO sense_edges (source_id, target_id, kind, file_id, line, confidence)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_id, target_id, kind, file_id) DO UPDATE SET
			line       = excluded.line,
			confidence = excluded.confidence
		RETURNING id`

	var id int64
	err := a.db.QueryRowContext(ctx, q,
		e.SourceID, e.TargetID, string(e.Kind), e.FileID,
		nullableInt(e.Line), e.Confidence,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("sqlite WriteEdge: %w", err)
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

// SymbolRefs returns every symbol's (id, qualified, file_id) ordered
// by ascending id. It exists so resolution passes that build an
// in-memory qualified-name index can avoid hydrating Snippet /
// Docstring / Visibility fields that they immediately discard — about
// a 5× reduction in bytes loaded on a real-sized repo. The ascending
// id guarantee lets callers build multi-value maps without a
// follow-up sort: the first id under each key is deterministically
// the earliest written.
func (a *Adapter) SymbolRefs(ctx context.Context) ([]model.SymbolRef, error) {
	const q = `SELECT id, qualified, file_id FROM sense_symbols ORDER BY id ASC`
	rows, err := a.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite SymbolRefs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var refs []model.SymbolRef
	for rows.Next() {
		var r model.SymbolRef
		if err := rows.Scan(&r.ID, &r.Qualified, &r.FileID); err != nil {
			return nil, fmt.Errorf("sqlite SymbolRefs scan: %w", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite SymbolRefs iterate: %w", err)
	}
	return refs, nil
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
			other.id, other.file_id, other.name, other.qualified, other.kind,
			other.visibility, other.parent_id, other.line_start, other.line_end,
			other.docstring, other.complexity, other.snippet
		FROM sense_edges   e
		JOIN sense_symbols other ON other.id = e.source_id
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
			line            sql.NullInt64
			otherParentID   sql.NullInt64
			otherComplexity sql.NullInt64
			otherVisibility sql.NullString
			otherDocstring  sql.NullString
			otherSnippet    sql.NullString
		)
		if err := rows.Scan(
			&e.ID, &e.SourceID, &e.TargetID, &e.Kind, &e.FileID, &line, &e.Confidence,
			&other.ID, &other.FileID, &other.Name, &other.Qualified, &other.Kind,
			&otherVisibility, &otherParentID, &other.LineStart, &other.LineEnd,
			&otherDocstring, &otherComplexity, &otherSnippet,
		); err != nil {
			return nil, err
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
