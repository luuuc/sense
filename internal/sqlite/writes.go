package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/luuuc/sense/internal/model"
)

// DeleteFile removes a file and (via FK CASCADE) its symbols from the index.
func (a *Adapter) DeleteFile(ctx context.Context, path string) error {
	_, err := a.db.ExecContext(ctx, "DELETE FROM sense_files WHERE path = ?", path)
	if err != nil {
		return fmt.Errorf("sqlite DeleteFile: %w", err)
	}
	return nil
}

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

// DeleteMeta removes a key from sense_meta.
func (a *Adapter) DeleteMeta(ctx context.Context, key string) error {
	_, err := a.db.ExecContext(ctx, "DELETE FROM sense_meta WHERE key = ?", key)
	if err != nil {
		return fmt.Errorf("sqlite DeleteMeta: %w", err)
	}
	return nil
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
