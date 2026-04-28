package blast

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/luuuc/sense/internal/model"
)

// ErrSymbolNotFound is returned when Compute's subject id doesn't
// resolve to a sense_symbols row. Kept as a sentinel so CLI / MCP
// callers can distinguish "you asked about a non-existent symbol"
// from a real I/O error.
var ErrSymbolNotFound = errors.New("blast: symbol not found")

// loadSymbol fetches one symbol by id. Kept separate from loadSymbols
// because the subject lookup is required for Compute —
// ErrSymbolNotFound is meaningful for a CLI that wants to print "no
// symbol with id X" instead of an I/O error trace.
//
// Column order mirrors the sqlite adapter's Query method; row scan
// uses model.HydrateSymbolNullables so both packages stay in sync
// when nullable columns are added to the schema.
func loadSymbol(ctx context.Context, db *sql.DB, id int64) (model.Symbol, error) {
	const q = `SELECT id, file_id, name, qualified, kind, visibility, parent_id,
	                  line_start, line_end, docstring, complexity, snippet
	           FROM sense_symbols WHERE id = ?`
	var (
		s          model.Symbol
		parentID   sql.NullInt64
		complexity sql.NullInt64
		visibility sql.NullString
		docstring  sql.NullString
		snippet    sql.NullString
	)
	err := db.QueryRowContext(ctx, q, id).Scan(
		&s.ID, &s.FileID, &s.Name, &s.Qualified, &s.Kind, &visibility,
		&parentID, &s.LineStart, &s.LineEnd, &docstring, &complexity, &snippet,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Symbol{}, ErrSymbolNotFound
	}
	if err != nil {
		return model.Symbol{}, err
	}
	model.HydrateSymbolNullables(&s, parentID, complexity, visibility, docstring, snippet)
	return s, nil
}

// loadSymbols hydrates a set of symbol ids into model.Symbol records,
// returned keyed by id for O(1) lookup in the BFS result assembly.
// Chunked on SQLITE_MAX_VARIABLE_NUMBER (999) for robustness on wide
// hops; pitch-scale calls typically fit in one chunk.
func loadSymbols(ctx context.Context, db *sql.DB, ids []int64) (map[int64]model.Symbol, error) {
	out := make(map[int64]model.Symbol, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]

		placeholders := strings.Repeat("?,", len(batch))
		placeholders = placeholders[:len(placeholders)-1]
		q := `SELECT id, file_id, name, qualified, kind, visibility, parent_id,
		             line_start, line_end, docstring, complexity, snippet
		      FROM sense_symbols
		      WHERE id IN (` + placeholders + `)`
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
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
				_ = rows.Close()
				return nil, fmt.Errorf("scan symbol: %w", err)
			}
			model.HydrateSymbolNullables(&s, parentID, complexity, visibility, docstring, snippet)
			out[s.ID] = s
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// SiblingSymbolIDs returns all symbol IDs sharing the same qualified
// name and kind as the given symbol. The input symbolID is always
// first in the returned slice so Compute uses it as the canonical
// subject for display. This aggregates Ruby class reopenings (and
// similar patterns) so blast radius can seed the BFS with all
// definitions of the same logical class.
func SiblingSymbolIDs(ctx context.Context, db *sql.DB, symbolID int64) ([]int64, error) {
	const q = `SELECT s2.id FROM sense_symbols s1
	           JOIN sense_symbols s2 ON s2.qualified = s1.qualified AND s2.kind = s1.kind
	           WHERE s1.id = ? AND s2.id != ?
	           ORDER BY s2.id`
	rows, err := db.QueryContext(ctx, q, symbolID, symbolID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []int64{symbolID}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func filterIDs(ids []int64, keep map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := keep[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// sortSymbolsByID provides deterministic output ordering. Callers
// consuming the blast Result (CLI tables, MCP responses) don't need
// to impose their own sort.
func sortSymbolsByID(ss []model.Symbol) {
	sort.Slice(ss, func(i, j int) bool { return ss[i].ID < ss[j].ID })
}

// sortHopsByID provides deterministic ordering for indirect callers
// — ascending by the caller symbol's id. Callers at the same hop
// distance land in index-insertion order.
func sortHopsByID(hops []CallerHop) {
	sort.Slice(hops, func(i, j int) bool { return hops[i].Symbol.ID < hops[j].Symbol.ID })
}
