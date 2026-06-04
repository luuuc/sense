// Command coveragegate enforces the cycle-27 per-file coverage floor.
//
// It is the mechanical form of goal 3 ("tested"): every production file in the
// packages the Polish cycle covered must hold >= floor% statement (line)
// coverage AND >= floor% function coverage, or the build reds. It is scoped on
// purpose — the long tail the cycle never touched (cli, summary, profile, …) is
// listed as excluded, not silently ignored, and a stragglerExceptions list
// names the handful of covered files whose residual uncovered lines are
// genuinely unreachable (CGO/ONNX shell, defensive nil-guards on grammar-
// guaranteed CST shapes), each with a reason.
//
// Adding a tail package to the gate later is a one-line edit to coveredPackages;
// retiring a straggler exception is deleting one line. The lists are the config.
package main

// floor is the per-file coverage floor, in percent, for both line and function
// coverage. 92 is the calibrated cycle floor: the extractor files saturate at
// ~90-92% on defensive nil-guards (see stragglerExceptions), so 92 is the
// honest enforceable bar, not the aspirational 95 the tail would never meet.
const floor = 92.0

// coveredPackages is the set of package directories (module-relative) the cycle
// worked and therefore gates per-file. A file is gated iff its directory is in
// this set exactly (so internal/scan gates, internal/scan/scantest does not).
//
// To bring a tail package under the gate later, add one line here.
var coveredPackages = map[string]bool{
	"internal/extract":          true, // 27-10/11/12 — registry + harness
	"internal/extract/erb":      true,
	"internal/extract/golang":   true, // 27-12
	"internal/extract/langspec": true, // 27-12
	"internal/extract/python":   true, // 27-12
	"internal/extract/ruby":     true, // 27-10
	"internal/extract/rust":     true, // 27-11
	"internal/extract/tsjs":     true, // 27-11
	"internal/blast":            true, // pure-core
	"internal/conventions":      true, // 27-08
	"internal/dead":             true, // 27-09
	"internal/dead/eval":        true, // 27-09
	"internal/mcpio":            true, // pure-core
	"internal/model":            true, // pure-core
	"internal/grammars":         true,
	"internal/resolve":          true, // 27-07 (storage/query split)
	"internal/sqlite":           true, // 27-07
	"internal/scan":             true, // 27-03/06
	"internal/mcpserver":        true, // 27-04/05
	"internal/embed":            true, // 27-02
	"internal/watch":            true, // 27-04
	"internal/search":           true,
}

// stragglerExceptions are covered-package files whose residual uncovered lines
// are genuinely unreachable without fabricating an impossible state, so they sit
// just under the floor and are exempted with a reason rather than chased (which
// would mean testing dead defensive code). Keep this list short and justified;
// retiring one is preferred to growing it.
var stragglerExceptions = map[string]string{
	"internal/embed/bundle.go":         "CGO/ONNX shell: bundled-ORT init and post-CreateTemp write/chmod faults need a real ONNX runtime + fault-injecting FS",
	"internal/embed/onnx.go":           "CGO/ONNX shell: ONNX session/tensor/inference error paths need a real ONNX runtime with failure injection",
	"internal/mcpserver/builder.go":    "process/orchestration edge: os.Getwd faults, self-healing index rebuild, and one-shot-embed goroutine races have no injection seam without a production change (a behavior-preserving cycle forbids it) — tracked follow-up",
	"internal/extract/rust/compose.go": "defensive nil-guards on NamedChild loops and mandatory tree-sitter fields that the Rust grammar always populates — unreachable without a malformed CST",
}

// Violation is one gated file below the floor on a metric.
type Violation struct {
	File   string
	Metric string // "line" or "function"
	Got    float64
}

// gated reports whether a production file is subject to the per-file floor.
func gated(file string) bool {
	if isTestFile(file) {
		return false
	}
	if _, ok := stragglerExceptions[file]; ok {
		return false
	}
	return coveredPackages[dirOf(file)]
}

// Check returns every gated file below the floor on line or function coverage.
// lineCov and funcCov are module-relative file -> percent. A file present in one
// map but not the other is checked only on the metric it has (the profile and
// the func report can, in principle, disagree on which files they list).
func Check(lineCov, funcCov map[string]float64) []Violation {
	var v []Violation
	for file, pct := range lineCov {
		if gated(file) && pct < floor {
			v = append(v, Violation{File: file, Metric: "line", Got: pct})
		}
	}
	for file, pct := range funcCov {
		if gated(file) && pct < floor {
			v = append(v, Violation{File: file, Metric: "function", Got: pct})
		}
	}
	sortViolations(v)
	return v
}
