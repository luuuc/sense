package blast_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
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

	res, err := blast.Compute(context.Background(), db, []int64{leafID}, blast.Options{MaxHops: 1})
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

	res, err := blast.Compute(context.Background(), db, []int64{leafID}, blast.Options{
		MaxHops:       3,
		MinConfidence: 0.4,
	})
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

	res, err := blast.Compute(context.Background(), db, []int64{leafID}, blast.Options{MaxHops: 1})
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

	res, err := blast.Compute(context.Background(), db, []int64{leafID}, blast.Options{
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

	_, err := blast.Compute(context.Background(), db, []int64{99999}, blast.Options{MaxHops: 1})
	if !errors.Is(err, blast.ErrSymbolNotFound) {
		t.Errorf("err = %v, want ErrSymbolNotFound", err)
	}
}

// TestComputeDefaultsStopWeakChains pins the zero-value behaviour:
// Options{} applies default MinConfidence 0.5, which stops chains
// of 0.7-confidence edges at hop 2 (0.7×0.7=0.49 < 0.5).
func TestComputeDefaultsStopWeakChains(t *testing.T) {
	db, adapter := setupGraph(t)
	leafID := idOf(t, adapter, "C#leaf")

	res, err := blast.Compute(context.Background(), db, []int64{leafID}, blast.Options{})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.TotalAffected != 1 {
		t.Errorf("TotalAffected = %d, want 1 (default MinConfidence 0.5 cuts 0.7*0.7=0.49 at hop 2)", res.TotalAffected)
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

	res, err := blast.Compute(ctx, db, []int64{aID}, blast.Options{MaxHops: 3})
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

	res, err := blast.Compute(context.Background(), db, []int64{leafID}, blast.Options{
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
	res, err := blast.Compute(ctx, db, []int64{targetID}, blast.Options{
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
	res, err := blast.Compute(ctx, db, []int64{userID}, blast.Options{MaxHops: 1})
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

func TestComputeWalksInheritsEdges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "base.rb"), `class ApplicationController
  def before_action
  end
end
`)
	writeFile(t, filepath.Join(root, "child.rb"), `class UsersController < ApplicationController
  def index
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

	baseID := idOf(t, adapter, "ApplicationController")
	res, err := blast.Compute(ctx, db, []int64{baseID}, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	found := false
	for _, c := range res.DirectCallers {
		if c.Qualified == "UsersController" {
			found = true
		}
	}
	if !found {
		t.Errorf("DirectCallers = %+v, want UsersController (via inherits edge)", res.DirectCallers)
	}
}

// TestComputeMultiSeedAggregatesReopenings verifies that passing
// multiple symbol IDs seeds the BFS from all of them, returning the
// union of callers. Builds edges at the database level with specific
// target IDs to simulate Ruby class reopenings where each reopened
// definition has distinct callers.
func TestComputeMultiSeedAggregatesReopenings(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "multi.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	err = adapter.InTx(ctx, func() error {
		// Two files, each with a "Widget" class (reopening).
		f1, err := adapter.WriteFile(ctx, &model.File{
			Path: "widget.rb", Language: "ruby", Hash: "w1",
			Symbols: 1, IndexedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		f2, err := adapter.WriteFile(ctx, &model.File{
			Path: "widget_ext.rb", Language: "ruby", Hash: "w2",
			Symbols: 1, IndexedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}

		// Widget in file 1 (id will be assigned by DB)
		widget1, err := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: f1, Name: "Widget", Qualified: "Widget",
			Kind: model.KindClass, LineStart: 1, LineEnd: 10,
		})
		if err != nil {
			return err
		}
		// Widget in file 2 (reopening — same qualified name)
		widget2, err := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: f2, Name: "Widget", Qualified: "Widget",
			Kind: model.KindClass, LineStart: 1, LineEnd: 10,
		})
		if err != nil {
			return err
		}

		// CallerA calls Widget in file 1 only
		callerA, err := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: f1, Name: "CallerA", Qualified: "CallerA",
			Kind: model.KindClass, LineStart: 20, LineEnd: 30,
		})
		if err != nil {
			return err
		}
		if _, err := adapter.WriteEdge(ctx, &model.Edge{
			SourceID: &callerA, TargetID: widget1,
			Kind: model.EdgeCalls, FileID: f1, Confidence: 1.0,
		}); err != nil {
			return err
		}

		// CallerB calls Widget in file 2 only
		callerB, err := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: f2, Name: "CallerB", Qualified: "CallerB",
			Kind: model.KindClass, LineStart: 20, LineEnd: 30,
		})
		if err != nil {
			return err
		}
		if _, err := adapter.WriteEdge(ctx, &model.Edge{
			SourceID: &callerB, TargetID: widget2,
			Kind: model.EdgeCalls, FileID: f2, Confidence: 1.0,
		}); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Find both Widget symbol IDs.
	all, err := adapter.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var widgetIDs []int64
	for _, s := range all {
		if s.Qualified == "Widget" && s.Kind == "class" {
			widgetIDs = append(widgetIDs, s.ID)
		}
	}
	if len(widgetIDs) != 2 {
		t.Fatalf("expected 2 Widget symbols, got %d", len(widgetIDs))
	}

	// Single-seed: only finds the caller targeting that specific ID.
	single, err := blast.Compute(ctx, db, []int64{widgetIDs[0]}, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute single: %v", err)
	}
	if single.TotalAffected != 1 {
		t.Errorf("single-seed TotalAffected = %d, want 1", single.TotalAffected)
	}

	// Multi-seed: finds callers targeting both Widget definitions.
	multi, err := blast.Compute(ctx, db, widgetIDs, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute multi: %v", err)
	}
	if multi.TotalAffected != 2 {
		t.Errorf("multi-seed TotalAffected = %d, want 2", multi.TotalAffected)
	}

	if multi.TotalAffected <= single.TotalAffected {
		t.Errorf("multi-seed (%d) should find more callers than single-seed (%d)",
			multi.TotalAffected, single.TotalAffected)
	}
}

// TestComputeTemporalEdgeBumpsRisk verifies that a temporal edge in
// the blast radius bumps risk to at least medium and appears in callers.
func TestComputeTemporalEdgeBumpsRisk(t *testing.T) {
	db, adapter := setupGraph(t)
	ctx := context.Background()

	// C#leaf has 1 structural caller (B#middle), so risk is normally low.
	// Insert a temporal edge from a new "phantom" symbol to C#leaf.
	leafID := idOf(t, adapter, "C#leaf")

	// First create a symbol in a different file to serve as temporal partner.
	// We'll reuse C's file for the edge's file_id.
	cFileID := fileIDOf(t, adapter, "c.rb")
	aFileID := fileIDOf(t, adapter, "a.rb")

	phantomID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID:    aFileID,
		Name:      "PhantomCron",
		Qualified: "PhantomCron",
		Kind:      "class",
		LineStart: 1,
		LineEnd:   5,
	})
	if err != nil {
		t.Fatalf("WriteSymbol: %v", err)
	}

	coChanges := 8
	_, err = adapter.WriteEdge(ctx, &model.Edge{
		SourceID:   model.Int64Ptr(phantomID),
		TargetID:   leafID,
		Kind:       model.EdgeTemporal,
		FileID:     cFileID,
		Line:       &coChanges,
		Confidence: 0.7,
	})
	if err != nil {
		t.Fatalf("WriteEdge temporal: %v", err)
	}

	res, err := blast.Compute(ctx, db, []int64{leafID}, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	// Should have 2 direct callers: B#middle (structural) + PhantomCron (temporal).
	if len(res.DirectCallers) != 2 {
		t.Fatalf("DirectCallers = %d, want 2", len(res.DirectCallers))
	}

	// Risk should be at least medium because of temporal edge.
	if res.Risk == blast.RiskLow {
		t.Errorf("Risk = %q, want at least medium (temporal coupling present)", res.Risk)
	}

	foundTemporal := false
	for _, reason := range res.RiskReasons {
		if reason == "temporal coupling detected (git co-change history)" {
			foundTemporal = true
		}
	}
	if !foundTemporal {
		t.Errorf("RiskReasons missing temporal: %v", res.RiskReasons)
	}

	// Verify DirectTemporalIDs contains the phantom.
	if !res.DirectTemporalIDs[phantomID] {
		t.Errorf("DirectTemporalIDs should contain phantom %d", phantomID)
	}
}

// TestComputeNoTemporalEdgesStaysLow confirms that without temporal
// edges, the risk classification is unchanged.
func TestComputeNoTemporalEdgesStaysLow(t *testing.T) {
	db, adapter := setupGraph(t)
	leafID := idOf(t, adapter, "C#leaf")

	res, err := blast.Compute(context.Background(), db, []int64{leafID}, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.Risk != blast.RiskLow {
		t.Errorf("Risk = %q, want low (no temporal edges)", res.Risk)
	}
	if len(res.DirectTemporalIDs) != 0 {
		t.Errorf("DirectTemporalIDs should be empty, got %v", res.DirectTemporalIDs)
	}
}

func fileIDOf(t *testing.T, a *sqlite.Adapter, filename string) int64 {
	t.Helper()
	all, err := a.Query(context.Background(), index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	seen := map[int64]bool{}
	for _, s := range all {
		if seen[s.FileID] {
			continue
		}
		seen[s.FileID] = true
		sc, rerr := a.ReadSymbol(context.Background(), s.ID)
		if rerr != nil {
			continue
		}
		if sc.File.Path == filename {
			return sc.File.ID
		}
	}
	t.Fatalf("file %q not found in index", filename)
	return 0
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

// --- Pitch 13-05 fixture tests ---

type fixtureDB struct {
	db      *sql.DB
	adapter *sqlite.Adapter
	fileID  int64
	nextLine int
}

func newFixtureDB(t *testing.T) *fixtureDB {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fixture.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	fid, err := adapter.WriteFile(ctx, &model.File{
		Path: "fixture.rb", Language: "ruby", Hash: "fix",
		Symbols: 1, IndexedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return &fixtureDB{db: db, adapter: adapter, fileID: fid, nextLine: 1}
}

func (f *fixtureDB) addSymbol(t *testing.T, name string) int64 {
	t.Helper()
	line := f.nextLine
	f.nextLine += 10
	id, err := f.adapter.WriteSymbol(context.Background(), &model.Symbol{
		FileID: f.fileID, Name: name, Qualified: name,
		Kind: model.KindClass, LineStart: line, LineEnd: line + 5,
	})
	if err != nil {
		t.Fatalf("WriteSymbol %s: %v", name, err)
	}
	return id
}

func (f *fixtureDB) addEdge(t *testing.T, sourceID, targetID int64, kind model.EdgeKind, conf float64) {
	t.Helper()
	if _, err := f.adapter.WriteEdge(context.Background(), &model.Edge{
		SourceID: &sourceID, TargetID: targetID,
		Kind: kind, FileID: f.fileID, Confidence: conf,
	}); err != nil {
		t.Fatalf("WriteEdge %d→%d: %v", sourceID, targetID, err)
	}
}

func TestGroupedOutputMultiEdge(t *testing.T) {
	fix := newFixtureDB(t)
	base := fix.addSymbol(t, "BaseModel")
	subA := fix.addSymbol(t, "SubModelA")
	subB := fix.addSymbol(t, "SubModelB")
	comp := fix.addSymbol(t, "CompositorX")
	incl := fix.addSymbol(t, "IncluderY")
	caller := fix.addSymbol(t, "CallerZ")

	fix.addEdge(t, subA, base, model.EdgeInherits, 1.0)
	fix.addEdge(t, subB, base, model.EdgeInherits, 1.0)
	fix.addEdge(t, comp, base, model.EdgeComposes, 0.9)
	fix.addEdge(t, incl, base, model.EdgeIncludes, 0.9)
	fix.addEdge(t, caller, base, model.EdgeCalls, 1.0)

	res, err := blast.Compute(context.Background(), fix.db, []int64{base}, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if len(res.DirectCallers) != 5 {
		t.Errorf("DirectCallers = %d, want 5", len(res.DirectCallers))
	}

	subclassIDs := map[int64]bool{}
	for _, s := range res.AffectedSubclasses {
		subclassIDs[s.ID] = true
	}
	if !subclassIDs[subA] || !subclassIDs[subB] {
		t.Errorf("AffectedSubclasses = %+v, want SubModelA + SubModelB", res.AffectedSubclasses)
	}
	if len(res.AffectedSubclasses) != 2 {
		t.Errorf("AffectedSubclasses len = %d, want 2", len(res.AffectedSubclasses))
	}

	compIDs := map[int64]bool{}
	for _, s := range res.AffectedViaComposition {
		compIDs[s.ID] = true
	}
	if !compIDs[comp] {
		t.Errorf("AffectedViaComposition = %+v, want CompositorX", res.AffectedViaComposition)
	}

	inclIDs := map[int64]bool{}
	for _, s := range res.AffectedViaIncludes {
		inclIDs[s.ID] = true
	}
	if !inclIDs[incl] {
		t.Errorf("AffectedViaIncludes = %+v, want IncluderY", res.AffectedViaIncludes)
	}
}

func TestConfidenceDecayStopsAtThreshold(t *testing.T) {
	fix := newFixtureDB(t)

	// Chain: A ←(0.9)— B ←(0.9)— C ←(0.9)— D ←(0.9)— E ←(0.7)— F
	// Cumulative at each hop:
	//   B: 0.9   C: 0.81   D: 0.729   E: 0.656   F: 0.459
	a := fix.addSymbol(t, "A")
	b := fix.addSymbol(t, "B")
	c := fix.addSymbol(t, "C")
	d := fix.addSymbol(t, "D")
	e := fix.addSymbol(t, "E")
	f := fix.addSymbol(t, "F")

	fix.addEdge(t, b, a, model.EdgeCalls, 0.9)
	fix.addEdge(t, c, b, model.EdgeCalls, 0.9)
	fix.addEdge(t, d, c, model.EdgeCalls, 0.9)
	fix.addEdge(t, e, d, model.EdgeCalls, 0.9)
	fix.addEdge(t, f, e, model.EdgeCalls, 0.7)

	res, err := blast.Compute(context.Background(), fix.db, []int64{a}, blast.Options{
		MaxHops:       10,
		MinConfidence: 0.5,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	found := map[int64]bool{}
	for _, c := range res.DirectCallers {
		found[c.ID] = true
	}
	for _, h := range res.IndirectCallers {
		found[h.Symbol.ID] = true
	}

	for _, want := range []int64{b, c, d, e} {
		if !found[want] {
			t.Errorf("expected symbol %d in results (above 0.5 threshold)", want)
		}
	}
	if found[f] {
		t.Errorf("symbol F (cumulative 0.459) should be excluded at MinConfidence 0.5")
	}
	if res.TotalAffected != 4 {
		t.Errorf("TotalAffected = %d, want 4", res.TotalAffected)
	}

	// With MinConfidence 0.8: only B (0.9) and C (0.81) pass.
	res2, err := blast.Compute(context.Background(), fix.db, []int64{a}, blast.Options{
		MaxHops:       10,
		MinConfidence: 0.8,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res2.TotalAffected != 2 {
		t.Errorf("TotalAffected at 0.8 = %d, want 2 (B=0.9, C=0.81)", res2.TotalAffected)
	}
}

func TestResultCapTruncatesWeakest(t *testing.T) {
	fix := newFixtureDB(t)
	subject := fix.addSymbol(t, "Hub")

	// Create 150 callers with varying confidence.
	// First 120 at confidence 1.0, last 30 at confidence 0.6.
	for i := 0; i < 120; i++ {
		id := fix.addSymbol(t, fmt.Sprintf("Strong%d", i))
		fix.addEdge(t, id, subject, model.EdgeCalls, 1.0)
	}
	for i := 0; i < 30; i++ {
		id := fix.addSymbol(t, fmt.Sprintf("Weak%d", i))
		fix.addEdge(t, id, subject, model.EdgeCalls, 0.6)
	}

	res, err := blast.Compute(context.Background(), fix.db, []int64{subject}, blast.Options{
		MaxHops:       1,
		MinConfidence: 0.5,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if res.TotalAffected != 150 {
		t.Errorf("TotalAffected = %d, want 150 (pre-truncation count)", res.TotalAffected)
	}
	returned := len(res.DirectCallers) + len(res.IndirectCallers)
	if returned != 100 {
		t.Errorf("returned symbols = %d, want 100 (cap)", returned)
	}

	// All returned should be the strong (1.0) callers — weakest were truncated.
	for _, c := range res.DirectCallers {
		if c.Qualified[:4] == "Weak" {
			// Some Weak callers may survive if there are <100 Strong+Weak at 1.0,
			// but with 120 strong at 1.0 and only 100 cap, no Weak should remain.
			t.Errorf("Weak caller %s survived truncation; expected only strong callers", c.Qualified)
		}
	}
}

func TestDoubleReachableAppearsInFirstGroup(t *testing.T) {
	fix := newFixtureDB(t)
	base := fix.addSymbol(t, "Base")
	child := fix.addSymbol(t, "Child")

	// Child reaches Base via both inherits and composes.
	// Both map to edge-kind groups. BFS visits Child once;
	// the first edge wins, so Child lands in exactly one group.
	fix.addEdge(t, child, base, model.EdgeInherits, 1.0)
	fix.addEdge(t, child, base, model.EdgeComposes, 1.0)

	res, err := blast.Compute(context.Background(), fix.db, []int64{base}, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if len(res.DirectCallers) != 1 {
		t.Errorf("DirectCallers = %d, want 1 (Child appears once)", len(res.DirectCallers))
	}
	if res.DirectCallers[0].ID != child {
		t.Errorf("DirectCallers[0] = %d, want %d (Child)", res.DirectCallers[0].ID, child)
	}

	// Child should appear in exactly one edge-kind group, not both.
	groupCount := 0
	for _, s := range res.AffectedSubclasses {
		if s.ID == child {
			groupCount++
		}
	}
	for _, s := range res.AffectedViaComposition {
		if s.ID == child {
			groupCount++
		}
	}
	for _, s := range res.AffectedViaIncludes {
		if s.ID == child {
			groupCount++
		}
	}
	if groupCount != 1 {
		t.Errorf("Child appears in %d edge-kind groups, want exactly 1", groupCount)
	}
}
