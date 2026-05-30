// Package eval is the dead-code trust harness: a hand-labeled synthetic
// corpus plus a runner that scores the decision layer on the metric that
// matters — precision of the `dead` verdict. A false `dead` (the engine
// calls a live symbol provably removable) is the only unforgivable error,
// so it is measured directly and weighted as fatal.
//
// The harness is "step zero" (see .doc/pitches/25-13): it exists before the
// arbiter rebuild so the rebuild can be shown to be *more* trustworthy, not
// merely different. It scores on the observable per-symbol verdict, so it is
// unchanged by how that verdict is computed (subtract-cascade today, voices
// later).
package eval

import (
	"sort"

	"github.com/luuuc/sense/internal/dead"
)

// Verdict is the observable classification of one symbol. It is the wire
// fact, not the engine's open/closed-world vocabulary: a symbol is either
// reported removable (Dead), reported unreferenced-but-maybe-reachable
// (PossiblyDead), or not reported at all (Alive).
type Verdict string

const (
	// Dead means the engine claims the symbol is provably unreferenced and
	// safe to remove. This is the sacred verdict — a wrong Dead is fatal.
	Dead Verdict = "dead"
	// PossiblyDead means the engine found no references but a hidden caller
	// could exist; the agent should verify before removing.
	PossiblyDead Verdict = "possibly_dead"
	// Alive means the engine did not report the symbol — it is reachable.
	Alive Verdict = "alive"
)

// Action is the agent action a verdict licenses. The harness scores action
// correctness, not just label correctness, because the consumer is an agent
// that acts on the output: a verdict is only useful if it leads to the right
// move (.doc/pitches/25-13, rabbit hole "metric measuring the wrong thing").
type Action string

const (
	// Remove: delete the symbol now (licensed only by Dead).
	Remove Action = "remove"
	// KeepAndVerify: it is unreferenced but a hidden caller may exist;
	// run the verify recipe before touching it (licensed by PossiblyDead).
	KeepAndVerify Action = "keep_and_verify"
	// Ignore: the symbol is reachable; do nothing (licensed by Alive).
	Ignore Action = "ignore"
)

// ActionFor maps a verdict to the agent action it licenses. The mapping is
// total and 1:1 — every verdict authorizes exactly one action, which is the
// whole point of the honest contract.
func ActionFor(v Verdict) Action {
	switch v {
	case Dead:
		return Remove
	case PossiblyDead:
		return KeepAndVerify
	default:
		return Ignore
	}
}

// Sym is one ground-truth label: a fully-qualified symbol and the verdict a
// trustworthy engine must produce for it. WantVerdict is the hand-assigned
// truth, justified by Why so the corpus reads as documentation of the hard
// cases, not an opaque answer key.
type Sym struct {
	Qualified string
	Want      Verdict
	Why       string
}

// Outcome is the per-symbol comparison of engine output against truth.
type Outcome struct {
	Qualified  string
	Got        Verdict
	Want       Verdict
	GotAction  Action
	WantAction Action
}

// VerdictMatch reports whether the engine's verdict equals the truth.
func (o Outcome) VerdictMatch() bool { return o.Got == o.Want }

// ActionMatch reports whether the engine's verdict licensed the correct
// agent action.
func (o Outcome) ActionMatch() bool { return o.GotAction == o.WantAction }

// FalseDead reports the fatal error: the engine said Dead on a symbol that
// truth says is not removable.
func (o Outcome) FalseDead() bool { return o.Got == Dead && o.Want != Dead }

// Report is the scored result over a label set.
type Report struct {
	Outcomes []Outcome
	// DeadPrecision is (true Dead) / (engine-labeled Dead). 1.0 means no
	// false Dead. NaN-free: a run with zero engine-Dead scores 1.0
	// (vacuously precise — it told no lies).
	DeadPrecision float64
	// DeadRecall is (true Dead found) / (truly Dead). Secondary to
	// precision: missing a dead symbol is a recall loss, not a lie.
	DeadRecall float64
	// ActionCorrectness is the fraction of symbols whose verdict licensed
	// the correct agent action.
	ActionCorrectness float64
	// LabeledDead is how many engine verdicts were Dead.
	LabeledDead int
	// TruthDead is how many symbols are truly Dead per ground truth.
	TruthDead int
}

// FalseDeads returns the qualified names the engine wrongly called Dead, in
// stable order. Empty is the only passing state for the precision gate.
func (r Report) FalseDeads() []string {
	var out []string
	for _, o := range r.Outcomes {
		if o.FalseDead() {
			out = append(out, o.Qualified)
		}
	}
	sort.Strings(out)
	return out
}

// Mismatches returns outcomes whose verdict differed from truth, in stable
// order, for diagnostics.
func (r Report) Mismatches() []Outcome {
	var out []Outcome
	for _, o := range r.Outcomes {
		if !o.VerdictMatch() {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Qualified < out[j].Qualified })
	return out
}

// Score compares engine verdicts (keyed by qualified name) against the
// hand-labeled truth and returns a Report. A symbol absent from got is
// treated as Alive — the engine did not report it. This is the pure core
// of the harness: it scans nothing, so it is fast, deterministic, and the
// natural place to prove the harness *measures* rather than rubber-stamps
// (feed it a wrong got and watch precision fall).
func Score(got map[string]Verdict, want []Sym) Report {
	r := Report{Outcomes: make([]Outcome, 0, len(want))}

	var deadHit, actionHit int
	for _, w := range want {
		g, ok := got[w.Qualified]
		if !ok {
			g = Alive
		}
		o := Outcome{
			Qualified:  w.Qualified,
			Got:        g,
			Want:       w.Want,
			GotAction:  ActionFor(g),
			WantAction: ActionFor(w.Want),
		}
		r.Outcomes = append(r.Outcomes, o)

		if g == Dead {
			r.LabeledDead++
			if w.Want == Dead {
				deadHit++
			}
		}
		if w.Want == Dead {
			r.TruthDead++
		}
		if o.ActionMatch() {
			actionHit++
		}
	}

	if r.LabeledDead == 0 {
		r.DeadPrecision = 1.0 // told no lies
	} else {
		r.DeadPrecision = float64(deadHit) / float64(r.LabeledDead)
	}
	if r.TruthDead == 0 {
		r.DeadRecall = 1.0 // nothing to find
	} else {
		r.DeadRecall = float64(deadHit) / float64(r.TruthDead)
	}
	if len(want) > 0 {
		r.ActionCorrectness = float64(actionHit) / float64(len(want))
	} else {
		r.ActionCorrectness = 1.0
	}
	return r
}

// VerdictsFrom collapses a dead.Result into the observable per-symbol
// verdict map the scorer consumes. Symbols the engine did not report are
// simply absent (Score reads absence as Alive). A symbol carrying an empty
// Confidence is treated as Dead, matching the wire builder's default.
func VerdictsFrom(res dead.Result) map[string]Verdict {
	out := make(map[string]Verdict, len(res.Findings))
	for _, f := range res.Findings {
		switch f.Verdict {
		case dead.VerdictPossiblyDead:
			out[f.Symbol.Qualified] = PossiblyDead
		case dead.VerdictDead:
			out[f.Symbol.Qualified] = Dead
		default:
			out[f.Symbol.Qualified] = PossiblyDead
		}
	}
	return out
}
