package blast_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// setupGraph writes a Ruby chain A → B → C (reading "C is called by
// B is called by A"), runs a scan, and returns an open *sql.DB
// pointing at the resulting index alongside the adapter so tests can
// look symbols up by qualified name.
func setupGraph(t *testing.T) (*sql.DB, *sqlite.Adapter) {
	t.Helper()
	root := t.TempDir()

	// c: leaf. Nothing calls it yet; b will.
	writeFile(t, filepath.Join(root, "c.rb"), `class C
  def leaf
    42
  end
end
`)
	// b: calls leaf via send so the call edge lands with a resolvable
	// bare target ("leaf"). Using send(:leaf) ensures a call_expression
	// with a literal symbol argument, which Ruby's extractor turns into
	// an EdgeCalls edge that the resolver's unqualified fallback finds.
	writeFile(t, filepath.Join(root, "b.rb"), `class B
  def middle
    send(:leaf)
  end
end
`)
	// a: calls middle. Again via send so the emitted edge is a
	// resolvable calls edge.
	writeFile(t, filepath.Join(root, "a.rb"), `class A
  def top
    send(:middle)
  end
end
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open adapter: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return db, adapter
}

func idOf(t *testing.T, a *sqlite.Adapter, qualified string) int64 {
	t.Helper()
	all, err := a.Query(context.Background(), index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for _, s := range all {
		if s.Qualified == qualified {
			return s.ID
		}
	}
	t.Fatalf("symbol %q not indexed", qualified)
	return 0
}

// TestComputeDirectCaller exercises the minimum useful blast:
// leaf → called by middle at hop=1. No deeper callers.
func TestComputeDirectCaller(t *testing.T) {
	db, adapter := setupGraph(t)
	leafID := idOf(t, adapter, "C#leaf")
	middleID := idOf(t, adapter, "B#middle")

	res, err := blast.Compute(context.Background(), db, leafID, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.Symbol.ID != leafID {
		t.Errorf("Symbol.ID = %d, want %d", res.Symbol.ID, leafID)
	}
	if len(res.DirectCallers) != 1 || res.DirectCallers[0].ID != middleID {
		t.Errorf("DirectCallers = %+v, want [B#middle]", res.DirectCallers)
	}
	if len(res.IndirectCallers) != 0 {
		t.Errorf("IndirectCallers = %+v, want []", res.IndirectCallers)
	}
	if res.TotalAffected != 1 {
		t.Errorf("TotalAffected = %d, want 1", res.TotalAffected)
	}
	// One direct caller is below the medium threshold (3); risk is
	// low. The Reason line carries the count verbatim so a CLI /
	// MCP consumer can render it without reformatting.
	if res.Risk != blast.RiskLow {
		t.Errorf("Risk = %q, want %q (1 caller is below medium threshold)", res.Risk, blast.RiskLow)
	}
	if len(res.RiskReasons) != 1 || res.RiskReasons[0] != "1 direct caller" {
		t.Errorf("RiskReasons = %v, want [%q]", res.RiskReasons, "1 direct caller")
	}
}

// TestComputeMultiHopReachesAncestor walks three hops to confirm BFS
// crosses more than one layer and tags indirect callers with the
// predecessor that introduced them.
func TestComputeMultiHopReachesAncestor(t *testing.T) {
	db, adapter := setupGraph(t)
	leafID := idOf(t, adapter, "C#leaf")
	middleID := idOf(t, adapter, "B#middle")
	topID := idOf(t, adapter, "A#top")

	res, err := blast.Compute(context.Background(), db, leafID, blast.Options{MaxHops: 3})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if len(res.DirectCallers) != 1 || res.DirectCallers[0].ID != middleID {
		t.Errorf("DirectCallers = %+v, want [B#middle]", res.DirectCallers)
	}
	if len(res.IndirectCallers) != 1 {
		t.Fatalf("IndirectCallers len = %d, want 1", len(res.IndirectCallers))
	}
	hop := res.IndirectCallers[0]
	if hop.Symbol.ID != topID {
		t.Errorf("Indirect.Symbol.ID = %d, want %d (A#top)", hop.Symbol.ID, topID)
	}
	if hop.Via.ID != middleID {
		t.Errorf("Indirect.Via.ID = %d, want %d (B#middle — the predecessor hop)", hop.Via.ID, middleID)
	}
	if hop.Hops != 2 {
		t.Errorf("Indirect.Hops = %d, want 2", hop.Hops)
	}
	if res.TotalAffected != 2 {
		t.Errorf("TotalAffected = %d, want 2", res.TotalAffected)
	}
}

// TestComputeMaxHopsCaps ensures the BFS stops at the configured
// depth and doesn't surface anything beyond it.
func TestComputeMaxHopsCaps(t *testing.T) {
	db, adapter := setupGraph(t)
	leafID := idOf(t, adapter, "C#leaf")

	res, err := blast.Compute(context.Background(), db, leafID, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(res.IndirectCallers) != 0 {
		t.Errorf("IndirectCallers with MaxHops=1 = %+v, want []", res.IndirectCallers)
	}
}

// TestComputeMinConfidenceFilters shows the confidence floor drops
// an edge whose confidence is below the threshold. Ruby send(:name)
// emits with confidence 0.7 (dynamic dispatch), so a MinConfidence
// of 0.8 removes every edge in the test graph.
func TestComputeMinConfidenceFilters(t *testing.T) {
	db, adapter := setupGraph(t)
	leafID := idOf(t, adapter, "C#leaf")

	res, err := blast.Compute(context.Background(), db, leafID, blast.Options{
		MaxHops:       3,
		MinConfidence: 0.8,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.TotalAffected != 0 {
		t.Errorf("TotalAffected = %d, want 0 (all edges filtered out at 0.8)", res.TotalAffected)
	}
}

// TestComputeMissingSubjectReturnsSentinel surfaces the sentinel
// error so CLI / MCP callers can branch on "no such symbol" without
// string-matching the error text.
func TestComputeMissingSubjectReturnsSentinel(t *testing.T) {
	db, _ := setupGraph(t)

	_, err := blast.Compute(context.Background(), db, 99999, blast.Options{MaxHops: 1})
	if !errors.Is(err, blast.ErrSymbolNotFound) {
		t.Errorf("err = %v, want ErrSymbolNotFound", err)
	}
}

// TestComputeDefaultMaxHops pins the zero-value behaviour: passing
// Options{} (all zero) invokes the default 3-hop traversal, not a
// no-op.
func TestComputeDefaultMaxHops(t *testing.T) {
	db, adapter := setupGraph(t)
	leafID := idOf(t, adapter, "C#leaf")

	res, err := blast.Compute(context.Background(), db, leafID, blast.Options{})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.TotalAffected != 2 {
		t.Errorf("TotalAffected = %d, want 2 (default 3 hops reach A#top via B#middle)", res.TotalAffected)
	}
}

// TestComputeHandlesCycle pins the pitch's explicit claim that BFS
// tolerates cycles via the visited set rather than flagging them.
// The fixture builds A → B → A (mutual recursion via send) and
// asserts Compute on A terminates, surfaces B as the direct caller,
// and does not loop forever or report A as its own caller.
func TestComputeHandlesCycle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cycle.rb"), `class Cycle
  def a
    send(:b)
  end

  def b
    send(:a)
  end
end
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	aID := idOf(t, adapter, "Cycle#a")
	bID := idOf(t, adapter, "Cycle#b")

	res, err := blast.Compute(ctx, db, aID, blast.Options{MaxHops: 3})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	// Direct caller of a is b. The cycle would reach a back via b, but
	// a is the subject and is in visited from the start, so it's not
	// added again — the pitch's "visited set handles cycles" claim.
	if len(res.DirectCallers) != 1 || res.DirectCallers[0].ID != bID {
		t.Errorf("DirectCallers = %+v, want [Cycle#b]", res.DirectCallers)
	}
	for _, hop := range res.IndirectCallers {
		if hop.Symbol.ID == aID {
			t.Errorf("subject a appears as its own indirect caller via cycle; BFS should skip via visited set")
		}
	}
}

// TestComputeIncludeTestsEmptyWhenNoEdges verifies the opt-in flag
// works and returns an empty slice when no tests edges exist —
// Card 12 adds the emission; this card exposes the query.
func TestComputeIncludeTestsEmptyWhenNoEdges(t *testing.T) {
	db, adapter := setupGraph(t)
	leafID := idOf(t, adapter, "C#leaf")

	res, err := blast.Compute(context.Background(), db, leafID, blast.Options{
		MaxHops:      3,
		IncludeTests: true,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.AffectedTests == nil {
		t.Error("AffectedTests = nil, want empty slice")
	}
	if len(res.AffectedTests) != 0 {
		t.Errorf("AffectedTests = %+v, want [] (no tests edges exist yet)", res.AffectedTests)
	}
}

// TestComputeAffectedTestsPopulated proves Card 12's tests edges
// flow through Compute when IncludeTests is set: scanning a Go
// package with a sibling _test.go file emits tests edges, and
// Compute on the subject impl symbol surfaces the test file path in
// Result.AffectedTests.
func TestComputeAffectedTestsPopulated(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "widget.go"), `package widget

func Target() int { return 42 }
`)
	writeFile(t, filepath.Join(root, "widget_test.go"), `package widget

import "testing"

func TestTarget(t *testing.T) {
	_ = Target()
}
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	targetID := idOf(t, adapter, "widget.Target")
	res, err := blast.Compute(ctx, db, targetID, blast.Options{
		MaxHops:      1,
		IncludeTests: true,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(res.AffectedTests) == 0 {
		t.Fatalf("AffectedTests empty; want the widget_test.go path")
	}
	found := false
	for _, p := range res.AffectedTests {
		if p == "widget_test.go" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("AffectedTests = %v, want to contain widget_test.go", res.AffectedTests)
	}
}

// TestComputeWalksComposesEdges verifies that the BFS traverses
// composes edges (Rails associations like has_many/belongs_to), not
// just calls edges.
func TestComputeWalksComposesEdges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "user.rb"), `class User
  has_many :orders
end
`)
	writeFile(t, filepath.Join(root, "order.rb"), `class Order
  belongs_to :user
end
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	userID := idOf(t, adapter, "User")
	res, err := blast.Compute(ctx, db, userID, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if res.TotalAffected == 0 {
		t.Error("TotalAffected = 0, want >= 1 (Order composes User via belongs_to)")
	}
	// Order should appear as a direct caller via the composes edge.
	found := false
	for _, c := range res.DirectCallers {
		if c.Qualified == "Order" {
			found = true
		}
	}
	if !found {
		t.Errorf("DirectCallers = %+v, want Order (via belongs_to composes edge)", res.DirectCallers)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
