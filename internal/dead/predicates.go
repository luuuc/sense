package dead

import (
	"strings"

	"github.com/luuuc/sense/internal/sqlite"
)

// This file holds the pure symbol-classification predicates and the structural
// filters built on them: the is*/exclude* helpers that decide whether a symbol
// is an entry point, a constructor, a test, a controller action, or a
// value-object method. They take a Symbol (and already-loaded facts) and return
// a verdict — no database, no effects.

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

// isInTestFile reports whether a symbol lives in a test, spec, or fixture file
// — non-production code whose symbols are not removable even with no caller. The
// check splits into a test/spec directory marker, a fixture directory marker,
// and a test-file suffix; a symbol matching any one is excluded.
func isInTestFile(s Symbol) bool {
	return inTestDir(s.File) || inFixtureDir(s.File) || hasTestFileSuffix(s.File)
}

// inTestDir reports whether the path sits under a conventional test or spec
// directory (or is a Go-style `*_test.go` file).
func inTestDir(file string) bool {
	return strings.Contains(file, "_test.") ||
		strings.Contains(file, "/test/") ||
		strings.HasPrefix(file, "test/") ||
		strings.Contains(file, "/tests/") ||
		strings.HasPrefix(file, "tests/") ||
		strings.Contains(file, "/spec/") ||
		strings.Contains(file, "/__tests__/")
}

// inFixtureDir reports whether the path sits under a fixture directory the
// toolchain reads as data, not live code.
func inFixtureDir(file string) bool {
	// `testdata/` is a fixture directory the Go toolchain (go build/vet,
	// staticcheck) ignores by convention; the same convention is common across
	// ecosystems. Symbols there are test fixtures, not live code, so an
	// unreferenced one is expected, not removable — excluding it keeps Sense's
	// `dead` set aligned with the toolchain's universe and free of fixture noise.
	return strings.Contains(file, "/testdata/") ||
		strings.HasPrefix(file, "testdata/") ||
		// `__testfixtures__/` is the jscodeshift convention for transform
		// input/output samples (next-codemod uses it heavily): standalone code the
		// test runner reads as text, never imported. Its symbols are fixtures, not
		// removable code — the TS/JS analog of `testdata/`.
		strings.Contains(file, "/__testfixtures__/") ||
		strings.HasPrefix(file, "__testfixtures__/")
}

// hasTestFileSuffix reports whether the path is a TS/JS spec or test file by its
// conventional filename suffix.
func hasTestFileSuffix(file string) bool {
	return strings.HasSuffix(file, ".spec.ts") ||
		strings.HasSuffix(file, ".spec.js") ||
		strings.HasSuffix(file, ".test.ts") ||
		strings.HasSuffix(file, ".test.js")
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
