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
// TestComputeHandlesCycle pins the pitch's explicit claim that BFS
// tolerates cycles via the visited set rather than flagging them.
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
	res, err := blast.Compute(ctx, db, []int64{userID}, blast.Options{MaxHops: 1, MinConfidence: 0.1})
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
	res, err := blast.Compute(ctx, db, []int64{baseID}, blast.Options{MaxHops: 1, MinConfidence: 0.1})
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

// addBlastSymbol writes a bare symbol into the index for traversal tests
// that need a precise edge topology rather than scanned source.
func addBlastSymbol(t *testing.T, a *sqlite.Adapter, fileID int64, qualified string, kind model.SymbolKind) int64 {
	t.Helper()
	id, err := a.WriteSymbol(context.Background(), &model.Symbol{
		FileID:    fileID,
		Name:      qualified,
		Qualified: qualified,
		Kind:      kind,
		LineStart: 1,
		LineEnd:   5,
	})
	if err != nil {
		t.Fatalf("WriteSymbol %q: %v", qualified, err)
	}
	return id
}

func addBlastEdge(t *testing.T, a *sqlite.Adapter, fileID, src, tgt int64, kind model.EdgeKind, conf float64) {
	t.Helper()
	line := 1
	if _, err := a.WriteEdge(context.Background(), &model.Edge{
		SourceID:   model.Int64Ptr(src),
		TargetID:   tgt,
		Kind:       kind,
		FileID:     fileID,
		Line:       &line,
		Confidence: conf,
	}); err != nil {
		t.Fatalf("WriteEdge %s %d->%d: %v", kind, src, tgt, err)
	}
}

// affectedIDs collects every symbol id in the blast radius (direct + indirect).
func affectedIDs(res blast.Result) map[int64]bool {
	ids := map[int64]bool{}
	for _, c := range res.DirectCallers {
		ids[c.ID] = true
	}
	for _, c := range res.IndirectCallers {
		ids[c.Symbol.ID] = true
	}
	return ids
}

// TestComputeTemporalIsSinkNotBridge pins the temporal-coupling-bridge bug
// (pitch 25-14). A temporal edge means "these two co-change", a pairwise
// signal about a node — not a path to keep walking from. Topology:
//
//	Seed ◀temporal─ TPartner ◀temporal─ Hub ◀references─ {Ref1,Ref2,Ref3}
//
// TPartner co-changes with both Seed and Hub (the way a shared test file
// does), so a transitive temporal walk reaches Hub and then fans out across
// Hub's unrelated `references` callers. None of Ref1-3 can break when Seed
// changes. TPartner must still appear (hop-1 co-change signal, bumps risk),
// but the walk must stop there.
func TestComputeTemporalIsSinkNotBridge(t *testing.T) {
	db, adapter := setupGraph(t)
	ctx := context.Background()
	fid := fileIDOf(t, adapter, "a.rb")

	seed := addBlastSymbol(t, adapter, fid, "Seed#change", model.KindMethod)
	tpartner := addBlastSymbol(t, adapter, fid, "TPartnerTest", model.KindClass)
	hub := addBlastSymbol(t, adapter, fid, "Hub", model.KindClass)
	ref1 := addBlastSymbol(t, adapter, fid, "Ref1#a", model.KindMethod)
	ref2 := addBlastSymbol(t, adapter, fid, "Ref2#b", model.KindMethod)
	ref3 := addBlastSymbol(t, adapter, fid, "Ref3#c", model.KindMethod)

	// Reverse-traversal direction: blast walks target_id IN frontier → source.
	addBlastEdge(t, adapter, fid, tpartner, seed, model.EdgeTemporal, 0.6) // TPartner co-changes with Seed
	addBlastEdge(t, adapter, fid, hub, tpartner, model.EdgeTemporal, 0.6)  // Hub co-changes with TPartner
	addBlastEdge(t, adapter, fid, ref1, hub, model.EdgeReferences, 1.0)    // Hub's unrelated callers
	addBlastEdge(t, adapter, fid, ref2, hub, model.EdgeReferences, 1.0)
	addBlastEdge(t, adapter, fid, ref3, hub, model.EdgeReferences, 1.0)

	res, err := blast.Compute(ctx, db, []int64{seed}, blast.Options{MaxHops: 3, MinConfidence: 0.3})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	got := affectedIDs(res)

	// TPartner is a legitimate hop-1 co-change signal: it must appear and bump risk.
	if !got[tpartner] {
		t.Errorf("TPartner (temporal partner) should be in the radius; got %v", got)
	}
	if res.Risk == blast.RiskLow {
		t.Errorf("Risk = %q, want bumped (temporal coupling present)", res.Risk)
	}

	// The bug: Hub and its unrelated callers must NOT be pulled in through the
	// transitive temporal chain. (Fails today; the fix makes temporal a sink.)
	for name, id := range map[string]int64{"Hub": hub, "Ref1": ref1, "Ref2": ref2, "Ref3": ref3} {
		if got[id] {
			t.Errorf("%s reached only through a temporal hop must NOT be in the radius; got %v", name, got)
		}
	}
}

// TestComputeStructuralWinsOverTemporal is the two-sided control for the
// sink rule: a node reached this hop by BOTH a temporal and a structural
// edge must still expand (its own callers are pulled), and must not be
// mislabeled as a temporal arrival. The temporal edge is written first so
// the test also guards order-independence — the per-node decision must not
// depend on which edge the hop query returns first.
//
//	Seed ◀{temporal,calls}─ Mid ◀calls─ Downstream
func TestComputeStructuralWinsOverTemporal(t *testing.T) {
	db, adapter := setupGraph(t)
	ctx := context.Background()
	fid := fileIDOf(t, adapter, "a.rb")

	seed := addBlastSymbol(t, adapter, fid, "Seed2#change", model.KindMethod)
	mid := addBlastSymbol(t, adapter, fid, "Mid2#m", model.KindMethod)
	downstream := addBlastSymbol(t, adapter, fid, "Downstream2#d", model.KindMethod)

	// Temporal edge written first; the structural one must still win.
	addBlastEdge(t, adapter, fid, mid, seed, model.EdgeTemporal, 0.9)
	addBlastEdge(t, adapter, fid, mid, seed, model.EdgeCalls, 0.9)
	addBlastEdge(t, adapter, fid, downstream, mid, model.EdgeCalls, 0.9)

	res, err := blast.Compute(ctx, db, []int64{seed}, blast.Options{MaxHops: 3, MinConfidence: 0.3})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	got := affectedIDs(res)
	if !got[mid] {
		t.Errorf("Mid should be in the radius; got %v", got)
	}
	// The whole point: Mid expanded (structural route present), so Downstream
	// is reached. A blanket temporal-mute would have stopped at Mid.
	if !got[downstream] {
		t.Errorf("Downstream (caller of Mid) should be reached because Mid has a structural route; got %v", got)
	}
	// Mid must be recorded as a structural caller, not a temporal one.
	if res.DirectTemporalIDs[mid] {
		t.Errorf("Mid was reached structurally and must not be flagged temporal")
	}
}

// TestComputeTemporalSinkCrossHopIsAccepted pins the one deliberate trade-off
// of the sink rule: a node first discovered via temporal at a near hop stays a
// sink even if it is ALSO structurally reachable at a later hop, because the
// visited-set guard keeps the first (temporal) discovery. Topology:
//
//	Seed ◀temporal─ X ◀calls─ Y
//	Seed ◀calls──── A ◀calls─ X   (X is structurally reachable via A at hop 2)
//
// X is reached temporally at hop 1 (sink) before its structural route through
// A at hop 2, so X never expands and Y is not reported. In the bug this is
// exactly what we want; in this rare legit shape it is a small, accepted
// recall trim. This test documents it so a future change is a conscious one.
func TestComputeTemporalSinkCrossHopIsAccepted(t *testing.T) {
	db, adapter := setupGraph(t)
	ctx := context.Background()
	fid := fileIDOf(t, adapter, "a.rb")

	seed := addBlastSymbol(t, adapter, fid, "Seed3#change", model.KindMethod)
	a := addBlastSymbol(t, adapter, fid, "A3#a", model.KindMethod)
	x := addBlastSymbol(t, adapter, fid, "X3#x", model.KindMethod)
	y := addBlastSymbol(t, adapter, fid, "Y3#y", model.KindMethod)

	addBlastEdge(t, adapter, fid, x, seed, model.EdgeTemporal, 0.9) // X co-changes with Seed (hop 1)
	addBlastEdge(t, adapter, fid, a, seed, model.EdgeCalls, 0.9)    // A calls Seed (hop 1)
	addBlastEdge(t, adapter, fid, x, a, model.EdgeCalls, 0.9)       // X calls A (would reach X at hop 2)
	addBlastEdge(t, adapter, fid, y, x, model.EdgeCalls, 0.9)       // Y calls X

	res, err := blast.Compute(ctx, db, []int64{seed}, blast.Options{MaxHops: 3, MinConfidence: 0.3})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	got := affectedIDs(res)
	if !got[x] || !got[a] {
		t.Errorf("X and A should be in the radius; got %v", got)
	}
	if !res.DirectTemporalIDs[x] {
		t.Errorf("X was first reached via temporal at hop 1 and should be flagged temporal")
	}
	// Accepted trade-off: X stays a sink, so Y (its caller) is not reported.
	if got[y] {
		t.Errorf("Y must NOT be reported: X's temporal hop-1 discovery makes it a sink; got %v", got)
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
	db       *sql.DB
	adapter  *sqlite.Adapter
	fileID   int64
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
	return f.addSymbolWith(t, name, model.KindClass, nil)
}

func (f *fixtureDB) addSymbolWith(t *testing.T, name string, kind model.SymbolKind, parentID *int64) int64 {
	t.Helper()
	line := f.nextLine
	f.nextLine += 10
	id, err := f.adapter.WriteSymbol(context.Background(), &model.Symbol{
		FileID: f.fileID, Name: name, Qualified: name,
		Kind: kind, ParentID: parentID, LineStart: line, LineEnd: line + 5,
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

	res, err := blast.Compute(context.Background(), fix.db, []int64{base}, blast.Options{MaxHops: 1, MinConfidence: 0.1})
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

	if res.SymbolTiers[caller] != blast.TierBreaks {
		t.Errorf("SymbolTiers[caller] = %d, want TierBreaks (%d)", res.SymbolTiers[caller], blast.TierBreaks)
	}
	if res.SymbolTiers[comp] != blast.TierReferences {
		t.Errorf("SymbolTiers[comp] = %d, want TierReferences (%d)", res.SymbolTiers[comp], blast.TierReferences)
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
		t.Errorf("TotalAffected = %d, want 4 (B + C survive, D pruned)", res.TotalAffected)
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

	res, err := blast.Compute(context.Background(), fix.db, []int64{base}, blast.Options{MaxHops: 1, MinConfidence: 0.1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if len(res.DirectCallers) != 1 {
		t.Errorf("DirectCallers = %d, want 1 (Child appears once)", len(res.DirectCallers))
	}
	if len(res.DirectCallers) == 0 {
		t.Fatal("no direct callers to inspect")
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

// TestReverseCompositionSurfacesMaskedComposer covers the visitedKind-masking
// fix: a dependent that both calls and composes the subject is recorded under
// the higher-confidence calls edge, so the old visitedKind bucketing never
// listed it as a composer. The edge-table-derived set must still surface it.
func TestReverseCompositionSurfacesMaskedComposer(t *testing.T) {
	fix := newFixtureDB(t)
	base := fix.addSymbol(t, "Base")
	dep := fix.addSymbol(t, "Dependent")
	fix.addEdge(t, dep, base, model.EdgeCalls, 1.0)
	fix.addEdge(t, dep, base, model.EdgeComposes, 0.5)

	res, err := blast.Compute(context.Background(), fix.db, []int64{base}, blast.Options{MaxHops: 1, MinConfidence: 0.1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	found := false
	for _, s := range res.AffectedViaComposition {
		if s.ID == dep {
			found = true
		}
	}
	if !found {
		t.Errorf("AffectedViaComposition = %+v, want Dependent (a composer masked by a calls edge must still surface)", res.AffectedViaComposition)
	}
}

// TestReverseCompositionSurvivesResultCap covers the capResults-eviction fix:
// strong 1.0 callers fill the result cap and evict the low-confidence composer
// from the caller lists, but it must remain in the reverse-composition set —
// the whole point on a high-fan-out hub like a Django model.
func TestReverseCompositionSurvivesResultCap(t *testing.T) {
	fix := newFixtureDB(t)
	base := fix.addSymbol(t, "Base")
	c1 := fix.addSymbol(t, "Caller1")
	c2 := fix.addSymbol(t, "Caller2")
	fix.addEdge(t, c1, base, model.EdgeCalls, 1.0)
	fix.addEdge(t, c2, base, model.EdgeCalls, 1.0)
	comp := fix.addSymbol(t, "Composer")
	fix.addEdge(t, comp, base, model.EdgeComposes, 0.5)

	res, err := blast.Compute(context.Background(), fix.db, []int64{base}, blast.Options{MaxHops: 1, MinConfidence: 0.1, MaxResults: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	inCallers := false
	for _, c := range res.DirectCallers {
		if c.ID == comp {
			inCallers = true
		}
	}
	if inCallers {
		t.Fatal("setup invalid: the composer was meant to be evicted by the result cap")
	}
	found := false
	for _, s := range res.AffectedViaComposition {
		if s.ID == comp {
			found = true
		}
	}
	if !found {
		t.Errorf("AffectedViaComposition = %+v, want Composer (a cap-evicted composer must still surface)", res.AffectedViaComposition)
	}
}

// TestReverseCompositionRespectsCap covers the maxResults trim on the
// reverse-composition set itself.
func TestReverseCompositionRespectsCap(t *testing.T) {
	fix := newFixtureDB(t)
	base := fix.addSymbol(t, "Base")
	c1 := fix.addSymbol(t, "Composer1")
	c2 := fix.addSymbol(t, "Composer2")
	fix.addEdge(t, c1, base, model.EdgeComposes, 0.9)
	fix.addEdge(t, c2, base, model.EdgeComposes, 0.9)

	res, err := blast.Compute(context.Background(), fix.db, []int64{base}, blast.Options{MaxHops: 1, MinConfidence: 0.1, MaxResults: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(res.AffectedViaComposition) != 1 {
		t.Errorf("AffectedViaComposition = %d, want 1 (capped to MaxResults)", len(res.AffectedViaComposition))
	}
	if !res.Truncated {
		t.Error("Truncated = false, want true (the reverse-composition set was capped — silent truncation breaks the audit-completeness promise)")
	}
}

// TestReverseCompositionReconcilesTotalAffected covers the total_affected
// reconciliation: a composer reachable only by a composes edge the BFS prunes by
// confidence still surfaces in the composition set AND is counted in the total,
// so the response never under-reports what it returns.
func TestReverseCompositionReconcilesTotalAffected(t *testing.T) {
	fix := newFixtureDB(t)
	base := fix.addSymbol(t, "Base")
	comp := fix.addSymbol(t, "Composer")
	fix.addEdge(t, comp, base, model.EdgeComposes, 0.5) // decays below the floor

	res, err := blast.Compute(context.Background(), fix.db, []int64{base}, blast.Options{MaxHops: 1, MinConfidence: 0.9})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	found := false
	for _, s := range res.AffectedViaComposition {
		if s.ID == comp {
			found = true
		}
	}
	if !found {
		t.Fatalf("AffectedViaComposition = %+v, want the BFS-pruned composer", res.AffectedViaComposition)
	}
	if res.TotalAffected < 1 {
		t.Errorf("TotalAffected = %d, want >= 1 (a composer surfaced in the response must be counted)", res.TotalAffected)
	}
}

// TestReverseCompositionDedupsAcrossSeeds locks the set-dedup behavior: one
// composer that holds an FK to two different seeds appears exactly once.
func TestReverseCompositionDedupsAcrossSeeds(t *testing.T) {
	fix := newFixtureDB(t)
	base1 := fix.addSymbol(t, "Base1")
	base2 := fix.addSymbol(t, "Base2")
	comp := fix.addSymbol(t, "Composer")
	fix.addEdge(t, comp, base1, model.EdgeComposes, 0.9)
	fix.addEdge(t, comp, base2, model.EdgeComposes, 0.9)

	res, err := blast.Compute(context.Background(), fix.db, []int64{base1, base2}, blast.Options{MaxHops: 1, MinConfidence: 0.1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	seen := 0
	for _, s := range res.AffectedViaComposition {
		if s.ID == comp {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("Composer appears %d times in AffectedViaComposition, want exactly 1", seen)
	}
}

// TestReverseCompositionExcludesSelfSeed covers the seed-skip filter: a seed that
// composes itself must not list itself as its own dependent.
func TestReverseCompositionExcludesSelfSeed(t *testing.T) {
	fix := newFixtureDB(t)
	base := fix.addSymbol(t, "Base")
	dep := fix.addSymbol(t, "Dependent")
	fix.addEdge(t, base, base, model.EdgeComposes, 0.9) // self-loop seed
	fix.addEdge(t, dep, base, model.EdgeComposes, 0.9)

	res, err := blast.Compute(context.Background(), fix.db, []int64{base}, blast.Options{MaxHops: 1, MinConfidence: 0.1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	for _, s := range res.AffectedViaComposition {
		if s.ID == base {
			t.Errorf("AffectedViaComposition contains the seed itself (%d); a self-compose must be skipped", base)
		}
	}
}

// TestReverseCompositionExcludesOwnMember covers the childSet-skip filter: a
// member of the subject is not one of the subject's external dependents.
func TestReverseCompositionExcludesOwnMember(t *testing.T) {
	fix := newFixtureDB(t)
	base := fix.addSymbol(t, "Base")
	member := fix.addSymbolWith(t, "Base#field", model.KindMethod, &base)
	fix.addEdge(t, member, base, model.EdgeComposes, 0.9)

	res, err := blast.Compute(context.Background(), fix.db, []int64{base}, blast.Options{MaxHops: 1, MinConfidence: 0.1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	for _, s := range res.AffectedViaComposition {
		if s.ID == member {
			t.Errorf("AffectedViaComposition contains the subject's own member (%d); members must be skipped", member)
		}
	}
}

// --- Pitch 15-02 fixture tests ---

func TestTierClassification(t *testing.T) {
	fix := newFixtureDB(t)
	subject := fix.addSymbol(t, "Target")

	kinds := []struct {
		edgeKind model.EdgeKind
		wantTier blast.Tier
		name     string
	}{
		{model.EdgeCalls, blast.TierBreaks, "calls"},
		{model.EdgeTemporal, blast.TierBreaks, "temporal"},
		{model.EdgeTests, blast.TierTests, "tests"},
		{model.EdgeComposes, blast.TierReferences, "composes"},
		{model.EdgeInherits, blast.TierReferences, "inherits"},
		{model.EdgeIncludes, blast.TierReferences, "includes"},
	}

	for _, tc := range kinds {
		t.Run(tc.name, func(t *testing.T) {
			caller := fix.addSymbol(t, "Caller_"+tc.name)
			fix.addEdge(t, caller, subject, tc.edgeKind, 1.0)

			res, err := blast.Compute(context.Background(), fix.db, []int64{subject}, blast.Options{
				MaxHops:       1,
				MinConfidence: 0.1,
			})
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}

			if res.SymbolTiers[caller] != tc.wantTier {
				t.Errorf("%s edge → tier %d, want %d", tc.name, res.SymbolTiers[caller], tc.wantTier)
			}
		})
	}
}

func TestTierMemberClassification(t *testing.T) {
	fix := newFixtureDB(t)
	typeID := fix.addSymbolWith(t, "Widget", model.KindType, nil)
	childID := fix.addSymbolWith(t, "Widget.Run", model.KindMethod, &typeID)
	callerID := fix.addSymbolWith(t, "App", model.KindFunction, nil)

	// Caller calls the child method.
	fix.addEdge(t, callerID, childID, model.EdgeCalls, 1.0)

	res, err := blast.Compute(context.Background(), fix.db, []int64{typeID}, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if res.SymbolTiers[callerID] != blast.TierBreaks {
		t.Errorf("caller tier = %d, want TierBreaks (%d); member seed should propagate TierBreaks",
			res.SymbolTiers[callerID], blast.TierBreaks)
	}
}

func TestTypeMemberSeedInclusion(t *testing.T) {
	fix := newFixtureDB(t)

	contextID := fix.addSymbolWith(t, "gin.Context", model.KindClass, nil)
	paramID := fix.addSymbolWith(t, "gin.Context.Param", model.KindMethod, &contextID)
	externalID := fix.addSymbolWith(t, "app.ExternalHandler", model.KindFunction, nil)

	fix.addEdge(t, paramID, contextID, model.EdgeCalls, 1.0)
	fix.addEdge(t, externalID, contextID, model.EdgeCalls, 1.0)

	res, err := blast.Compute(context.Background(), fix.db, []int64{contextID}, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	// Param is a child of Context — it seeds the BFS frontier but is NOT
	// part of the blast radius (only external dependents are).
	names := map[string]bool{}
	for _, c := range res.DirectCallers {
		names[c.Qualified] = true
	}
	if names["gin.Context.Param"] {
		t.Errorf("child method gin.Context.Param should NOT appear in DirectCallers: %+v", res.DirectCallers)
	}
	if !names["app.ExternalHandler"] {
		t.Errorf("external caller app.ExternalHandler missing from DirectCallers: %+v", res.DirectCallers)
	}

	if res.TotalAffected != 1 {
		t.Errorf("TotalAffected = %d, want 1 (external caller only)", res.TotalAffected)
	}
}

func TestTypeMemberBFSPropagation(t *testing.T) {
	fix := newFixtureDB(t)

	contextID := fix.addSymbolWith(t, "gin.Context", model.KindClass, nil)
	paramID := fix.addSymbolWith(t, "gin.Context.Param", model.KindMethod, &contextID)
	handlerID := fix.addSymbolWith(t, "app.HandlerFunc", model.KindFunction, nil)

	// HandlerFunc calls Param (not Context directly).
	fix.addEdge(t, handlerID, paramID, model.EdgeCalls, 1.0)

	res, err := blast.Compute(context.Background(), fix.db, []int64{contextID}, blast.Options{MaxHops: 3})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	names := map[string]bool{}
	for _, c := range res.DirectCallers {
		names[c.Qualified] = true
	}
	// Param is a child of Context — seeds the BFS but is NOT part of output.
	if names["gin.Context.Param"] {
		t.Errorf("child method gin.Context.Param should be excluded from output: %+v", res.DirectCallers)
	}
	// HandlerFunc calls Param → appears as direct caller (Param is a seed, so
	// HandlerFunc is discovered at hop 1).
	if !names["app.HandlerFunc"] {
		t.Errorf("HandlerFunc should appear via Param seed, got: %+v", res.DirectCallers)
	}

	if res.TotalAffected != 1 {
		t.Errorf("TotalAffected = %d, want 1 (caller of child only)", res.TotalAffected)
	}
}

// TestBlastFollowsCompositionTwoHops verifies that a User --composes--> Post
// --calls--> Validator chain is fully traversed: Post at hop 1 and Validator
// at hop 2 should both appear in blast results.
func TestBlastFollowsCompositionTwoHops(t *testing.T) {
	fix := newFixtureDB(t)
	user := fix.addSymbol(t, "User")
	post := fix.addSymbol(t, "Post")
	validator := fix.addSymbol(t, "Validator")

	// Post composes User (reverse: blast from User sees Post)
	fix.addEdge(t, post, user, model.EdgeComposes, 1.0)
	// Validator calls Post (reverse: blast from Post sees Validator)
	fix.addEdge(t, validator, post, model.EdgeCalls, 1.0)

	res, err := blast.Compute(context.Background(), fix.db, []int64{user}, blast.Options{
		MaxHops: 3,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	foundPost := false
	for _, c := range res.DirectCallers {
		if c.ID == post {
			foundPost = true
		}
	}
	if !foundPost {
		t.Errorf("Post should appear at hop 1 (composes), got DirectCallers: %+v", res.DirectCallers)
	}

	foundValidator := false
	for _, hop := range res.IndirectCallers {
		if hop.Symbol.ID == validator {
			foundValidator = true
		}
	}
	if !foundValidator {
		t.Errorf("Validator should appear at hop 2 (calls via Post), got IndirectCallers: %+v", res.IndirectCallers)
	}
}

func TestBlastCapsFrontierWidth(t *testing.T) {
	fix := newFixtureDB(t)
	hub := fix.addSymbol(t, "Hub")

	callerCount := blast.MaxFrontierWidth + 100
	for i := 0; i < callerCount; i++ {
		caller := fix.addSymbol(t, fmt.Sprintf("Caller%d", i))
		fix.addEdge(t, caller, hub, model.EdgeCalls, 1.0)
	}

	res, err := blast.Compute(context.Background(), fix.db, []int64{hub}, blast.Options{
		MaxHops:    2,
		MaxResults: callerCount + 10,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if !res.Truncated {
		t.Error("expected Truncated=true when frontier exceeds MaxFrontierWidth")
	}
	if res.TotalAffected > blast.MaxFrontierWidth {
		t.Errorf("TotalAffected = %d, want <= %d", res.TotalAffected, blast.MaxFrontierWidth)
	}
}

// TestBlastCapResultsDeterministic verifies that when a high-fan-out symbol's
// callers exceed MaxResults and share a confidence (the common case — call
// edges are 1.0), the kept caller set is identical across repeated computations.
// A confidence-only unstable sort lands the cap cutoff among ties and keeps an
// arbitrary subset that varies run to run, so repeated blasts of the same symbol
// return different callers and "audit every dependent" becomes unreproducible.
func TestBlastCapResultsDeterministic(t *testing.T) {
	fix := newFixtureDB(t)
	hub := fix.addSymbol(t, "Hub")

	const maxResults = 50
	callerCount := maxResults + 40 // exceed the cap with equal-confidence ties
	for i := 0; i < callerCount; i++ {
		caller := fix.addSymbol(t, fmt.Sprintf("Caller%d", i))
		fix.addEdge(t, caller, hub, model.EdgeCalls, 1.0)
	}

	keptSet := func(run int) map[int64]struct{} {
		res, err := blast.Compute(context.Background(), fix.db, []int64{hub}, blast.Options{
			MaxHops:    2,
			MaxResults: maxResults,
		})
		if err != nil {
			t.Fatalf("Compute run %d: %v", run, err)
		}
		set := make(map[int64]struct{}, len(res.DirectCallers))
		for _, c := range res.DirectCallers {
			set[c.ID] = struct{}{}
		}
		return set
	}

	first := keptSet(0)
	if len(first) == 0 {
		t.Fatal("expected some direct callers kept under the cap")
	}
	if len(first) >= callerCount {
		t.Fatalf("cap did not engage: kept %d of %d callers", len(first), callerCount)
	}
	for run := 1; run < 5; run++ {
		got := keptSet(run)
		if len(got) != len(first) {
			t.Fatalf("run %d kept %d callers, run 0 kept %d (nondeterministic cap)", run, len(got), len(first))
		}
		for id := range first {
			if _, ok := got[id]; !ok {
				t.Errorf("run %d dropped caller %d that run 0 kept (nondeterministic cap)", run, id)
			}
		}
	}
}

// TestBlastPrunesCompositionThreeHops verifies that three consecutive
// composition hops get pruned: cumulative confidence 0.5^3 = 0.125 < StructuralMinConf (0.2).
func TestBlastPrunesCompositionThreeHops(t *testing.T) {
	fix := newFixtureDB(t)
	a := fix.addSymbol(t, "A")
	b := fix.addSymbol(t, "B")
	c := fix.addSymbol(t, "C")
	d := fix.addSymbol(t, "D")

	fix.addEdge(t, b, a, model.EdgeComposes, 1.0)
	fix.addEdge(t, c, b, model.EdgeComposes, 1.0)
	fix.addEdge(t, d, c, model.EdgeComposes, 1.0)

	res, err := blast.Compute(context.Background(), fix.db, []int64{a}, blast.Options{
		MaxHops: 4,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	// B at hop 1: 1.0 * 1.0 * 0.5 = 0.5 >= 0.2 ✓
	// C at hop 2: 0.5 * 1.0 * 0.5 = 0.25 >= 0.2 ✓
	// D at hop 3: 0.25 * 1.0 * 0.5 = 0.125 < 0.2 ✗ (pruned)
	foundD := false
	for _, c := range res.DirectCallers {
		if c.ID == d {
			foundD = true
		}
	}
	for _, hop := range res.IndirectCallers {
		if hop.Symbol.ID == d {
			foundD = true
		}
	}
	if foundD {
		t.Errorf("D at hop 3 should be pruned (0.5^3=0.125 < StructuralMinConf 0.2)")
	}

	if res.TotalAffected != 2 {
		t.Errorf("TotalAffected = %d, want 2 (B + C survive, D pruned)", res.TotalAffected)
	}
}

func TestModuleDoesNotExpandChildren(t *testing.T) {
	fix := newFixtureDB(t)

	modID := fix.addSymbolWith(t, "Helpers", model.KindModule, nil)
	methodID := fix.addSymbolWith(t, "Helpers.format", model.KindMethod, &modID)
	callerID := fix.addSymbolWith(t, "Controller", model.KindClass, nil)

	fix.addEdge(t, callerID, modID, model.EdgeCalls, 1.0)
	fix.addEdge(t, methodID, modID, model.EdgeCalls, 1.0)

	res, err := blast.Compute(context.Background(), fix.db, []int64{modID}, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	for _, c := range res.DirectCallers {
		if c.ID == methodID {
			t.Error("module's own method should be excluded via isSelfMethod, not expanded as child")
		}
	}
	if res.TotalAffected != 1 {
		t.Errorf("TotalAffected = %d, want 1 (only Controller; method excluded)", res.TotalAffected)
	}
}

func TestInterfaceDoesNotExpandChildren(t *testing.T) {
	fix := newFixtureDB(t)

	ifaceID := fix.addSymbolWith(t, "Reader", model.KindInterface, nil)
	sigID := fix.addSymbolWith(t, "Reader.Read", model.KindMethod, &ifaceID)
	implID := fix.addSymbolWith(t, "FileReader", model.KindClass, nil)

	fix.addEdge(t, implID, ifaceID, model.EdgeInherits, 1.0)
	fix.addEdge(t, sigID, ifaceID, model.EdgeCalls, 1.0)

	res, err := blast.Compute(context.Background(), fix.db, []int64{ifaceID}, blast.Options{MaxHops: 1, MinConfidence: 0.1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	for _, c := range res.DirectCallers {
		if c.ID == sigID {
			t.Error("interface method signature should be excluded via isSelfMethod")
		}
	}
	if res.TotalAffected != 1 {
		t.Errorf("TotalAffected = %d, want 1 (only FileReader; signature excluded)", res.TotalAffected)
	}
}

func TestTypeMemberTierClassification(t *testing.T) {
	fix := newFixtureDB(t)

	typeID := fix.addSymbolWith(t, "Widget", model.KindType, nil)
	childID := fix.addSymbolWith(t, "Widget.Run", model.KindMethod, &typeID)
	callerID := fix.addSymbolWith(t, "App", model.KindFunction, nil)

	// Caller calls the child method.
	fix.addEdge(t, callerID, childID, model.EdgeCalls, 1.0)

	res, err := blast.Compute(context.Background(), fix.db, []int64{typeID}, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	// Child seeds BFS but is NOT in output; caller is discovered via BFS.
	if len(res.DirectCallers) != 1 || res.DirectCallers[0].ID != callerID {
		t.Fatalf("DirectCallers = %+v, want [App]", res.DirectCallers)
	}
	if res.SymbolTiers[callerID] != blast.TierBreaks {
		t.Errorf("caller tier = %d, want TierBreaks (%d); calls edge should classify as tier 1",
			res.SymbolTiers[callerID], blast.TierBreaks)
	}
	// Child should not have a tier since it's excluded from output.
	if _, ok := res.SymbolTiers[childID]; ok {
		t.Errorf("child should not have a tier; got %d", res.SymbolTiers[childID])
	}
}

func TestTypeKindExpandsChildren(t *testing.T) {
	fix := newFixtureDB(t)

	typeID := fix.addSymbolWith(t, "Config", model.KindType, nil)
	m1 := fix.addSymbolWith(t, "Config.Get", model.KindMethod, &typeID)
	m2 := fix.addSymbolWith(t, "Config.Set", model.KindMethod, &typeID)

	res, err := blast.Compute(context.Background(), fix.db, []int64{typeID}, blast.Options{MaxHops: 1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	// Children seed the BFS but are NOT part of the blast radius output.
	ids := map[int64]bool{}
	for _, c := range res.DirectCallers {
		ids[c.ID] = true
	}
	if ids[m1] || ids[m2] {
		t.Errorf("children should NOT appear in blast output: got %+v", res.DirectCallers)
	}
	if res.TotalAffected != 0 {
		t.Errorf("TotalAffected = %d, want 0 (no external callers)", res.TotalAffected)
	}
}

func TestComputeExpandFrontierErrors(t *testing.T) {
	t.Run("db-closed", func(t *testing.T) {
		fix := newFixtureDB(t)
		subject := fix.addSymbol(t, "Subject")
		fix.addEdge(t, fix.addSymbol(t, "Caller"), subject, model.EdgeCalls, 1.0)

		db := fix.db
		_ = db.Close()

		_, err := blast.Compute(context.Background(), db, []int64{subject}, blast.Options{MaxHops: 1})
		if err == nil {
			t.Error("expected error when DB is closed, got nil")
		}
	})

	t.Run("context-cancelled", func(t *testing.T) {
		fix := newFixtureDB(t)
		subject := fix.addSymbol(t, "Subject")
		fix.addEdge(t, fix.addSymbol(t, "Caller"), subject, model.EdgeCalls, 1.0)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := blast.Compute(ctx, fix.db, []int64{subject}, blast.Options{MaxHops: 1})
		if err == nil {
			t.Error("expected error when context is cancelled, got nil")
		}
	})
}
