package dead

import (
	"path/filepath"
	"strings"
)

// tsVoice is the TypeScript / JavaScript language voice. It reasons about the
// .ts / .tsx / .js family — one voice registered once per language string the
// shared extractor emits ("typescript", "tsx", "javascript"), carried in the
// `lang` field. Unlike Go (capitalization) and Rust (`pub`), TS/JS has no
// compiler dead-code lint to bind against; its closed-world signal is the module
// system: a symbol NOT exported from its module is reachable only within its own
// file. A module-private symbol with zero incoming edges, unmentioned and not
// reached by any framework idiom, is the narrow shape that may earn `dead`.
//
// The voice re-expresses TS/JS's invisible-reach idioms as open-world reasons;
// like every voice it can only raise a hand (push → possibly_dead), never vote
// for `dead`. Every EXPORTED symbol stays open-world: a barrel re-export
// (`export * from`), a dynamic `import()`, or an external consumer may reach it
// where the static graph shows zero callers — the analog of "staticcheck flags
// only unexported symbols".
//
// TS earns `dead`; JS stays conservative. TypeScript's module discipline and
// type annotations make the module-private closed-world bet sound. Plain
// JavaScript is looser (no types, CommonJS `module.exports` mutation, more
// dynamic patterns), so a would-be-`dead` JS symbol raises js_dynamic and stays
// possibly_dead — the honest ship pending the eval.
type tsVoice struct{ lang string }

func (v tsVoice) Lang() string { return v.lang }

// Inspect returns the most-specific (most-likely-live) reason a hidden caller
// could exist for s, or nil when s is a module-private .ts/.tsx symbol with no
// invisible-reach idiom — the only shape that may fall through to `dead`.
func (v tsVoice) Inspect(s Symbol, f Facts) *Reason {
	// Specific framework-reach idioms first (matching the Go/Rust voices, which
	// check init/interface/cgo and ffi/test/trait before the generic public
	// gate). Each is a way a hidden caller exists that is more precise than "it
	// is exported", so it wins over the export/library deferral below.

	// Decorator-annotated: Angular's @Component/@Injectable, Nest's @Controller,
	// a class-method route decorator — the framework's DI container or router
	// instantiates or routes to it with no source caller. True even when the
	// symbol is module-private.
	if _, ok := f.TSDecoratedNames[s.Name]; ok {
		return reasonPtr(ReasonTSDecorator)
	}
	// Next.js file-system routing renders a page/layout/route export with no
	// import edge anywhere — recognized by path/name convention. A route entry is
	// always an export, so this is gated on public.
	if s.Visibility == "public" && isNextRouteSymbol(s) {
		return reasonPtr(ReasonTSFrameworkRoute)
	}
	// A JSX component is used as `<Component/>`; the usage edge may live in a file
	// the resolver could not bind to this definition (a re-export, a dynamic
	// import, a prop-passed component).
	if s.Visibility == "public" && isJSXComponent(s) {
		return reasonPtr(ReasonTSJSX)
	}

	// Generic export gate: an exported symbol never earns `dead` — a re-export, a
	// dynamic import, or an external consumer may reach it.
	if s.Visibility == "public" {
		// A library's public callable/type API is the core voice's concern
		// (core_exported_api), matching the Go/Rust voices.
		if f.IsLibrary && isPublicAPISymbol(s) {
			return nil
		}
		// A default export is imported by path, not by name.
		if _, ok := f.TSDefaultExportNames[s.Name]; ok {
			return reasonPtr(ReasonTSDefaultExport)
		}
		return reasonPtr(ReasonTSExported)
	}

	// Module-private from here. Plain JavaScript is held open-world pending the
	// eval: its looser module/typing discipline makes the closed-world bet
	// unsound, so a would-be-`dead` JS symbol raises js_dynamic instead.
	if s.Language == "javascript" {
		return reasonPtr(ReasonJSDynamic)
	}

	// Module-private .ts/.tsx. The earned-`dead` candidate is narrow — the pitch
	// scopes it to a non-exported function / const / class. Only those three
	// kinds fall through to `dead`; everything else raises a hand, because a
	// module boundary does NOT prove a method or a type unreachable:
	//
	//   - A method may satisfy an interface, a structural type, or a framework
	//     protocol (React's componentDidMount/getDerivedStateFromError, Node's
	//     AsyncLocalStorage.enterWith) — invoked by name with no source caller,
	//     exactly the class of false `dead` the nextjs eval surfaced.
	//   - An interface / type alias is reached structurally and by
	//     declaration-merging (a `declare global { interface Window }` augmentation,
	//     a type used only in an annotation position) with no value-level reference
	//     edge the resolver can see.
	switch s.Kind {
	case "function", "class", "constant":
		return nil
	case "method":
		return reasonPtr(ReasonTSMethod)
	default: // interface, type, module
		return reasonPtr(ReasonTSType)
	}
}

// isJSXComponent reports whether s is a PascalCase function or class in a JSX
// file (.tsx/.jsx). Such a symbol is the shape used as a `<Component/>` element,
// whose usage edge may live in a file the resolver could not bind here. Checked
// only in the exported branch — a module-private component used as JSX in its own
// file already carries an incoming edge and is not a candidate.
func isJSXComponent(s Symbol) bool {
	switch s.Kind {
	case "function", "class":
	default:
		return false
	}
	if !isJSXFile(s.File) {
		return false
	}
	return s.Name != "" && s.Name[0] >= 'A' && s.Name[0] <= 'Z'
}

// isJSXFile reports whether path is a JSX-bearing file.
func isJSXFile(path string) bool {
	return strings.HasSuffix(path, ".tsx") || strings.HasSuffix(path, ".jsx")
}

// isNextRouteSymbol reports whether s lives in a Next.js route file — a
// framework-dispatched entry rendered by file-system routing with no import edge.
func isNextRouteSymbol(s Symbol) bool { return isNextRouteFile(s.File) }

// nextAppRouteFiles are the reserved file names the Next.js `app/` router renders
// by convention. Their default (and, for route.ts, named GET/POST/…) exports are
// framework-dispatched. Pinning the exact set keeps the convention from being too
// broad (which would mute real dead code in `app/`).
var nextAppRouteFiles = map[string]bool{
	"page": true, "layout": true, "route": true, "loading": true,
	"error": true, "not-found": true, "template": true, "default": true,
	"global-error": true,
}

// isNextRouteFile reports whether path is a Next.js route file: any file under a
// `pages/` directory (the pages router treats every file as a route), or a
// reserved-name file under an `app/` directory (the app router). The extension
// must be a TS/JS one. Convention-based by design — there is no import edge to
// follow, so path/name is the only signal.
func isNextRouteFile(path string) bool {
	p := filepath.ToSlash(path)
	ext := filepath.Ext(p)
	switch ext {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
	default:
		return false
	}
	if pathHasDir(p, "pages") {
		return true
	}
	if pathHasDir(p, "app") {
		base := strings.TrimSuffix(filepath.Base(p), ext)
		return nextAppRouteFiles[base]
	}
	return false
}

// pathHasDir reports whether a forward-slash path contains dir as a path segment.
func pathHasDir(path, dir string) bool {
	return strings.HasPrefix(path, dir+"/") || strings.Contains(path, "/"+dir+"/")
}

func init() {
	registerReasons(map[string]reasonSpec{
		ReasonJSDynamic: {
			priority: 60,
			hint:     "module-private JavaScript symbol with no caller; Sense holds JS open-world (CommonJS `module.exports`, no types, dynamic dispatch) — confirm it is unused, then it is likely removable",
			verify:   "This module-private JavaScript symbol has no resolved caller and would earn `dead` in TypeScript, but Sense holds plain JS open-world: CommonJS `module.exports` mutation and untyped dynamic access can reach it invisibly. Grep the repo for its bare name and as a string key before removing.",
		},
		ReasonTSExported: {
			priority: 50,
			hint:     "exported TS/JS symbol with no caller in this repo; a barrel re-export, a dynamic import(), or an external consumer may use it — search dependents before removing",
			verify:   "Exported TS/JS symbols can be reached by a barrel re-export (`export * from`), a dynamic `import()`, or a consumer outside this repo. For each, search dependents and grep the repo for its name before removing.",
		},
		ReasonTSDefaultExport: {
			priority: 48,
			hint:     "default export; imported by path rather than by name, so callers are invisible to a name-based graph — search for imports of this module before removing",
			verify:   "This is a default export, imported as `import X from './module'` under any local name. The import names the module path, not this symbol, so callers do not reference its name. Search for imports of this module's path before removing.",
		},
		ReasonTSJSX: {
			priority: 40,
			hint:     "JSX component (PascalCase, in a .tsx/.jsx file); it may be rendered as `<Component/>` in a file the resolver could not bind — grep for `<Name` before removing",
			verify:   "This is a JSX component. It may be rendered as `<Component/>` somewhere the resolver could not tie back to this definition (a re-export, a dynamic import, a prop-passed component). Grep the repo for `<Name` and for the name in import statements before removing.",
		},
		ReasonTSDecorator: {
			priority: 30,
			hint:     "decorator-annotated class/method (@Component/@Injectable/@Controller/route decorator); a framework's DI container or router invokes it with no source caller — do not remove without checking the framework wiring",
			verify:   "This symbol carries a decorator, so a framework (Angular/Nest DI, a route decorator) instantiates or routes to it with no source-level caller. Check the module/provider registration and route table before removing.",
		},
		ReasonTSFrameworkRoute: {
			priority: 25,
			hint:     "Next.js route file export (page/layout/route in app/, or any file in pages/); file-system routing renders it with no import edge — do not remove unless deleting the route",
			verify:   "This export lives in a Next.js route file and is rendered/invoked by the framework's file-system router with no import edge anywhere. It is a route entry point; remove it only if you are deleting the route itself.",
		},
		ReasonTSMethod: {
			priority: 30,
			hint:     "method with no resolved caller; it may satisfy an interface, a structural type, or a framework protocol (React lifecycle like componentDidMount/getDerivedStateFromError, AsyncLocalStorage) — confirm before removing",
			verify:   "TS methods are reached by name through interfaces, structural types, and framework protocols (React class-component lifecycle, web/Node API implementations) with no source-level caller. Check whether the enclosing class extends a framework base or implements an interface, and grep for the method name as a `.member` access before removing.",
		},
		ReasonTSType: {
			priority: 35,
			hint:     "interface/type with no resolved reference; it may be used structurally, in a type annotation, or via declaration-merging (e.g. `declare global { interface Window }`) — confirm before removing",
			verify:   "TS interfaces and type aliases are reached structurally and by declaration-merging, with no value-level reference edge. Grep for the name in type-annotation and `extends`/`implements` positions, and check for a `declare global` augmentation, before removing.",
		},
	})
}
