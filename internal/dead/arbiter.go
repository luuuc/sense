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
	// IsLibrary is true when the project has no main entry point, so its
	// public symbols may be consumed by code outside the indexed tree.
	IsLibrary bool
	// DispatchNames is the set of literal names that appear as reflection /
	// metaprogramming dispatch targets (send/public_send/__send__/
	// define_method/respond_to?/method/const_get/constantize). A symbol
	// whose name is in this set could be invoked dynamically. Populated from
	// sense_meta at scan time (see the Ruby visibility card).
	DispatchNames map[string]struct{}
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
	return Finding{Symbol: s, Verdict: VerdictDead}
}
