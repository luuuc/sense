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

	fid4, err := adapter.WriteFile(ctx, &model.File{
		Path: "internal/extract/testdata/go/basic.go", Language: "go",
		Hash: "ddd", Symbols: 1, IndexedAt: now,
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

	_, err = adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid4, Name: "main", Qualified: "testdata.main",
		Kind: model.KindFunction, LineStart: 1, LineEnd: 5,
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
	if !strings.Contains(got, "4 files") {
		t.Errorf("expected file count, got: %s", got)
	}
	if !strings.Contains(got, "5 symbols") {
		t.Errorf("expected symbol count, got: %s", got)
	}
	if !strings.Contains(got, "3 edges") {
		t.Errorf("expected edge count, got: %s", got)
	}
	if !strings.Contains(got, "Languages:") {
		t.Errorf("expected multi-language breakdown, got: %s", got)
	}
}

func TestRenderMainAreas(t *testing.T) {
	db, cleanup := seedTestDB(t)
	defer cleanup()

	got, err := renderMainAreas(context.Background(), db)
	if err != nil {
		t.Fatalf("renderMainAreas: %v", err)
	}

	if !strings.Contains(got, "cmd/app") {
		t.Errorf("expected cmd/app namespace, got: %s", got)
	}
	if !strings.Contains(got, "internal/server") {
		t.Errorf("expected internal/server namespace, got: %s", got)
	}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if strings.HasPrefix(line, "*") {
			continue
		}
		if !strings.HasPrefix(line, "- `") {
			t.Errorf("expected bullet format, got line: %s", line)
		}
		if !strings.Contains(line, "symbols") {
			t.Errorf("expected symbol count in line: %s", line)
		}
	}
	if !strings.Contains(got, "functions") {
		t.Errorf("expected dominant kind description, got: %s", got)
	}
	if !strings.Contains(got, "sense_conventions") {
		t.Errorf("expected section-level hint, got: %s", got)
	}
}

func TestRenderKeyAbstractions(t *testing.T) {
	db, cleanup := seedTestDB(t)
	defer cleanup()

	got, err := renderKeyAbstractions(context.Background(), db)
	if err != nil {
		t.Fatalf("renderKeyAbstractions: %v", err)
	}

	if !strings.Contains(got, "server.Handle") {
		t.Errorf("expected top hub symbol server.Handle, got: %s", got)
	}
	if !strings.Contains(got, "incoming edges") {
		t.Errorf("expected 'incoming edges' label, got: %s", got)
	}
	if !strings.Contains(got, "sense_graph") {
		t.Errorf("expected sense_graph section-level hint, got: %s", got)
	}
	if strings.Count(got, "sense_graph") != 1 {
		t.Errorf("expected exactly one sense_graph hint (section-level), got %d", strings.Count(got, "sense_graph"))
	}
}

func TestRenderKeyAbstractionsFiltersUtility(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.db")

	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}

	now := time.Now()
	fid, err := adapter.WriteFile(ctx, &model.File{
		Path: "internal/core/core.go", Language: "go",
		Hash: "aaa", Symbols: 3, IndexedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	closeID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Close", Qualified: "core.Close",
		Kind: model.KindMethod, LineStart: 1, LineEnd: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	runID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Run", Qualified: "core.Run",
		Kind: model.KindFunction, LineStart: 10, LineEnd: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	callerID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "caller", Qualified: "core.caller",
		Kind: model.KindFunction, LineStart: 60, LineEnd: 70,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, targetID := range []int64{closeID, runID} {
		_, err = adapter.WriteEdge(ctx, &model.Edge{
			SourceID: model.Int64Ptr(callerID), TargetID: targetID,
			Kind: model.EdgeCalls, FileID: fid, Confidence: 1.0,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	_ = adapter.Close()

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	got, err := renderKeyAbstractions(ctx, db)
	if err != nil {
		t.Fatalf("renderKeyAbstractions: %v", err)
	}

	if strings.Contains(got, "core.Close") {
		t.Errorf("expected Close filtered as utility hub, got: %s", got)
	}
	if !strings.Contains(got, "core.Run") {
		t.Errorf("expected core.Run to be present, got: %s", got)
	}
}

func TestRenderReadingPath(t *testing.T) {
	db, cleanup := seedTestDB(t)
	defer cleanup()

	got, err := renderReadingPath(context.Background(), db)
	if err != nil {
		t.Fatalf("renderReadingPath: %v", err)
	}

	if !strings.Contains(got, "cmd/app/main.go") {
		t.Errorf("expected main entry in reading path, got: %s", got)
	}
	if strings.Contains(got, "testdata") {
		t.Errorf("expected testdata filtered out, got: %s", got)
	}
	if !strings.HasPrefix(strings.TrimSpace(got), "1.") {
		t.Errorf("expected numbered list starting at 1, got: %s", got)
	}
	if !strings.Contains(got, "sense_search") {
		t.Errorf("expected sense_search next-step hint, got: %s", got)
	}
}

func TestRenderKnownNoise(t *testing.T) {
	db, cleanup := seedTestDB(t)
	defer cleanup()

	got, err := renderKnownNoise(context.Background(), db)
	if err != nil {
		t.Fatalf("renderKnownNoise: %v", err)
	}

	if !strings.Contains(got, "testdata") {
		t.Errorf("expected testdata noise detected, got: %s", got)
	}
	if !strings.Contains(got, "ignore for architecture") {
		t.Errorf("expected noise description, got: %s", got)
	}
}

func TestCommonPrefix(t *testing.T) {
	tests := []struct {
		a, b string
		want string
	}{
		{"a/b/c", "a/b/d", "a/b"},
		{"a/b/c", "a/b/c", "a/b"},
		{"foo", "bar", ""},
		{"a/b", "a/c", "a"},
		{"", "a/b", ""},
		{"a/b", "", ""},
		{"internal/extract/testdata", "internal/scan/testdata", "internal"},
	}
	for _, tt := range tests {
		got := commonPrefix(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("commonPrefix(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestDominantKindDesc(t *testing.T) {
	tests := []struct {
		kinds map[string]int
		want  string
	}{
		{nil, "code"},
		{map[string]int{}, "code"},
		{map[string]int{"function": 10}, "functions"},
		{map[string]int{"method": 5, "function": 2}, "methods"},
		{map[string]int{"class": 3}, "classes"},
		{map[string]int{"module": 1}, "modules"},
		{map[string]int{"type": 4}, "types"},
		{map[string]int{"interface": 2}, "interfaces"},
		{map[string]int{"constant": 7}, "constants"},
		{map[string]int{"unknown": 1}, "unknowns"},
	}
	for _, tt := range tests {
		got := dominantKindDesc(tt.kinds)
		if got != tt.want {
			t.Errorf("dominantKindDesc(%v) = %q, want %q", tt.kinds, got, tt.want)
		}
	}
}
