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
	db.SetMaxOpenConns(1)

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

	hydrateNullableSymbol(&sym, parentID, complexity, visibility, docstring, snippet)
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
		hydrateNullableSymbol(&s, parentID, complexity, visibility, docstring, snippet)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite Query iterate: %w", err)
	}
	return out, nil
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
		hydrateNullableSymbol(&other, otherParentID, otherComplexity, otherVisibility, otherDocstring, otherSnippet)
		refs = append(refs, model.EdgeRef{Edge: e, Target: other})
	}
	return refs, rows.Err()
}

// hydrateNullableSymbol copies nullable columns from their sql.NullXxx
// carriers back onto the Symbol. Kept as a free function so the row-scan
// sites above stay linear.
func hydrateNullableSymbol(
	s *model.Symbol,
	parentID sql.NullInt64,
	complexity sql.NullInt64,
	visibility sql.NullString,
	docstring sql.NullString,
	snippet sql.NullString,
) {
	if parentID.Valid {
		p := parentID.Int64
		s.ParentID = &p
	}
	if complexity.Valid {
		c := int(complexity.Int64)
		s.Complexity = &c
	}
	s.Visibility = visibility.String
	s.Docstring = docstring.String
	s.Snippet = snippet.String
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
