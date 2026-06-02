package eval

import (
	"context"
	"testing"
)

// TestPythonCorpusPrecisionGate is the binding real-world-discipline gate for
// the Python voice (pitch 25-19): the hand-labeled Python corpus must score
// dead-precision == 1.0 with ZERO false deads when run through scan + the live
// arbiter. Exactly one symbol earns `dead` (the underscore-private orphan); the
// signal receiver, the dunder, the model class, and the `__all__` export all
// stay possibly_dead. A regression that lets any of them earn `dead` fails here.
func TestPythonCorpusPrecisionGate(t *testing.T) {
	if testing.Short() {
		t.Skip("scans real fixtures; skipped in -short")
	}
	ctx := context.Background()
	corpus := pythonCorpus()

	report, results, err := RunCorpus(ctx, t.TempDir(), corpus)
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}

	if !approx(report.DeadPrecision, 1.0) {
		t.Errorf("DeadPrecision = %.3f, want 1.0 — false deads: %v", report.DeadPrecision, report.FalseDeads())
	}
	if fd := report.FalseDeads(); len(fd) != 0 {
		t.Errorf("FalseDeads = %v, want none (a Python `dead` at <100%% precision does not ship)", fd)
	}
	for _, o := range report.Mismatches() {
		t.Errorf("mismatch %s: got=%s want=%s", o.Qualified, o.Got, o.Want)
	}
	if len(results) != len(corpus) {
		t.Errorf("got %d fixture results, want %d", len(results), len(corpus))
	}

	// The earned `dead` must actually be found — proving the corpus exercises the
	// tier, not that the engine simply never says `dead`.
	got := mergeFixtureVerdicts(results)
	if got["_orphaned_helper"] != Dead {
		t.Errorf("_orphaned_helper = %q, want dead (the one earned dead)", got["_orphaned_helper"])
	}
	// The invisible-reach symbols must each stay open-world.
	for q, want := range map[string]Verdict{
		"_on_user_saved":    PossiblyDead, // Django @receiver
		"Cache.__getitem__": PossiblyDead, // dunder
		"Article":           PossiblyDead, // model class
		"_internal_api":     PossiblyDead, // __all__ export
	} {
		if got[q] != want {
			t.Errorf("%s = %q, want %q", q, got[q], want)
		}
	}
}

// TestPythonSignalReceiverFalseDeadCaught is the harness self-test: it confirms
// the precision gate MEASURES rather than rubber-stamps. If the engine ever
// wrongly called the Django signal receiver `dead`, the score must fall below
// 1.0 and the receiver must surface in FalseDeads. (If this test passed with the
// planted lie, a real false `dead` on a live receiver would slip through.)
func TestPythonSignalReceiverFalseDeadCaught(t *testing.T) {
	want := flatten(pythonCorpus())

	got := map[string]Verdict{}
	for _, w := range want {
		got[w.Qualified] = w.Want // a perfect engine
	}
	// Plant the lie: the live signal receiver is wrongly reported removable.
	victim := "_on_user_saved"
	got[victim] = Dead

	r := Score(got, want)
	if r.DeadPrecision >= 1.0 {
		t.Errorf("planted false dead on %s did not drop precision: got %.3f", victim, r.DeadPrecision)
	}
	found := false
	for _, fd := range r.FalseDeads() {
		if fd == victim {
			found = true
		}
	}
	if !found {
		t.Errorf("FalseDeads = %v, expected to contain planted victim %q", r.FalseDeads(), victim)
	}
}

// mergeFixtureVerdicts unions the per-fixture verdict maps so a test can look up
// a symbol by qualified name across the whole corpus run.
func mergeFixtureVerdicts(results []FixtureResult) map[string]Verdict {
	out := map[string]Verdict{}
	for _, fr := range results {
		for q, v := range fr.Got {
			out[q] = v
		}
	}
	return out
}
