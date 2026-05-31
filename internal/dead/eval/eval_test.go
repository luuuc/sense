package eval

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/dead"
)

// flatten returns the union of every fixture's labels — the full ground
// truth the corpus encodes.
func flatten(corpus []Fixture) []Sym {
	var out []Sym
	for _, f := range corpus {
		out = append(out, f.Want...)
	}
	return out
}

// truthMap returns a verdict map that exactly satisfies the ground truth,
// i.e. a perfect engine's output (Alive symbols omitted, as a real engine
// omits them).
func truthMap(want []Sym) map[string]Verdict {
	m := make(map[string]Verdict)
	for _, w := range want {
		if w.Want != Alive {
			m[w.Qualified] = w.Want
		}
	}
	return m
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestActionFor(t *testing.T) {
	cases := map[Verdict]Action{
		Dead:         Remove,
		PossiblyDead: KeepAndVerify,
		Alive:        Ignore,
		Verdict("?"): Ignore, // unknown falls through to the safe action
	}
	for v, want := range cases {
		if got := ActionFor(v); got != want {
			t.Errorf("ActionFor(%q) = %q, want %q", v, got, want)
		}
	}
}

func TestScorePerfectMatch(t *testing.T) {
	want := flatten(Corpus())
	got := truthMap(want)

	r := Score(got, want)
	if !approx(r.DeadPrecision, 1.0) {
		t.Errorf("DeadPrecision = %v, want 1.0", r.DeadPrecision)
	}
	if !approx(r.DeadRecall, 1.0) {
		t.Errorf("DeadRecall = %v, want 1.0", r.DeadRecall)
	}
	if !approx(r.ActionCorrectness, 1.0) {
		t.Errorf("ActionCorrectness = %v, want 1.0", r.ActionCorrectness)
	}
	if fd := r.FalseDeads(); len(fd) != 0 {
		t.Errorf("FalseDeads = %v, want none", fd)
	}
	if len(r.Mismatches()) != 0 {
		t.Errorf("Mismatches = %v, want none", r.Mismatches())
	}
}

// TestScoreDetectsPlantedFalseDead is the harness's self-test: a planted
// false `dead` (the engine calling a live symbol removable) MUST drop
// precision below 1.0 and surface in FalseDeads. If this test ever passes
// with a planted lie, the harness is rubber-stamping, not measuring.
func TestScoreDetectsPlantedFalseDead(t *testing.T) {
	want := flatten(Corpus())
	got := truthMap(want)

	// Plant the lie: mark a genuinely-alive symbol as dead.
	var victim string
	for _, w := range want {
		if w.Want == Alive {
			victim = w.Qualified
			break
		}
	}
	if victim == "" {
		t.Fatal("corpus has no Alive control to plant a false dead on")
	}
	got[victim] = Dead

	r := Score(got, want)
	if r.DeadPrecision >= 1.0 {
		t.Errorf("planted false dead did not drop precision: got %v", r.DeadPrecision)
	}
	fd := r.FalseDeads()
	found := false
	for _, q := range fd {
		if q == victim {
			found = true
		}
	}
	if !found {
		t.Errorf("FalseDeads = %v, expected to contain planted victim %q", fd, victim)
	}
}

// TestScorePlantedMislabelFlipsScore mirrors the coverage-gate requirement:
// a deliberately wrong label flips the score, proving the metric responds
// to the data and is not hard-coded.
func TestScorePlantedMislabelFlipsScore(t *testing.T) {
	want := []Sym{
		{"A#a", Dead, "truly dead"},
		{"B#b", PossiblyDead, "uncertain"},
	}
	// Engine output is perfect against truth.
	good := Score(map[string]Verdict{"A#a": Dead, "B#b": PossiblyDead}, want)
	if !approx(good.DeadPrecision, 1.0) {
		t.Fatalf("baseline precision = %v, want 1.0", good.DeadPrecision)
	}
	// Now the engine also calls the uncertain symbol dead: precision halves.
	bad := Score(map[string]Verdict{"A#a": Dead, "B#b": Dead}, want)
	if !approx(bad.DeadPrecision, 0.5) {
		t.Errorf("precision with one false dead of two = %v, want 0.5", bad.DeadPrecision)
	}
}

func TestScoreAbsentIsAlive(t *testing.T) {
	want := []Sym{{"X#gone", Alive, "engine omits it"}}
	r := Score(map[string]Verdict{}, want)
	if r.Outcomes[0].Got != Alive {
		t.Errorf("absent symbol Got = %q, want %q", r.Outcomes[0].Got, Alive)
	}
	if !r.Outcomes[0].VerdictMatch() || !r.Outcomes[0].ActionMatch() {
		t.Error("absent Alive symbol should match an Alive label")
	}
}

func TestScoreRecallAndAction(t *testing.T) {
	want := []Sym{
		{"A#a", Dead, ""},
		{"B#b", Dead, ""},
		{"C#c", PossiblyDead, ""},
	}
	// Engine finds one of two deads, and mislabels the possibly_dead as alive.
	got := map[string]Verdict{"A#a": Dead}
	r := Score(got, want)

	if !approx(r.DeadPrecision, 1.0) {
		t.Errorf("DeadPrecision = %v, want 1.0 (no false dead)", r.DeadPrecision)
	}
	if !approx(r.DeadRecall, 0.5) {
		t.Errorf("DeadRecall = %v, want 0.5 (found 1 of 2)", r.DeadRecall)
	}
	// 1 of 3 actions correct (A#a → remove); B#b missed, C#c wrong action.
	if !approx(r.ActionCorrectness, 1.0/3.0) {
		t.Errorf("ActionCorrectness = %v, want 1/3", r.ActionCorrectness)
	}
	if r.TruthDead != 2 || r.LabeledDead != 1 {
		t.Errorf("TruthDead=%d LabeledDead=%d, want 2 and 1", r.TruthDead, r.LabeledDead)
	}
}

func TestScoreEmptyAndVacuous(t *testing.T) {
	empty := Score(map[string]Verdict{}, nil)
	if !approx(empty.DeadPrecision, 1.0) || !approx(empty.DeadRecall, 1.0) || !approx(empty.ActionCorrectness, 1.0) {
		t.Errorf("empty corpus should score vacuously perfect, got %+v", empty)
	}

	// Only possibly_dead labels and outputs: zero labeled dead → precision
	// is vacuously 1.0 (the engine told no lies).
	want := []Sym{{"A#a", PossiblyDead, ""}}
	r := Score(map[string]Verdict{"A#a": PossiblyDead}, want)
	if !approx(r.DeadPrecision, 1.0) {
		t.Errorf("zero-labeled-dead precision = %v, want 1.0", r.DeadPrecision)
	}
	if r.LabeledDead != 0 {
		t.Errorf("LabeledDead = %d, want 0", r.LabeledDead)
	}
}

func TestVerdictsFrom(t *testing.T) {
	res := dead.Result{Findings: []dead.Finding{
		{Symbol: dead.Symbol{Qualified: "A#a"}, Verdict: dead.VerdictDead},
		{Symbol: dead.Symbol{Qualified: "B#b"}, Verdict: dead.VerdictPossiblyDead},
		{Symbol: dead.Symbol{Qualified: "C#c"}, Verdict: ""}, // empty defaults to possibly_dead (honest)
	}}
	v := VerdictsFrom(res)
	if v["A#a"] != Dead {
		t.Errorf("A#a = %q, want %q", v["A#a"], Dead)
	}
	if v["B#b"] != PossiblyDead {
		t.Errorf("B#b = %q, want %q", v["B#b"], PossiblyDead)
	}
	if v["C#c"] != PossiblyDead {
		t.Errorf("C#c (empty verdict) = %q, want %q", v["C#c"], PossiblyDead)
	}
	if _, ok := v["D#d"]; ok {
		t.Error("unreported symbol should be absent from the verdict map")
	}
}

func TestReportOrdering(t *testing.T) {
	want := []Sym{
		{"Z#z", Alive, ""},
		{"A#a", PossiblyDead, ""},
		{"M#m", Alive, ""},
	}
	// Two false deads, planted out of order.
	got := map[string]Verdict{"Z#z": Dead, "A#a": PossiblyDead, "M#m": Dead}
	r := Score(got, want)
	fd := r.FalseDeads()
	if len(fd) != 2 || fd[0] != "M#m" || fd[1] != "Z#z" {
		t.Errorf("FalseDeads = %v, want sorted [M#m Z#z]", fd)
	}
	mm := r.Mismatches()
	if len(mm) != 2 || mm[0].Qualified != "M#m" || mm[1].Qualified != "Z#z" {
		t.Errorf("Mismatches order = %v, want sorted by qualified", mm)
	}
}

func TestMaterialize(t *testing.T) {
	root := t.TempDir()
	err := Materialize(root, map[string]string{
		"a.rb":            "class A; end\n",
		"nested/dir/b.rb": "class B; end\n",
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	for _, rel := range []string{"a.rb", "nested/dir/b.rb"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
}

// TestRunCorpusBaseline runs the real corpus through scan + the live
// decision layer and records the baseline metric. It is intentionally soft
// on the absolute precision number (the precision == 100% gate is enforced
// post-rebuild in the validation card); here it proves the runner works
// end-to-end and prints the false-dead count against ground truth.
func TestRunCorpusBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("scans real fixtures; skipped in -short")
	}
	ctx := context.Background()
	corpus := Corpus()

	report, results, err := RunCorpus(ctx, t.TempDir(), corpus)
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}

	wantLabels := len(flatten(corpus))
	if len(report.Outcomes) != wantLabels {
		t.Errorf("scored %d outcomes, want %d labels", len(report.Outcomes), wantLabels)
	}
	if len(results) != len(corpus) {
		t.Errorf("got %d fixture results, want %d", len(results), len(corpus))
	}
	if report.DeadPrecision < 0 || report.DeadPrecision > 1 {
		t.Errorf("DeadPrecision out of range: %v", report.DeadPrecision)
	}

	t.Logf("baseline: dead-precision=%.3f recall=%.3f action-correctness=%.3f false-deads=%d %v",
		report.DeadPrecision, report.DeadRecall, report.ActionCorrectness,
		len(report.FalseDeads()), report.FalseDeads())
	for _, o := range report.Mismatches() {
		t.Logf("  mismatch %s: got=%s want=%s", o.Qualified, o.Got, o.Want)
	}
}
