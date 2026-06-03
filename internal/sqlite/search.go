package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"slices"
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

	ftsQuery := buildFTSQuery(query)

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
		args = []any{ftsQuery, language, limit}
	} else {
		q = `SELECT s.id, s.name, s.qualified, s.kind, s.file_id, s.line_start,
		            s.snippet, -rank AS score
		     FROM sense_symbols_fts
		     JOIN sense_symbols s ON s.id = sense_symbols_fts.rowid
		     WHERE sense_symbols_fts MATCH ?
		     ORDER BY rank
		     LIMIT ?`
		args = []any{ftsQuery, limit}
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

// buildFTSQuery constructs an FTS5 MATCH expression that searches the
// original tokens plus decomposed tokens in name_parts. For a query
// like "PaymentLink", the result is:
//
//	("PaymentLink") OR (name_parts:"payment" name_parts:"link")
func buildFTSQuery(raw string) string {
	sanitized := sanitizeFTS5Query(raw)
	decomposed := Decompose(raw)
	decompTokens := strings.Fields(decomposed)
	origTokens := strings.Fields(strings.ToLower(raw))

	if len(decompTokens) <= 1 || slices.Equal(decompTokens, origTokens) {
		return sanitized
	}

	var parts []string
	for _, tok := range decompTokens {
		tok = strings.ReplaceAll(tok, `"`, `""`)
		parts = append(parts, `name_parts:"`+tok+`"`)
	}
	return "(" + sanitized + ") OR (" + strings.Join(parts, " ") + ")"
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

// DocumentFrequency returns, for each distinct term, the number of
// symbols whose FTS5 row matches that term in any indexed column (name,
// qualified, docstring, snippet, name_parts). It runs one COUNT(*) MATCH
// query per distinct term. A term that sanitizes to empty is reported
// with count 0. Used to identify high-frequency "generic" query tokens
// (e.g. "prevent", "handle") that should not, on their own, rank a hit.
func (a *Adapter) DocumentFrequency(ctx context.Context, terms []string) (map[string]int, error) {
	df := make(map[string]int, len(terms))
	for _, term := range terms {
		if _, done := df[term]; done {
			continue
		}
		sanitized := sanitizeFTS5Query(term)
		if strings.TrimSpace(sanitized) == "" {
			df[term] = 0
			continue
		}
		var count int
		err := a.db.QueryRowContext(ctx,
			`SELECT count(*) FROM sense_symbols_fts WHERE sense_symbols_fts MATCH ?`,
			sanitized).Scan(&count)
		if err != nil {
			return nil, fmt.Errorf("sqlite DocumentFrequency %q: %w", term, err)
		}
		df[term] = count
	}
	return df, nil
}

// LoadEmbeddings returns all embeddings as a map from symbol ID to
// float32 vector. Used at startup to populate the in-memory flat index.
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

// CalleeIDs returns outbound "calls" edge targets for each source symbol.
// Used by graph-augmented search enrichment to find 1-hop callees.
func (a *Adapter) CalleeIDs(ctx context.Context, symbolIDs []int64) (map[int64][]int64, error) {
	if len(symbolIDs) == 0 {
		return nil, nil
	}
	out := make(map[int64][]int64, len(symbolIDs))
	placeholders := make([]byte, 0, len(symbolIDs)*2-1)
	args := make([]any, len(symbolIDs))
	for i, id := range symbolIDs {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args[i] = id
	}
	q := `SELECT source_id, target_id FROM sense_edges
	      WHERE source_id IN (` + string(placeholders) + `) AND kind = 'calls'`
	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite CalleeIDs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var src, tgt int64
		if err := rows.Scan(&src, &tgt); err != nil {
			return nil, fmt.Errorf("sqlite CalleeIDs scan: %w", err)
		}
		out[src] = append(out[src], tgt)
	}
	return out, rows.Err()
}

// FilePathsByIDs returns the file path for each of the given file IDs.
// Used by search re-ranking to apply path-based score weights.
func (a *Adapter) FilePathsByIDs(ctx context.Context, fileIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(fileIDs))
	if len(fileIDs) == 0 {
		return out, nil
	}
	const chunk = 500
	for start := 0; start < len(fileIDs); start += chunk {
		end := start + chunk
		if end > len(fileIDs) {
			end = len(fileIDs)
		}
		batch := fileIDs[start:end]
		placeholders := make([]byte, 0, len(batch)*2-1)
		args := make([]any, len(batch))
		for i, id := range batch {
			if i > 0 {
				placeholders = append(placeholders, ',')
			}
			placeholders = append(placeholders, '?')
			args[i] = id
		}
		q := `SELECT id, path FROM sense_files WHERE id IN (` + string(placeholders) + `)`
		rows, err := a.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("sqlite FilePathsByIDs: %w", err)
		}
		for rows.Next() {
			var id int64
			var path string
			if err := rows.Scan(&id, &path); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("sqlite FilePathsByIDs scan: %w", err)
			}
			out[id] = path
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// ParentInfo holds metadata about a parent symbol, returned by ParentSymbols.
type ParentInfo struct {
	ParentID  int64
	Name      string
	Qualified string
	Kind      string
	FileID    int64
	LineStart int
	Snippet   string
}

// ParentSymbols returns parent symbol info for each child symbol ID.
// The result map is keyed by child symbol ID. Only symbols that have
// a non-null parent_id are included.
func (a *Adapter) ParentSymbols(ctx context.Context, childIDs []int64) (map[int64]ParentInfo, error) {
	if len(childIDs) == 0 {
		return nil, nil
	}
	out := make(map[int64]ParentInfo, len(childIDs))
	const chunk = 500
	for start := 0; start < len(childIDs); start += chunk {
		end := start + chunk
		if end > len(childIDs) {
			end = len(childIDs)
		}
		if err := a.parentSymbolsBatch(ctx, childIDs[start:end], out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (a *Adapter) parentSymbolsBatch(ctx context.Context, batch []int64, out map[int64]ParentInfo) error {
	placeholders := make([]byte, 0, len(batch)*2-1)
	args := make([]any, len(batch))
	for i, id := range batch {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args[i] = id
	}
	q := `SELECT s.id, p.id, p.name, p.qualified, p.kind, p.file_id, p.line_start, p.snippet
	      FROM sense_symbols s
	      JOIN sense_symbols p ON s.parent_id = p.id
	      WHERE s.id IN (` + string(placeholders) + `)`
	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("sqlite ParentSymbols: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var childID int64
		var pi ParentInfo
		var snippet sql.NullString
		if err := rows.Scan(&childID, &pi.ParentID, &pi.Name, &pi.Qualified,
			&pi.Kind, &pi.FileID, &pi.LineStart, &snippet); err != nil {
			return fmt.Errorf("sqlite ParentSymbols scan: %w", err)
		}
		pi.Snippet = snippet.String
		out[childID] = pi
	}
	return rows.Err()
}

// SubstringSearch runs a LIKE query against the qualified column in
// sense_symbols as a fallback when FTS5 returns few results. This catches
// partial name matches like "middleware" finding "applyMiddlewareStack".
func (a *Adapter) SubstringSearch(ctx context.Context, query string, language string, limit int) ([]SearchResult, error) {
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	escaped := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(strings.ToLower(query))
	q := `SELECT s.id, s.name, s.qualified, s.kind, s.file_id, s.line_start,
	             s.snippet, 1.0 AS score
	      FROM sense_symbols s
	      JOIN sense_files f ON f.id = s.file_id
	      WHERE LOWER(s.qualified) LIKE ? ESCAPE '\'`
	args := []any{"%" + escaped + "%"}
	if language != "" {
		q += " AND f.language = ?"
		args = append(args, language)
	}
	q += " LIMIT ?"
	args = append(args, limit)

	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite SubstringSearch: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var snippet sql.NullString
		if err := rows.Scan(&r.SymbolID, &r.Name, &r.Qualified, &r.Kind,
			&r.FileID, &r.LineStart, &snippet, &r.Score); err != nil {
			return nil, fmt.Errorf("sqlite SubstringSearch scan: %w", err)
		}
		r.Snippet = snippet.String
		results = append(results, r)
	}
	return results, rows.Err()
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
	return strings.Join(quoted, " OR ")
}
