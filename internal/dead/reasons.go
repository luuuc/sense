package dead

// Reason codes are a stable, voice-prefixed enum. The prefix names the voice
// that owns the knowledge (core_/ruby_/rails_), so the wire contract reads as
// a flat namespace the agent can switch on, while the codebase keeps each
// reason traceable to its source. Codes are append-only: renaming one is a
// breaking wire change.
const (
	// ReasonNoLanguageVoice is the open-world fallback for a symbol whose
	// language has no registered voice. Sense cannot reason about the stack,
	// so it refuses to claim `dead`. This is the inverse of the old
	// subtract-rulebook: an unsupported stack is honestly uncertain, never a
	// confident lie.
	ReasonNoLanguageVoice = "core_no_language_voice"
	// ReasonExportedAPI marks a public symbol of a library — reachable by
	// code outside the indexed tree.
	ReasonExportedAPI = "core_exported_api"
	// ReasonReflection marks a symbol whose name is a literal reflection /
	// metaprogramming dispatch target — reachable dynamically.
	ReasonReflection = "core_reflection"
	// ReasonNameMentioned is the soundness-gate reason: the symbol would
	// otherwise earn `dead`, but its bare name is mentioned somewhere the
	// resolver could not bind to an edge (an inherited bare call, a `**splat`,
	// a chain receiver, a `validate :sym` symbol arg) — or the mention harvest
	// was unavailable. Either way closed-world is not proven, so it stays
	// open-world. This is what makes `dead` sound against an incomplete resolver.
	ReasonNameMentioned = "core_name_mentioned"
	// ReasonNoHarvest is the per-language soundness-gate reason: a language
	// voice for the symbol's language is registered (so it cleared the
	// no-language-voice gate), but that language never harvested its own
	// mention set, so the gate has no project-wide names to prove the symbol
	// unmentioned. It fails closed — `dead` off another language's mentions is
	// exactly the cross-language lie the per-language gate exists to refuse.
	// Dormant for any language that harvests (Ruby always does); it is the
	// guard a future voice trips until it ships its own harvest.
	ReasonNoHarvest = "core_no_harvest"

	// Ruby-voice reason codes (voice_ruby.go owns their catalog specs).
	ReasonRubyValueObject  = "ruby_value_object"
	ReasonRubyServiceCall  = "ruby_service_call"
	ReasonRubyModuleMixin  = "ruby_module_mixin"
	ReasonRubyPublicMethod = "ruby_public_method"
	ReasonRubyClass        = "ruby_class"
	ReasonRubyModule       = "ruby_module"
	ReasonRubyConstant     = "ruby_constant"

	// Rails-voice reason codes (voice_rails.go owns their catalog specs).
	ReasonRailsRouting  = "rails_routing"
	ReasonRailsCallback = "rails_callback"
	ReasonRailsConcern  = "rails_concern"

	// Go-voice reason codes (voice_go.go owns their catalog specs).
	ReasonGoInit      = "go_init"
	ReasonGoInterface = "go_interface"
	ReasonGoCgo       = "go_cgo"
	ReasonGoGenerated = "go_generated"
	ReasonGoConst     = "go_const"
	ReasonGoExported  = "go_exported"

	// TS-voice reason codes (voice_ts.go owns their catalog specs).
	ReasonTSExported       = "ts_exported"
	ReasonTSJSX            = "ts_jsx"
	ReasonTSDecorator      = "ts_decorator"
	ReasonTSFrameworkRoute = "ts_framework_route"
	ReasonTSDefaultExport  = "ts_default_export"
	ReasonTSMethod         = "ts_method"
	ReasonTSType           = "ts_type"
	ReasonJSDynamic        = "js_dynamic"

	// Python-voice reason codes (voice_python.go owns their catalog specs).
	ReasonPythonDunder    = "py_dunder"
	ReasonPythonDecorator = "py_decorator"
	ReasonPythonRoute     = "py_route"
	ReasonPythonDjango    = "py_django"
	ReasonPythonAllExport = "py_all_export"
	ReasonPythonPublic    = "py_public"
	ReasonPythonClass     = "py_class"
	ReasonPythonConstant  = "py_constant"

	// Langspec-voice reason codes (voice_langspec.go owns their catalog specs).
	// Shared across the Standard-tier table-driven languages (Java, Kotlin, C#,
	// Scala, C++, PHP, C), which have no per-framework voice.
	ReasonLangspecInterfaceMethod   = "ls_interface_method"
	ReasonLangspecAnnotated         = "ls_annotated"
	ReasonLangspecPublicNoFramework = "ls_public_no_framework"
	ReasonLangspecReflectiveType    = "ls_reflective_type"
	ReasonLangspecDynamic           = "ls_dynamic"
	ReasonLangspecUnvalidated       = "ls_unvalidated"

	// Rust-voice reason codes (voice_rust.go owns their catalog specs).
	ReasonRustTraitImpl = "rust_trait_impl"
	ReasonRustDerive    = "rust_derive"
	ReasonRustFFI       = "rust_ffi"
	ReasonRustUsed      = "rust_used"
	ReasonRustTest      = "rust_test"
	ReasonRustPub       = "rust_pub"
	ReasonRustModule    = "rust_module"
	ReasonRustAllowDead = "rust_allow_dead"
)

// reasonSpec is the static metadata for a reason code: a removability
// priority, the default imperative hint, and a group-level verify recipe.
// Priority orders the possibly_dead groups in the output — HIGHER means more
// likely genuinely removable, so it ranks first; near-certain-live reasons
// sort last. The agent reads the most-actionable groups before the noise.
// Verify is the one copy-paste check that applies to the whole group; the
// agent fills in each symbol's name from the group's symbol list.
type reasonSpec struct {
	priority int
	hint     string
	verify   string
}

// reasonCatalog is the single source of truth for every reason's priority,
// default hint, and group verify recipe. Voices reference codes from here;
// the arbiter ranks by the priorities here; the wire builder groups by them.
// Language-voice codes (ruby_*/rails_*) are appended by their voices' files
// via registerReasons.
var reasonCatalog = map[string]reasonSpec{
	// Unknown stack: we genuinely cannot tell, which makes it more likely to
	// be removable than the framework reasons below — lead with it.
	ReasonNoLanguageVoice: {
		priority: 70,
		hint:     "no language voice for this stack; Sense cannot prove it unreachable — confirm callers manually before removing",
		verify:   "Sense has no voice for this language, so any caller is invisible to it. For each symbol below, grep the repo for its name as a call and as a string/symbol literal before removing.",
	},
	// Public library API: could go either way; a downstream consumer may or
	// may not exist.
	ReasonExportedAPI: {
		priority: 50,
		hint:     "public API of a library — search dependent projects/usages before removing",
		verify:   "Public API of a library — callers may live outside this repo. Search dependent projects and the rest of this tree for each name before removing.",
	},
	// Reflection target: the name is dispatched dynamically somewhere, so it
	// is likely live — sort near the bottom. The hint is language-neutral: each
	// language's dispatch set is harvested from its own reflection idioms (Ruby
	// send/const_get, Go reflect.MethodByName/struct tags, …), so the recipe names
	// the general shape rather than one language's keywords.
	ReasonReflection: {
		priority: 30,
		hint:     "name is a dynamic/reflective dispatch target; grep for it as a string literal before removing",
		verify:   "These names appear as reflective dispatch targets (e.g. a reflection call's string argument or a struct-tag key). For each, grep the repo for it as a string literal before removing.",
	},
	// Name mentioned where the resolver could not bind it: very likely a real
	// (just unresolved) caller, so this sorts near the bottom — least likely
	// to be genuinely removable.
	ReasonNameMentioned: {
		priority: 15,
		hint:     "name appears in a call or symbol position Sense could not bind to this definition; grep for it before removing",
		verify:   "These names are mentioned somewhere Sense could not resolve to a caller (an inherited bare call, a `**splat`, a chain receiver, or a `validate :sym`/`delegate`-style symbol argument). For each, grep the repo for its bare name and as a `:symbol` before removing.",
	},
	// Harvest unavailable for this language: Sense has a voice for the stack but
	// no project-wide mention set to reason against, so it cannot prove the
	// symbol unreachable. Sorts at the bottom with the other soundness-gate
	// reason — least likely to be safely removable.
	ReasonNoHarvest: {
		priority: 15,
		hint:     "Sense has not harvested this language's names, so it cannot prove the symbol unreachable; confirm callers manually before removing",
		verify:   "Sense registered a voice for this language but harvested no project-wide names for it, so the soundness gate cannot prove these symbols unmentioned. For each, grep the repo for its bare name and as a string/symbol literal before removing.",
	},
}

// registerReasons merges language-voice reason specs into the catalog. Voices
// call it from their files' init so the catalog stays the single source of
// truth while each voice owns its codes. A duplicate code panics — codes must
// be unique across voices.
func registerReasons(specs map[string]reasonSpec) {
	for code, spec := range specs {
		if _, dup := reasonCatalog[code]; dup {
			panic("dead: duplicate reason code " + code)
		}
		reasonCatalog[code] = spec
	}
}

// reasonPriority returns a code's removability priority, or 0 for an unknown
// code (sorts last, the safe default).
func reasonPriority(code string) int {
	if s, ok := reasonCatalog[code]; ok {
		return s.priority
	}
	return 0
}

// ReasonPriority is the exported view of reasonPriority for the wire builder,
// which ranks possibly_dead groups by removability. Higher sorts first.
func ReasonPriority(code string) int { return reasonPriority(code) }

// ReasonGroupVerify returns the group-level verify recipe for a reason code,
// or "" for an unknown code. The wire builder attaches it to each
// possibly_dead group.
func ReasonGroupVerify(code string) string {
	return reasonCatalog[code].verify
}

// newReason builds a Reason from a catalog code, attaching its default hint.
func newReason(code string) Reason {
	return Reason{Code: code, Hint: reasonCatalog[code].hint}
}
