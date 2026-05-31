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
	// is likely live — sort near the bottom.
	ReasonReflection: {
		priority: 30,
		hint:     "name is a dynamic dispatch target (send/const_get/define_method); grep for it as a symbol/string before removing",
		verify:   "These names appear as dynamic-dispatch targets. For each, grep for it as a string/symbol literal (send/public_send/const_get/define_method arguments) before removing.",
	},
	// Name mentioned where the resolver could not bind it: very likely a real
	// (just unresolved) caller, so this sorts near the bottom — least likely
	// to be genuinely removable.
	ReasonNameMentioned: {
		priority: 15,
		hint:     "name appears in a call or symbol position Sense could not bind to this definition; grep for it before removing",
		verify:   "These names are mentioned somewhere Sense could not resolve to a caller (an inherited bare call, a `**splat`, a chain receiver, or a `validate :sym`/`delegate`-style symbol argument). For each, grep the repo for its bare name and as a `:symbol` before removing.",
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
