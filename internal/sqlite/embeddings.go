package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// symbolsForFilesChunkSize caps the number of placeholders per IN clause
// to stay well within SQLite's variable limit.
const symbolsForFilesChunkSize = 500

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
