package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
)

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
