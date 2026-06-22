package mcpserver

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func openTestAdapter(t *testing.T) (*sqlite.Adapter, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	adapter, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return adapter, db
}

func seedInterfaceFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`INSERT INTO sense_files (id, path, language, hash, symbols, indexed_at) VALUES (1, 'iface.go', 'go', 'abc', 0, '2026-01-01T00:00:00Z')`,
		`INSERT INTO sense_files (id, path, language, hash, symbols, indexed_at) VALUES (2, 'caller.go', 'go', 'def', 0, '2026-01-01T00:00:00Z')`,

		// Interface I with method M
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end) VALUES (10, 1, 'I', 'pkg.I', 'interface', 1, 10)`,
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end, parent_id) VALUES (11, 1, 'M', 'pkg.I.M', 'method', 2, 5, 10)`,

		// Struct S implementing I, with method M
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end) VALUES (20, 1, 'S', 'pkg.S', 'class', 20, 40)`,
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end, parent_id) VALUES (21, 1, 'M', 'pkg.S.M', 'method', 22, 30, 20)`,

		// S inherits I
		`INSERT INTO sense_edges (source_id, target_id, kind, file_id, confidence) VALUES (20, 10, 'inherits', 1, 1.0)`,

		// F calls S.M (caller of the struct method)
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end) VALUES (30, 2, 'F', 'pkg.F', 'function', 1, 5)`,
		`INSERT INTO sense_edges (source_id, target_id, kind, file_id, confidence) VALUES (30, 21, 'calls', 2, 1.0)`,

		// G calls I.M (caller through the interface)
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end) VALUES (40, 2, 'G', 'pkg.G', 'function', 10, 15)`,
		`INSERT INTO sense_edges (source_id, target_id, kind, file_id, confidence) VALUES (40, 11, 'calls', 2, 1.0)`,
	}
	for _, q := range stmts {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("seed: %v\nquery: %s", err, q)
		}
	}
}

func makeHandlers(adapter *sqlite.Adapter, db *sql.DB) *handlers {
	return &handlers{adapter: adapter, db: db}
}

func fileLookup(files map[int64]string) mcpio.FileLookup {
	return func(id int64) (string, bool) {
		p, ok := files[id]
		return p, ok
	}
}

func TestGraphResolvesInterfaceCallers(t *testing.T) {
	adapter, db := openTestAdapter(t)
	seedInterfaceFixture(t, db)
	ctx := context.Background()
	h := makeHandlers(adapter, db)

	ifaceParent := int64(10)
	root := &model.SymbolContext{
		Symbol: model.Symbol{ID: 11, Name: "M", Qualified: "pkg.I.M", Kind: model.KindMethod, ParentID: &ifaceParent},
	}
	resp := &mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{Name: "M", Qualified: "pkg.I.M", Kind: "method"},
		Edges: mcpio.GraphEdges{
			CalledBy: []mcpio.CallEdgeRef{{Symbol: "pkg.G", File: strPtr("caller.go"), Confidence: 1.0}},
		},
	}
	lookup := fileLookup(map[int64]string{1: "iface.go", 2: "caller.go"})

	inferred := h.resolveDispatchCallers(ctx, root, resp, lookup)
	if len(inferred) != 1 {
		t.Fatalf("want 1 inferred caller, got %d: %+v", len(inferred), inferred)
	}
	if inferred[0].Symbol != "pkg.F" {
		t.Errorf("inferred[0].Symbol = %q, want pkg.F", inferred[0].Symbol)
	}
	if inferred[0].Via != "pkg.S.M" {
		t.Errorf("inferred[0].Via = %q, want pkg.S.M", inferred[0].Via)
	}
}

func TestGraphResolvesReverseInterfaceCallers(t *testing.T) {
	adapter, db := openTestAdapter(t)
	seedInterfaceFixture(t, db)
	ctx := context.Background()
	h := makeHandlers(adapter, db)

	structParent := int64(20)
	root := &model.SymbolContext{
		Symbol: model.Symbol{ID: 21, Name: "M", Qualified: "pkg.S.M", Kind: model.KindMethod, ParentID: &structParent},
	}
	resp := &mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{Name: "M", Qualified: "pkg.S.M", Kind: "method"},
		Edges: mcpio.GraphEdges{
			CalledBy: []mcpio.CallEdgeRef{{Symbol: "pkg.F", File: strPtr("caller.go"), Confidence: 1.0}},
		},
	}
	lookup := fileLookup(map[int64]string{1: "iface.go", 2: "caller.go"})

	inferred := h.resolveDispatchCallers(ctx, root, resp, lookup)
	if len(inferred) != 1 {
		t.Fatalf("want 1 inferred caller, got %d: %+v", len(inferred), inferred)
	}
	if inferred[0].Symbol != "pkg.G" {
		t.Errorf("inferred[0].Symbol = %q, want pkg.G", inferred[0].Symbol)
	}
	if inferred[0].Via != "pkg.I.M" {
		t.Errorf("inferred[0].Via = %q, want pkg.I.M", inferred[0].Via)
	}
}

func TestGraphNoInterfaceResolutionAtThreshold(t *testing.T) {
	adapter, db := openTestAdapter(t)
	seedInterfaceFixture(t, db)
	ctx := context.Background()
	h := makeHandlers(adapter, db)

	ifaceParent := int64(10)
	root := &model.SymbolContext{
		Symbol: model.Symbol{ID: 11, Name: "M", Qualified: "pkg.I.M", Kind: model.KindMethod, ParentID: &ifaceParent},
	}
	callers := make([]mcpio.CallEdgeRef, InterfaceResolutionThreshold)
	for i := range callers {
		callers[i] = mcpio.CallEdgeRef{Symbol: "caller" + string(rune('A'+i))}
	}
	resp := &mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{Name: "M", Qualified: "pkg.I.M", Kind: "method"},
		Edges:  mcpio.GraphEdges{CalledBy: callers},
	}
	lookup := fileLookup(nil)

	inferred := h.resolveDispatchCallers(ctx, root, resp, lookup)
	if len(inferred) != 0 {
		t.Errorf("want 0 inferred at threshold (%d callers), got %d", InterfaceResolutionThreshold, len(inferred))
	}
}

func TestDispatchCallersNotMethod(t *testing.T) {
	adapter, db := openTestAdapter(t)
	seedInterfaceFixture(t, db)
	ctx := context.Background()
	h := makeHandlers(adapter, db)

	root := &model.SymbolContext{
		Symbol: model.Symbol{ID: 30, Name: "F", Qualified: "pkg.F", Kind: model.KindFunction},
	}
	resp := &mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{Name: "F", Qualified: "pkg.F", Kind: "function"},
		Edges:  mcpio.GraphEdges{CalledBy: []mcpio.CallEdgeRef{}},
	}
	lookup := fileLookup(nil)
	inferred := h.resolveDispatchCallers(ctx, root, resp, lookup)
	if len(inferred) != 0 {
		t.Error("want 0 inferred for non-method symbols")
	}
}

func TestDispatchCallersNoParent(t *testing.T) {
	adapter, db := openTestAdapter(t)
	seedInterfaceFixture(t, db)
	ctx := context.Background()
	h := makeHandlers(adapter, db)

	root := &model.SymbolContext{
		Symbol: model.Symbol{ID: 30, Name: "F", Qualified: "pkg.F", Kind: model.KindMethod, ParentID: nil},
	}
	resp := &mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{Name: "F", Qualified: "pkg.F", Kind: "method"},
		Edges:  mcpio.GraphEdges{CalledBy: []mcpio.CallEdgeRef{}},
	}
	lookup := fileLookup(nil)
	inferred := h.resolveDispatchCallers(ctx, root, resp, lookup)
	if len(inferred) != 0 {
		t.Error("want 0 inferred for method with no parent")
	}
}

func TestDispatchCallersAboveThreshold(t *testing.T) {
	adapter, db := openTestAdapter(t)
	seedInterfaceFixture(t, db)
	ctx := context.Background()
	h := makeHandlers(adapter, db)

	ifaceParent := int64(10)
	root := &model.SymbolContext{
		Symbol: model.Symbol{ID: 11, Name: "M", Qualified: "pkg.I.M", Kind: model.KindMethod, ParentID: &ifaceParent},
	}
	callers := make([]mcpio.CallEdgeRef, InterfaceResolutionThreshold+1)
	for i := range callers {
		callers[i] = mcpio.CallEdgeRef{Symbol: "c" + string(rune('A'+i))}
	}
	resp := &mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{Name: "M", Qualified: "pkg.I.M", Kind: "method"},
		Edges:  mcpio.GraphEdges{CalledBy: callers},
	}
	lookup := fileLookup(nil)
	inferred := h.resolveDispatchCallers(ctx, root, resp, lookup)
	if len(inferred) != 0 {
		t.Errorf("want 0 inferred above threshold (%d callers), got %d", len(callers), len(inferred))
	}
}

func TestGraphInterfaceResolutionBelowThreshold(t *testing.T) {
	adapter, db := openTestAdapter(t)
	seedInterfaceFixture(t, db)
	ctx := context.Background()
	h := makeHandlers(adapter, db)

	ifaceParent := int64(10)
	root := &model.SymbolContext{
		Symbol: model.Symbol{ID: 11, Name: "M", Qualified: "pkg.I.M", Kind: model.KindMethod, ParentID: &ifaceParent},
	}
	callers := make([]mcpio.CallEdgeRef, InterfaceResolutionThreshold-1)
	for i := range callers {
		callers[i] = mcpio.CallEdgeRef{Symbol: "caller" + string(rune('A'+i))}
	}
	resp := &mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{Name: "M", Qualified: "pkg.I.M", Kind: "method"},
		Edges:  mcpio.GraphEdges{CalledBy: callers},
	}
	lookup := fileLookup(map[int64]string{1: "iface.go", 2: "caller.go"})

	inferred := h.resolveDispatchCallers(ctx, root, resp, lookup)
	if len(inferred) == 0 {
		t.Error("want inferred callers below threshold, got 0")
	}
}

func TestGraphConfidenceValue(t *testing.T) {
	adapter, db := openTestAdapter(t)
	seedInterfaceFixture(t, db)
	ctx := context.Background()
	h := makeHandlers(adapter, db)

	ifaceParent := int64(10)
	root := &model.SymbolContext{
		Symbol: model.Symbol{ID: 11, Name: "M", Qualified: "pkg.I.M", Kind: model.KindMethod, ParentID: &ifaceParent},
	}
	resp := &mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{Name: "M", Qualified: "pkg.I.M", Kind: "method"},
		Edges:  mcpio.GraphEdges{CalledBy: []mcpio.CallEdgeRef{}},
	}
	lookup := fileLookup(map[int64]string{1: "iface.go", 2: "caller.go"})

	inferred := h.resolveDispatchCallers(ctx, root, resp, lookup)
	if len(inferred) == 0 {
		t.Fatal("expected inferred callers")
	}
	for i, ref := range inferred {
		if float64(ref.Confidence) != DispatchInferredConfidence {
			t.Errorf("inferred[%d].Confidence = %v, want %v", i, ref.Confidence, DispatchInferredConfidence)
		}
	}
}

// TestAppendDispatchCallers exercises the per-equivalent folding in isolation:
// non-call edges are ignored, callers already seen (direct or via an earlier
// equivalent) are deduped, a genuine call edge is appended with its via-method
// and resolved file, and the per-query cap stops further appends.
func TestAppendDispatchCallers(t *testing.T) {
	lookup := fileLookup(map[int64]string{2: "caller.go"})

	inbound := []model.EdgeRef{
		{Edge: model.Edge{Kind: model.EdgeInherits}, Target: model.Symbol{Qualified: "pkg.Skip"}},
		{Edge: model.Edge{Kind: model.EdgeCalls}, Target: model.Symbol{Qualified: "pkg.New", FileID: 2, LineStart: 7, LineEnd: 9}},
		{Edge: model.Edge{Kind: model.EdgeCalls}, Target: model.Symbol{Qualified: "pkg.Dup"}},
	}
	directCallers := map[string]struct{}{"pkg.Dup": {}}

	var ids []int64
	got := appendDispatchCallers(nil, &ids, inbound, "pkg.S.M", directCallers, lookup)
	if len(got) != 1 {
		t.Fatalf("want 1 appended (non-call and dup skipped), got %d: %+v", len(got), got)
	}
	if got[0].Symbol != "pkg.New" || got[0].Via != "pkg.S.M" {
		t.Errorf("appended ref = %+v, want Symbol=pkg.New Via=pkg.S.M", got[0])
	}
	if got[0].File == nil || *got[0].File != "caller.go" {
		t.Errorf("want file caller.go from lookup, got %v", got[0].File)
	}
	// Only the emitted (non-skipped) caller's id is collected.
	if len(ids) != 1 {
		t.Errorf("collected ids = %v, want exactly the emitted caller", ids)
	}

	// At the per-query cap, further callers are not appended.
	full := make([]mcpio.DispatchInferredRef, maxDispatchInferred)
	var overflowIDs []int64
	capped := appendDispatchCallers(full, &overflowIDs,
		[]model.EdgeRef{{Edge: model.Edge{Kind: model.EdgeCalls}, Target: model.Symbol{Qualified: "pkg.Overflow"}}},
		"pkg.S.M", map[string]struct{}{}, lookup)
	if len(capped) != maxDispatchInferred {
		t.Errorf("want cap held at %d, got %d", maxDispatchInferred, len(capped))
	}
}

func strPtr(s string) *string { return &s }
