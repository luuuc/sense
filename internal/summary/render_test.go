package summary

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
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

// TestRenderHubSymbolsConfidenceFilter proves the hub list counts only edges at
// or above blast's traversal floor (extract.ConfidenceUnresolved). A bare-name
// collision target (0.3) with many incoming edges must NOT out-rank a genuinely
// resolved target with fewer high-confidence edges — the litellm `get`-8019-callers
// nonsense-hub bug.
func TestRenderHubSymbolsConfidenceFilter(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.db")

	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}

	now := time.Now()
	fid, err := adapter.WriteFile(ctx, &model.File{
		Path: "app/models/thing.rb", Language: "ruby", Hash: "h", Symbols: 6, IndexedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	mkSym := func(name string) int64 {
		id, e := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: fid, Name: name, Qualified: name, Kind: model.KindMethod, LineStart: 1, LineEnd: 2,
		})
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	// `noise` = bare-name collision target; `Real` = resolved central symbol.
	noiseID := mkSym("noise")
	realID := mkSym("Real")

	callerN := 0
	writeEdges := func(target int64, n int, conf float64) {
		for i := 0; i < n; i++ {
			callerN++
			caller := mkSym(fmt.Sprintf("caller_%d", callerN))
			if _, e := adapter.WriteEdge(ctx, &model.Edge{
				SourceID: model.Int64Ptr(caller), TargetID: target,
				Kind: model.EdgeCalls, FileID: fid, Confidence: conf,
			}); e != nil {
				t.Fatal(e)
			}
		}
	}
	writeEdges(noiseID, 20, 0.3) // below the 0.5 floor — must be excluded
	writeEdges(realID, 3, 1.0)   // resolved — must appear

	_ = adapter.Close()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	got := renderHubSymbols(ctx, db)
	if !strings.Contains(got, "`Real` (3 callers)") {
		t.Errorf("expected resolved hub `Real` (3 callers), got: %q", got)
	}
	if strings.Contains(got, "noise") {
		t.Errorf("bare-name collision target (0.3) must be excluded from hubs, got: %q", got)
	}
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
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.db")

	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}

	now := time.Now()
	fid1, err := adapter.WriteFile(ctx, &model.File{
		Path: "internal/server/server.go", Language: "go",
		Hash: "aaa", Symbols: 2, IndexedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	fid2, err := adapter.WriteFile(ctx, &model.File{
		Path: "internal/handler/handler.go", Language: "go",
		Hash: "bbb", Symbols: 1, IndexedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	serverID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid1, Name: "Server", Qualified: "server.Server",
		Kind: "class", LineStart: 1, LineEnd: 30,
		Snippet: "type Server struct { router *Router }",
	})
	if err != nil {
		t.Fatal(err)
	}
	callerID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid2, Name: "Handler", Qualified: "handler.Handler",
		Kind: "class", LineStart: 1, LineEnd: 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = adapter.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(callerID), TargetID: serverID,
		Kind: model.EdgeCalls, FileID: fid2, Confidence: 1.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	got, err := renderKeyAbstractions(ctx, adapter)
	if err != nil {
		t.Fatalf("renderKeyAbstractions: %v", err)
	}

	if !strings.Contains(got, "server.Server") {
		t.Errorf("expected server.Server in key abstractions, got: %s", got)
	}
	if !strings.Contains(got, "file refs") {
		t.Errorf("expected 'file refs' label, got: %s", got)
	}
	if !strings.Contains(got, "sense_graph") {
		t.Errorf("expected sense_graph section-level hint, got: %s", got)
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
	fid2, err := adapter.WriteFile(ctx, &model.File{
		Path: "internal/app/app.go", Language: "go",
		Hash: "bbb", Symbols: 1, IndexedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	// "Close" is a utility name that should be filtered
	closeID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Close", Qualified: "core.Close",
		Kind: "type", LineStart: 1, LineEnd: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	// "Runner" is a real type that should appear
	runnerID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Runner", Qualified: "core.Runner",
		Kind: "class", LineStart: 10, LineEnd: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	callerID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid2, Name: "App", Qualified: "app.App",
		Kind: "class", LineStart: 1, LineEnd: 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, targetID := range []int64{closeID, runnerID} {
		_, err = adapter.WriteEdge(ctx, &model.Edge{
			SourceID: model.Int64Ptr(callerID), TargetID: targetID,
			Kind: model.EdgeCalls, FileID: fid2, Confidence: 1.0,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	defer func() { _ = adapter.Close() }()

	got, err := renderKeyAbstractions(ctx, adapter)
	if err != nil {
		t.Fatalf("renderKeyAbstractions: %v", err)
	}

	if strings.Contains(got, "core.Close") {
		t.Errorf("expected Close filtered as utility name, got: %s", got)
	}
	if !strings.Contains(got, "core.Runner") {
		t.Errorf("expected core.Runner to be present, got: %s", got)
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

func TestRenderProject(t *testing.T) {
	tests := []struct {
		name    string
		readme  string
		want    string
		wantNil bool
	}{
		{
			name:   "skips badges and headings",
			readme: "[![CI](https://example.com)]\n\n# MyProject\n\nA fast HTTP framework for Go.\n",
			want:   "A fast HTTP framework for Go.",
		},
		{
			name:   "takes first paragraph only",
			readme: "# Foo\n\nFirst paragraph.\n\nSecond paragraph.\n",
			want:   "First paragraph.",
		},
		{
			name:   "joins multi-line paragraph",
			readme: "# X\n\nLine one of the\ndescription continues.\n\nAnother para.\n",
			want:   "Line one of the description continues.",
		},
		{
			name:    "empty readme",
			readme:  "",
			wantNil: true,
		},
		{
			name:    "only badges and headings",
			readme:  "[![badge](url)]\n# Title\n",
			wantNil: true,
		},
		{
			name:   "skips HTML tags",
			readme: "<p align=\"center\">\n<img src=\"logo.png\">\n</p>\n\nThe real description.\n",
			want:   "The real description.",
		},
		{
			name:   "truncates long description",
			readme: "# X\n\n" + strings.Repeat("abcdefghij", 35) + "\n",
			want:   strings.Repeat("abcdefghij", 29) + "abcdefg...",
		},
		{
			name:   "truncation is rune-safe",
			readme: "# X\n\n" + strings.Repeat("a", 295) + "ééé\n",
			want:   strings.Repeat("a", 295) + "é...",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.readme != "" {
				if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(tt.readme), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := renderProject(root)
			if tt.wantNil && got != "" {
				t.Errorf("expected empty, got %q", got)
			}
			if !tt.wantNil && got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		want     string
	}{
		{"short string unchanged", "hello", 10, "hello"},
		{"exact length unchanged", "hello", 5, "hello"},
		{"truncates ASCII", "hello world", 5, "hello"},
		{"preserves multibyte rune", "aaaa\xc3\xa9\xc3\xa9", 5, "aaaa"},
		{"empty string", "", 5, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateUTF8(tt.input, tt.maxBytes)
			if got != tt.want {
				t.Errorf("truncateUTF8(%q, %d) = %q, want %q", tt.input, tt.maxBytes, got, tt.want)
			}
		})
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
