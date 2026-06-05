// Command coveragegate enforces the per-file coverage floor over the whole
// production tree.
//
// Every production file must hold >= floor% statement (line) coverage AND
// >= floor% function coverage, or the build reds. The gate is deny-by-default:
// a file is gated unless it is a _test.go file, a justified stragglerException,
// or lives in a pinned excludedDir (test-support packages and the gate's own
// tooling). 28-02 inverted it from the cycle-27 allow-list (coveredPackages) to
// this deny-list, so a package a contributor adds tomorrow holds the floor
// automatically instead of slipping through ungated.
//
// Bringing nothing under the gate is now needed — everything is. The only knobs
// are excludedDirs (a reviewer-visible one-line edit to exempt a test-support
// dir) and stragglerExceptions (one line per genuinely-unreachable file, each
// with a PERMANENT/DEFERRED reason). Both lists are the config.
package main

// floor is the per-file coverage floor, in percent, for both line and function
// coverage. 92 is the calibrated cycle floor: the extractor files saturate at
// ~90-92% on defensive nil-guards (see stragglerExceptions), so 92 is the
// honest enforceable bar, not the aspirational 95 the tail would never meet.
const floor = 92.0

// excludedDirs is the deny-list: the package directories whose files are NOT
// subject to the per-file floor. Everything else in the production tree is
// gated by default — 28-02 inverted the gate from an allow-list (the old
// coveredPackages) to this deny-list so a package a contributor adds tomorrow
// holds the floor automatically, rather than slipping through ungated.
//
// It is an EXACT, pinned set — not a "directory ends in test" suffix rule — so a
// future production package that happens to end in `test` can never be silently
// un-gated. The four non-_test.go files here live in exactly those three `*test`
// support packages; scripts/coveragegate is the only scripts/ package in the
// profile and leaks ~2880 blocks via -coverpkg=./..., so the gate cannot gate
// itself. Adding a new entry is a deliberate, reviewer-visible one-line edit.
var excludedDirs = map[string]bool{
	"internal/scan/scantest":   true, // fixtures.go, scantest.go — scan test harness
	"internal/index/indextest": true, // conformance.go — index conformance suite
	"internal/embed/embedtest": true, // embedtest.go — embed test harness
	"internal/smoke":           true, // smoke_test.go only, no production file
	"scripts/coveragegate":     true, // the gate itself (~2880 -coverpkg leak)
}

// stragglerExceptions are production files whose residual uncovered lines are
// genuinely unreachable without fabricating an impossible state, so they sit
// just under the floor and are exempted with a reason rather than chased (which
// would mean testing dead defensive code). Each reason states whether the
// exception is PERMANENT (unreachable by construction — it never retires) or
// DEFERRED (reachable only at a cost a future pitch must decide is worth it).
// The list shrank in 28-02 when mcpserver/builder.go grew an injection seam;
// keep it short and justified — retiring one is preferred to growing it.
var stragglerExceptions = map[string]string{
	"internal/embed/bundle.go":         "DEFERRED. CGO/ONNX shell: the bundled-ORT init fault and the post-CreateTemp write/chmod faults fire only when a real ONNX runtime or the temp filesystem fails mid-extract. Reaching them needs a production fault-hook driving real-runtime failures you cannot meaningfully recover from — a different appetite at a poor cost/benefit. A future pitch may add the hook; 28-02 deliberately does not (see 28-02 No-gos).",
	"internal/embed/onnx.go":           "DEFERRED. CGO/ONNX shell: the ONNX session/tensor/inference error paths fire only under a real ORT runtime fault. Same production fault-hook, same poor cost/benefit as embed/bundle.go — out of 28-02's behavior-preserving scope; a future pitch decides whether the hook is ever worth it.",
	"internal/extract/rust/compose.go": "PERMANENT. Defensive nil-guards on tree-sitter NamedChild loops and mandatory grammar fields the Rust grammar always populates: unreachable without a malformed CST no valid parse can produce. Chasing it means fabricating impossible trees or deleting the guards (a panic mid-scan aborts a file — worse than an uncovered branch). It never retires.",
}

// Violation is one gated file below the floor on a metric.
type Violation struct {
	File   string
	Metric string // "line" or "function"
	Got    float64
}

// gated reports whether a production file is subject to the per-file floor.
// Deny-by-convention: every production file is gated EXCEPT a *_test.go file, a
// justified stragglerException, or a file in a pinned excludedDir. Zero-statement
// files (the index interface, version constants, extract/languages) carry no
// coverable blocks, so they never appear in the profile and pass implicitly.
func gated(file string) bool {
	if isTestFile(file) {
		return false
	}
	if _, ok := stragglerExceptions[file]; ok {
		return false
	}
	return !excludedDirs[dirOf(file)]
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
