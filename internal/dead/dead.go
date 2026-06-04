package dead

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

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
		HarvestedLangs: readHarvestedLangs(ctx, db),
		// Flat per-feature harvested-name sets: each read directly from its own
		// sense_meta key (named once, here, beside its field). readStringSetMeta
		// degrades an absent or corrupt key to an empty set, so a missing harvest
		// is recall loss, never a crash or a false `dead`.
		CgoExportNames:           readStringSetMeta(ctx, db, "cgo_exports"),
		RustExportNames:          readStringSetMeta(ctx, db, "rust_exports"),
		RustTestSymbolNames:      readStringSetMeta(ctx, db, "rust_test_symbols"),
		RustTraitImplMethodNames: readStringSetMeta(ctx, db, "rust_trait_impl_methods"),
		RustAllowDeadNames:       readStringSetMeta(ctx, db, "rust_allow_dead"),
		TSDecoratedNames:         readStringSetMeta(ctx, db, "ts_decorated"),
		TSDefaultExportNames:     readStringSetMeta(ctx, db, "ts_default_exports"),
		PythonDecoratedNames:     readStringSetMeta(ctx, db, "py_decorated"),
		PythonRouteNames:         readStringSetMeta(ctx, db, "py_routes"),
		PythonDjangoNames:        readStringSetMeta(ctx, db, "py_django"),
		PythonAllExportNames:     readStringSetMeta(ctx, db, "py_all_exports"),
		LangspecAnnotatedNames:   readStringSetMeta(ctx, db, "langspec_annotated"),
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
