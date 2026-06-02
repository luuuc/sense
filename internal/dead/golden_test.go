package dead_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/dead"
	"github.com/luuuc/sense/internal/scan"
)

var updateGolden = flag.Bool("update-golden", false, "regenerate dead-golden.json")

// goldenEntry is one classified symbol: its identity plus the verdict and (for
// possibly_dead) the open-world reason code. Recording verdict + reason — not
// just membership — is the point: the golden pins the two-sided gate
// (something earns `dead`, the rest are honestly `possibly_dead`) end to end.
type goldenEntry struct {
	Qualified string `json:"qualified"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	Verdict   string `json:"verdict"`
	Reason    string `json:"reason,omitempty"`
}

type goldenFile struct {
	Findings          []goldenEntry `json:"findings"`
	TotalSymbols      int           `json:"total_symbols"`
	DeadCount         int           `json:"dead_count"`
	PossiblyDeadCount int           `json:"possibly_dead_count"`
}

func smokeRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "smoke"))
	if err != nil {
		t.Fatalf("resolve smoke root: %v", err)
	}
	return root
}

func goldenPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(smokeRoot(t), "dead-golden.json")
}

func scanSmoke(t *testing.T) *sql.DB {
	t.Helper()
	root := smokeRoot(t)
	senseDir := t.TempDir()

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Sense:    senseDir,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(senseDir, "index.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func smokeGolden(t *testing.T) goldenFile {
	t.Helper()
	db := scanSmoke(t)
	result, err := dead.FindDead(context.Background(), db, dead.Options{Limit: 200})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	entries := make([]goldenEntry, 0, len(result.Findings))
	var deadCount, possiblyCount int
	for _, f := range result.Findings {
		e := goldenEntry{
			Qualified: f.Symbol.Qualified,
			Kind:      f.Symbol.Kind,
			File:      f.Symbol.File,
			Verdict:   string(f.Verdict),
		}
		if f.Verdict == dead.VerdictDead {
			deadCount++
		} else {
			possiblyCount++
			if f.Reason != nil {
				e.Reason = f.Reason.Code
			}
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Qualified != entries[j].Qualified {
			return entries[i].Qualified < entries[j].Qualified
		}
		return entries[i].File < entries[j].File
	})

	return goldenFile{
		Findings:          entries,
		TotalSymbols:      result.TotalSymbols,
		DeadCount:         deadCount,
		PossiblyDeadCount: possiblyCount,
	}
}

func TestDeadCodeGolden(t *testing.T) {
	actual := smokeGolden(t)

	if *updateGolden {
		data, err := json.MarshalIndent(actual, "", "  ")
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(goldenPath(t), append(data, '\n'), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated %s: %d dead, %d possibly_dead out of %d total",
			goldenPath(t), actual.DeadCount, actual.PossiblyDeadCount, actual.TotalSymbols)
		return
	}

	data, err := os.ReadFile(goldenPath(t))
	if err != nil {
		t.Fatalf("read golden file (run with -update-golden to generate): %v", err)
	}
	var expected goldenFile
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatalf("parse golden: %v", err)
	}

	actualJSON, _ := json.MarshalIndent(actual, "", "  ")
	expectedJSON, _ := json.MarshalIndent(expected, "", "  ")
	if !bytes.Equal(actualJSON, expectedJSON) {
		t.Errorf("dead code output differs from golden file.\nGot:\n%s\nWant:\n%s\n\nRun with -update-golden to regenerate.",
			string(actualJSON), string(expectedJSON))
	}
}

// TestDeadCLIIntegration is the two-sided-gate proof on a real scanned
// fixture: a genuinely-dead Ruby private AND an unexported Go helper earn
// `dead`; the value-object predicates, an `init`, an interface method, and every
// exported Go symbol stay `possibly_dead` with the exact reason; and no
// known-live symbol is reported at all.
func TestDeadCLIIntegration(t *testing.T) {
	g := smokeGolden(t)

	verdict := map[string]string{}
	reason := map[string]string{}
	for _, e := range g.Findings {
		verdict[e.Qualified] = e.Verdict
		reason[e.Qualified] = e.Reason
	}

	// EARNED dead: the genuinely-dead private method. The exact qualified name
	// depends on extractor nesting, so match on the method-name suffix.
	var foundDeadPrivate bool
	for q, v := range verdict {
		if endsWith(q, "#orphaned_private") {
			foundDeadPrivate = true
			if v != "dead" {
				t.Errorf("%s verdict = %q, want dead (private, no caller, not reflection-reachable)", q, v)
			}
		}
	}
	if !foundDeadPrivate {
		t.Error("orphaned_private not found in findings — expected the one earned `dead`")
	}

	// POSSIBLY_DEAD: value-object predicates must never be `dead`.
	for q, v := range verdict {
		if endsWith(q, "#success?") || endsWith(q, "#pending?") {
			if v == "dead" {
				t.Errorf("%s was flagged dead but is a duck-typed value-object predicate", q)
			}
		}
	}

	// EARNED dead (Go): the unexported, zero-edge, unmentioned helper.
	if verdict["smoke.reconcileLedger"] != "dead" {
		t.Errorf("smoke.reconcileLedger verdict = %q, want dead (unexported, no caller, no mention)", verdict["smoke.reconcileLedger"])
	}
	// POSSIBLY_DEAD (Go), exact reason: init is runtime-invoked; an interface
	// method is reachable through any implementor.
	if v, r := verdict["smoke.init"], reason["smoke.init"]; v != "possibly_dead" || r != "go_init" {
		t.Errorf("smoke.init = (%q, %q), want (possibly_dead, go_init)", v, r)
	}
	if v, r := verdict["smoke.Notifier.Notify"], reason["smoke.Notifier.Notify"]; v != "possibly_dead" || r != "go_interface" {
		t.Errorf("smoke.Notifier.Notify = (%q, %q), want (possibly_dead, go_interface)", v, r)
	}
	// SAFETY INVARIANT: no EXPORTED Go symbol earns dead — staticcheck U1000
	// flags only unexported symbols, so an exported one must stay possibly_dead.
	for _, e := range g.Findings {
		if hasSuffix(e.File, ".go") && e.Verdict == "dead" && startsUpper(lastSegment(e.Qualified)) {
			t.Errorf("%s (exported Go) was flagged dead; exported symbols must stay possibly_dead", e.Qualified)
		}
	}

	// EARNED dead (Rust): the non-`pub`, zero-edge, unmentioned function.
	if verdict["orphaned_rust"] != "dead" {
		t.Errorf("orphaned_rust verdict = %q, want dead (non-pub, no caller, no mention)", verdict["orphaned_rust"])
	}
	// POSSIBLY_DEAD (Rust), exact reason: a trait-impl method is reached through the
	// trait; a derive-named method is reached through a synthesized impl. Both would
	// otherwise be `dead` (Money::clone is unmentioned), so this is the two-sided proof.
	if v, r := verdict["Robot::greet"], reason["Robot::greet"]; v != "possibly_dead" || r != "rust_trait_impl" {
		t.Errorf("Robot::greet = (%q, %q), want (possibly_dead, rust_trait_impl)", v, r)
	}
	if v, r := verdict["Money::clone"], reason["Money::clone"]; v != "possibly_dead" || r != "rust_derive" {
		t.Errorf("Money::clone = (%q, %q), want (possibly_dead, rust_derive)", v, r)
	}
	// SAFETY INVARIANT: no `pub` Rust symbol earns dead — rustc's dead_code lint
	// flags only non-`pub` items, so a `pub` one must stay possibly_dead.
	for _, e := range g.Findings {
		if hasSuffix(e.File, ".rs") && e.Verdict == "dead" && (e.Qualified == "Registry" || e.Qualified == "Greeter") {
			t.Errorf("%s (pub Rust) was flagged dead; pub items must stay possibly_dead", e.Qualified)
		}
	}

	// EARNED dead (TS): the module-private, zero-edge, unmentioned .ts function.
	if verdict["orphanedTs"] != "dead" {
		t.Errorf("orphanedTs verdict = %q, want dead (module-private .ts, no caller, no mention)", verdict["orphanedTs"])
	}
	// TS/JS PRECISION SPLIT: the byte-identical shape in a .js file stays
	// possibly_dead (js_dynamic) — plain JS is too loose for the closed-world bet.
	if v, r := verdict["orphanedJs"], reason["orphanedJs"]; v != "possibly_dead" || r != "js_dynamic" {
		t.Errorf("orphanedJs = (%q, %q), want (possibly_dead, js_dynamic) — the TS/JS split", v, r)
	}
	// POSSIBLY_DEAD (TS), exact reasons — the two-sided control for each hand-raise:
	// an exported symbol, a JSX component, a Next.js route default, and a
	// decorator-annotated (module-private) class all stay open-world.
	for q, wantReason := range map[string]string{
		"exportedHelper": "core_exported_api",  // exported callable in a library
		"Badge":          "ts_jsx",             // PascalCase component in a .tsx file
		"Page":           "ts_framework_route", // app/page.tsx default export
		"TokenStore":     "ts_decorator",       // @Injectable on a module-private class
	} {
		if v, r := verdict[q], reason[q]; v != "possibly_dead" || r != wantReason {
			t.Errorf("%s = (%q, %q), want (possibly_dead, %q)", q, v, r, wantReason)
		}
	}

	// EARNED dead (Python): the underscore-private, zero-edge, unmentioned,
	// non-dunder, non-decorated function — the one shape Python lets through.
	if verdict["_orphaned_helper"] != "dead" {
		t.Errorf("_orphaned_helper verdict = %q, want dead (underscore-private, no caller, no mention)", verdict["_orphaned_helper"])
	}
	// SOUNDNESS BACKSTOP (Python): an underscore-private name mentioned where the
	// resolver could not bind it stays open-world via the mention gate — proving
	// `dead` rests on the gate, not the underscore alone.
	if v, r := verdict["_mentioned_private"], reason["_mentioned_private"]; v != "possibly_dead" || r != "core_name_mentioned" {
		t.Errorf("_mentioned_private = (%q, %q), want (possibly_dead, core_name_mentioned)", v, r)
	}
	// POSSIBLY_DEAD (Python), exact reasons — the two-sided control for each
	// hand-raise: a dunder, a decorated method, and a route handler all stay open.
	for q, wantReason := range map[string]string{
		"Account.__repr__": "py_dunder",    // interpreter-invoked protocol method
		"Account.label":    "py_decorator", // @property — attribute access
		"health_check":     "py_route",     // @app.route handler
	} {
		if v, r := verdict[q], reason[q]; v != "possibly_dead" || r != wantReason {
			t.Errorf("%s = (%q, %q), want (possibly_dead, %q)", q, v, r, wantReason)
		}
	}
	// SAFETY INVARIANT: no public (non-underscore) Python symbol earns dead —
	// public Python is always reachable by duck-typed dispatch.
	for _, e := range g.Findings {
		if hasSuffix(e.File, ".py") && e.Verdict == "dead" && !startsWithUnderscore(lastSegment(e.Qualified)) {
			t.Errorf("%s (public Python) was flagged dead; public symbols must stay possibly_dead", e.Qualified)
		}
	}

	// Known-live symbols must not be reported at all.
	for q := range verdict {
		for _, live := range []string{
			"smoke.OrderService.Process",
			"smoke.PaymentGateway.Charge",
			"_used_private", // underscore-private but CALLED — never a candidate
		} {
			if q == live {
				t.Errorf("%s should be alive, but appears in findings", q)
			}
		}
	}

	// Test files must never appear.
	for _, e := range g.Findings {
		if e.File == "order_test.go" || e.File == "order_test.rb" {
			t.Errorf("test file symbol %q should be excluded", e.Qualified)
		}
	}
}

func endsWith(s, suffix string) bool { return hasSuffix(s, suffix) }

// lastSegment returns the final dot-separated segment of a qualified name
// (smoke.PaymentGateway.Refund → Refund), the symbol's own name.
func lastSegment(qualified string) string {
	for i := len(qualified) - 1; i >= 0; i-- {
		if qualified[i] == '.' {
			return qualified[i+1:]
		}
	}
	return qualified
}

// startsUpper reports whether s begins with an ASCII uppercase letter — Go's
// exported-visibility rule.
func startsUpper(s string) bool { return s != "" && s[0] >= 'A' && s[0] <= 'Z' }

func startsWithUnderscore(s string) bool { return s != "" && s[0] == '_' }

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
