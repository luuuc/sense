package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/cli"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

type resolveFixture struct {
	db      *sql.DB
	adapter *sqlite.Adapter
	h       *handlers
	symbols map[string]int64
}

func setupResolveFixture(t *testing.T) *resolveFixture {
	t.Helper()
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	now := time.Now()

	fid1, _ := adapter.WriteFile(ctx, &model.File{Path: "pkg/auth/auth.go", Language: "go", Hash: "a", Symbols: 2, IndexedAt: now})
	fid2, _ := adapter.WriteFile(ctx, &model.File{Path: "pkg/http/handler.go", Language: "go", Hash: "b", Symbols: 2, IndexedAt: now})
	fid3, _ := adapter.WriteFile(ctx, &model.File{Path: "pkg/grpc/handler.go", Language: "go", Hash: "c", Symbols: 1, IndexedAt: now})
	fid4, _ := adapter.WriteFile(ctx, &model.File{Path: "pkg/util/parse.go", Language: "go", Hash: "d", Symbols: 1, IndexedAt: now})
	fid5, _ := adapter.WriteFile(ctx, &model.File{Path: "pkg/caller.go", Language: "go", Hash: "e", Symbols: 1, IndexedAt: now})

	symIDs := map[string]int64{}

	// Unique symbol — exact qualified match
	id, _ := adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid1, Name: "Authenticate", Qualified: "auth.Authenticate", Kind: "function", LineStart: 1, LineEnd: 10})
	symIDs["auth.Authenticate"] = id

	// Two symbols with same unqualified name "Handle" — for disambiguation
	id, _ = adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid2, Name: "Handle", Qualified: "http.Handle", Kind: "function", LineStart: 1, LineEnd: 20})
	symIDs["http.Handle"] = id
	id, _ = adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid3, Name: "Handle", Qualified: "grpc.Handle", Kind: "function", LineStart: 1, LineEnd: 15})
	symIDs["grpc.Handle"] = id

	// Three symbols with same name "Parse" — one dominant (many edges), others weak
	id, _ = adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid1, Name: "Parse", Qualified: "auth.Parse", Kind: "function", LineStart: 15, LineEnd: 30})
	symIDs["auth.Parse"] = id
	id, _ = adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid4, Name: "Parse", Qualified: "util.Parse", Kind: "function", LineStart: 1, LineEnd: 10})
	symIDs["util.Parse"] = id
	id, _ = adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid2, Name: "Parse", Qualified: "http.Parse", Kind: "function", LineStart: 25, LineEnd: 35})
	symIDs["http.Parse"] = id

	// A caller to create edges
	callerID, _ := adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid5, Name: "Main", Qualified: "caller.Main", Kind: "function", LineStart: 1, LineEnd: 5})
	symIDs["caller.Main"] = callerID

	// Give auth.Parse many edges (dominant), others zero/one
	for i := 0; i < 10; i++ {
		cID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: fid5, Name: "c" + string(rune('A'+i)), Qualified: "caller.c" + string(rune('A'+i)),
			Kind: "function", LineStart: 10 + i, LineEnd: 12 + i,
		})
		_, _ = adapter.WriteEdge(ctx, &model.Edge{SourceID: model.Int64Ptr(cID), TargetID: symIDs["auth.Parse"], Kind: model.EdgeCalls, FileID: fid5, Confidence: 1.0})
	}
	// Give util.Parse 1 edge (runner-up)
	_, _ = adapter.WriteEdge(ctx, &model.Edge{SourceID: model.Int64Ptr(callerID), TargetID: symIDs["util.Parse"], Kind: model.EdgeCalls, FileID: fid5, Confidence: 1.0})

	// Give both Handle symbols similar edge counts (3 each) — no dominant
	for i := 0; i < 3; i++ {
		cID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: fid5, Name: "h" + string(rune('A'+i)), Qualified: "caller.h" + string(rune('A'+i)),
			Kind: "function", LineStart: 30 + i, LineEnd: 32 + i,
		})
		_, _ = adapter.WriteEdge(ctx, &model.Edge{SourceID: model.Int64Ptr(cID), TargetID: symIDs["http.Handle"], Kind: model.EdgeCalls, FileID: fid5, Confidence: 1.0})
	}
	for i := 0; i < 3; i++ {
		cID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: fid5, Name: "g" + string(rune('A'+i)), Qualified: "caller.g" + string(rune('A'+i)),
			Kind: "function", LineStart: 40 + i, LineEnd: 42 + i,
		})
		_, _ = adapter.WriteEdge(ctx, &model.Edge{SourceID: model.Int64Ptr(cID), TargetID: symIDs["grpc.Handle"], Kind: model.EdgeCalls, FileID: fid5, Confidence: 1.0})
	}

	h := makeHandlers(adapter, adapter.DB())

	return &resolveFixture{db: adapter.DB(), adapter: adapter, h: h, symbols: symIDs}
}

// --- resolveSymbol tests ---

func TestResolveSymbol(t *testing.T) {
	rf := setupResolveFixture(t)
	ctx := context.Background()

	tests := []struct {
		name   string
		symbol string
		wantOK bool
		check  func(t *testing.T, err error)
	}{
		{
			name:   "exact qualified match",
			symbol: "auth.Authenticate",
			wantOK: true,
		},
		{
			name:   "single name match",
			symbol: "Authenticate",
			wantOK: true,
		},
		{
			name:   "ambiguous name with dominant resolves",
			symbol: "Parse",
			wantOK: true,
			check: func(t *testing.T, err error) {
				// Should not error — auth.Parse dominates
				if err != nil {
					t.Errorf("expected dominantMatch to resolve Parse, got error")
				}
			},
		},
		{
			name:   "ambiguous name without dominant returns disambiguation",
			symbol: "Handle",
			wantOK: false,
			check: func(t *testing.T, err error) {
				re, ok := err.(*resolveError)
				if !ok {
					t.Fatalf("expected *resolveError, got %T: %v", err, err)
				}
				raw := resultText(t, re.result)
				var resp map[string]any
				if err := json.Unmarshal([]byte(raw), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp["ambiguous"] != true {
					t.Error("expected ambiguous=true")
				}
				matches, ok := resp["top_matches"].([]any)
				if !ok || len(matches) < 2 {
					t.Errorf("expected at least 2 top_matches, got %v", resp["top_matches"])
				}
			},
		},
		{
			name:   "no match returns not-found",
			symbol: "CompletelyUnknown",
			wantOK: false,
			check: func(t *testing.T, err error) {
				re, ok := err.(*resolveError)
				if !ok {
					t.Fatalf("expected *resolveError, got %T: %v", err, err)
				}
				if !re.result.IsError {
					t.Error("expected IsError for not-found")
				}
			},
		},
		{
			name:   "qualified name prioritized over unqualified",
			symbol: "http.Handle",
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match, err := rf.h.resolveSymbol(ctx, "test", tt.symbol, "")
			if tt.wantOK {
				if err != nil {
					t.Fatalf("expected success, got error: %v", err)
				}
				if match.ID == 0 {
					t.Error("expected non-zero match ID")
				}
				if tt.check != nil {
					tt.check(t, nil)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error, got match: %+v", match)
				}
				if tt.check != nil {
					tt.check(t, err)
				}
			}
		})
	}
}

// --- dominantMatch tests ---

func TestDominantMatch(t *testing.T) {
	rf := setupResolveFixture(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		symbol  string
		wantWin bool
	}{
		{
			name:    "auth.Parse dominates with 10 edges vs 1",
			symbol:  "Parse",
			wantWin: true,
		},
		{
			name:    "Handle has no dominant (3 vs 3)",
			symbol:  "Handle",
			wantWin: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches, err := lookupByNameForTest(ctx, rf.db, tt.symbol)
			if err != nil {
				t.Fatal(err)
			}
			if len(matches) < 2 {
				t.Fatalf("expected ambiguous matches for %q, got %d", tt.symbol, len(matches))
			}

			winner, ok := rf.h.dominantMatch(ctx, matches)
			if tt.wantWin {
				if !ok {
					t.Error("expected dominant match")
				}
				if winner.Qualified != "auth.Parse" {
					t.Errorf("expected auth.Parse to win, got %q", winner.Qualified)
				}
			} else if ok {
				t.Errorf("expected no dominant, got winner: %+v", winner)
			}
		})
	}
}

// --- disambiguationResult tests ---

func TestDisambiguationResult(t *testing.T) {
	rf := setupResolveFixture(t)
	ctx := context.Background()

	matches, err := lookupByNameForTest(ctx, rf.db, "Handle")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) < 2 {
		t.Fatalf("expected ambiguous matches, got %d", len(matches))
	}

	result := rf.h.disambiguationResult(ctx, "Handle", matches)
	text := resultText(t, result)

	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp["ambiguous"] != true {
		t.Error("expected ambiguous=true")
	}
	if resp["query"] != "Handle" {
		t.Errorf("query = %v, want Handle", resp["query"])
	}
	topMatches, ok := resp["top_matches"].([]any)
	if !ok {
		t.Fatal("top_matches missing or wrong type")
	}
	if len(topMatches) < 2 {
		t.Errorf("expected >= 2 top_matches, got %d", len(topMatches))
	}
	if resp["hint"] == nil || resp["hint"] == "" {
		t.Error("expected non-empty hint")
	}
}

// lookupByNameForTest wraps cli.Lookup for test use — we call it directly
// to get the raw match slice before resolveSymbol processes it.
func lookupByNameForTest(ctx context.Context, db *sql.DB, name string) ([]cli.Match, error) {
	return cli.Lookup(ctx, db, name)
}
