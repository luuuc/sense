package eval

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseCargoDeadCode(t *testing.T) {
	out := `{"reason":"compiler-artifact","package_id":"x"}
{"reason":"compiler-message","message":{"message":"function ` + "`dead_helper`" + ` is never used","code":{"code":"dead_code"},"level":"warning","spans":[{"file_name":"src/lib.rs","is_primary":true}]}}
{"reason":"compiler-message","message":{"message":"method ` + "`compute`" + ` is never used","code":{"code":"dead_code"},"level":"warning","spans":[{"file_name":"./src/money.rs","is_primary":true}]}}
{"reason":"compiler-message","message":{"message":"struct ` + "`Unused`" + ` is never constructed","code":{"code":"dead_code"},"level":"warning","spans":[{"file_name":"src/lib.rs","is_primary":true}]}}
{"reason":"compiler-message","message":{"message":"unused variable: ` + "`x`" + `","code":{"code":"unused_variables"},"level":"warning","spans":[{"file_name":"src/lib.rs","is_primary":true}]}}
not json at all`
	set := ParseCargoDeadCode(out)

	want := []rustSymRef{
		{File: "src/lib.rs", Name: "dead_helper"},
		{File: "src/money.rs", Name: "compute"}, // leading ./ normalized
		{File: "src/lib.rs", Name: "Unused"},
	}
	if len(set) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %v", len(set), len(want), set)
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			t.Errorf("missing %v in parsed set %v", w, set)
		}
	}
	// The unused_variables diagnostic must NOT be in the dead_code set.
	if _, ok := set[rustSymRef{File: "src/lib.rs", Name: "x"}]; ok {
		t.Error("unused_variables diagnostic must not be parsed as dead_code")
	}
}

func TestParseCargoDeadCodeGroupedNames(t *testing.T) {
	// A grouped dead_code message names several items; each shares the file.
	out := `{"reason":"compiler-message","message":{"message":"methods ` + "`a`" + ` and ` + "`b`" + ` are never used","code":{"code":"dead_code"},"level":"warning","spans":[{"file_name":"src/lib.rs","is_primary":true}]}}`
	set := ParseCargoDeadCode(out)
	for _, name := range []string{"a", "b"} {
		if _, ok := set[rustSymRef{File: "src/lib.rs", Name: name}]; !ok {
			t.Errorf("grouped name %q missing from %v", name, set)
		}
	}
}

func TestParseCargoDeadCodeEdgeCases(t *testing.T) {
	// A line that starts like JSON but is malformed (unmarshal error), a dead_code
	// diagnostic with no primary span (fallback to the first span), and one with no
	// spans at all (skipped). Only the no-primary one yields a parsed entry.
	out := `{ this is not valid json
{"reason":"compiler-message","message":{"message":"struct ` + "`Lonely`" + ` is never constructed","code":{"code":"dead_code"},"level":"warning","spans":[{"file_name":"src/only.rs","is_primary":false}]}}
{"reason":"compiler-message","message":{"message":"function ` + "`nospan`" + ` is never used","code":{"code":"dead_code"},"level":"warning","spans":[]}}
{"reason":"compiler-message","message":{"message":"a note with no code","level":"warning","spans":[{"file_name":"src/x.rs","is_primary":true}]}}`
	set := ParseCargoDeadCode(out)
	if _, ok := set[rustSymRef{File: "src/only.rs", Name: "Lonely"}]; !ok {
		t.Errorf("no-primary span should fall back to the first span; got %v", set)
	}
	if _, ok := set[rustSymRef{File: "", Name: "nospan"}]; ok {
		t.Error("a diagnostic with no spans must be skipped, not keyed on an empty file")
	}
	if len(set) != 1 {
		t.Errorf("expected exactly the one parseable entry, got %v", set)
	}
}

// TestRustFalseDeadsSubsetPasses: every Sense `dead` is in the oracle → no
// violation (the passing state).
func TestRustFalseDeadsSubsetPasses(t *testing.T) {
	oracle := DeadCodeSet{
		{File: "a.rs", Name: "x"}: {},
		{File: "a.rs", Name: "y"}: {},
	}
	senseDead := []RustDead{
		{Qualified: "m::x", File: "a.rs", Name: "x"},
	}
	if fd := RustFalseDeads(senseDead, oracle); len(fd) != 0 {
		t.Errorf("expected zero false dead, got %v", fd)
	}
}

// TestRustFalseDeadsPlantedLiveMethodFails is the coverage-gate requirement: a
// planted false `dead` (a live trait method Sense wrongly called dead, absent from
// cargo's dead_code set) MUST be reported, proving the gate detects the only
// unforgivable error rather than rubber-stamping.
func TestRustFalseDeadsPlantedLiveMethodFails(t *testing.T) {
	oracle := DeadCodeSet{
		{File: "a.rs", Name: "really_dead"}: {},
	}
	senseDead := []RustDead{
		{Qualified: "m::really_dead", File: "a.rs", Name: "really_dead"}, // legitimate
		{Qualified: "m::Worker::run", File: "b.rs", Name: "run"},         // planted false dead
	}
	fd := RustFalseDeads(senseDead, oracle)
	if len(fd) != 1 {
		t.Fatalf("expected exactly one false dead (the planted live method), got %v", fd)
	}
	if fd[0].Qualified != "m::Worker::run" {
		t.Errorf("false dead = %q, want m::Worker::run", fd[0].Qualified)
	}
}

const sampleCargoDeadCode = `{"reason":"compiler-message","message":{"message":"function ` + "`dead_helper`" + ` is never used","code":{"code":"dead_code"},"level":"warning","spans":[{"file_name":"src/lib.rs","is_primary":true}]}}` + "\n"

// TestRunCargoDeadCodeSuccess: a clean run (nil error) parses the findings.
func TestRunCargoDeadCodeSuccess(t *testing.T) {
	restore := runCargo
	defer func() { runCargo = restore }()
	runCargo = func(context.Context, string) ([]byte, error) {
		return []byte(sampleCargoDeadCode), nil
	}
	set, err := RunCargoDeadCode(context.Background(), "ignored")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := set[rustSymRef{File: "src/lib.rs", Name: "dead_helper"}]; !ok {
		t.Errorf("expected dead_helper in set, got %v", set)
	}
}

// TestRunCargoDeadCodeFindingsExit: cargo exits non-zero on a compile error but
// still streams diagnostics; the output must be parsed, not treated as a launch
// failure.
func TestRunCargoDeadCodeFindingsExit(t *testing.T) {
	restore := runCargo
	defer func() { runCargo = restore }()
	runCargo = func(context.Context, string) ([]byte, error) {
		return []byte(sampleCargoDeadCode), &exec.ExitError{}
	}
	set, err := RunCargoDeadCode(context.Background(), "ignored")
	if err != nil {
		t.Fatalf("ExitError must not be fatal: %v", err)
	}
	if len(set) != 1 {
		t.Errorf("expected findings parsed despite non-zero exit, got %v", set)
	}
}

// TestRunCargoDeadCodeLaunchFailure: a non-ExitError (binary missing) maps to
// ErrCargoUnavailable so callers can skip.
func TestRunCargoDeadCodeLaunchFailure(t *testing.T) {
	restore := runCargo
	defer func() { runCargo = restore }()
	runCargo = func(context.Context, string) ([]byte, error) {
		return nil, errors.New("exec: \"cargo\": executable file not found in $PATH")
	}
	_, err := RunCargoDeadCode(context.Background(), "ignored")
	if !errors.Is(err, ErrCargoUnavailable) {
		t.Errorf("expected ErrCargoUnavailable, got %v", err)
	}
}

// TestRustFalseDeadsSortOrder exercises both arms of the violation sort: across
// files and within one file.
func TestRustFalseDeadsSortOrder(t *testing.T) {
	oracle := DeadCodeSet{}
	senseDead := []RustDead{
		{Qualified: "p::zeta", File: "b.rs", Name: "zeta"},
		{Qualified: "p::alpha", File: "a.rs", Name: "alpha"},
		{Qualified: "p::beta", File: "a.rs", Name: "beta"},
	}
	fd := RustFalseDeads(senseDead, oracle)
	got := []string{fd[0].Name, fd[1].Name, fd[2].Name}
	want := []string{"alpha", "beta", "zeta"} // a.rs:alpha, a.rs:beta, b.rs:zeta
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sort order = %v, want %v", got, want)
			break
		}
	}
}

// TestRustDeadSymbolsScanError covers the scan-failure path: a root that cannot be
// scanned surfaces an error rather than an empty set.
func TestRustDeadSymbolsScanError(t *testing.T) {
	_, err := RustDeadSymbols(context.Background(), filepath.Join(t.TempDir(), "does-not-exist"), filepath.Join(t.TempDir(), ".sense"))
	if err == nil {
		t.Error("expected an error scanning a missing root")
	}
}

// TestRustDeadSymbols scans a tiny Rust crate and confirms RustDeadSymbols returns
// the genuinely-dead non-pub helper and nothing live. No cargo needed.
func TestRustDeadSymbols(t *testing.T) {
	root := t.TempDir()
	if err := Materialize(root, map[string]string{
		"src/lib.rs": `pub fn entry() { live(); }

fn live() {}

fn dead_helper() {}
`,
	}); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	got, err := RustDeadSymbols(context.Background(), root, filepath.Join(root, ".sense"))
	if err != nil {
		t.Fatalf("RustDeadSymbols: %v", err)
	}
	names := map[string]bool{}
	for _, d := range got {
		names[d.Name] = true
	}
	if !names["dead_helper"] {
		t.Errorf("dead_helper should be earned dead; got %v", got)
	}
	if names["live"] || names["entry"] {
		t.Errorf("live/exported symbols must not be dead; got %v", got)
	}
}

// TestCargoOracleEndToEnd runs the full gate against a real cargo when it is
// installed, asserting Sense's `dead` set is a subset (zero false dead). It skips
// cleanly when cargo is unavailable so CI without the toolchain stays green; the
// binding repo-level run lives in the benchmark card.
func TestCargoOracleEndToEnd(t *testing.T) {
	root := t.TempDir()
	if err := Materialize(root, map[string]string{
		"Cargo.toml": `[package]
name = "oracletest"
version = "0.1.0"
edition = "2021"

[lib]
path = "src/lib.rs"
`,
		"src/lib.rs": `pub trait Runner {
    fn run(&self);
}

struct Worker;

impl Runner for Worker {
    // run satisfies Runner; it is reached through the trait, so Sense must NOT
    // call it dead even though no direct caller exists. cargo does not warn it.
    fn run(&self) {}
}

// dead_helper is genuinely unused — both Sense and cargo must agree.
fn dead_helper() {}

pub fn make() -> impl Runner {
    Worker
}
`,
	}); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	ctx := context.Background()

	oracle, err := RunCargoDeadCode(ctx, root)
	if errors.Is(err, ErrCargoUnavailable) {
		t.Skip("cargo not installed; skipping the live oracle check")
	}
	if err != nil {
		t.Fatalf("RunCargoDeadCode: %v", err)
	}

	senseDead, err := RustDeadSymbols(ctx, root, filepath.Join(root, ".sense"))
	if err != nil {
		t.Fatalf("RustDeadSymbols: %v", err)
	}

	falseDead := RustFalseDeads(senseDead, oracle)
	t.Logf("Sense dead=%d, cargo dead_code=%d, false dead=%d", len(senseDead), len(oracle), len(falseDead))
	if len(falseDead) != 0 {
		t.Errorf("false-dead-count = %d, want 0; violations: %v", len(falseDead), falseDead)
	}
}
