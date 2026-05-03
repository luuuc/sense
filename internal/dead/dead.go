package dead

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const defaultLimit = 100

type Options struct {
	Language        string
	Domain          string
	Limit           int
	ExcludeTestRefs bool
}

type Symbol struct {
	ID         int64
	Name       string
	Qualified  string
	Kind       string
	File       string
	FileID     int64
	LineStart  int
	LineEnd    int
	ParentID   *int64
	Confidence string
}

type Result struct {
	Dead         []Symbol
	TotalSymbols int
}

func FindDead(ctx context.Context, db *sql.DB, opts Options) (Result, error) {
	if opts.Limit <= 0 {
		opts.Limit = defaultLimit
	}

	totalSymbols, err := countSymbols(ctx, db, opts)
	if err != nil {
		return Result{}, fmt.Errorf("dead: count symbols: %w", err)
	}

	candidates, err := queryCandidates(ctx, db, opts)
	if err != nil {
		return Result{}, fmt.Errorf("dead: query candidates: %w", err)
	}

	// Interface implementation check: exclude methods whose parent type
	// implements an interface with callers on the matching method.
	ifaceAlive, err := queryInterfaceAliveMethods(ctx, db)
	if err != nil {
		return Result{}, fmt.Errorf("dead: interface alive methods: %w", err)
	}
	candidates = excludeInterfaceImplementors(candidates, ifaceAlive)

	testsTargets, err := queryTestsTargets(ctx, db)
	if err != nil {
		return Result{}, fmt.Errorf("dead: query tests targets: %w", err)
	}

	interfaceIDs, err := queryInterfaceIDs(ctx, db)
	if err != nil {
		return Result{}, fmt.Errorf("dead: query interface IDs: %w", err)
	}

	candidates = excludeEntryPoints(candidates, testsTargets, interfaceIDs)

	liveContainers, err := findLiveContainers(ctx, db, candidates)
	if err != nil {
		return Result{}, fmt.Errorf("dead: live containers: %w", err)
	}
	candidates = excludeIDs(candidates, liveContainers)

	implementorIDs, err := queryInterfaceImplementors(ctx, db)
	if err != nil {
		return Result{}, fmt.Errorf("dead: interface implementors: %w", err)
	}

	candidates = annotateConfidence(candidates, interfaceIDs, implementorIDs)

	if len(candidates) > opts.Limit {
		candidates = candidates[:opts.Limit]
	}

	return Result{
		Dead:         candidates,
		TotalSymbols: totalSymbols,
	}, nil
}

func countSymbols(ctx context.Context, db *sql.DB, opts Options) (int, error) {
	q := `SELECT COUNT(*) FROM sense_symbols s
		JOIN sense_files f ON s.file_id = f.id
		WHERE s.kind IN ('function', 'method', 'class', 'module', 'type', 'interface')`
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
			AND e.kind IN ('calls', 'composes', 'includes', 'inherits')`
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

	q := `SELECT s.id, s.name, s.qualified, s.kind, f.path, s.file_id, s.line_start, s.line_end, s.parent_id
		FROM sense_symbols s
		JOIN sense_files f ON s.file_id = f.id
		WHERE NOT EXISTS (` + edgeFilter + `)
		AND s.kind IN ('function', 'method', 'class', 'module', 'type', 'interface')`
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
		if err := rows.Scan(&sym.ID, &sym.Name, &sym.Qualified, &sym.Kind,
			&sym.File, &sym.FileID, &sym.LineStart, &sym.LineEnd, &parentID); err != nil {
			return nil, err
		}
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

func queryInterfaceIDs(ctx context.Context, db *sql.DB) (map[int64]struct{}, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM sense_symbols WHERE kind = 'interface'`)
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

func queryInterfaceImplementors(ctx context.Context, db *sql.DB) (map[int64]struct{}, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT e.source_id FROM sense_edges e
		JOIN sense_symbols s ON s.id = e.target_id AND s.kind = 'interface'
		WHERE e.kind = 'inherits' AND e.source_id IS NOT NULL`)
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

func excludeEntryPoints(candidates []Symbol, testsTargets, interfaceIDs map[int64]struct{}) []Symbol {
	var out []Symbol
	for _, s := range candidates {
		if isEntryPoint(s, testsTargets, interfaceIDs) {
			continue
		}
		out = append(out, s)
	}
	return out
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

	// Bulk-load all children of candidate containers in one query,
	// then partition in Go to find which containers have live children.
	childrenByParent := map[int64][]int64{}
	const chunk = 500
	for start := 0; start < len(containerIDs); start += chunk {
		end := start + chunk
		if end > len(containerIDs) {
			end = len(containerIDs)
		}
		batch := containerIDs[start:end]

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

func excludeIDs(candidates []Symbol, exclude map[int64]struct{}) []Symbol {
	if len(exclude) == 0 {
		return candidates
	}
	var out []Symbol
	for _, s := range candidates {
		if _, excluded := exclude[s.ID]; !excluded {
			out = append(out, s)
		}
	}
	return out
}

func isEntryPoint(s Symbol, testsTargets map[int64]struct{}, interfaceIDs map[int64]struct{}) bool {
	if isMainFunction(s) {
		return true
	}
	if isTestSymbol(s) {
		return true
	}
	if isInTestFile(s) {
		return true
	}
	if isConstructor(s) {
		return true
	}
	if isFrameworkHook(s) {
		return true
	}
	if isInterfaceMethod(s, interfaceIDs) {
		return true
	}
	if _, ok := testsTargets[s.ID]; ok {
		return true
	}
	return false
}

func isMainFunction(s Symbol) bool {
	return s.Name == "main" || s.Name == "Main"
}

func isTestSymbol(s Symbol) bool {
	if strings.HasPrefix(s.Name, "Test") {
		return true
	}
	if strings.HasPrefix(s.Name, "test_") {
		return true
	}
	if strings.HasPrefix(s.Name, "Benchmark") {
		return true
	}
	return s.Name == "it" || s.Name == "describe" || s.Name == "specify"
}

func isInTestFile(s Symbol) bool {
	return strings.Contains(s.File, "_test.") ||
		strings.Contains(s.File, "/test/") ||
		strings.Contains(s.File, "/tests/") ||
		strings.Contains(s.File, "/spec/") ||
		strings.Contains(s.File, "/__tests__/") ||
		strings.HasSuffix(s.File, ".spec.ts") ||
		strings.HasSuffix(s.File, ".spec.js") ||
		strings.HasSuffix(s.File, ".test.ts") ||
		strings.HasSuffix(s.File, ".test.js")
}

func isConstructor(s Symbol) bool {
	return s.Name == "initialize" || s.Name == "__init__" || s.Name == "constructor" ||
		s.Name == "init" || s.Name == "Init"
}

var frameworkHooks = map[string]struct{}{
	"setUp": {}, "tearDown": {}, "setUpClass": {}, "tearDownClass": {},
	"before_action": {}, "after_action": {}, "around_action": {},
	"before_create": {}, "after_create": {}, "before_save": {}, "after_save": {},
	"before_destroy": {}, "after_destroy": {}, "before_update": {}, "after_update": {},
	"before_validation": {}, "after_validation": {},
	"setup": {}, "teardown": {},
	"BeforeEach": {}, "AfterEach": {}, "BeforeAll": {}, "AfterAll": {},
	"componentDidMount": {}, "componentWillUnmount": {}, "componentDidUpdate": {},
	"ServeHTTP": {},
}

func isFrameworkHook(s Symbol) bool {
	_, ok := frameworkHooks[s.Name]
	return ok
}

func isInterfaceMethod(s Symbol, interfaceIDs map[int64]struct{}) bool {
	if s.ParentID == nil {
		return false
	}
	_, ok := interfaceIDs[*s.ParentID]
	return ok
}

type ifaceMethodKey struct {
	parentID   int64
	methodName string
}

// queryInterfaceAliveMethods finds method names on interfaces that have
// callers, then maps those to all types that implement those interfaces.
// Returns a set of (implementor_parent_id, method_name) pairs that
// should be excluded from dead code results.
func queryInterfaceAliveMethods(ctx context.Context, db *sql.DB) (map[ifaceMethodKey]struct{}, error) {
	// Find interface methods that have callers (alive via dynamic dispatch).
	// Then find all types that inherit those interfaces.
	q := `SELECT impl.source_id, im.name
		FROM sense_symbols im
		JOIN sense_edges ie ON ie.target_id = im.id AND ie.kind = 'calls'
		JOIN sense_symbols iface ON im.parent_id = iface.id AND iface.kind = 'interface'
		JOIN sense_edges impl ON impl.target_id = iface.id AND impl.kind = 'inherits' AND impl.source_id IS NOT NULL
		GROUP BY impl.source_id, im.name`

	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[ifaceMethodKey]struct{})
	for rows.Next() {
		var parentID int64
		var methodName string
		if err := rows.Scan(&parentID, &methodName); err != nil {
			return nil, err
		}
		out[ifaceMethodKey{parentID: parentID, methodName: methodName}] = struct{}{}
	}
	return out, rows.Err()
}

func excludeInterfaceImplementors(candidates []Symbol, alive map[ifaceMethodKey]struct{}) []Symbol {
	if len(alive) == 0 {
		return candidates
	}
	var out []Symbol
	for _, s := range candidates {
		if s.ParentID != nil {
			if _, ok := alive[ifaceMethodKey{parentID: *s.ParentID, methodName: s.Name}]; ok {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

const (
	ConfidenceDead     = "dead"
	ConfidencePossibly = "possibly_dead"
)

func annotateConfidence(candidates []Symbol, interfaceIDs, implementorIDs map[int64]struct{}) []Symbol {
	for i := range candidates {
		s := &candidates[i]
		if s.ParentID != nil {
			if _, ok := interfaceIDs[*s.ParentID]; ok {
				s.Confidence = ConfidencePossibly
				continue
			}
			if _, ok := implementorIDs[*s.ParentID]; ok {
				s.Confidence = ConfidencePossibly
				continue
			}
		}
		s.Confidence = ConfidenceDead
	}
	return candidates
}
