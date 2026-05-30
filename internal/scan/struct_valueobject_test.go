package scan_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/dead"
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/scan"
)

// TestStructInheritsEdgePersists proves the value-object inherits edge
// survives a full scan → resolve round-trip. The extractor emitting the
// edge is not enough: sense_edges.target_id is NOT NULL, so the edge only
// lands if the synthetic ruby-core:Struct symbol is also written and the
// resolver binds the edge to it. This is the anti-pattern the pitch calls
// out — asserting the emit, not the persistence.
func TestStructInheritsEdgePersists(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "process_payment_service.rb"), `class Checkout::ProcessPaymentService
  Result = Struct.new(:success, keyword_init: true) do
    def success?
      success
    end
  end

  def call
    Result.new(success: true)
  end
end
`)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// The inherits edge resolved to a real target row.
	var target string
	err = db.QueryRow(`
		SELECT t.qualified
		FROM sense_edges e
		JOIN sense_symbols s ON s.id = e.source_id
		JOIN sense_symbols t ON t.id = e.target_id
		WHERE e.kind = 'inherits'
		  AND s.qualified = 'Checkout::ProcessPaymentService::Result'`).Scan(&target)
	if err != nil {
		t.Fatalf("inherits edge did not persist after resolution: %v", err)
	}
	if target != extract.RubyCoreStruct {
		t.Errorf("inherits target = %q, want %q", target, extract.RubyCoreStruct)
	}

	// The method qualifies to the struct, not the enclosing service.
	var kind string
	if err := db.QueryRow(`SELECT kind FROM sense_symbols WHERE qualified = ?`,
		"Checkout::ProcessPaymentService::Result#success?").Scan(&kind); err != nil {
		t.Fatalf("success? not attributed to the struct: %v", err)
	}

	// The synthetic base is emitted exactly once across the scan.
	var baseCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sense_symbols WHERE qualified = ?`,
		extract.RubyCoreStruct).Scan(&baseCount); err != nil {
		t.Fatal(err)
	}
	if baseCount != 1 {
		t.Errorf("ruby-core:Struct symbol count = %d, want 1", baseCount)
	}
}

// TestValueObjectDeadCodeTargeted drives the whole pipeline — extract,
// resolve, dead-code analysis — and proves the value-object softening is
// targeted AND the two-sided gate holds: a struct predicate with zero static
// callers is possibly_dead (open-world: reached via a duck-typed local),
// while a genuinely uncalled *private* method on an ordinary class earns the
// `dead` verdict (closed-world: a private method cannot be reached by an
// external receiver). The control proves the gate is not a blanket softener;
// the value object proves it is not a blanket dead-stamper.
func TestValueObjectDeadCodeTargeted(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "services.rb"), `class ProcessPaymentService
  Result = Struct.new(:success, keyword_init: true) do
    def success?
      success
    end
  end

  def call
    Result.new(success: true)
  end
end

# Gadget stays alive through its referenced private helper; orphaned_helper is
# private, has zero callers, and its name is not a reflection-dispatch target,
# so it is the closed-world control that must earn the dead verdict.
class Gadget
  def run
    helper
  end

  private

  def helper
    1
  end

  def orphaned_helper
    2
  end
end
`)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	res, err := dead.FindDead(context.Background(), db, dead.Options{Language: "ruby"})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	byQualified := map[string]dead.Finding{}
	for _, f := range res.Findings {
		byQualified[f.Symbol.Qualified] = f
	}

	// The value-object predicate is open-world (possibly_dead) and attributed
	// to the struct (proving the attribution fix fed the value-object reason).
	vo, ok := byQualified["ProcessPaymentService::Result#success?"]
	if !ok {
		t.Fatalf("value-object predicate not found in findings; got %v", keys(byQualified))
	}
	if vo.Verdict != dead.VerdictPossiblyDead {
		t.Errorf("value-object predicate verdict = %q, want %q", vo.Verdict, dead.VerdictPossiblyDead)
	}
	if vo.Reason == nil || vo.Reason.Code != dead.ReasonRubyValueObject {
		t.Errorf("value-object predicate reason = %v, want %q", vo.Reason, dead.ReasonRubyValueObject)
	}

	// CONTROL: the genuinely-dead private method earns the dead verdict.
	ctrl, ok := byQualified["Gadget#orphaned_helper"]
	if !ok {
		t.Fatalf("control private method Gadget#orphaned_helper not found in findings; got %v", keys(byQualified))
	}
	if ctrl.Verdict != dead.VerdictDead {
		t.Errorf("control private method verdict = %q, want %q (closed-world proof)", ctrl.Verdict, dead.VerdictDead)
	}

	// The synthetic base never surfaces as a finding.
	if _, leaked := byQualified[extract.RubyCoreStruct]; leaked {
		t.Error("synthetic ruby-core:Struct leaked into dead output")
	}
}

func keys(m map[string]dead.Finding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
