package summary

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
	_ "modernc.org/sqlite"
)

func seedTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.db")

	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}

	now := time.Now()

	fid1, err := adapter.WriteFile(ctx, &model.File{
		Path: "cmd/app/main.go", Language: "go",
		Hash: "aaa", Symbols: 3, IndexedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	fid2, err := adapter.WriteFile(ctx, &model.File{
		Path: "internal/server/handler.go", Language: "go",
		Hash: "bbb", Symbols: 2, IndexedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	fid3, err := adapter.WriteFile(ctx, &model.File{
		Path: "lib/utils.py", Language: "python",
		Hash: "ccc", Symbols: 1, IndexedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	mainID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid1, Name: "main", Qualified: "main",
		Kind: model.KindFunction, LineStart: 1, LineEnd: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	helperID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid1, Name: "helper", Qualified: "app.helper",
		Kind: model.KindFunction, LineStart: 12, LineEnd: 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	handleID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid2, Name: "Handle", Qualified: "server.Handle",
		Kind: model.KindFunction, LineStart: 1, LineEnd: 30,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid3, Name: "parse", Qualified: "utils.parse",
		Kind: model.KindFunction, LineStart: 1, LineEnd: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = adapter.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(mainID), TargetID: handleID,
		Kind: model.EdgeCalls, FileID: fid1, Confidence: 1.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(mainID), TargetID: helperID,
		Kind: model.EdgeCalls, FileID: fid1, Confidence: 1.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(helperID), TargetID: handleID,
		Kind: model.EdgeCalls, FileID: fid2, Confidence: 1.0,
	})
	if err != nil {
		t.Fatal(err)
	}

	_ = adapter.Close()

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	return db, func() { _ = db.Close() }
}

func TestRenderFingerprint(t *testing.T) {
	db, cleanup := seedTestDB(t)
	defer cleanup()

	got, err := renderFingerprint(context.Background(), db)
	if err != nil {
		t.Fatalf("renderFingerprint: %v", err)
	}

	if !strings.Contains(got, "Go project") {
		t.Errorf("expected primary language, got: %s", got)
	}
	if !strings.Contains(got, "3 files") {
		t.Errorf("expected file count, got: %s", got)
	}
	if !strings.Contains(got, "4 symbols") {
		t.Errorf("expected symbol count, got: %s", got)
	}
	if !strings.Contains(got, "3 edges") {
		t.Errorf("expected edge count, got: %s", got)
	}
	if !strings.Contains(got, "Languages:") {
		t.Errorf("expected multi-language breakdown, got: %s", got)
	}
}

func TestRenderTopNamespaces(t *testing.T) {
	db, cleanup := seedTestDB(t)
	defer cleanup()

	got, err := renderTopNamespaces(context.Background(), db)
	if err != nil {
		t.Fatalf("renderTopNamespaces: %v", err)
	}

	if !strings.Contains(got, "cmd/app") {
		t.Errorf("expected cmd/app namespace, got: %s", got)
	}
	if !strings.Contains(got, "internal/server") {
		t.Errorf("expected internal/server namespace, got: %s", got)
	}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if !strings.HasPrefix(line, "- `") {
			t.Errorf("expected bullet format, got line: %s", line)
		}
		if !strings.Contains(line, "symbols") {
			t.Errorf("expected symbol count in line: %s", line)
		}
	}
}

func TestRenderHubSymbols(t *testing.T) {
	db, cleanup := seedTestDB(t)
	defer cleanup()

	got, err := renderHubSymbols(context.Background(), db)
	if err != nil {
		t.Fatalf("renderHubSymbols: %v", err)
	}

	if !strings.Contains(got, "server.Handle") {
		t.Errorf("expected top hub symbol server.Handle, got: %s", got)
	}
	if !strings.Contains(got, "incoming edges") {
		t.Errorf("expected 'incoming edges' label, got: %s", got)
	}
}

func TestRenderEntryPoints(t *testing.T) {
	db, cleanup := seedTestDB(t)
	defer cleanup()

	got, err := renderEntryPoints(context.Background(), db)
	if err != nil {
		t.Fatalf("renderEntryPoints: %v", err)
	}

	if !strings.Contains(got, "main") {
		t.Errorf("expected main entry point, got: %s", got)
	}
	if !strings.Contains(got, "cmd/app/main.go") {
		t.Errorf("expected file path, got: %s", got)
	}
}

func TestRenderConventions(t *testing.T) {
	db, cleanup := seedTestDB(t)
	defer cleanup()

	got, err := renderConventions(context.Background(), db)
	if err != nil {
		t.Fatalf("renderConventions: %v", err)
	}

	// With a minimal fixture, conventions may return empty — that's valid
	// current behavior. The test captures that the function runs without error
	// and returns either empty or bullet-formatted lines.
	if got == "" {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if !strings.HasPrefix(line, "- ") {
			t.Errorf("expected bullet format, got line: %s", line)
		}
		if !strings.Contains(line, "strength") {
			t.Errorf("expected strength in line: %s", line)
		}
	}
}
