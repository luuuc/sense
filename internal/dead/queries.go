package dead

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/luuuc/sense/internal/extract"
)

// This file holds the index queries the dead-code pass runs against the SQLite
// store: the candidate set (zero-reference symbols), the structural-liveness
// queries (tests targets, included modules, controller concerns, value objects,
// interface methods, live containers), and the name-occurrence estimate. Each is
// a thin wrapper over a SQL statement returning typed sets the orchestrator and
// the predicates consume.

func countSymbols(ctx context.Context, db *sql.DB, opts Options) (int, error) {
	q := `SELECT COUNT(*) FROM sense_symbols s
		JOIN sense_files f ON s.file_id = f.id
		WHERE s.kind IN ('function', 'method', 'class', 'module', 'type', 'interface', 'constant')
		AND s.qualified NOT LIKE '` + syntheticPrefixPattern + `'
		AND s.qualified NOT LIKE '` + routePrefixPattern + `'`
	var args []any

	if opts.Language != "" {
		q += " AND f.language = ?"
		args = append(args, opts.Language)
	}
	if opts.Domain != "" {
		q += " AND f.path LIKE ?"
		args = append(args, "%"+opts.Domain+"%")
	}

	var count int
	if err := db.QueryRowContext(ctx, q, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func queryCandidates(ctx context.Context, db *sql.DB, opts Options) ([]Symbol, error) {
	edgeFilter := `SELECT 1 FROM sense_edges e
			WHERE e.target_id = s.id
			AND e.kind IN ('calls', 'composes', 'includes', 'inherits', 'references')`
	if opts.ExcludeTestRefs {
		edgeFilter += `
			AND NOT EXISTS (
				SELECT 1 FROM sense_files ef
				WHERE ef.id = e.file_id
				AND (ef.path LIKE '%_test.%'
					OR ef.path LIKE '%/test/%'
					OR ef.path LIKE '%/tests/%'
					OR ef.path LIKE '%/spec/%'
					OR ef.path LIKE '%.test.%'
					OR ef.path LIKE '%.spec.%'
					OR ef.path LIKE '%test_%'
					OR ef.path LIKE '%/__tests__/%')
			)`
	}

	q := `SELECT s.id, s.name, s.qualified, s.kind, f.path, s.file_id, f.language, s.line_start, s.line_end, s.parent_id, s.visibility
		FROM sense_symbols s
		JOIN sense_files f ON s.file_id = f.id
		WHERE NOT EXISTS (` + edgeFilter + `)
		AND s.kind IN ('function', 'method', 'class', 'module', 'type', 'interface', 'constant')
		AND s.qualified NOT LIKE '` + syntheticPrefixPattern + `'
		AND s.qualified NOT LIKE '` + routePrefixPattern + `'
		AND f.path NOT LIKE '%.d.ts'`
	var args []any

	if opts.Language != "" {
		q += " AND f.language = ?"
		args = append(args, opts.Language)
	}
	if opts.Domain != "" {
		q += " AND f.path LIKE ?"
		args = append(args, "%"+opts.Domain+"%")
	}

	q += " ORDER BY f.path, s.line_start"

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Symbol
	for rows.Next() {
		var sym Symbol
		var parentID sql.NullInt64
		var visibility sql.NullString
		if err := rows.Scan(&sym.ID, &sym.Name, &sym.Qualified, &sym.Kind,
			&sym.File, &sym.FileID, &sym.Language, &sym.LineStart, &sym.LineEnd, &parentID, &visibility); err != nil {
			return nil, err
		}
		sym.Visibility = visibility.String
		if parentID.Valid {
			p := parentID.Int64
			sym.ParentID = &p
		}
		out = append(out, sym)
	}
	return out, rows.Err()
}

func queryTestsTargets(ctx context.Context, db *sql.DB) (map[int64]struct{}, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT target_id FROM sense_edges WHERE kind = 'tests'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// queryControllerConcernModuleIDs returns IDs of modules included into a
// class whose name ends in "Controller". Their instance methods become
// routed controller actions (ActiveSupport::Concern mixed into a
// controller), so they are framework entry points, not dead code.
func queryControllerConcernModuleIDs(ctx context.Context, db *sql.DB) (map[int64]struct{}, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT e.target_id FROM sense_edges e
		JOIN sense_symbols s ON s.id = e.source_id
		WHERE e.kind = 'includes' AND e.target_id IS NOT NULL AND s.name LIKE '%Controller'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// queryInterfaceMethodNames returns the set of method names declared on any
// interface (a method symbol whose parent's kind is 'interface'). The Go voice
// reads it: a concrete method sharing a name with an interface method may be
// reached only through the interface, where the static graph shows zero direct
// callers, so it stays open-world (go_interface) rather than earning `dead`. Go
// interface satisfaction is structural (no `implements` keyword), so name match
// is the soundest signal the index carries without recomputing satisfaction.
func queryInterfaceMethodNames(ctx context.Context, db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT s.name FROM sense_symbols s
		JOIN sense_symbols p ON p.id = s.parent_id
		WHERE s.kind = 'method' AND p.kind = 'interface'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = struct{}{}
	}
	return out, rows.Err()
}

// queryIncludedModuleIDs returns IDs of modules included anywhere (any
// incoming includes edge). A method on such a module is reachable through
// the including type, so a zero-caller verdict is uncertain rather than
// dead.
func queryIncludedModuleIDs(ctx context.Context, db *sql.DB) (map[int64]struct{}, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT target_id FROM sense_edges WHERE kind = 'includes' AND target_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// queryValueObjectClassIDs returns IDs of classes that carry an
// `inherits` edge to a synthetic Ruby-core value-object base
// (ruby-core:Struct / ruby-core:Data). These are `CONST = Struct.new`/
// `Data.define` value objects; their public instance methods form a
// duck-typed API surface reached via `x.method` on a local whose type
// the static indexer cannot infer — so a zero-caller verdict is
// uncertain, not dead. Keying on the structural inherits edge (not a
// `*Result` name suffix) is the whole point of the synthetic base.
func queryValueObjectClassIDs(ctx context.Context, db *sql.DB) (map[int64]struct{}, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT e.source_id FROM sense_edges e
		JOIN sense_symbols t ON t.id = e.target_id
		WHERE e.kind = 'inherits' AND e.source_id IS NOT NULL
		  AND t.qualified IN (?, ?)`,
		extract.RubyCoreStruct, extract.RubyCoreData)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// populateFindingNameOccurrences fills NameOccurrences on each finding's
// Symbol with an index-derived estimate of how common its bare name is: the
// number of symbols sharing the name plus the number of resolved edges
// pointing at a symbol of that name. This proxies textual frequency without
// shelling out — a name defined and called many times is one a text grep
// would flood the caller with, so the verify-recipe builder swaps in a
// manual-inspect hint. Two batched queries (GROUP BY name over the finding
// set), so cost is independent of repo size.
func populateFindingNameOccurrences(ctx context.Context, db *sql.DB, findings []Finding) error {
	if len(findings) == 0 {
		return nil
	}
	names := make([]string, 0, len(findings))
	seen := make(map[string]struct{}, len(findings))
	for _, f := range findings {
		if _, ok := seen[f.Symbol.Name]; ok {
			continue
		}
		seen[f.Symbol.Name] = struct{}{}
		names = append(names, f.Symbol.Name)
	}

	counts := make(map[string]int, len(names))

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	args := make([]any, len(names))
	for i, n := range names {
		args[i] = n
	}

	symQ := `SELECT name, COUNT(*) FROM sense_symbols WHERE name IN (` + placeholders + `) GROUP BY name`
	if err := accumulateCounts(ctx, db, symQ, args, counts); err != nil {
		return err
	}

	edgeQ := `SELECT s.name, COUNT(*) FROM sense_edges e
		JOIN sense_symbols s ON s.id = e.target_id
		WHERE s.name IN (` + placeholders + `) GROUP BY s.name`
	if err := accumulateCounts(ctx, db, edgeQ, args, counts); err != nil {
		return err
	}

	for i := range findings {
		findings[i].Symbol.NameOccurrences = counts[findings[i].Symbol.Name]
	}
	return nil
}

// accumulateCounts runs a `SELECT name, COUNT(*) … GROUP BY name` query
// and adds each row's count into the shared totals map.
func accumulateCounts(ctx context.Context, db *sql.DB, q string, args []any, totals map[string]int) error {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return err
		}
		totals[name] += n
	}
	return rows.Err()
}

func hasMainFunction(ctx context.Context, db *sql.DB, opts Options) (bool, error) {
	q := `SELECT 1 FROM sense_symbols s
		JOIN sense_files f ON s.file_id = f.id
		WHERE s.name = 'main' AND s.kind = 'function'`
	var args []any

	if opts.Language != "" {
		q += " AND f.language = ?"
		args = append(args, opts.Language)
	}
	if opts.Domain != "" {
		q += " AND f.path LIKE ?"
		args = append(args, "%"+opts.Domain+"%")
	}

	q += " LIMIT 1"

	var exists int
	err := db.QueryRowContext(ctx, q, args...).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("dead: detect library: %w", err)
	}
	return true, nil
}

// findLiveContainers returns IDs of class/module candidates that have
// at least one child with incoming edges (i.e., child is NOT in the
// dead candidate set). A container with live children is alive by
// purpose — it's the namespace for referenced code.
func findLiveContainers(ctx context.Context, db *sql.DB, candidates []Symbol) (map[int64]struct{}, error) {
	deadIDs := make(map[int64]struct{}, len(candidates))
	for _, s := range candidates {
		deadIDs[s.ID] = struct{}{}
	}

	var containerIDs []int64
	for _, s := range candidates {
		if s.Kind == "class" || s.Kind == "module" {
			containerIDs = append(containerIDs, s.ID)
		}
	}

	if len(containerIDs) == 0 {
		return nil, nil
	}

	// Bulk-load all children of candidate containers, then partition in Go to
	// find which containers have a live (non-dead) child.
	childrenByParent, err := loadChildrenByParent(ctx, db, containerIDs)
	if err != nil {
		return nil, err
	}

	result := make(map[int64]struct{})
	for parentID, children := range childrenByParent {
		for _, childID := range children {
			if _, isDead := deadIDs[childID]; !isDead {
				result[parentID] = struct{}{}
				break
			}
		}
	}

	return result, nil
}

// loadChildrenByParent returns, for the given parent symbol IDs, the IDs of
// their direct children grouped by parent. The IN list is chunked so the query
// stays within SQLite's bound-parameter limit on a large candidate set.
func loadChildrenByParent(ctx context.Context, db *sql.DB, parentIDs []int64) (map[int64][]int64, error) {
	childrenByParent := map[int64][]int64{}
	const chunk = 500
	for start := 0; start < len(parentIDs); start += chunk {
		end := start + chunk
		if end > len(parentIDs) {
			end = len(parentIDs)
		}
		batch := parentIDs[start:end]

		placeholders := strings.Repeat("?,", len(batch))
		placeholders = placeholders[:len(placeholders)-1]

		q := `SELECT parent_id, id FROM sense_symbols
			WHERE parent_id IN (` + placeholders + `)`

		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}

		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var parentID, childID int64
			if err := rows.Scan(&parentID, &childID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			childrenByParent[parentID] = append(childrenByParent[parentID], childID)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return childrenByParent, nil
}
