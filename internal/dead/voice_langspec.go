package dead

// langspecVoice is the shared voice for the Standard-tier table-driven languages
// (Java, Kotlin, C#, Scala, C++, PHP, C) that all run through the langspec
// extractor. These languages have NO per-framework voice — Sense models no Spring
// DI, no JPA, no ASP.NET, no Laravel — so the voice's whole job is to replace the
// blanket core_no_language_voice fallback with specific, honest open-world reasons,
// and to let only the narrowest, validated subset earn `dead`.
//
// One voice instance is registered per language string the extractor emits, so
// Lang() scopes it and (per the arbiter) marks each language one Sense can prove
// closed-world for. Like every voice it can only raise a hand (push →
// possibly_dead); it never votes for `dead`.
//
// The crucial honesty move is ls_public_no_framework: with no framework
// inference, ANY public symbol may be reached by a Spring bean, a JPA entity
// method, a JUnit test, or an ASP.NET action that Sense cannot see, so every
// public (or visibility-unknown) symbol stays open-world. Only an explicitly
// file-local (private) callable is even a candidate, and then only for a language
// in langspecDeadEligible — the statically-typed, visibility-enforcing subset
// validated against a real-world repo (pitch 25-20). Everything else raises a
// hand: ls_dynamic for the reflection-pervasive dynamic languages (PHP),
// ls_unvalidated for a static language with no benchmark to validate its `dead`
// tier yet.
type langspecVoice struct{ lang string }

// langspecDeadEligible names the langspec languages whose private (file-local)
// callables may fall silent and so reach the arbiter's soundness gate — the only
// path to `dead`. Treated as read-only after package init. It is deliberately
// conservative: a language earns a place here
// only after a real-world repo confirms zero false `dead` (the 25-13 trap is a
// synthetic 1.00 that collapses to 0.22 on a real codebase). Java is validated
// against the javalin benchmark; the other six langspec languages have no indexed
// repo in the suite, so they ship reasons-only until one exists.
var langspecDeadEligible = map[string]bool{
	"java": true,
}

// langspecDynamic names the langspec languages whose privacy is not a sound
// closed-world signal because reflection is pervasive: PHP reaches a private
// method through call_user_func, the magic __call, or ReflectionMethod, none of
// which leaves a static caller edge. Their private callables raise ls_dynamic
// rather than falling silent, so PHP is fixed at reasons-only regardless of any
// future benchmark.
var langspecDynamic = map[string]bool{
	"php": true,
}

func (v langspecVoice) Lang() string { return v.lang }

// Inspect returns the most-specific (most-likely-live) reason a hidden caller
// could exist for s, or nil when s is an explicitly file-local callable in a
// dead-eligible language — the only shape that may fall through to the arbiter's
// soundness gate and earn `dead`. Checks are ordered most-live-first so the
// returned reason carries the most useful hint; the arbiter independently picks
// the lowest-priority reason across voices.
func (v langspecVoice) Inspect(s Symbol, f Facts) *Reason {
	// Annotated / attributed: a DI container, test runner, or router dispatches it
	// with no source caller (Java @Service/@Test, C# [Fact]/[HttpGet], Kotlin/Scala
	// annotations, PHP #[Route]). True even for a file-local symbol, so it wins over
	// the visibility gates below.
	if _, ok := f.LangspecAnnotatedNames[s.Name]; ok {
		return reasonPtr(ReasonLangspecAnnotated)
	}
	// Interface / abstract method: reached through any implementor, where the
	// static graph shows no direct caller. Name-based, mirroring the Go voice.
	if s.Kind == "method" {
		if _, ok := f.InterfaceMethodNames[s.Name]; ok {
			return reasonPtr(ReasonLangspecInterfaceMethod)
		}
	}
	// Public (or visibility-unknown): with no framework voice, a framework may
	// dispatch any public symbol invisibly. A library's public callable/type API is
	// the core voice's concern (core_exported_api), matching the TS/Python voices.
	if s.Visibility == "public" || s.Visibility == "" {
		if f.IsLibrary && isPublicAPISymbol(s) {
			return nil
		}
		return reasonPtr(ReasonLangspecPublicNoFramework)
	}
	// Non-public, explicit visibility. Only a callable may ever be `dead`; a
	// non-callable (class / type / constant / module) is reachable by reflection
	// (Class.forName, typeof, get_class, a metaclass/DI registry) with no direct
	// reference, so it always raises a hand.
	switch s.Kind {
	case "function", "method":
	default:
		return reasonPtr(ReasonLangspecReflectiveType)
	}
	// protected / internal / package: reachable from a subclass, the same package,
	// or the same assembly — not file-local, so never proven dead here.
	if s.Visibility != "private" {
		return reasonPtr(ReasonLangspecPublicNoFramework)
	}
	// Explicitly private (file-local) callable. The dead-tier decision is per
	// language. A reflection-pervasive dynamic language never trusts privacy; a
	// static language without a validated benchmark holds open-world until one
	// exists; only a validated language falls silent so the soundness gate decides.
	if langspecDynamic[v.lang] {
		return reasonPtr(ReasonLangspecDynamic)
	}
	if !langspecDeadEligible[v.lang] {
		return reasonPtr(ReasonLangspecUnvalidated)
	}
	return nil
}

func init() {
	registerReasons(map[string]reasonSpec{
		ReasonLangspecInterfaceMethod: {
			priority: 30,
			hint:     "interface/abstract method; an implementor is reached through the interface with no direct caller — confirm no implementor relies on it before removing",
			verify:   "This method's name matches a method declared on an interface/abstract type, so it may satisfy that contract and be invoked through the interface with no direct caller. Check the implementors and the interface declaration before removing.",
		},
		ReasonLangspecAnnotated: {
			priority: 30,
			hint:     "annotated/attributed symbol (@Service/@Test/[Fact]/[HttpGet]/#[Route]); a DI container, test runner, or router may dispatch it with no source caller — do not remove without checking the framework wiring",
			verify:   "This symbol carries an annotation/attribute. With no framework voice for this language, a DI container, test runner, ORM, or router may instantiate or invoke it by reflection with no source-level caller. Check what the annotation does and how the framework reaches it before removing.",
		},
		ReasonLangspecPublicNoFramework: {
			priority: 55,
			hint:     "public symbol with no caller in this repo; Sense models no framework for this language, so a Spring bean, JPA entity, JUnit test, or controller action may reach it — search callers and framework wiring before removing",
			verify:   "Public symbols in this language can be reached by a framework Sense does not model (Spring DI, JPA, JUnit, ASP.NET, Laravel) or by an external consumer, with no source caller. For each, grep the repo for its name and check annotations/config wiring before removing.",
		},
		ReasonLangspecReflectiveType: {
			priority: 45,
			hint:     "class/type/constant with no static reference; it may be loaded reflectively (Class.forName / typeof / get_class) or by a DI/serialization registry — confirm before removing",
			verify:   "Types and constants in these languages can be loaded by reflection (Class.forName, typeof, get_class) or registered by a DI/serialization framework with no direct reference. For each, grep the repo for its name as a string literal and bare reference before removing.",
		},
		ReasonLangspecDynamic: {
			priority: 40,
			hint:     "private symbol in a reflection-heavy language (PHP call_user_func / __call / ReflectionMethod); privacy is not a closed-world proof here — grep for the name before removing",
			verify:   "PHP reaches even private methods through call_user_func, the magic __call, and ReflectionMethod, none of which leaves a static caller edge. Grep the repo for the name as a string literal and a `->name(` / `::name(` call before removing.",
		},
		ReasonLangspecUnvalidated: {
			priority: 58,
			hint:     "private symbol with no caller; Sense has no validated `dead` tier for this language yet (no benchmark repo), so it stays open-world — confirm it is unused before removing",
			verify:   "This private symbol has no resolved caller and would be a strong removal candidate, but Sense has not validated a `dead` tier for this language against a real-world repo, so it holds it open-world. Grep the repo for the name and confirm no reflective/framework reach before removing.",
		},
	})
}
