package main

import (
	"strings"
	"testing"
)

// TestCheckRedsSyntheticSubFloorFile is the gate's reason to exist: a gated
// file below the floor must produce a violation.
func TestCheckRedsSyntheticSubFloorFile(t *testing.T) {
	line := map[string]float64{
		"internal/scan/scan.go": 80.0, // gated, below floor
	}
	v := Check(line, nil)
	if len(v) != 1 {
		t.Fatalf("want 1 violation, got %d: %+v", len(v), v)
	}
	if v[0].File != "internal/scan/scan.go" || v[0].Metric != "line" {
		t.Errorf("unexpected violation: %+v", v[0])
	}
}

func TestCheckHonoursFloorBoundary(t *testing.T) {
	line := map[string]float64{
		"internal/scan/scan.go":    floor,       // exactly at floor: passes
		"internal/sqlite/reads.go": floor - 0.1, // just under: fails
	}
	v := Check(line, nil)
	if len(v) != 1 || v[0].File != "internal/sqlite/reads.go" {
		t.Fatalf("only the sub-floor file should violate, got %+v", v)
	}
}

func TestCheckExemptsStragglersAndTail(t *testing.T) {
	line := map[string]float64{
		"internal/embed/onnx.go":        50.0, // straggler exception: exempt
		"internal/mcpserver/builder.go": 50.0, // straggler exception: exempt
		"internal/cli/root.go":          50.0, // tail (not a covered package): exempt
		"internal/scan/scan_test.go":    10.0, // test file: never gated
	}
	v := Check(line, nil)
	if len(v) != 0 {
		t.Fatalf("stragglers, tail, and test files must be exempt, got %+v", v)
	}
}

func TestCheckFunctionMetricIsIndependent(t *testing.T) {
	// A file can clear the line floor but fail the function floor (statement-
	// heavy covered functions, several wholly-dead functions).
	v := Check(
		map[string]float64{"internal/dead/dead.go": 99.0},
		map[string]float64{"internal/dead/dead.go": 80.0},
	)
	if len(v) != 1 || v[0].Metric != "function" {
		t.Fatalf("want one function violation, got %+v", v)
	}
}

func TestParseLineCoverageMergesDuplicateBlocks(t *testing.T) {
	// The same block appears twice (the -coverpkg=./... shape): uncovered in one
	// run, covered in another. The merge must treat it as covered, not average
	// it down. Two blocks, 1 stmt each; one is covered in run B.
	profile := strings.Join([]string{
		"mode: atomic",
		"github.com/luuuc/sense/internal/scan/scan.go:10.1,12.2 1 0",
		"github.com/luuuc/sense/internal/scan/scan.go:10.1,12.2 1 4", // same block, covered
		"github.com/luuuc/sense/internal/scan/scan.go:20.1,22.2 1 0", // never covered
		"github.com/luuuc/sense/internal/scan/scan.go:20.1,22.2 1 0",
	}, "\n")
	cov, err := ParseLineCoverage(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	got := cov["internal/scan/scan.go"]
	if got != 50.0 { // 1 of 2 distinct blocks covered
		t.Fatalf("want 50.0%% after merge, got %.1f%%", got)
	}
}

func TestParseFuncCoverage(t *testing.T) {
	funcOut := strings.Join([]string{
		"github.com/luuuc/sense/internal/scan/scan.go:99:\tRun\t85.0%",
		"github.com/luuuc/sense/internal/scan/scan.go:200:\thelper\t0.0%", // dead func
		"total:\t\t\t\t\t\t\t\t93.0%",
	}, "\n")
	cov, err := ParseFuncCoverage(strings.NewReader(funcOut))
	if err != nil {
		t.Fatal(err)
	}
	if got := cov["internal/scan/scan.go"]; got != 50.0 { // 1 of 2 funcs hit
		t.Fatalf("want 50.0%% function coverage, got %.1f%%", got)
	}
}

func TestGatedClassification(t *testing.T) {
	cases := []struct {
		file string
		want bool
	}{
		{"internal/scan/scan.go", true},              // covered package
		{"internal/scan/scan_test.go", false},        // test file
		{"internal/scan/scantest/harness.go", false}, // sub-package, not covered set
		{"internal/cli/root.go", false},              // tail
		{"internal/embed/onnx.go", false},            // straggler exception
		{"internal/embed/embedder.go", true},         // covered package, not excepted
		{"cmd/sense/main.go", false},                 // not a covered package
	}
	for _, c := range cases {
		if got := gated(c.file); got != c.want {
			t.Errorf("gated(%q) = %v, want %v", c.file, got, c.want)
		}
	}
}
