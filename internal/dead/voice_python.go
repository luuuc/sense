package dead

import "strings"

// pythonVoice is the Python language voice. Python is the language most like
// Ruby — duck-typed, no enforced privacy, reached by getattr, decorators, dunder
// protocols, signals, and string-named framework registration — so its `dead`
// verdict is the hardest to make airtight and is shaped with the same discipline
// the Ruby retro earned (pitch 25-19). Like every voice it can only raise a hand
// (push → possibly_dead); it never votes for `dead`.
//
// The governing rule mirrors Ruby's: only a leading-underscore (convention- or
// mangling-private) function or method that this voice cannot tie to any
// dynamic-reach idiom may fall through to `dead`. A leading underscore is
// Python's only structural "not part of the public API" signal, and even it is
// trusted only in concert with the arbiter's broad mention gate (a `_helper`
// imported elsewhere leaves a textual mention and stays open-world). Every
// PUBLIC (non-underscore) function/method raises py_public — duck-typed dispatch
// on a receiver whose type the static indexer never resolved — exactly as Ruby
// keeps every public method open-world. Every class and constant raises a hand
// too (reachable via importlib / getattr / `__subclasses__` / metaclass
// registries), so no Python class or constant ever earns `dead`.
type pythonVoice struct{}

func (pythonVoice) Lang() string { return "python" }

// Inspect returns the most-specific (most-likely-live) reason a hidden caller
// could exist for s, or nil when s is an underscore-private function/method with
// no invisible-reach idiom — the only shape that may fall through to `dead`.
// Checks are ordered most-live-first so the returned reason carries the most
// useful hint; the arbiter independently picks the lowest-priority reason across
// voices (so a name in the reflection dispatch set still wins core_reflection).
func (pythonVoice) Inspect(s Symbol, f Facts) *Reason {
	// Dunder protocol methods (__init__, __call__, __enter__, __getitem__,
	// __post_init__, a module-level __getattr__) are invoked by the interpreter,
	// never by a visible caller. Matching the double-underscore PATTERN rather
	// than a fixed table is deliberate: the protocol surface is vast and grows
	// (PEP 562 module dunders, dataclass __post_init__), and a missed entry in a
	// table would let a live protocol method earn a false `dead`. The pattern
	// over-approximates toward caution, which is the safe direction.
	if isDunderName(s.Name) {
		return reasonPtr(ReasonPythonDunder)
	}
	// Framework reach, most-specific first. A route handler (Flask/FastAPI) and a
	// Django signal/admin target are dispatched by the framework with no source
	// caller; any other decorator (@property, @pytest.fixture, @click.command)
	// still changes the call story. All three rest on the scan-time decorator
	// harvest, so they hold even when the symbol is module-private.
	if _, ok := f.PythonRouteNames[s.Name]; ok {
		return reasonPtr(ReasonPythonRoute)
	}
	if _, ok := f.PythonDjangoNames[s.Name]; ok {
		return reasonPtr(ReasonPythonDjango)
	}
	if _, ok := f.PythonDecoratedNames[s.Name]; ok {
		return reasonPtr(ReasonPythonDecorator)
	}
	// Declared public API via `__all__` — re-exported by `from mod import *`,
	// the one signal that overrides the underscore convention. The broad mention
	// set misses it (names listed there are string literals, not identifiers), so
	// this dedicated harvest is what keeps a `_helper` listed in `__all__` open.
	if _, ok := f.PythonAllExportNames[s.Name]; ok {
		return reasonPtr(ReasonPythonAllExport)
	}

	// Kind gate. Only an underscore-private function/method may fall through to
	// `dead`; everything else raises a hand.
	switch s.Kind {
	case "function", "method":
		// Public (or visibility-unknown) callables are reached by duck-typed
		// dispatch on an unresolved receiver — the rule that keeps every public
		// Python function/method possibly_dead, mirroring ruby_public_method. A
		// library's public API is the core voice's concern (core_exported_api),
		// which out-prioritizes py_public when IsLibrary.
		if s.Visibility == "public" || s.Visibility == "" {
			return reasonPtr(ReasonPythonPublic)
		}
		// Underscore-private with no special reach: stay silent so the arbiter's
		// soundness gate (mention set + dispatch set) can earn `dead`.
		return nil
	case "class":
		// No Python class earns `dead`: reachable via importlib, getattr,
		// `__subclasses__`, or a metaclass registry with no direct caller.
		return reasonPtr(ReasonPythonClass)
	case "constant":
		// No module constant earns `dead`: reachable dynamically (getattr /
		// importlib) or re-exported indirectly.
		return reasonPtr(ReasonPythonConstant)
	default:
		// Any other kind (a future module/type): hold open-world with the safe
		// generic rather than mislabel it a constant.
		return reasonPtr(ReasonPythonPublic)
	}
}

// isDunderName reports whether name is a double-underscore "magic" name
// (`__init__`, `__call__`, module `__getattr__`) — leading AND trailing `__`
// around a non-empty core. Such names are invoked by the interpreter/runtime, so
// the voice keeps them open-world regardless of kind. The length guard (> 4)
// excludes the degenerate all-underscore forms (`____`).
func isDunderName(name string) bool {
	return len(name) > 4 && strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__")
}

func init() {
	registerReasons(map[string]reasonSpec{
		ReasonPythonDunder: {
			priority: 20,
			hint:     "Python dunder/protocol method (__init__, __call__, __enter__, __getitem__, a module __getattr__); the interpreter invokes it, never a caller — do not remove as dead",
			verify:   "Double-underscore names are invoked by the Python runtime through a protocol (construction, context-manager, iteration, operator overloading, dataclass __post_init__), with no source-level caller. Do not remove these as dead; if one is truly unused, the protocol it implements is simply unexercised.",
		},
		ReasonPythonRoute: {
			priority: 25,
			hint:     "route handler (Flask @app.route / FastAPI @app.get); the framework's router dispatches it with no source caller — do not remove unless deleting the route",
			verify:   "This handler carries a web-route decorator, so the framework's router invokes it by URL with no source-level caller. Remove it only if you are deleting the route itself; check the route table first.",
		},
		ReasonPythonDjango: {
			priority: 25,
			hint:     "Django-dispatched symbol (@receiver signal handler / @admin.register); Django's signal or admin machinery invokes it with no source caller — confirm the wiring before removing",
			verify:   "This symbol is wired into Django's signal or admin machinery by decorator, so it is invoked by the framework with no source-level caller. Check the signal connections / admin registry before removing.",
		},
		ReasonPythonDecorator: {
			priority: 30,
			hint:     "decorator-annotated (@property/@staticmethod/@pytest.fixture/@click.command/…); the decorator changes how it is reached (attribute access, injected fixture, CLI entry) — confirm before removing",
			verify:   "A decorator changes the call story: @property makes a method an attribute access, @pytest.fixture injects it by name, @click.command makes it a CLI entry. Check what the decorator does and how the framework reaches it before removing.",
		},
		ReasonPythonAllExport: {
			priority: 50,
			hint:     "name listed in the module's __all__; it is re-exported by `from module import *`, so an external consumer may use it — search importers before removing",
			verify:   "Names in `__all__` are the module's declared public API, re-exported by `from module import *`. Callers may live outside this scan. Search the repo and dependents for the name before removing.",
		},
		ReasonPythonPublic: {
			priority: 60,
			hint:     "public Python function/method with no static caller; may be reached by duck-typed dispatch on an unresolved receiver — grep for .name before removing",
			verify:   "Public Python functions/methods can be called on a receiver whose type Sense can't resolve (duck typing), or bound dynamically. For each, grep for `.name` and the bare name (including in templates/serializers) before removing.",
		},
		ReasonPythonClass: {
			priority: 55,
			hint:     "Python class with no static reference; may be reached via importlib, getattr, __subclasses__, or a metaclass/registry — confirm before removing",
			verify:   "Python classes can be loaded by importlib / getattr, discovered via `__subclasses__()`, or registered by a metaclass with no direct reference. For each, grep for the class name as a string and bare reference before removing.",
		},
		ReasonPythonConstant: {
			priority: 55,
			hint:     "Python module constant with no static reference; may be resolved dynamically (getattr / importlib) or re-exported — confirm before removing",
			verify:   "Python module constants can be read via getattr / importlib or re-exported indirectly. For each, grep for the name as a string and bare reference before removing.",
		},
	})
}
