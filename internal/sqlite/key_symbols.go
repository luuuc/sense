package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// KeySymbol is a high-reach type/interface/class with its declaration snippet.
type KeySymbol struct {
	ID        int64
	Qualified string
	Kind      string
	Snippet   string
	RefFiles  int
}

// SymbolCaller is a caller of a key symbol with its location.
type SymbolCaller struct {
	Qualified string
	Kind      string
	File      string
}

var builtinSymbols = map[string]bool{
	"new": true, "id": true, "map": true, "each": true,
	"present?": true, "nil?": true, "Close": true, "Error": true,
	"String": true, "make": true, "len": true, "append": true,
	"initialize": true, "to_s": true, "inspect": true,
}

// TopSymbolsByReach returns the top N types/interfaces/classes ordered by
// the number of distinct files that reference them (including references to
// their methods). Domain filters by file path prefix. This query powers the
// key_symbols section of sense_conventions.
func (a *Adapter) TopSymbolsByReach(ctx context.Context, domain string, limit int) ([]KeySymbol, error) {
	if limit <= 0 {
		limit = 15
	}

	q := `WITH type_edges AS (
	          SELECT e.target_id as type_id, e.file_id
	          FROM sense_edges e
	          JOIN sense_symbols s ON s.id = e.target_id
	          WHERE s.kind IN ('class','interface','module','type','struct','trait')
	          UNION ALL
	          SELECT child.parent_id as type_id, e.file_id
	          FROM sense_edges e
	          JOIN sense_symbols child ON child.id = e.target_id
	          WHERE child.parent_id IS NOT NULL
	      )
	      SELECT s.id, s.qualified, s.kind, s.snippet,
	             COUNT(DISTINCT te.file_id) as ref_files
	      FROM sense_symbols s
	      JOIN sense_files sf ON sf.id = s.file_id
	      JOIN type_edges te ON te.type_id = s.id
	      WHERE sf.path LIKE ? || '%'
	        AND s.kind IN ('class','interface','module','type','struct','trait')
	        AND sf.path NOT LIKE '%\_test.%' ESCAPE '\'
	        AND sf.path NOT LIKE '%/test/%'
	        AND sf.path NOT LIKE 'test/%'
	        AND sf.path NOT LIKE '%/spec/%'
	        AND sf.path NOT LIKE 'spec/%'
	        AND sf.path NOT LIKE '%/tests/%'
	        AND sf.path NOT LIKE 'tests/%'
	        AND sf.path NOT LIKE '%testdata%'
	        AND sf.path NOT LIKE '%fixture%'
	        AND sf.path NOT LIKE '%/mock/%'
	        AND sf.path NOT LIKE '%/mocks/%'
	        AND sf.path NOT LIKE '%vendor%'
	      GROUP BY s.id
	      ORDER BY ref_files DESC
	      LIMIT ?`

	args := []any{domain, limit * 2} // fetch extra to filter builtins

	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite TopSymbolsByReach: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []KeySymbol
	for rows.Next() {
		var ks KeySymbol
		var snippet sql.NullString
		if err := rows.Scan(&ks.ID, &ks.Qualified, &ks.Kind, &snippet, &ks.RefFiles); err != nil {
			return nil, fmt.Errorf("sqlite TopSymbolsByReach scan: %w", err)
		}
		ks.Snippet = snippet.String

		name := lastPart(ks.Qualified)
		if builtinSymbols[name] {
			continue
		}
		results = append(results, ks)
		if len(results) >= limit {
			break
		}
	}
	return results, rows.Err()
}

// TopCallers returns the top N callers/references for a given symbol ID.
func (a *Adapter) TopCallers(ctx context.Context, symbolID int64, limit int) ([]SymbolCaller, error) {
	if limit <= 0 {
		limit = 3
	}

	q := `SELECT src.qualified, src.kind, srcf.path
	      FROM sense_edges e
	      JOIN sense_symbols src ON src.id = e.source_id
	      JOIN sense_files srcf ON srcf.id = src.file_id
	      WHERE e.target_id = ?
	      ORDER BY src.kind = 'class' DESC, src.qualified
	      LIMIT ?`

	rows, err := a.db.QueryContext(ctx, q, symbolID, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite TopCallers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []SymbolCaller
	for rows.Next() {
		var c SymbolCaller
		if err := rows.Scan(&c.Qualified, &c.Kind, &c.File); err != nil {
			return nil, fmt.Errorf("sqlite TopCallers scan: %w", err)
		}
		results = append(results, c)
	}
	return results, rows.Err()
}

// CallersOfTargets returns the qualified names of callers for each target
// symbol ID, excluding callers that match excludeSourceID (typically the
// root symbol being queried). maxPerTarget caps results per target.
func (a *Adapter) CallersOfTargets(ctx context.Context, targetIDs []int64, excludeSourceID int64, maxPerTarget int) (map[int64][]string, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	if maxPerTarget <= 0 {
		maxPerTarget = 20
	}

	placeholders := make([]string, len(targetIDs))
	args := make([]any, len(targetIDs))
	for i, id := range targetIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	q := fmt.Sprintf(`SELECT e.target_id, src.qualified `+ //nolint:gosec // placeholders are literal "?" strings, not user input
		`FROM sense_edges e
		JOIN sense_symbols src ON src.id = e.source_id
		WHERE e.target_id IN (%s)
		  AND e.source_id != ?
		  AND e.kind IN ('calls', 'references')
		ORDER BY e.target_id, src.qualified`,
		strings.Join(placeholders, ","))
	args = append(args, excludeSourceID)

	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite CallersOfTargets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[int64][]string)
	for rows.Next() {
		var targetID int64
		var qualified string
		if err := rows.Scan(&targetID, &qualified); err != nil {
			return nil, fmt.Errorf("sqlite CallersOfTargets scan: %w", err)
		}
		if len(result[targetID]) < maxPerTarget {
			result[targetID] = append(result[targetID], qualified)
		}
	}
	return result, rows.Err()
}

func lastPart(qualified string) string {
	if i := strings.LastIndexByte(qualified, '.'); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}
