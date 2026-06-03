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
	// NameOccurrences estimates how common this symbol's bare name is
	// across the index — symbols sharing the name plus resolved edges
	// pointing at a symbol of that name. The verify-command builder uses
	// it to decide whether a text grep would flood the caller (a name like
	// "success?" appears everywhere) and a manual-inspect hint should
	// replace it. Estimated from the index, never by shelling out.
	NameOccurrences int
}

type Result struct {
	// Findings are the classified zero-reference symbols: each carries a
	// verdict (dead / possibly_dead) and, for possibly_dead, the open-world
	// reason. The arbiter produces these from the candidate set.
	Findings     []Finding
	TotalSymbols int
	// Frameworks are the detected project frameworks (e.g. "Rails").
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

	// Structural-correctness filters (kept from the old pipeline): these
	// remove symbols that are provably NOT dead by construction — an
	// interface method with a live implementor, a container whose children
	// are alive, a genuine entry point (main/test/constructor). The
	// judgement-call exclusions (library API, framework hooks, controller
	// actions) are gone; the arbiter's voices now express those as honest
	// open-world reasons instead of silently dropping the symbol.
	ifaceAlive, err := sqlite.InterfaceAliveMethods(ctx, db)
	if err != nil {
		return Result{}, fmt.Errorf("dead: interface alive methods: %w", err)
	}
	candidates = excludeInterfaceImplementors(candidates, ifaceAlive)

	testsTargets, err := queryTestsTargets(ctx, db)
	if err != nil {
		return Result{}, fmt.Errorf("dead: query tests targets: %w", err)
	}

	liveContainers, err := findLiveContainers(ctx, db, candidates)
	if err != nil {
		return Result{}, fmt.Errorf("dead: live containers: %w", err)
	}
	candidates = excludeIDs(candidates, liveContainers)

	candidates = excludeEntryPoints(candidates, entryPointFilters{testsTargets: testsTargets})

	// Collapse parent-child before classifying: a dead class with dead
	// methods reports as the class alone.
	candidates = Rollup(candidates)

	// Gather the index facts the voices read, then classify every candidate
	// through the open/closed-world arbiter.
	facts, err := buildFacts(ctx, db)
	if err != nil {
		return Result{}, err
	}
	findings := defaultArbiter().Decide(candidates, facts)

	if err := populateFindingNameOccurrences(ctx, db, findings); err != nil {
		return Result{}, fmt.Errorf("dead: name occurrences: %w", err)
	}

	return Result{
		Findings:     findings,
		TotalSymbols: totalSymbols,
		Frameworks:   frameworkNames(facts.Frameworks),
	}, nil
}

// defaultArbiter is the registered voice stack for this build: the generic core
// voice plus the Ruby, Rails, Go, Rust, TypeScript/JavaScript, Python, and
// langspec (Standard-tier) language voices. The TS and langspec voices are each
// registered once per language string the shared extractor emits so each is a
// language for which closed-world can be proven. Adding a language voice is a
// one-line change here once it exists.
func defaultArbiter() *Arbiter {
	return NewArbiter(coreVoice{}, rubyVoice{}, railsVoice{}, goVoice{}, rustVoice{},
		tsVoice{lang: "typescript"}, tsVoice{lang: "tsx"}, tsVoice{lang: "javascript"},
		pythonVoice{},
		// One langspec voice per Standard-tier language the table-driven extractor
		// emits. Each marks its language one Sense can prove closed-world for, but
		// only langspecDeadEligible languages let a symbol actually fall through to
		// `dead` (see voice_langspec.go); the rest ship reasons-only.
		langspecVoice{lang: "java"}, langspecVoice{lang: "kotlin"}, langspecVoice{lang: "csharp"},
		langspecVoice{lang: "scala"}, langspecVoice{lang: "cpp"}, langspecVoice{lang: "php"},
		langspecVoice{lang: "c"})
}

// buildFacts gathers the project-wide index facts the voices consume. It is
// computed once per analysis so voices stay database-free.
func buildFacts(ctx context.Context, db *sql.DB) (Facts, error) {
	frameworks := readFrameworks(ctx, db)

	hasMain, err := hasMainFunction(ctx, db, Options{})
	if err != nil {
		return Facts{}, err
	}

	valueObjectClassIDs, err := queryValueObjectClassIDs(ctx, db)
	if err != nil {
		return Facts{}, fmt.Errorf("dead: value-object classes: %w", err)
	}
	includedModuleIDs, err := queryIncludedModuleIDs(ctx, db)
	if err != nil {
		return Facts{}, fmt.Errorf("dead: included modules: %w", err)
	}
	controllerConcernIDs, err := queryControllerConcernModuleIDs(ctx, db)
	if err != nil {
		return Facts{}, fmt.Errorf("dead: controller concern modules: %w", err)
	}
	interfaceMethodNames, err := queryInterfaceMethodNames(ctx, db)
	if err != nil {
		return Facts{}, fmt.Errorf("dead: interface method names: %w", err)
	}

	return Facts{
		Frameworks: frameworks,
		// A library is a tree with no application entry point whose public
		// symbols may be consumed from outside. "No main" alone is not enough:
		// a framework application (Rails, etc.) also has no main, yet its public
		// methods are internal, not an exported API. A detected framework means
		// the tree is an application, so it is not a library.
		IsLibrary:      !hasMain && len(frameworks) == 0,
		DispatchNames:  readDispatchNames(ctx, db),
		MentionedNames: readMentionedNames(ctx, db),
		// HarvestedLangs comes from its own meta key, not the mention keyset, so a
		// language that harvested an empty set (present here, absent from
		// MentionedNames) is still allowed to earn `dead` against its empty set —
		// distinct from a language that never harvested (absent here → fail closed).
		HarvestedLangs:           readHarvestedLangs(ctx, db),
		CgoExportNames:           readCgoExportNames(ctx, db),
		RustExportNames:          readRustExportNames(ctx, db),
		RustTestSymbolNames:      readRustTestSymbolNames(ctx, db),
		RustTraitImplMethodNames: readRustTraitImplMethodNames(ctx, db),
		RustAllowDeadNames:       readRustAllowDeadNames(ctx, db),
		TSDecoratedNames:         readTSDecoratedNames(ctx, db),
		TSDefaultExportNames:     readTSDefaultExportNames(ctx, db),
		PythonDecoratedNames:     readPythonDecoratedNames(ctx, db),
		PythonRouteNames:         readPythonRouteNames(ctx, db),
		PythonDjangoNames:        readPythonDjangoNames(ctx, db),
		PythonAllExportNames:     readPythonAllExportNames(ctx, db),
		LangspecAnnotatedNames:   readLangspecAnnotatedNames(ctx, db),
		ValueObjectClassIDs:      valueObjectClassIDs,
		IncludedModuleIDs:        includedModuleIDs,
		ControllerConcernIDs:     controllerConcernIDs,
		InterfaceMethodNames:     interfaceMethodNames,
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

// entryPointFilters carries the structural-correctness inputs for
// excludeEntryPoints. Only testsTargets remains: the judgement-call
// exclusions (library API, interface methods, controller actions, framework
// hooks) are now expressed as open-world voice reasons by the arbiter, not
// silently dropped here.
type entryPointFilters struct {
	testsTargets map[int64]struct{}
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
//
//nolint:gocyclo // 27-09: retired by the dead-code split
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
	if s.Kind != "constant" {
		if _, ok := f.testsTargets[s.ID]; ok {
			return true
		}
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
		strings.HasPrefix(s.File, "test/") ||
		strings.Contains(s.File, "/tests/") ||
		strings.HasPrefix(s.File, "tests/") ||
		strings.Contains(s.File, "/spec/") ||
		strings.Contains(s.File, "/__tests__/") ||
		// `testdata/` is a fixture directory the Go toolchain (go build/vet,
		// staticcheck) ignores by convention; the same convention is common across
		// ecosystems. Symbols there are test fixtures, not live code, so an
		// unreferenced one is expected, not removable — excluding it keeps Sense's
		// `dead` set aligned with the toolchain's universe and free of fixture noise.
		strings.Contains(s.File, "/testdata/") ||
		strings.HasPrefix(s.File, "testdata/") ||
		// `__testfixtures__/` is the jscodeshift convention for transform
		// input/output samples (next-codemod uses it heavily): standalone code the
		// test runner reads as text, never imported. Its symbols are fixtures, not
		// removable code — the TS/JS analog of `testdata/`.
		strings.Contains(s.File, "/__testfixtures__/") ||
		strings.HasPrefix(s.File, "__testfixtures__/") ||
		strings.HasSuffix(s.File, ".spec.ts") ||
		strings.HasSuffix(s.File, ".spec.js") ||
		strings.HasSuffix(s.File, ".test.ts") ||
		strings.HasSuffix(s.File, ".test.js")
}

// isConstructor reports whether s is an instance constructor invoked implicitly
// by object creation (Ruby `initialize`, Python `__init__`, JS `constructor`),
// which the resolver rarely ties to an explicit call. Go's `func init()` is NOT
// here: it is a runtime-invoked package initializer, not a constructor, and the
// Go voice owns it (go_init) so it surfaces as possibly_dead with an accurate
// "runtime-invoked, never remove" hint rather than being silently excluded.
func isConstructor(s Symbol) bool {
	return s.Name == "initialize" || s.Name == "__init__" || s.Name == "constructor"
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

var railsHooks = map[string]struct{}{
	"after_commit": {}, "included": {}, "class_methods": {},
	"before_commit": {}, "after_rollback": {},
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

// readDispatchNames returns the per-language sets of reflective dispatch-target
// names persisted to sense_meta by the scan layer under per-language keys
// (dispatch_names:<lang>). A symbol whose name is in its own language's set
// could be invoked dynamically, so the core voice keeps it open-world. A
// missing or corrupt value yields an empty map — degrading to recall loss,
// never a crash or a false `dead`.
func readDispatchNames(ctx context.Context, db *sql.DB) map[string]map[string]struct{} {
	return readNameSetMetaByLang(ctx, db, "dispatch_names")
}

// readMentionedNames returns the per-language broad mention sets persisted to
// sense_meta by the scan layer under per-language keys (mentioned_names:<lang>,
// every identifier/symbol token except definition names). The arbiter's
// soundness gate earns `dead` only when a candidate's name is absent from a
// NON-EMPTY set FOR ITS OWN LANGUAGE — mentioned nowhere a hidden caller could
// be. A language with no key never harvested, which the gate treats as
// "cannot prove closed-world" and blocks `dead` (core_no_harvest, fail-closed).
// A pre-feature index carries only the legacy union key `mentioned_names` (no
// language suffix); it does not match the per-language prefix, so every language
// reads as un-harvested and the whole index degrades to possibly_dead — never a
// false `dead` off stale union data.
func readMentionedNames(ctx context.Context, db *sql.DB) map[string]map[string]struct{} {
	return readNameSetMetaByLang(ctx, db, "mentioned_names")
}

// readNameSetMetaByLang reads every per-language sense_meta key with the given
// prefix (e.g. mentioned_names:ruby, mentioned_names:go) into a language-keyed
// map of name sets. A GLOB match on `prefix:*` discovers the languages present;
// the legacy union key (the bare prefix, no `:lang`) does not match and is
// ignored, so a pre-feature index reads as no-languages-harvested (fail-closed).
// An absent or corrupt value for any one language yields an empty set for it,
// self-healing on the next scan.
// readHarvestedLangs returns the set of languages whose mention harvest ran,
// persisted to the harvested_langs sense_meta key by the scan layer. The
// soundness gate refuses `dead` for a symbol whose language is absent here. A
// pre-feature index has no such key (the harvest predates it), so every language
// reads as un-harvested and the index degrades to possibly_dead — the safe
// direction. An absent or corrupt value yields an empty set.
func readHarvestedLangs(ctx context.Context, db *sql.DB) map[string]struct{} {
	return readStringSetMeta(ctx, db, "harvested_langs")
}

// readCgoExportNames returns the set of Go function names marked with a cgo
// `//export` directive, persisted to the cgo_exports sense_meta key by the scan
// layer. The Go voice keeps a name in this set open-world (go_cgo): it is called
// from C, never by an indexed Go caller. A missing or corrupt key yields an empty
// set — degrading to a possible false `dead` for a cgo export only until the next
// full scan, never a crash.
func readCgoExportNames(ctx context.Context, db *sql.DB) map[string]struct{} {
	return readStringSetMeta(ctx, db, "cgo_exports")
}

// readRustExportNames returns the set of Rust function/static names whose
// reachability the edge graph cannot see (`#[no_mangle]` / `#[export_name]`
// functions, `#[no_mangle]` / `#[used]` statics), persisted to the rust_exports
// sense_meta key by the scan layer. The Rust voice keeps such a name open-world
// (rust_ffi / rust_used). A missing or corrupt key yields an empty set — degrading
// to a possible false `dead` only until the next full scan, never a crash.
func readRustExportNames(ctx context.Context, db *sql.DB) map[string]struct{} {
	return readStringSetMeta(ctx, db, "rust_exports")
}

// readRustTestSymbolNames returns the set of Rust test-only symbol names
// (`#[test]` / `#[bench]`, or nested under `#[cfg(test)]`), persisted to the
// rust_test_symbols sense_meta key by the scan layer. The Rust voice keeps them
// open-world (rust_test). A missing or corrupt key yields an empty set.
func readRustTestSymbolNames(ctx context.Context, db *sql.DB) map[string]struct{} {
	return readStringSetMeta(ctx, db, "rust_test_symbols")
}

// readRustTraitImplMethodNames returns the set of method names defined in
// `impl Trait for Type` blocks, persisted to the rust_trait_impl_methods
// sense_meta key by the scan layer. The Rust voice keeps such a name open-world
// (rust_trait_impl). A missing or corrupt key yields an empty set.
func readRustTraitImplMethodNames(ctx context.Context, db *sql.DB) map[string]struct{} {
	return readStringSetMeta(ctx, db, "rust_trait_impl_methods")
}

// readRustAllowDeadNames returns the set of Rust item names annotated
// `#[allow(dead_code)]` / `#[allow(unused)]`, persisted to the rust_allow_dead
// sense_meta key by the scan layer. The Rust voice keeps such a name open-world
// (rust_allow_dead). A missing or corrupt key yields an empty set.
func readRustAllowDeadNames(ctx context.Context, db *sql.DB) map[string]struct{} {
	return readStringSetMeta(ctx, db, "rust_allow_dead")
}

// readTSDecoratedNames returns the set of TS/JS class/method names carrying a
// decorator, persisted to the ts_decorated sense_meta key by the scan layer. The
// TS voice keeps such a name open-world (ts_decorator). A missing or corrupt key
// yields an empty set.
func readTSDecoratedNames(ctx context.Context, db *sql.DB) map[string]struct{} {
	return readStringSetMeta(ctx, db, "ts_decorated")
}

// readTSDefaultExportNames returns the set of TS/JS names bound by an `export
// default` form, persisted to the ts_default_exports sense_meta key by the scan
// layer. The TS voice raises ts_default_export for such a name. A missing or
// corrupt key yields an empty set.
func readTSDefaultExportNames(ctx context.Context, db *sql.DB) map[string]struct{} {
	return readStringSetMeta(ctx, db, "ts_default_exports")
}

// readPythonDecoratedNames returns the set of Python function/method/class names
// carrying any decorator, persisted to the py_decorated sense_meta key by the
// scan layer. The Python voice keeps such a name open-world (py_decorator). A
// missing or corrupt key yields an empty set.
func readPythonDecoratedNames(ctx context.Context, db *sql.DB) map[string]struct{} {
	return readStringSetMeta(ctx, db, "py_decorated")
}

// readPythonRouteNames returns the set of Python handler names carrying a route
// decorator (Flask/FastAPI), persisted to the py_routes sense_meta key. The
// Python voice raises py_route for such a name. A missing or corrupt key yields
// an empty set.
func readPythonRouteNames(ctx context.Context, db *sql.DB) map[string]struct{} {
	return readStringSetMeta(ctx, db, "py_routes")
}

// readPythonDjangoNames returns the set of Python names carrying a Django-dispatch
// decorator (`@receiver` / `@admin.register`), persisted to the py_django
// sense_meta key. The Python voice raises py_django for such a name. A missing or
// corrupt key yields an empty set.
func readPythonDjangoNames(ctx context.Context, db *sql.DB) map[string]struct{} {
	return readStringSetMeta(ctx, db, "py_django")
}

// readPythonAllExportNames returns the set of names Python modules declare public
// via `__all__`, persisted to the py_all_exports sense_meta key. The Python voice
// raises py_all_export for such a name (even when underscore-private). A missing
// or corrupt key yields an empty set.
func readPythonAllExportNames(ctx context.Context, db *sql.DB) map[string]struct{} {
	return readStringSetMeta(ctx, db, "py_all_exports")
}

// readLangspecAnnotatedNames returns the set of langspec (Java/Kotlin/C#/Scala/
// C++/PHP/C) class/method/function names carrying an annotation or attribute,
// persisted to the langspec_annotated sense_meta key. The langspec voice keeps
// such a name open-world (ls_annotated). A missing or corrupt key yields an empty
// set.
func readLangspecAnnotatedNames(ctx context.Context, db *sql.DB) map[string]struct{} {
	return readStringSetMeta(ctx, db, "langspec_annotated")
}

// readStringSetMeta reads a JSON string-array sense_meta value into a set,
// treating an absent or corrupt value as empty.
func readStringSetMeta(ctx context.Context, db *sql.DB, key string) map[string]struct{} {
	out := map[string]struct{}{}
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM sense_meta WHERE key = ?`, key).Scan(&raw)
	if err != nil || raw == "" {
		return out
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		log.Printf("dead: corrupt %s meta: %v", key, err)
		return out
	}
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

func readNameSetMetaByLang(ctx context.Context, db *sql.DB, prefix string) map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{}
	rows, err := db.QueryContext(ctx,
		`SELECT key, value FROM sense_meta WHERE key GLOB ?`, prefix+":*")
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			continue
		}
		lang := strings.TrimPrefix(key, prefix+":")
		if lang == "" {
			continue
		}
		set := map[string]struct{}{}
		var names []string
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &names); err != nil {
				log.Printf("dead: corrupt %s meta: %v", key, err)
			}
		}
		for _, n := range names {
			set[n] = struct{}{}
		}
		out[lang] = set
	}
	return out
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

// serviceClassSuffixes name the command-object conventions whose entry
// point is a polymorphic `call` — invoked through `Klass.new.call`, a
// `.()` shorthand, or a duck-typed handler the static indexer often can't
// tie back to the definition.
var serviceClassSuffixes = []string{
	"Service", "Command", "Query", "Interactor", "Operation", "Job", "Worker",
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
