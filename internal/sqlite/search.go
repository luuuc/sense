package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// SearchResult is a single keyword-search hit with its BM25 score and
// enough metadata to render a result line or feed into fusion.
type SearchResult struct {
	SymbolID  int64
	Name      string
	Qualified string
	Kind      string
	FileID    int64
	LineStart int
	Snippet   string
	Score     float64
}

// KeywordSearch runs an FTS5 MATCH query against the sense_symbols_fts
// table and returns results ranked by BM25 score. The language filter
// is optional — pass "" to search all languages. Results are capped at
// limit. The query is sanitized to prevent FTS5 syntax errors from
// user input.
func (a *Adapter) KeywordSearch(ctx context.Context, query string, language string, limit int) ([]SearchResult, error) {
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}

	query = sanitizeFTS5Query(query)

	var q string
	var args []any

	if language != "" {
		q = `SELECT s.id, s.name, s.qualified, s.kind, s.file_id, s.line_start,
		            s.snippet, -rank AS score
		     FROM sense_symbols_fts
		     JOIN sense_symbols s ON s.id = sense_symbols_fts.rowid
		     JOIN sense_files   f ON f.id = s.file_id
		     WHERE sense_symbols_fts MATCH ?
		       AND f.language = ?
		     ORDER BY rank
		     LIMIT ?`
		args = []any{query, language, limit}
	} else {
		q = `SELECT s.id, s.name, s.qualified, s.kind, s.file_id, s.line_start,
		            s.snippet, -rank AS score
		     FROM sense_symbols_fts
		     JOIN sense_symbols s ON s.id = sense_symbols_fts.rowid
		     WHERE sense_symbols_fts MATCH ?
		     ORDER BY rank
		     LIMIT ?`
		args = []any{query, limit}
	}

	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite KeywordSearch: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var snippet sql.NullString
		if err := rows.Scan(&r.SymbolID, &r.Name, &r.Qualified, &r.Kind,
			&r.FileID, &r.LineStart, &snippet, &r.Score); err != nil {
			return nil, fmt.Errorf("sqlite KeywordSearch scan: %w", err)
		}
		r.Snippet = snippet.String
		results = append(results, r)
	}
	return results, rows.Err()
}

// SymbolCount returns the total number of symbols in the index.
func (a *Adapter) SymbolCount(ctx context.Context) (int, error) {
	var count int
	err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_symbols`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("sqlite SymbolCount: %w", err)
	}
	return count, nil
}

// LoadEmbeddings returns all embeddings as a map from symbol ID to
// float32 vector. Used at startup to populate the in-memory HNSW index.
func (a *Adapter) LoadEmbeddings(ctx context.Context) (map[int64][]float32, error) {
	var count int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_embeddings`).Scan(&count); err != nil {
		return nil, fmt.Errorf("sqlite LoadEmbeddings count: %w", err)
	}

	rows, err := a.db.QueryContext(ctx, `SELECT symbol_id, vector FROM sense_embeddings`)
	if err != nil {
		return nil, fmt.Errorf("sqlite LoadEmbeddings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64][]float32, count)
	for rows.Next() {
		var id int64
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, fmt.Errorf("sqlite LoadEmbeddings scan: %w", err)
		}
		out[id] = blobToVector(blob)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite LoadEmbeddings iterate: %w", err)
	}
	return out, nil
}

func blobToVector(blob []byte) []float32 {
	n := len(blob) / 4
	vec := make([]float32, n)
	for i := range n {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return vec
}

// SymbolsByIDs returns symbol metadata for the given IDs, keyed by ID.
// Used to hydrate vector-only search results with display metadata.
func (a *Adapter) SymbolsByIDs(ctx context.Context, ids []int64) (map[int64]SearchResult, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out := make(map[int64]SearchResult, len(ids))
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := make([]byte, 0, len(batch)*2-1)
		args := make([]any, len(batch))
		for i, id := range batch {
			if i > 0 {
				placeholders = append(placeholders, ',')
			}
			placeholders = append(placeholders, '?')
			args[i] = id
		}
		q := `SELECT s.id, s.name, s.qualified, s.kind, s.file_id, s.line_start, s.snippet
		      FROM sense_symbols s
		      WHERE s.id IN (` + string(placeholders) + `)`
		rows, err := a.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("sqlite SymbolsByIDs: %w", err)
		}
		for rows.Next() {
			var r SearchResult
			var snippet sql.NullString
			if err := rows.Scan(&r.SymbolID, &r.Name, &r.Qualified, &r.Kind,
				&r.FileID, &r.LineStart, &snippet); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("sqlite SymbolsByIDs scan: %w", err)
			}
			r.Snippet = snippet.String
			out[r.SymbolID] = r
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// InboundEdgeCounts returns the inbound edge count for each of the
// given symbol IDs in a single query. Used as a graph centrality proxy
// for search re-ranking. The result map only contains entries for
// symbols that have at least one inbound edge.
func (a *Adapter) InboundEdgeCounts(ctx context.Context, symbolIDs []int64) (map[int64]int, error) {
	if len(symbolIDs) == 0 {
		return nil, nil
	}
	out := make(map[int64]int, len(symbolIDs))
	const chunk = 500
	for start := 0; start < len(symbolIDs); start += chunk {
		end := start + chunk
		if end > len(symbolIDs) {
			end = len(symbolIDs)
		}
		batch := symbolIDs[start:end]
		placeholders := make([]byte, 0, len(batch)*2-1)
		args := make([]any, len(batch))
		for i, id := range batch {
			if i > 0 {
				placeholders = append(placeholders, ',')
			}
			placeholders = append(placeholders, '?')
			args[i] = id
		}
		q := `SELECT target_id, COUNT(*) FROM sense_edges
		      WHERE target_id IN (` + string(placeholders) + `)
		      GROUP BY target_id`
		rows, err := a.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("sqlite InboundEdgeCounts: %w", err)
		}
		for rows.Next() {
			var id int64
			var count int
			if err := rows.Scan(&id, &count); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("sqlite InboundEdgeCounts scan: %w", err)
			}
			out[id] = count
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// sanitizeFTS5Query quotes each whitespace-delimited token so that
// FTS5 operator characters (*, ", OR, AND, NOT, NEAR, ^, :) in user
// input are treated as literals. Embedded double-quotes are escaped
// by doubling them per the FTS5 spec. Empty tokens are dropped.
func sanitizeFTS5Query(q string) string {
	tokens := strings.Fields(q)
	if len(tokens) == 0 {
		return q
	}
	quoted := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		tok = strings.ReplaceAll(tok, `"`, `""`)
		quoted = append(quoted, `"`+tok+`"`)
	}
	return strings.Join(quoted, " ")
}

