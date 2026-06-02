package dead

// The arbiter is the dead-code decision engine. It inverts the old
// subtract-cascade's polarity: instead of "dead unless a rule rescues it"
// (where every gap in the rulebook ships as a confident lie on live code),
// the default is open-world — a hidden caller *could* exist — and a symbol
// earns `dead` only when no registered voice can name a way it might be
// reached AND a language voice that can reason about closed-world is present
// for its language. See .doc/pitches/25-13-dead-code-honest-verdicts.md.
//
// The invariant that makes proliferating voices safe: a voice can ONLY add
// an open-world reason (raise its hand → possibly_dead). It can never push
// toward dead. More knowledge ⇒ more caution, monotonically. A stack with
// no registered voice defaults to open-world (core_no_language_voice), so
// adding Sense to an unsupported framework can never produce a confident lie.

// Verdict is the per-symbol decision. It is the internal value; the wire
// layer maps it to the public contract. There is no third state — a symbol
// the arbiter examines is either earned-`dead` or honest-`possibly_dead`.
type Verdict string

const (
	// VerdictDead means no voice could name a hidden caller AND a language
	// voice for the symbol's language is registered: closed-world is proven,
	// removal is safe. Rare and earned.
	VerdictDead Verdict = "dead"
	// VerdictPossiblyDead means a voice raised its hand, or no language voice
	// exists for the stack: a hidden caller could exist, so verify before
	// removing. The honest default and the majority verdict.
	VerdictPossiblyDead Verdict = "possibly_dead"
)

// Reason explains why a symbol is open-world. Code is a stable,
// voice-prefixed enum (rails_routing, ruby_value_object, core_exported_api,
// …); Hint is an imperative, actionable sentence the agent can act on. The
// internal open/closed-world vocabulary never appears here — Reason is shaped
// for the wire.
type Reason struct {
	Code string
	Hint string
}

// Finding is the arbiter's verdict for one candidate symbol. Reason is nil
// exactly when Verdict is VerdictDead — a dead symbol has no open-world
// reason, every possibly_dead symbol carries one.
type Finding struct {
	Symbol  Symbol
	Verdict Verdict
	Reason  *Reason
}

// Voice answers one question for a symbol: "could a hidden caller exist?"
// It may ONLY raise its hand — return a Reason to push the symbol open-world.
// Returning nil means "I see no hidden-caller risk", never "this is dead".
// No voice can vote for `dead`; dead is the absence of every raised hand.
type Voice interface {
	// Lang reports the language this voice reasons about. The core voice
	// returns "" meaning it applies to every language. A non-empty Lang both
	// scopes the voice to matching symbols AND registers that language as
	// one Sense can prove closed-world for (so its symbols may earn `dead`).
	Lang() string
	// Inspect returns a reason a hidden caller could exist for s, or nil if
	// this voice sees no such risk. Facts carries the precomputed index
	// facts; a voice never queries the database itself.
	Inspect(s Symbol, f Facts) *Reason
}

// Facts is the precomputed index context every voice shares. It is gathered
// once per analysis (not per symbol) so voices stay cheap and database-free.
// Core-voice fields (IsLibrary, DispatchNames) and language-voice fields
// (the ID sets) coexist here; a voice reads only what it needs.
type Facts struct {
	// Frameworks is the set of detected frameworks (e.g. "Rails").
	Frameworks map[string]struct{}
	// IsLibrary is true when the tree has no application entry point — no main
	// function AND no detected framework — so its public symbols may be an API
	// surface consumed from outside. A framework application (Rails, etc.) has
	// no main yet is not a library: its public methods are internal, so a
	// detected framework makes IsLibrary false. See buildFacts.
	IsLibrary bool
	// DispatchNames maps a language to the set of literal names that appear as
	// reflection / metaprogramming dispatch targets in THAT language
	// (Ruby send/public_send/__send__/define_method/respond_to?/method/
	// const_get/constantize; other voices bring their own idioms). A symbol
	// whose name is in its own language's set could be invoked dynamically. The
	// set is keyed by language so one language's reflection literals never keep
	// another language's symbol open-world. Populated from sense_meta at scan
	// time (see the Ruby visibility card).
	DispatchNames map[string]map[string]struct{}
	// MentionedNames maps a language to the broad set of every bare name THAT
	// language's code mentions — every identifier/symbol token except definition
	// names. It drives the arbiter's soundness gate: a candidate earns `dead`
	// only when its name is absent from its own language's set (mentioned
	// nowhere a hidden caller could be) AND that set is non-empty. Keying by
	// language is the soundness primitive: a symbol may earn `dead` only against
	// mentions harvested from its OWN language, never off another's. Populated
	// from sense_meta.
	MentionedNames map[string]map[string]struct{}
	// CgoExportNames is the set of Go function names marked with a cgo `//export`
	// directive. Such a function is called from C with no Go caller edge, so the
	// Go voice keeps it open-world (go_cgo) rather than letting it earn `dead` off
	// its absent caller. Flat, not per-language: cgo is Go-only. Populated from the
	// cgo_exports sense_meta key; an absent key yields an empty set (no cgo known).
	CgoExportNames map[string]struct{}
	// RustExportNames is the set of Rust function/static names whose reachability
	// the edge graph cannot see: `#[no_mangle]` / `#[export_name]` functions
	// (called across the FFI boundary) and `#[no_mangle]` / `#[used]` statics
	// (kept alive by the linker). The Rust voice keeps such a name open-world
	// (rust_ffi for a function, rust_used for a static). Flat, not per-language —
	// these are Rust-only attributes. Populated from the rust_exports sense_meta
	// key; an absent key yields an empty set.
	RustExportNames map[string]struct{}
	// RustTestSymbolNames is the set of Rust test-only symbol names (`#[test]` /
	// `#[bench]`, or nested under a `#[cfg(test)]` module). The Rust voice keeps
	// them open-world (rust_test): the test harness invokes them and `cargo build`
	// does not compile them, so a zero-edge verdict would be a false `dead`.
	// Populated from the rust_test_symbols sense_meta key.
	RustTestSymbolNames map[string]struct{}
	// RustTraitImplMethodNames is the set of method names defined in `impl Trait
	// for Type` blocks. The Rust voice keeps such a method open-world
	// (rust_trait_impl): it satisfies a trait and is reached through a trait object
	// or generic bound, where the static graph shows no direct caller. This is the
	// sound, name-independent trait-impl signal — it covers external traits (serde's
	// Deserializer, std::io::Write, …) the voice's static table cannot enumerate.
	// Populated from the rust_trait_impl_methods sense_meta key.
	RustTraitImplMethodNames map[string]struct{}
	// RustAllowDeadNames is the set of Rust item names annotated
	// `#[allow(dead_code)]` / `#[allow(unused)]`. The Rust voice keeps such a name
	// open-world (rust_allow_dead): the author deliberately suppressed the lint, so
	// rustc never warns it and it is absent from the cargo oracle. Populated from
	// the rust_allow_dead sense_meta key.
	RustAllowDeadNames map[string]struct{}
	// TSDecoratedNames is the set of TS/JS class and method names carrying a
	// decorator (`@Component` / `@Injectable` / `@Controller` / route-method
	// decorators). The TS voice keeps such a name open-world (ts_decorator): a
	// framework's DI/router instantiates or routes to it with no source caller,
	// even when the symbol is module-private. Flat, not per-language — decorators
	// span the .ts/.tsx/.js family. Populated from the ts_decorated sense_meta key.
	TSDecoratedNames map[string]struct{}
	// TSDefaultExportNames is the set of TS/JS names bound by an `export default`
	// form. The TS voice raises the more specific ts_default_export (over the
	// generic ts_exported) for such a name: it is imported by path, not by name.
	// Flat, like TSDecoratedNames. Populated from the ts_default_exports sense_meta
	// key.
	TSDefaultExportNames map[string]struct{}
	// PythonDecoratedNames is the set of Python function/method/class names
	// carrying any decorator. The Python voice keeps such a name open-world
	// (py_decorator): a decorator changes the call story (an attribute access via
	// @property, an injected @pytest.fixture, a CLI @click.command) so the static
	// graph's zero-edge verdict cannot prove it unreachable. Flat, not
	// per-language — Python-only. Populated from the py_decorated sense_meta key.
	PythonDecoratedNames map[string]struct{}
	// PythonRouteNames is the subset of decorated names whose decorator is a web
	// route (Flask `@app.route`, FastAPI `@app.get`/`@router.post`). The Python
	// voice raises the more specific py_route. Populated from the py_routes
	// sense_meta key.
	PythonRouteNames map[string]struct{}
	// PythonDjangoNames is the subset of decorated names whose decorator is a
	// Django-dispatch idiom (`@receiver` signal handler, `@admin.register`). The
	// Python voice raises py_django. Populated from the py_django sense_meta key.
	PythonDjangoNames map[string]struct{}
	// PythonAllExportNames is the set of names Python modules declare public via
	// `__all__`. The Python voice raises py_all_export — the one signal that
	// overrides the underscore convention (a `_helper` listed in `__all__` is
	// re-exported by `from mod import *`), and one the identifier mention set
	// misses because `__all__` lists names as string literals. Populated from the
	// py_all_exports sense_meta key.
	PythonAllExportNames map[string]struct{}
	// LangspecAnnotatedNames is the set of langspec (Java/Kotlin/C#/Scala/C++/PHP/C)
	// class/method/function names carrying any annotation or attribute (Java
	// `@Service`/`@Test`, C# `[Fact]`/`[HttpGet]`, Kotlin/Scala annotations, PHP
	// `#[Route]`). The langspec voice keeps such a name open-world (ls_annotated):
	// with no per-framework voice for these languages, a DI container, test runner,
	// or router may dispatch any annotated symbol with no source caller. Flat, not
	// per-language — annotations span the shared table-driven extractor. Populated
	// from the langspec_annotated sense_meta key.
	LangspecAnnotatedNames map[string]struct{}
	// HarvestedLangs is the set of languages whose mention harvest actually ran
	// for this index. The soundness gate refuses `dead` for a symbol whose
	// language is absent here (reason core_no_harvest): a missing harvest cannot
	// prove a name unmentioned, so it fails closed rather than earn `dead` off
	// another language's mentions. This makes the gate's per-language scope
	// explicit instead of resting on a coincidentally-empty global set.
	HarvestedLangs map[string]struct{}
	// InterfaceIDs are symbol IDs of interfaces; a method whose parent is one
	// is part of a contract and reachable through any implementor.
	InterfaceIDs map[int64]struct{}
	// ImplementorIDs are symbol IDs of types that implement an interface.
	ImplementorIDs map[int64]struct{}
	// IncludedModuleIDs are symbol IDs of modules included somewhere; their
	// instance methods are reachable through the including type.
	IncludedModuleIDs map[int64]struct{}
	// ValueObjectClassIDs are symbol IDs of Struct.new / Data.define value
	// objects; their instance methods form a duck-typed surface.
	ValueObjectClassIDs map[int64]struct{}
	// ControllerConcernIDs are module IDs included into a *Controller; their
	// instance methods become routed actions.
	ControllerConcernIDs map[int64]struct{}
	// InterfaceMethodNames is the set of method names declared on any
	// interface, used to spot trait/interface implementation methods.
	InterfaceMethodNames map[string]struct{}
}

// Arbiter holds the registered voices and the derived set of languages a
// language voice exists for. Construct it once with NewArbiter and reuse it
// across candidates.
type Arbiter struct {
	voices     []Voice
	langVoices map[string]struct{}
}

// NewArbiter registers the given voices. The order matters only as a
// tie-break when two voices raise reasons of equal priority (the first
// registered wins). Languages named by a non-core voice's Lang() become the
// set for which `dead` can be earned.
func NewArbiter(voices ...Voice) *Arbiter {
	lv := make(map[string]struct{})
	for _, v := range voices {
		if l := v.Lang(); l != "" {
			lv[l] = struct{}{}
		}
	}
	return &Arbiter{voices: voices, langVoices: lv}
}

// Decide classifies every candidate. Candidates are expected to have already
// passed the structural-correctness filters (zero-edge candidacy, entry
// points, interface-alive exclusion, live containers) — the arbiter applies
// only the judgement layer.
func (a *Arbiter) Decide(candidates []Symbol, f Facts) []Finding {
	out := make([]Finding, 0, len(candidates))
	for _, s := range candidates {
		out = append(out, a.decideOne(s, f))
	}
	return out
}

// decideOne applies the open/closed-world rule to a single symbol.
func (a *Arbiter) decideOne(s Symbol, f Facts) Finding {
	// Collect every raised hand; keep the most-likely-live one (lowest
	// removability priority). That reason is the strongest argument the
	// symbol is reachable, so its verify recipe is the check most likely to
	// surface a hidden caller — the most useful thing to hand the agent.
	var best *Reason
	bestPrio := 0
	for _, v := range a.voices {
		if l := v.Lang(); l != "" && l != s.Language {
			continue
		}
		r := v.Inspect(s, f)
		if r == nil {
			continue
		}
		p := reasonPriority(r.Code)
		if best == nil || p < bestPrio {
			rr := *r
			best = &rr
			bestPrio = p
		}
	}
	if best != nil {
		return Finding{Symbol: s, Verdict: VerdictPossiblyDead, Reason: best}
	}

	// No voice raised a hand. `dead` is earned ONLY if a language voice for
	// this symbol's language is registered — otherwise Sense cannot prove
	// closed-world and must stay honest.
	if _, ok := a.langVoices[s.Language]; !ok {
		r := newReason(ReasonNoLanguageVoice)
		return Finding{Symbol: s, Verdict: VerdictPossiblyDead, Reason: &r}
	}

	// Soundness gate. A language voice's closed-world proof rests on "zero
	// resolved incoming edges == no caller", which is only true if the resolver
	// bound every call — and on a dynamic language it does not (inherited bare
	// calls, `**splat`s, chain receivers, `validate :sym` symbol args all go
	// unbound). So `dead` is earned only when the candidate's bare name is
	// absent from the broad mention set: mentioned nowhere a hidden caller could
	// be. A live-but-unbindable call still left a textual mention, which keeps
	// the symbol open-world instead of falsely dead.
	//
	// The gate is PER-LANGUAGE: a symbol is proven against the mentions
	// harvested from its OWN language, never off another's. The set is keyed by
	// s.Language and gated twice, both failing closed toward caution:
	//
	//   1. HarvestedLangs miss → the symbol's language never harvested mentions,
	//      so there is nothing to prove against. Refuse `dead` (core_no_harvest).
	//      This is the invariant that makes proliferating voices safe: a future
	//      voice that registers without shipping its own harvest earns
	//      core_no_harvest, not `dead` off another language's mentions.
	//   2. Empty set or name present → the gate cannot prove the name unmentioned
	//      (an unavailable harvest, or a real unbound mention). Refuse `dead`
	//      (core_name_mentioned). The gate assumes the harvest ran over the same
	//      tree the candidates came from; a partial-tree harvest is not trusted.
	set := f.MentionedNames[s.Language]
	if _, harvested := f.HarvestedLangs[s.Language]; !harvested {
		r := newReason(ReasonNoHarvest)
		return Finding{Symbol: s, Verdict: VerdictPossiblyDead, Reason: &r}
	}
	if _, mentioned := set[s.Name]; len(set) == 0 || mentioned {
		r := newReason(ReasonNameMentioned)
		return Finding{Symbol: s, Verdict: VerdictPossiblyDead, Reason: &r}
	}
	return Finding{Symbol: s, Verdict: VerdictDead}
}
