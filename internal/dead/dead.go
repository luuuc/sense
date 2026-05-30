package dead

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/sqlite"
)

const defaultLimit = 100

// syntheticPrefixPattern is a SQL LIKE pattern matching the synthetic
// ruby-core:* base symbols (ruby-core:Struct / ruby-core:Data). These are
// plumbing for value-object inheritance edges and must never surface as
// dead candidates or inflate the analysed-symbol denominator. The prefix
// contains no LIKE metacharacters, so no ESCAPE clause is needed.
const syntheticPrefixPattern = extract.PrefixRubyCore + "%"

// routePrefixPattern matches the synthetic route:* helper symbols the route
// DSL emits (route:orders_path → OrdersController#index). They are plumbing
// for the view → route-helper → controller chain; a rarely-referenced one
// (e.g. an `*_url` variant) has no incoming edge and would otherwise read as a
// dead Ruby constant, so it is excluded from dead-code analysis like ruby-core.
const routePrefixPattern = extract.PrefixRoute + "%"

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
	Language   string
	LineStart  int
	LineEnd    int
	ParentID   *int64
	Visibility string
	Confidence string
	// NameOccurrences estimates how common this symbol's bare name is
	// across the index — symbols sharing the name plus resolved edges
	// pointing at a symbol of that name. The verify-command builder uses
	// it to decide whether a text grep would flood the caller (a name like
	// "success?" appears everywhere) and a manual-inspect hint should
	// replace it. Estimated from the index, never by shelling out.
	NameOccurrences int
}

type Result struct {
	Dead         []Symbol
	TotalSymbols int
	// Frameworks are the detected project frameworks (e.g. "Rails"),
	// used to tailor the blind-spot caveat to the right ecosystem.
	Frameworks []string
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

	ifaceAlive, err := sqlite.InterfaceAliveMethods(ctx, db)
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

	frameworks := readFrameworks(ctx, db)
	hasMain, err := hasMainFunction(ctx, db, opts)
	if err != nil {
		return Result{}, err
	}
	isLibrary := !hasMain
	interfaceMethodNames, err := queryInterfaceMethodNames(ctx, db)
	if err != nil {
		return Result{}, fmt.Errorf("dead: query interface method names: %w", err)
	}

	liveContainers, err := findLiveContainers(ctx, db, candidates)
	if err != nil {
		return Result{}, fmt.Errorf("dead: live containers: %w", err)
	}
	candidates = excludeIDs(candidates, liveContainers)

	implementorIDs, err := queryInterfaceImplementors(ctx, db)
	if err != nil {
		return Result{}, fmt.Errorf("dead: interface implementors: %w", err)
	}

	controllerConcernIDs, err := queryControllerConcernModuleIDs(ctx, db)
	if err != nil {
		return Result{}, fmt.Errorf("dead: controller concern modules: %w", err)
	}
	includedModuleIDs, err := queryIncludedModuleIDs(ctx, db)
	if err != nil {
		return Result{}, fmt.Errorf("dead: included modules: %w", err)
	}
	valueObjectClassIDs, err := queryValueObjectClassIDs(ctx, db)
	if err != nil {
		return Result{}, fmt.Errorf("dead: value-object classes: %w", err)
	}

	candidates = excludeEntryPoints(candidates, entryPointFilters{
		testsTargets:         testsTargets,
		interfaceIDs:         interfaceIDs,
		frameworks:           frameworks,
		isLibrary:            isLibrary,
		interfaceMethodNames: interfaceMethodNames,
		implementorIDs:       implementorIDs,
		controllerConcernIDs: controllerConcernIDs,
	})

	candidates = annotateConfidence(candidates, confidenceInputs{
		interfaceIDs:        interfaceIDs,
		implementorIDs:      implementorIDs,
		includedModuleIDs:   includedModuleIDs,
		valueObjectClassIDs: valueObjectClassIDs,
		dynamicFramework:    usesDynamicAutoload(frameworks),
	})

	if len(candidates) > opts.Limit {
		candidates = candidates[:opts.Limit]
	}

	if err := populateNameOccurrences(ctx, db, candidates); err != nil {
		return Result{}, fmt.Errorf("dead: name occurrences: %w", err)
	}

	return Result{
		Dead:         candidates,
		TotalSymbols: totalSymbols,
		Frameworks:   frameworkNames(frameworks),
	}, nil
}

// frameworkNames returns the detected framework names in sorted order
// for stable output.
func frameworkNames(frameworks map[string]struct{}) []string {
	names := make([]string, 0, len(frameworks))
	for n := range frameworks {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

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

func queryInterfaceMethodNames(ctx context.Context, db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT s.name FROM sense_symbols s
		JOIN sense_symbols p ON s.parent_id = p.id AND p.kind = 'interface'
		WHERE s.kind = 'method'`)
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

// populateNameOccurrences fills Symbol.NameOccurrences for each candidate
// with an index-derived estimate of how common its bare name is: the
// number of symbols sharing the name plus the number of resolved edges
// pointing at a symbol of that name. This proxies textual frequency
// without shelling out — a name defined and called many times is one a
// text grep would flood the caller with. Two batched queries (GROUP BY
// name over the candidate set), so cost is independent of repo size.
func populateNameOccurrences(ctx context.Context, db *sql.DB, candidates []Symbol) error {
	if len(candidates) == 0 {
		return nil
	}
	names := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, s := range candidates {
		if _, ok := seen[s.Name]; ok {
			continue
		}
		seen[s.Name] = struct{}{}
		names = append(names, s.Name)
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

	for i := range candidates {
		candidates[i].NameOccurrences = counts[candidates[i].Name]
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

type entryPointFilters struct {
	testsTargets         map[int64]struct{}
	interfaceIDs         map[int64]struct{}
	frameworks           map[string]struct{}
	isLibrary            bool
	interfaceMethodNames map[string]struct{}
	implementorIDs       map[int64]struct{}
	// controllerConcernIDs are module IDs included into a *Controller —
	// their methods are routed actions. Populated only for Rails projects.
	controllerConcernIDs map[int64]struct{}
}

func excludeEntryPoints(candidates []Symbol, filters entryPointFilters) []Symbol {
	var out []Symbol
	for _, s := range candidates {
		if isEntryPoint(s, filters) {
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

func isEntryPoint(s Symbol, f entryPointFilters) bool {
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
	if isFrameworkHook(s, f.frameworks) {
		return true
	}
	if isStimulusController(s) {
		return true
	}
	if _, rails := f.frameworks["Rails"]; rails {
		if isRailsControllerClass(s) || isRailsControllerAction(s, f.controllerConcernIDs) {
			return true
		}
	}
	if isInterfaceMethod(s, f.interfaceIDs) {
		return true
	}
	if isLibraryPublicAPI(s, f.isLibrary) {
		return true
	}
	if mightBeTraitImplMethod(s, f.interfaceMethodNames, f.implementorIDs) {
		return true
	}
	if s.Kind != "constant" {
		if _, ok := f.testsTargets[s.ID]; ok {
			return true
		}
	}
	return false
}

func mightBeTraitImplMethod(s Symbol, interfaceMethodNames map[string]struct{}, implementorIDs map[int64]struct{}) bool {
	if s.Kind != "method" || s.ParentID == nil {
		return false
	}
	if _, ok := implementorIDs[*s.ParentID]; !ok {
		return false
	}
	_, ok := interfaceMethodNames[s.Name]
	return ok
}

func isLibraryPublicAPI(s Symbol, isLibrary bool) bool {
	if !isLibrary {
		return false
	}
	return s.Visibility == "public" && (s.Kind == "function" || s.Kind == "method" || s.Kind == "class" || s.Kind == "type")
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
	// Test lifecycle
	"setUp": {}, "tearDown": {}, "setUpClass": {}, "tearDownClass": {},
	"setup": {}, "teardown": {},
	"BeforeEach": {}, "AfterEach": {}, "BeforeAll": {}, "AfterAll": {},
	// Rails callbacks
	"before_action": {}, "after_action": {}, "around_action": {},
	"before_create": {}, "after_create": {}, "before_save": {}, "after_save": {},
	"before_destroy": {}, "after_destroy": {}, "before_update": {}, "after_update": {},
	"before_validation": {}, "after_validation": {},
	// React lifecycle
	"componentDidMount": {}, "componentWillUnmount": {}, "componentDidUpdate": {},
	// Go HTTP
	"ServeHTTP": {},
	// Android lifecycle (unique prefixes, safe globally)
	"onCreate": {}, "onResume": {}, "onDestroy": {}, "onBind": {}, "onStartCommand": {},
}

// jvmFrameworkHooks are entry points too generic for all languages
// but valid in Java/Kotlin/Scala. Includes functional interface
// method names and common SAM type names (SAM detection was cut
// because the graph lacks abstract method tagging).
var jvmFrameworkHooks = map[string]struct{}{
	"handle": {}, "create": {}, "configure": {}, "routes": {}, "addEndpoints": {}, "register": {},
	"accept": {}, "apply": {}, "run": {}, "get": {}, "test": {}, "compare": {},
	"Runnable": {}, "Callable": {}, "Supplier": {}, "Consumer": {}, "Function": {}, "Predicate": {},
	"EndpointGroup": {}, "ExceptionHandler": {}, "ThrowingConsumer": {}, "ThrowingRunnable": {},
	"RequestLogger": {},
}

var railsHooks = map[string]struct{}{
	"after_commit": {}, "included": {}, "class_methods": {},
	"before_commit": {}, "after_rollback": {},
}

func isJVMLanguage(lang string) bool {
	return lang == "java" || lang == "kotlin" || lang == "scala"
}

func isFrameworkHook(s Symbol, frameworks map[string]struct{}) bool {
	if s.Language == "python" && strings.HasPrefix(s.Name, "__") && strings.HasSuffix(s.Name, "__") {
		return true
	}
	if _, ok := frameworkHooks[s.Name]; ok {
		return true
	}
	if isJVMLanguage(s.Language) {
		if _, ok := jvmFrameworkHooks[s.Name]; ok {
			return true
		}
	}
	if _, ok := frameworks["Rails"]; ok {
		if _, ok := railsHooks[s.Name]; ok {
			return true
		}
	}
	return false
}

// isRailsControllerClass reports whether s is a Rails controller class.
// Controllers are instantiated and dispatched by the router, never by a
// Ruby caller, so a zero-edge controller is an entry point, not dead.
func isRailsControllerClass(s Symbol) bool {
	return s.Language == "ruby" && s.Kind == "class" && strings.HasSuffix(s.Name, "Controller")
}

// isRailsControllerAction reports whether s is a public instance method
// that the router can dispatch — defined directly on a *Controller, or
// on a concern mixed into one.
//
// The visibility guard is currently inert: the Ruby extractor does not
// populate Visibility, so every controller method passes it. That is the
// intended trade — wrongly excluding an unused private helper is benign,
// while flagging a routed action as dead would make a reader delete live
// code. The guard stays so the policy is explicit and tightens for free
// if visibility is ever indexed.
func isRailsControllerAction(s Symbol, controllerConcernIDs map[int64]struct{}) bool {
	if s.Language != "ruby" || s.Kind != "method" {
		return false
	}
	if s.Visibility == "private" || s.Visibility == "protected" {
		return false
	}
	if strings.HasSuffix(rubyMethodParentName(s.Qualified), "Controller") {
		return true
	}
	if s.ParentID != nil {
		_, ok := controllerConcernIDs[*s.ParentID]
		return ok
	}
	return false
}

// isStimulusController reports whether s belongs to a Stimulus controller.
// Stimulus dispatches lifecycle callbacks (connect/disconnect), action
// methods, and target getters through the runtime and HTML data-*
// attributes — none are static JS call edges. The *_controller.js
// convention is the signal; no framework flag is required because the
// filename is unambiguous.
func isStimulusController(s Symbol) bool {
	if s.Language != "javascript" && s.Language != "typescript" {
		return false
	}
	return strings.HasSuffix(s.File, "_controller.js") || strings.HasSuffix(s.File, "_controller.ts")
}

func readFrameworks(ctx context.Context, db *sql.DB) map[string]struct{} {
	out := map[string]struct{}{}
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM sense_meta WHERE key = 'frameworks'`).Scan(&raw)
	if err != nil {
		return out
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		log.Printf("dead: corrupt frameworks meta: %v", err)
		return out
	}
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

func isInterfaceMethod(s Symbol, interfaceIDs map[int64]struct{}) bool {
	if s.ParentID == nil {
		return false
	}
	_, ok := interfaceIDs[*s.ParentID]
	return ok
}

func excludeInterfaceImplementors(candidates []Symbol, alive map[sqlite.InterfaceMethodKey]struct{}) []Symbol {
	if len(alive) == 0 {
		return candidates
	}
	var out []Symbol
	for _, s := range candidates {
		if s.ParentID != nil {
			if _, ok := alive[sqlite.InterfaceMethodKey{ParentID: *s.ParentID, MethodName: s.Name}]; ok {
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

func isGoConstructor(s Symbol) bool {
	return s.Language == "go" && strings.HasPrefix(s.Name, "New") && s.Kind == "function"
}

type confidenceInputs struct {
	interfaceIDs        map[int64]struct{}
	implementorIDs      map[int64]struct{}
	includedModuleIDs   map[int64]struct{}
	valueObjectClassIDs map[int64]struct{}
	dynamicFramework    bool
}

func annotateConfidence(candidates []Symbol, in confidenceInputs) []Symbol {
	for i := range candidates {
		s := &candidates[i]
		if s.ParentID != nil {
			if _, ok := in.interfaceIDs[*s.ParentID]; ok {
				s.Confidence = ConfidencePossibly
				continue
			}
			if _, ok := in.implementorIDs[*s.ParentID]; ok {
				s.Confidence = ConfidencePossibly
				continue
			}
			// A method on a module included somewhere is reachable through
			// the including type, so a zero-caller verdict is uncertain.
			if _, ok := in.includedModuleIDs[*s.ParentID]; ok {
				s.Confidence = ConfidencePossibly
				continue
			}
		}
		// A value object's public instance methods are duck-typed API
		// (`result.success?` on a local of unknown type). This is a
		// pure-Ruby idiom, not Rails-specific, so it softens regardless
		// of framework — and it makes the value-object nature queryable
		// (the inherits→ruby-core:Struct edge) rather than guessed from a
		// `?` suffix.
		if isValueObjectMethod(*s, in.valueObjectClassIDs) {
			s.Confidence = ConfidencePossibly
			continue
		}
		if isGoConstructor(*s) {
			s.Confidence = ConfidencePossibly
			continue
		}
		if in.dynamicFramework && (isDynamicallyReferenceable(*s) || isDynamicServiceCall(*s) || isDynamicRubyPredicate(*s)) {
			s.Confidence = ConfidencePossibly
			continue
		}
		s.Confidence = ConfidenceDead
	}
	return candidates
}

// usesDynamicAutoload reports whether the project relies on a framework
// whose autoloading / const_get / constantize / STI conventions make a
// statically-unreferenced type genuinely uncertain rather than dead.
func usesDynamicAutoload(frameworks map[string]struct{}) bool {
	_, ok := frameworks["Rails"]
	return ok
}

// isDynamicallyReferenceable flags Ruby types and constants that are
// commonly reached through paths the indexer cannot see — autoloading,
// const_get / constantize, STI, and other metaprogrammed lookups. They
// are reported as possibly-dead rather than dead so a reader double-
// checks before deleting.
func isDynamicallyReferenceable(s Symbol) bool {
	if s.Language != "ruby" {
		return false
	}
	switch s.Kind {
	case "class", "module", "constant":
		return true
	}
	return false
}

// serviceClassSuffixes name the command-object conventions whose entry
// point is a polymorphic `call` — invoked through `Klass.new.call`, a
// `.()` shorthand, or a duck-typed handler the static indexer often can't
// tie back to the definition.
var serviceClassSuffixes = []string{
	"Service", "Command", "Query", "Interactor", "Operation", "Job", "Worker",
}

// isDynamicServiceCall flags the service-object `call` entry point —
// reached through `Klass.new.call`, a `.()` shorthand, or a duck-typed
// handler the static indexer can't tie back to the definition. Reported
// possibly-dead rather than dead.
func isDynamicServiceCall(s Symbol) bool {
	if s.Language != "ruby" || s.Kind != "method" {
		return false
	}
	parent := rubyMethodParentName(s.Qualified)
	return s.Name == "call" && hasAnySuffix(parent, serviceClassSuffixes)
}

// isDynamicRubyPredicate flags Ruby predicate methods (`foo?`) under a
// dynamic framework as uncertain. Validation against a real Rails app
// (maket) showed predicates are pervasively invoked on duck-typed
// receivers the indexer can't resolve — `@transaction.pending?` in a view,
// `record.cancelled?` on an association — so hard-flagging a zero-static-
// caller predicate as `dead` produced confident false negatives on live
// methods (pending? alone had 28 call sites). A false "dead" erodes trust
// far more than a conservative "possibly_dead", so this residual
// softening stays — gated on a dynamic framework, where the dispatch the
// indexer misses actually happens.
//
// The narrower, framework-independent win this pitch adds is
// isValueObjectMethod: value-object members soften by their structural
// inheritance edge, so a pure-Ruby gem (no framework) gets correct
// verdicts that this framework-gated rule would not reach.
func isDynamicRubyPredicate(s Symbol) bool {
	return s.Language == "ruby" && s.Kind == "method" && strings.HasSuffix(s.Name, "?")
}

// isValueObjectMethod reports whether s is a public instance method of a
// Struct.new / Data.define value object (its parent class is in
// valueObjectClassIDs). Instance methods are reached via duck-typed
// `x.method` on a local whose type the indexer cannot infer, so a
// zero-caller verdict is uncertain. Singleton methods (`Result.build`) are
// excluded — they are not the duck-typed instance surface.
//
// Visibility is not gated: the Ruby extractor does not record method
// visibility, so public and private cannot be distinguished here. That is
// safe — a private struct method can't be called with an explicit receiver
// anyway, so if one is genuinely dead, softening it to `possibly_dead` is
// merely conservative, and private methods on a value object are rare.
func isValueObjectMethod(s Symbol, valueObjectClassIDs map[int64]struct{}) bool {
	if s.Language != "ruby" || s.Kind != "method" || s.ParentID == nil {
		return false
	}
	if _, ok := valueObjectClassIDs[*s.ParentID]; !ok {
		return false
	}
	return rubyInstanceMethod(s.Qualified)
}

// rubyInstanceMethod reports whether a Ruby method's qualified name is an
// instance method (`Parent#name`) rather than a singleton (`Parent.name`).
func rubyInstanceMethod(qualified string) bool {
	sep := strings.LastIndexAny(qualified, "#.")
	return sep >= 0 && qualified[sep] == '#'
}

// rubyMethodParentName returns the unqualified parent class/module name from
// a Ruby method's qualified name: "Checkout::ProcessPaymentService#call" →
// "ProcessPaymentService", "A.b" → "A". Returns "" when there is no
// receiver separator (top-level def).
func rubyMethodParentName(qualified string) string {
	sep := strings.LastIndexAny(qualified, "#.")
	if sep < 0 {
		return ""
	}
	parent := qualified[:sep]
	if i := strings.LastIndex(parent, "::"); i >= 0 {
		parent = parent[i+len("::"):]
	}
	return parent
}

func hasAnySuffix(s string, suffixes []string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}
