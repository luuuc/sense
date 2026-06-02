package dead

import "testing"

func pySym(name, qualified, kind, visibility string) Symbol {
	return Symbol{
		Name:       name,
		Qualified:  qualified,
		Kind:       kind,
		Language:   "python",
		Visibility: visibility,
		File:       "mod.py",
	}
}

func TestPythonVoiceLang(t *testing.T) {
	if got := (pythonVoice{}).Lang(); got != "python" {
		t.Errorf("pythonVoice.Lang() = %q, want python", got)
	}
}

func TestPythonVoiceUnderscorePrivateEarnsSilent(t *testing.T) {
	v := pythonVoice{}
	// The only shapes that may fall through to `dead`: an underscore-private
	// function or method with no invisible-reach idiom. The voice stays silent.
	assertReason(t, v, pySym("_helper", "_helper", "function", "private"), Facts{}, "")
	assertReason(t, v, pySym("_mangled", "C._mangled", "method", "private"), Facts{}, "")
}

func TestPythonVoicePublicNeverEarnsDead(t *testing.T) {
	v := pythonVoice{}
	// Every public function/method stays open-world (duck-typed dispatch),
	// mirroring ruby_public_method — the rule that makes the eligible set narrow.
	assertReason(t, v, pySym("process", "process", "function", "public"), Facts{}, ReasonPythonPublic)
	assertReason(t, v, pySym("render", "C.render", "method", "public"), Facts{}, ReasonPythonPublic)
	// Visibility-unknown (a pre-rescan index) is treated as public — the safe
	// direction (raise a hand) rather than risk a false `dead`.
	assertReason(t, v, pySym("legacy", "legacy", "function", ""), Facts{}, ReasonPythonPublic)
}

func TestPythonVoiceClassAndConstantNeverEarnDead(t *testing.T) {
	v := pythonVoice{}
	// No Python class or constant earns `dead` — reachable via importlib /
	// getattr / __subclasses__ / metaclass registries, even when underscore-named.
	assertReason(t, v, pySym("_Cache", "_Cache", "class", "private"), Facts{}, ReasonPythonClass)
	assertReason(t, v, pySym("Widget", "Widget", "class", "public"), Facts{}, ReasonPythonClass)
	assertReason(t, v, pySym("_SECRET", "_SECRET", "constant", "private"), Facts{}, ReasonPythonConstant)
	assertReason(t, v, pySym("TIMEOUT", "TIMEOUT", "constant", "public"), Facts{}, ReasonPythonConstant)
	// Any unexpected kind (a future module/type) holds open-world with the safe
	// generic reason rather than being mislabeled or earning `dead`.
	assertReason(t, v, pySym("Mod", "Mod", "module", "public"), Facts{}, ReasonPythonPublic)
}

func TestPythonVoiceDunder(t *testing.T) {
	v := pythonVoice{}
	// Dunder/protocol methods are interpreter-invoked regardless of visibility —
	// the double-underscore pattern catches the whole protocol surface.
	for _, name := range []string{"__init__", "__call__", "__enter__", "__getitem__", "__post_init__", "__getattr__"} {
		assertReason(t, v, pySym(name, "C."+name, "method", "private"), Facts{}, ReasonPythonDunder)
	}
	// A module-level dunder function (PEP 562) is also a dunder.
	assertReason(t, v, pySym("__getattr__", "__getattr__", "function", "public"), Facts{}, ReasonPythonDunder)
}

func TestPythonVoiceRoute(t *testing.T) {
	v := pythonVoice{}
	f := Facts{PythonRouteNames: nameSet("index"), PythonDecoratedNames: nameSet("index")}
	// A route handler is dispatched by the framework's router — the more specific
	// py_route wins over the generic py_decorator even though both sets match.
	assertReason(t, v, pySym("index", "index", "function", "public"), f, ReasonPythonRoute)
}

func TestPythonVoiceDjango(t *testing.T) {
	v := pythonVoice{}
	f := Facts{PythonDjangoNames: nameSet("on_save"), PythonDecoratedNames: nameSet("on_save")}
	// A Django signal receiver is invoked by Django with no source caller. The
	// crucial precision case: even underscore-private it stays open-world (this is
	// the planted false-`dead` the eval guards against).
	assertReason(t, v, pySym("on_save", "on_save", "function", "private"), f, ReasonPythonDjango)
}

func TestPythonVoiceDecorator(t *testing.T) {
	v := pythonVoice{}
	f := Facts{PythonDecoratedNames: nameSet("name")}
	// A generic decorator (@property/@pytest.fixture) changes the call story —
	// py_decorator, even for an underscore-private method.
	assertReason(t, v, pySym("name", "C.name", "method", "private"), f, ReasonPythonDecorator)
}

func TestPythonVoiceAllExport(t *testing.T) {
	v := pythonVoice{}
	f := Facts{PythonAllExportNames: nameSet("_reexported")}
	// `__all__` overrides the underscore convention: a `_reexported` private name
	// listed in __all__ is declared public API → py_all_export, not `dead`.
	assertReason(t, v, pySym("_reexported", "_reexported", "function", "private"), f, ReasonPythonAllExport)
}

func TestPythonVoiceReasonPriorityOrder(t *testing.T) {
	v := pythonVoice{}
	// When a symbol matches several facts, the most-specific (most-live) reason is
	// returned: dunder beats route beats django beats decorator beats all_export.
	f := Facts{
		PythonRouteNames:     nameSet("__call__"),
		PythonDjangoNames:    nameSet("__call__"),
		PythonDecoratedNames: nameSet("__call__"),
		PythonAllExportNames: nameSet("__call__"),
	}
	assertReason(t, v, pySym("__call__", "C.__call__", "method", "private"), f, ReasonPythonDunder)

	f = Facts{
		PythonDjangoNames:    nameSet("handler"),
		PythonDecoratedNames: nameSet("handler"),
		PythonRouteNames:     nameSet("handler"),
	}
	assertReason(t, v, pySym("handler", "handler", "function", "public"), f, ReasonPythonRoute)
}

func nameSet(names ...string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}
