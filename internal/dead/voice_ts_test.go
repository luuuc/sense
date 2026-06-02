package dead

import "testing"

func tsSym(name, qualified, kind, visibility, lang, file string) Symbol {
	return Symbol{
		Name:       name,
		Qualified:  qualified,
		Kind:       kind,
		Language:   lang,
		Visibility: visibility,
		File:       file,
	}
}

func TestTSVoiceLang(t *testing.T) {
	for _, lang := range []string{"typescript", "tsx", "javascript"} {
		if got := (tsVoice{lang: lang}).Lang(); got != lang {
			t.Errorf("tsVoice{%q}.Lang() = %q, want %q", lang, got, lang)
		}
	}
}

func TestTSVoiceModulePrivateEarnsSilent(t *testing.T) {
	v := tsVoice{lang: "typescript"}
	// The narrow shapes that may fall through to `dead` (the pitch's
	// function / const / class): the voice stays silent (nil) for them.
	assertReason(t, v, tsSym("helper", "helper", "function", "private", "typescript", "mod.ts"), Facts{}, "")
	assertReason(t, v, tsSym("CONST", "CONST", "constant", "private", "typescript", "mod.ts"), Facts{}, "")
	assertReason(t, v, tsSym("Widget", "Widget", "class", "private", "typescript", "mod.ts"), Facts{}, "")
}

func TestTSVoiceMethodAndTypeNeverEarnDead(t *testing.T) {
	v := tsVoice{lang: "typescript"}
	// A module-private method may satisfy an interface or a framework protocol
	// (React lifecycle, AsyncLocalStorage) — it must never earn `dead`. This is
	// the false-`dead` class the nextjs eval surfaced (componentDidMount, etc.).
	assertReason(t, v, tsSym("getDerivedStateFromError", "C.getDerivedStateFromError", "method", "private", "typescript", "c.tsx"), Facts{}, ReasonTSMethod)
	// A module-private interface / type is reached structurally or by
	// declaration-merging (e.g. `declare global { interface Window }`).
	assertReason(t, v, tsSym("Window", "Window", "interface", "private", "typescript", "index.tsx"), Facts{}, ReasonTSType)
	assertReason(t, v, tsSym("Loader", "Loader", "type", "private", "typescript", "types.ts"), Facts{}, ReasonTSType)
}

func TestTSVoiceExported(t *testing.T) {
	v := tsVoice{lang: "typescript"}
	// An exported symbol never earns `dead` — a re-export / dynamic import /
	// external consumer may reach it. In a non-library (app) context.
	assertReason(t, v, tsSym("publicFn", "publicFn", "function", "public", "typescript", "mod.ts"),
		Facts{IsLibrary: false}, ReasonTSExported)
}

func TestTSVoiceExportedLibraryDefersToCore(t *testing.T) {
	v := tsVoice{lang: "typescript"}
	// In a library, a public callable/type defers to the core voice
	// (core_exported_api), matching the Go/Rust convention.
	assertReason(t, v, tsSym("publicFn", "publicFn", "function", "public", "typescript", "mod.ts"),
		Facts{IsLibrary: true}, "")
}

func TestTSVoiceDefaultExport(t *testing.T) {
	v := tsVoice{lang: "typescript"}
	f := Facts{TSDefaultExportNames: map[string]struct{}{"Page": {}}}
	// A default export is imported by path → the more specific ts_default_export.
	assertReason(t, v, tsSym("Page", "Page", "function", "public", "typescript", "lib.ts"), f, ReasonTSDefaultExport)
}

func TestTSVoiceJSXComponent(t *testing.T) {
	v := tsVoice{lang: "tsx"}
	// A PascalCase exported function/class in a .tsx file is a JSX component.
	assertReason(t, v, tsSym("Button", "Button", "function", "public", "tsx", "Button.tsx"), Facts{}, ReasonTSJSX)
	assertReason(t, v, tsSym("Card", "Card", "class", "public", "tsx", "Card.tsx"), Facts{}, ReasonTSJSX)
	// A lowercase exported function in a .tsx file is NOT a component → ts_exported.
	assertReason(t, v, tsSym("useThing", "useThing", "function", "public", "tsx", "hooks.tsx"), Facts{}, ReasonTSExported)
	// A PascalCase symbol in a non-JSX .ts file is not a JSX component.
	assertReason(t, v, tsSym("Button", "Button", "function", "public", "typescript", "Button.ts"), Facts{}, ReasonTSExported)
	// A PascalCase CONSTANT in a .tsx file is not a component (only func/class) → ts_exported.
	assertReason(t, v, tsSym("Theme", "Theme", "constant", "public", "tsx", "theme.tsx"), Facts{}, ReasonTSExported)
}

func TestTSVoiceDecorator(t *testing.T) {
	v := tsVoice{lang: "typescript"}
	f := Facts{TSDecoratedNames: map[string]struct{}{"AppComponent": {}, "list": {}}}
	// A decorated class is framework-dispatched → ts_decorator, even module-private.
	assertReason(t, v, tsSym("AppComponent", "AppComponent", "class", "private", "typescript", "app.ts"), f, ReasonTSDecorator)
	// A decorated method (in a private class) is also kept open-world.
	assertReason(t, v, tsSym("list", "Ctrl.list", "method", "private", "typescript", "ctrl.ts"), f, ReasonTSDecorator)
	// Decorator wins over the exported branch (more specific framework hint).
	assertReason(t, v, tsSym("AppComponent", "AppComponent", "class", "public", "typescript", "app.ts"), f, ReasonTSDecorator)
	// An undecorated module-private symbol is unaffected.
	assertReason(t, v, tsSym("plain", "plain", "function", "private", "typescript", "app.ts"), f, "")
}

func TestTSVoiceFrameworkRoute(t *testing.T) {
	v := tsVoice{lang: "tsx"}
	// A page/layout/route export in app/ is framework-dispatched.
	assertReason(t, v, tsSym("Page", "Page", "function", "public", "tsx", "app/users/page.tsx"), Facts{}, ReasonTSFrameworkRoute)
	assertReason(t, v, tsSym("Layout", "Layout", "function", "public", "tsx", "app/layout.tsx"), Facts{}, ReasonTSFrameworkRoute)
	// Any file in pages/ is a route.
	assertReason(t, v, tsSym("About", "About", "function", "public", "tsx", "pages/about.tsx"), Facts{}, ReasonTSFrameworkRoute)
	// A non-reserved name in app/ is NOT a route file → ts_exported (a non-JSX .ts
	// helper, isolated from the JSX-component refinement).
	assertReason(t, v, tsSym("getThing", "getThing", "function", "public", "tsx", "app/lib/helper.ts"), Facts{}, ReasonTSExported)
	// Route wins over the JSX refinement (route is the more specific reason).
	assertReason(t, v, tsSym("Page", "Page", "function", "public", "tsx", "app/page.tsx"), Facts{}, ReasonTSFrameworkRoute)
	// A MODULE-PRIVATE helper in a route file is NOT muted — it may earn `dead`.
	assertReason(t, v, tsSym("formatDate", "formatDate", "function", "private", "tsx", "app/page.tsx"), Facts{}, "")
}

func TestTSVoiceJavaScriptHeldConservative(t *testing.T) {
	v := tsVoice{lang: "javascript"}
	// A module-private JS symbol that would earn `dead` in TS is held open-world
	// (js_dynamic) — JS is looser (CommonJS, no types).
	assertReason(t, v, tsSym("helper", "helper", "function", "private", "javascript", "mod.js"), Facts{}, ReasonJSDynamic)
	// An exported JS symbol still reads as ts_exported (export reach is the same).
	assertReason(t, v, tsSym("publicFn", "publicFn", "function", "public", "javascript", "mod.js"), Facts{}, ReasonTSExported)
}

func TestTSVoiceReasonPriorityOrder(t *testing.T) {
	v := tsVoice{lang: "tsx"}
	// A decorated, exported, PascalCase .tsx symbol → decorator wins (checked first).
	f := Facts{TSDecoratedNames: map[string]struct{}{"Widget": {}}}
	assertReason(t, v, tsSym("Widget", "Widget", "class", "public", "tsx", "app/page.tsx"), f, ReasonTSDecorator)
}
