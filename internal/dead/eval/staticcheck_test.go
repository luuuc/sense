package eval

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseStaticcheckU1000(t *testing.T) {
	out := `internal/foo/bar.go:12:6: func unusedHelper is unused (U1000)
./internal/foo/bar.go:20:1: func (*T).deadMethod is unused (U1000)
types.go:5:6: type unusedType is unused (U1000)
some/file.go:1:1: this is not a U1000 line
const.go:3:7: const deadConst is unused (U1000)`
	set := ParseStaticcheckU1000(out)

	want := []goSymRef{
		{File: "internal/foo/bar.go", Name: "unusedHelper"},
		{File: "internal/foo/bar.go", Name: "deadMethod"}, // receiver stripped + leading ./ normalized
		{File: "types.go", Name: "unusedType"},
		{File: "const.go", Name: "deadConst"},
	}
	if len(set) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %v", len(set), len(want), set)
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			t.Errorf("missing %v in parsed set %v", w, set)
		}
	}
}

func TestBareGoName(t *testing.T) {
	cases := map[string]string{
		"unusedHelper":    "unusedHelper",
		"(*T).deadMethod": "deadMethod",
		"(T).method":      "method",
	}
	for in, want := range cases {
		if got := bareGoName(in); got != want {
			t.Errorf("bareGoName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGoFalseDeadsSubsetPasses: every Sense `dead` is in the oracle → no
// violation (the passing state).
func TestGoFalseDeadsSubsetPasses(t *testing.T) {
	oracle := U1000Set{
		{File: "a.go", Name: "x"}: {},
		{File: "a.go", Name: "y"}: {},
	}
	senseDead := []GoDead{
		{Qualified: "pkg.x", File: "a.go", Name: "x"},
	}
	if fd := GoFalseDeads(senseDead, oracle); len(fd) != 0 {
		t.Errorf("expected zero false dead, got %v", fd)
	}
}

// TestGoFalseDeadsPlantedLiveMethodFails is the coverage-gate requirement: a
// planted false `dead` (a live interface method Sense wrongly called dead, absent
// from staticcheck's U1000 set) MUST be reported, proving the gate detects the
// only unforgivable error rather than rubber-stamping.
func TestGoFalseDeadsPlantedLiveMethodFails(t *testing.T) {
	oracle := U1000Set{
		{File: "a.go", Name: "reallyDead"}: {},
	}
	senseDead := []GoDead{
		{Qualified: "pkg.reallyDead", File: "a.go", Name: "reallyDead"}, // legitimate
		{Qualified: "pkg.T.Handle", File: "b.go", Name: "Handle"},       // planted false dead
	}
	fd := GoFalseDeads(senseDead, oracle)
	if len(fd) != 1 {
		t.Fatalf("expected exactly one false dead (the planted live method), got %v", fd)
	}
	if fd[0].Qualified != "pkg.T.Handle" {
		t.Errorf("false dead = %q, want pkg.T.Handle", fd[0].Qualified)
	}
}

const sampleU1000 = "pkg/a.go:3:6: func deadHelper is unused (U1000)\n"

// TestRunStaticcheckU1000Success: a clean run (nil error) parses the findings.
func TestRunStaticcheckU1000Success(t *testing.T) {
	restore := runStaticcheck
	defer func() { runStaticcheck = restore }()
	runStaticcheck = func(context.Context, string) ([]byte, error) {
		return []byte(sampleU1000), nil
	}
	set, err := RunStaticcheckU1000(context.Background(), "ignored")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := set[goSymRef{File: "pkg/a.go", Name: "deadHelper"}]; !ok {
		t.Errorf("expected deadHelper in set, got %v", set)
	}
}

// TestRunStaticcheckU1000FindingsExit: staticcheck exits non-zero when it has
// findings (an *exec.ExitError); the output must still be parsed, not treated as
// a launch failure.
func TestRunStaticcheckU1000FindingsExit(t *testing.T) {
	restore := runStaticcheck
	defer func() { runStaticcheck = restore }()
	runStaticcheck = func(context.Context, string) ([]byte, error) {
		return []byte(sampleU1000), &exec.ExitError{}
	}
	set, err := RunStaticcheckU1000(context.Background(), "ignored")
	if err != nil {
		t.Fatalf("ExitError must not be fatal: %v", err)
	}
	if len(set) != 1 {
		t.Errorf("expected findings parsed despite non-zero exit, got %v", set)
	}
}

// TestRunStaticcheckU1000LaunchFailure: a non-ExitError (binary missing) maps to
// ErrStaticcheckUnavailable so callers can skip.
func TestRunStaticcheckU1000LaunchFailure(t *testing.T) {
	restore := runStaticcheck
	defer func() { runStaticcheck = restore }()
	runStaticcheck = func(context.Context, string) ([]byte, error) {
		return nil, errors.New("exec: \"staticcheck\": executable file not found in $PATH")
	}
	_, err := RunStaticcheckU1000(context.Background(), "ignored")
	if !errors.Is(err, ErrStaticcheckUnavailable) {
		t.Errorf("expected ErrStaticcheckUnavailable, got %v", err)
	}
}

// TestGoFalseDeadsSortOrder exercises both arms of the violation sort: across
// files and within one file.
func TestGoFalseDeadsSortOrder(t *testing.T) {
	oracle := U1000Set{}
	senseDead := []GoDead{
		{Qualified: "p.zeta", File: "b.go", Name: "zeta"},
		{Qualified: "p.alpha", File: "a.go", Name: "alpha"},
		{Qualified: "p.beta", File: "a.go", Name: "beta"},
	}
	fd := GoFalseDeads(senseDead, oracle)
	got := []string{fd[0].Name, fd[1].Name, fd[2].Name}
	want := []string{"alpha", "beta", "zeta"} // a.go:alpha, a.go:beta, b.go:zeta
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sort order = %v, want %v", got, want)
			break
		}
	}
}

// TestGoDeadSymbolsScanError covers the scan-failure path: a root that cannot be
// scanned surfaces an error rather than an empty set.
func TestGoDeadSymbolsScanError(t *testing.T) {
	_, err := GoDeadSymbols(context.Background(), filepath.Join(t.TempDir(), "does-not-exist"), filepath.Join(t.TempDir(), ".sense"))
	if err == nil {
		t.Error("expected an error scanning a missing root")
	}
}

// TestGoDeadSymbols scans a tiny Go module and confirms GoDeadSymbols returns the
// genuinely-dead unexported helper and nothing live. No staticcheck needed.
func TestGoDeadSymbols(t *testing.T) {
	root := t.TempDir()
	if err := Materialize(root, map[string]string{
		"go.mod": "module oracletest\n\ngo 1.21\n",
		"main.go": `package main

func main() { used() }

func used() {}

func deadHelper() {}
`,
	}); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	got, err := GoDeadSymbols(context.Background(), root, filepath.Join(root, ".sense"))
	if err != nil {
		t.Fatalf("GoDeadSymbols: %v", err)
	}
	names := map[string]bool{}
	for _, d := range got {
		names[d.Name] = true
	}
	if !names["deadHelper"] {
		t.Errorf("deadHelper should be earned dead; got %v", got)
	}
	if names["used"] || names["main"] {
		t.Errorf("live/entry symbols must not be dead; got %v", got)
	}
}

// TestStaticcheckOracleEndToEnd runs the full gate against a real staticcheck
// when it is installed, asserting Sense's `dead` set is a subset (zero false
// dead). It skips cleanly when staticcheck is unavailable so CI without the
// binary stays green; the binding repo-level run lives in the benchmark card.
func TestStaticcheckOracleEndToEnd(t *testing.T) {
	root := t.TempDir()
	if err := Materialize(root, map[string]string{
		"go.mod": "module oracletest\n\ngo 1.21\n",
		"lib.go": `package oracletest

type Runner interface{ Run() }

type Worker struct{}

// Run satisfies Runner; it is reached through the interface, so Sense must NOT
// call it dead even though no direct caller exists.
func (Worker) Run() {}

// deadHelper is genuinely unused — both Sense and staticcheck must agree.
func deadHelper() {}

func Use() Runner { return Worker{} }
`,
	}); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	ctx := context.Background()

	oracle, err := RunStaticcheckU1000(ctx, root)
	if errors.Is(err, ErrStaticcheckUnavailable) {
		t.Skip("staticcheck not installed; skipping the live oracle check")
	}
	if err != nil {
		t.Fatalf("RunStaticcheckU1000: %v", err)
	}

	senseDead, err := GoDeadSymbols(ctx, root, filepath.Join(root, ".sense"))
	if err != nil {
		t.Fatalf("GoDeadSymbols: %v", err)
	}

	falseDead := GoFalseDeads(senseDead, oracle)
	t.Logf("Sense dead=%d, staticcheck U1000=%d, false dead=%d", len(senseDead), len(oracle), len(falseDead))
	if len(falseDead) != 0 {
		t.Errorf("false-dead-count = %d, want 0; violations: %v", len(falseDead), falseDead)
	}
}
