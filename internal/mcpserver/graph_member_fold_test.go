package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/metrics"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/profile"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

// setupMemberFoldFixture seeds the laravelio Thread shape: a class with ONE
// class-level caller and twelve method-level callers. Pre-fix, the rendered
// tool answer was called_by=1 with completeness "complete", the exact lie
// the member-caller fold exists to prevent, observed at this layer.
func setupMemberFoldFixture(t *testing.T) *handlers {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sense"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	fid, err := adapter.WriteFile(ctx, &model.File{
		Path: "app/Models/Thread.php", Language: "php", Hash: "t1", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	classID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Thread", Qualified: `App\Models\Thread`,
		Kind: model.KindClass, LineStart: 1, LineEnd: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	methodID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "feed", Qualified: `App\Models\Thread\feed`,
		Kind: model.KindMethod, ParentID: &classID, LineStart: 5, LineEnd: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	directID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "handle", Qualified: `App\Jobs\CreateThread\handle`,
		Kind: model.KindFunction, LineStart: 30, LineEnd: 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(directID), TargetID: classID, Kind: model.EdgeCalls,
		FileID: fid, Line: intPtr(31), Confidence: 1.0,
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		callerID, err := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: fid, Name: fmt.Sprintf("caller%d", i), Qualified: fmt.Sprintf(`App\Http\C%d\index`, i),
			Kind: model.KindFunction, LineStart: 50 + 5*i, LineEnd: 53 + 5*i,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adapter.WriteEdge(ctx, &model.Edge{
			SourceID: model.Int64Ptr(callerID), TargetID: methodID, Kind: model.EdgeCalls,
			FileID: fid, Line: intPtr(50 + 5*i), Confidence: 1.0,
		}); err != nil {
			t.Fatal(err)
		}
	}

	tracker := metrics.NewTracker(adapter.DB())
	t.Cleanup(func() { tracker.Close() })
	return &handlers{
		adapter:     adapter,
		db:          adapter.DB(),
		dir:         dir,
		search:      search.NewEngine(adapter, nil, nil),
		tracker:     tracker,
		defaults:    profile.DefaultParams(),
		seenSymbols: make(map[int64]bool),
	}
}

// The layer agents actually hit: the rendered sense_graph caller list for a
// class must carry the method-derived callers alongside the class-level one,
// and the completeness verdict must describe THAT list, not the pre-fold one.
func TestHandleGraphRendersFoldedMemberCallers(t *testing.T) {
	h := setupMemberFoldFixture(t)
	ctx := context.Background()

	result, err := h.handleGraph(ctx, toolReq(map[string]any{
		"symbol":    "Thread",
		"direction": "callers",
	}))
	if err != nil {
		t.Fatalf("handleGraph: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool error: %s", resultText(t, result))
	}
	var resp mcpio.GraphResponse
	if err := json.Unmarshal([]byte(resultText(t, result)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Edges.CalledBy) != 13 {
		t.Fatalf("rendered called_by = %d, want 13 (1 class-level + 12 folded)", len(resp.Edges.CalledBy))
	}
	var direct, folded bool
	for _, e := range resp.Edges.CalledBy {
		if e.Symbol == `App\Jobs\CreateThread\handle` {
			direct = true
		}
		if e.Symbol == `App\Http\C0\index` {
			folded = true
		}
	}
	if !direct {
		t.Error("class-level caller missing from the rendered list")
	}
	if !folded {
		t.Error("method-derived caller missing from the rendered list")
	}
}
