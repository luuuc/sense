package mcpserver

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
)

// seedAlsoCalledByFixture creates:
//   - A (id=1) calls B (id=2) and C (id=3)
//   - D (id=4) also calls B
//   - E (id=5) also calls C
//
// All in file_id=1, symbolCount total = 5 (well under 5000).
func seedAlsoCalledByFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`INSERT INTO sense_files (id, path, language, hash, symbols, indexed_at) VALUES (1, 'main.go', 'go', 'aaa', 5, '2026-01-01T00:00:00Z')`,
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end) VALUES (1, 1, 'A', 'pkg.A', 'function', 1, 5)`,
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end) VALUES (2, 1, 'B', 'pkg.B', 'function', 10, 15)`,
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end) VALUES (3, 1, 'C', 'pkg.C', 'function', 20, 25)`,
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end) VALUES (4, 1, 'D', 'pkg.D', 'function', 30, 35)`,
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end) VALUES (5, 1, 'E', 'pkg.E', 'function', 40, 45)`,
		// A calls B and C
		`INSERT INTO sense_edges (source_id, target_id, kind, file_id, confidence) VALUES (1, 2, 'calls', 1, 1.0)`,
		`INSERT INTO sense_edges (source_id, target_id, kind, file_id, confidence) VALUES (1, 3, 'calls', 1, 1.0)`,
		// D also calls B
		`INSERT INTO sense_edges (source_id, target_id, kind, file_id, confidence) VALUES (4, 2, 'calls', 1, 1.0)`,
		// E also calls C
		`INSERT INTO sense_edges (source_id, target_id, kind, file_id, confidence) VALUES (5, 3, 'calls', 1, 1.0)`,
	}
	for _, q := range stmts {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("seed: %v\nquery: %s", err, q)
		}
	}
}

func TestEnrichAlsoCalledBySmallRepo(t *testing.T) {
	adapter, db := openTestAdapter(t)
	seedAlsoCalledByFixture(t, db)
	ctx := context.Background()

	gr := &model.GraphResult{
		Root: model.SymbolContext{
			Symbol: model.Symbol{ID: 1, Name: "A", Qualified: "pkg.A"},
			Outbound: []model.EdgeRef{
				{Edge: model.Edge{Kind: model.EdgeCalls}, Target: model.Symbol{ID: 2, Qualified: "pkg.B"}},
				{Edge: model.Edge{Kind: model.EdgeCalls}, Target: model.Symbol{ID: 3, Qualified: "pkg.C"}},
			},
		},
	}
	resp := &mcpio.GraphResponse{
		Edges: mcpio.GraphEdges{
			Calls: []mcpio.CallEdgeRef{
				{Symbol: "pkg.B", Confidence: 1.0},
				{Symbol: "pkg.C", Confidence: 1.0},
			},
		},
	}

	enrichAlsoCalledBy(ctx, adapter, 5, gr, resp)

	// B should have also_called_by = ["pkg.D"]
	if len(resp.Edges.Calls[0].AlsoCalledBy) != 1 || resp.Edges.Calls[0].AlsoCalledBy[0] != "pkg.D" {
		t.Errorf("B also_called_by = %v, want [pkg.D]", resp.Edges.Calls[0].AlsoCalledBy)
	}
	// C should have also_called_by = ["pkg.E"]
	if len(resp.Edges.Calls[1].AlsoCalledBy) != 1 || resp.Edges.Calls[1].AlsoCalledBy[0] != "pkg.E" {
		t.Errorf("C also_called_by = %v, want [pkg.E]", resp.Edges.Calls[1].AlsoCalledBy)
	}
}

func TestEnrichAlsoCalledByLargeRepoSuppressed(t *testing.T) {
	adapter, _ := openTestAdapter(t)
	ctx := context.Background()

	gr := &model.GraphResult{
		Root: model.SymbolContext{
			Symbol: model.Symbol{ID: 1, Name: "A", Qualified: "pkg.A"},
			Outbound: []model.EdgeRef{
				{Edge: model.Edge{Kind: model.EdgeCalls}, Target: model.Symbol{ID: 2, Qualified: "pkg.B"}},
			},
		},
	}
	resp := &mcpio.GraphResponse{
		Edges: mcpio.GraphEdges{
			Calls: []mcpio.CallEdgeRef{
				{Symbol: "pkg.B", Confidence: 1.0},
			},
		},
	}

	enrichAlsoCalledBy(ctx, adapter, 5000, gr, resp)

	if resp.Edges.Calls[0].AlsoCalledBy != nil {
		t.Errorf("large repo should suppress also_called_by, got %v", resp.Edges.Calls[0].AlsoCalledBy)
	}
}

func TestEnrichAlsoCalledByAtThresholdBoundary(t *testing.T) {
	adapter, db := openTestAdapter(t)
	seedAlsoCalledByFixture(t, db)
	ctx := context.Background()

	gr := &model.GraphResult{
		Root: model.SymbolContext{
			Symbol: model.Symbol{ID: 1, Name: "A", Qualified: "pkg.A"},
			Outbound: []model.EdgeRef{
				{Edge: model.Edge{Kind: model.EdgeCalls}, Target: model.Symbol{ID: 2, Qualified: "pkg.B"}},
			},
		},
	}
	resp := &mcpio.GraphResponse{
		Edges: mcpio.GraphEdges{
			Calls: []mcpio.CallEdgeRef{
				{Symbol: "pkg.B", Confidence: 1.0},
			},
		},
	}

	// At exactly 4999: should enrich.
	enrichAlsoCalledBy(ctx, adapter, 4999, gr, resp)
	if len(resp.Edges.Calls[0].AlsoCalledBy) == 0 {
		t.Error("at 4999 symbols, also_called_by should be populated")
	}

	// At exactly 5000: should suppress.
	resp.Edges.Calls[0].AlsoCalledBy = nil
	enrichAlsoCalledBy(ctx, adapter, 5000, gr, resp)
	if resp.Edges.Calls[0].AlsoCalledBy != nil {
		t.Errorf("at 5000 symbols, also_called_by should be suppressed, got %v", resp.Edges.Calls[0].AlsoCalledBy)
	}
}

func TestEnrichAlsoCalledByHighCallerSuppressed(t *testing.T) {
	adapter, db := openTestAdapter(t)
	seedAlsoCalledByFixture(t, db)
	ctx := context.Background()

	// Add 21 more callers of B (total = 22 including A and D), exceeding the 20-caller cap.
	for i := 100; i < 121; i++ {
		symQ := fmt.Sprintf(`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end) VALUES (%d, 1, 'X%d', 'pkg.X%d', 'function', 1, 1)`, i, i, i)
		edgeQ := fmt.Sprintf(`INSERT INTO sense_edges (source_id, target_id, kind, file_id, confidence) VALUES (%d, 2, 'calls', 1, 1.0)`, i)
		if _, err := db.ExecContext(ctx, symQ); err != nil {
			t.Fatalf("seed extra sym: %v", err)
		}
		if _, err := db.ExecContext(ctx, edgeQ); err != nil {
			t.Fatalf("seed extra edge: %v", err)
		}
	}

	gr := &model.GraphResult{
		Root: model.SymbolContext{
			Symbol: model.Symbol{ID: 1, Name: "A", Qualified: "pkg.A"},
			Outbound: []model.EdgeRef{
				{Edge: model.Edge{Kind: model.EdgeCalls}, Target: model.Symbol{ID: 2, Qualified: "pkg.B"}},
				{Edge: model.Edge{Kind: model.EdgeCalls}, Target: model.Symbol{ID: 3, Qualified: "pkg.C"}},
			},
		},
	}
	resp := &mcpio.GraphResponse{
		Edges: mcpio.GraphEdges{
			Calls: []mcpio.CallEdgeRef{
				{Symbol: "pkg.B", Confidence: 1.0},
				{Symbol: "pkg.C", Confidence: 1.0},
			},
		},
	}

	enrichAlsoCalledBy(ctx, adapter, 5, gr, resp)

	// B has > 20 callers — should be suppressed.
	if resp.Edges.Calls[0].AlsoCalledBy != nil {
		t.Errorf("high-caller callee should suppress also_called_by, got %v", resp.Edges.Calls[0].AlsoCalledBy)
	}
	// C still has only 1 other caller (E) — should still populate.
	if len(resp.Edges.Calls[1].AlsoCalledBy) != 1 {
		t.Errorf("C also_called_by = %v, want [pkg.E]", resp.Edges.Calls[1].AlsoCalledBy)
	}
}

func TestEnrichAlsoCalledByEmptyCalls(t *testing.T) {
	adapter, _ := openTestAdapter(t)
	ctx := context.Background()

	gr := &model.GraphResult{
		Root: model.SymbolContext{
			Symbol: model.Symbol{ID: 1, Name: "A", Qualified: "pkg.A"},
		},
	}
	resp := &mcpio.GraphResponse{
		Edges: mcpio.GraphEdges{},
	}

	// Should not panic on empty calls.
	enrichAlsoCalledBy(ctx, adapter, 5, gr, resp)
}

func TestEnrichAlsoCalledByUnqualifiedFallback(t *testing.T) {
	adapter, db := openTestAdapter(t)
	seedAlsoCalledByFixture(t, db)
	ctx := context.Background()

	// Use Name instead of Qualified in the outbound edge to exercise the fallback.
	gr := &model.GraphResult{
		Root: model.SymbolContext{
			Symbol: model.Symbol{ID: 1, Name: "A", Qualified: "pkg.A"},
			Outbound: []model.EdgeRef{
				{Edge: model.Edge{Kind: model.EdgeCalls}, Target: model.Symbol{ID: 2, Name: "B"}},
			},
		},
	}
	resp := &mcpio.GraphResponse{
		Edges: mcpio.GraphEdges{
			Calls: []mcpio.CallEdgeRef{
				{Symbol: "B", Confidence: 1.0},
			},
		},
	}

	enrichAlsoCalledBy(ctx, adapter, 5, gr, resp)

	if len(resp.Edges.Calls[0].AlsoCalledBy) != 1 || resp.Edges.Calls[0].AlsoCalledBy[0] != "pkg.D" {
		t.Errorf("unqualified fallback: B also_called_by = %v, want [pkg.D]", resp.Edges.Calls[0].AlsoCalledBy)
	}
}

func TestEnrichAlsoCalledByNoMatchingCallees(t *testing.T) {
	adapter, db := openTestAdapter(t)
	seedAlsoCalledByFixture(t, db)
	ctx := context.Background()

	// Outbound edges exist but resp.Edges.Calls has a symbol not in outbound.
	gr := &model.GraphResult{
		Root: model.SymbolContext{
			Symbol: model.Symbol{ID: 1, Name: "A", Qualified: "pkg.A"},
			Outbound: []model.EdgeRef{
				{Edge: model.Edge{Kind: model.EdgeCalls}, Target: model.Symbol{ID: 2, Qualified: "pkg.B"}},
			},
		},
	}
	resp := &mcpio.GraphResponse{
		Edges: mcpio.GraphEdges{
			Calls: []mcpio.CallEdgeRef{
				{Symbol: "pkg.Z", Confidence: 1.0},
			},
		},
	}

	enrichAlsoCalledBy(ctx, adapter, 5, gr, resp)

	if resp.Edges.Calls[0].AlsoCalledBy != nil {
		t.Errorf("non-matching callee should have nil also_called_by, got %v", resp.Edges.Calls[0].AlsoCalledBy)
	}
}
