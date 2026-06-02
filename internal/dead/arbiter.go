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
