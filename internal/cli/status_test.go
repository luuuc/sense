package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/freshen"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func TestRunStatusJSONProducesValidJSON(t *testing.T) {
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
	_, err = adapter.WriteFile(ctx, &model.File{
		Path: "main.go", Language: "go", Hash: "abc", Symbols: 1,
		IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = adapter.Close()

	var stdout, stderr bytes.Buffer
	code := RunStatus([]string{"--json"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}

	raw := bytes.TrimSpace(stdout.Bytes())
	if !json.Valid(raw) {
		t.Fatalf("stdout is not valid JSON:\n%s", raw)
	}
	if raw[0] != '{' {
		t.Errorf("stdout starts with %q, want '{'  — preamble is leaking into JSON output", string(raw[:1]))
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1K"},
		{6400, "6K"},
		{138200, "138K"},
		{999999, "1000K"},
		{1000000, "1.0M"},
		{4200000, "4.2M"},
		{10500000, "10.5M"},
	}
	for _, tt := range tests {
		got := formatTokens(tt.n)
		if got != tt.want {
			t.Errorf("formatTokens(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// healthTestDB builds an in-memory-equivalent index with the schema
// computeHealth queries, so the verdict branches can be driven directly.
func healthTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "health.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	_, err = db.ExecContext(ctx, `
		CREATE TABLE sense_files (id INTEGER PRIMARY KEY, path TEXT UNIQUE, language TEXT, hash TEXT, symbols INTEGER, indexed_at TEXT);
		CREATE TABLE sense_symbols (id INTEGER PRIMARY KEY, file_id INTEGER, name TEXT, qualified TEXT, kind TEXT, visibility TEXT, parent_id INTEGER, line_start INTEGER, line_end INTEGER, docstring TEXT, complexity INTEGER, snippet TEXT, UNIQUE(file_id, qualified));
		CREATE TABLE sense_edges (id INTEGER PRIMARY KEY, source_id INTEGER, target_id INTEGER, kind TEXT, file_id INTEGER, line INTEGER, confidence REAL);
	`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestComputeHealthOrphanedEdges(t *testing.T) {
	ctx := context.Background()
	db := healthTestDB(t)

	// A symbol exists, plus an edge whose target_id points to a missing symbol.
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_files VALUES (1, 'a.go', 'go', 'abc', 1, '2026-01-01T00:00:00Z')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_symbols VALUES (1, 1, 'Foo', 'Foo', 'function', 'public', NULL, 1, 5, NULL, NULL, NULL)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_edges VALUES (1, 1, 99, 'calls', 1, 3, 1.0)`)

	resp := mcpio.StatusResponse{
		Version: &mcpio.StatusVersion{SchemaCurrent: true, EmbeddingModelCurrent: true},
	}
	h := computeHealth(ctx, db, t.TempDir(), resp)

	if h.verdict != "unhealthy" {
		t.Errorf("verdict = %q, want %q", h.verdict, "unhealthy")
	}
	if h.detail != "orphaned edges — run 'sense scan --rebuild'" {
		t.Errorf("detail = %q, want orphaned edges message", h.detail)
	}
}

func TestComputeHealthEmbeddingModelMismatch(t *testing.T) {
	ctx := context.Background()
	db := healthTestDB(t)

	// No orphaned edges, schema current, but the embedding model is stale.
	resp := mcpio.StatusResponse{
		Version: &mcpio.StatusVersion{SchemaCurrent: true, EmbeddingModelCurrent: false},
	}
	h := computeHealth(ctx, db, t.TempDir(), resp)

	if h.verdict != "unhealthy" {
		t.Errorf("verdict = %q, want %q", h.verdict, "unhealthy")
	}
	if h.detail != "embedding model mismatch — run 'sense scan --rebuild'" {
		t.Errorf("detail = %q, want embedding model mismatch message", h.detail)
	}
}

func TestEmbeddingsEnabled(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		cfgYAML string
		want    bool
	}{
		{"default (no env, no config)", "", "", true},
		{"env false", "false", "", false},
		{"env FALSE", "FALSE", "", false},
		{"env 0", "0", "", false},
		{"env true", "true", "", true},
		{"env 1", "1", "", true},
		{"config disabled", "", "embeddings:\n  enabled: false\n", false},
		{"config enabled", "", "embeddings:\n  enabled: true\n", true},
		{"env overrides config", "false", "embeddings:\n  enabled: true\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()

			if tt.env != "" {
				t.Setenv("SENSE_EMBEDDINGS", tt.env)
			} else {
				t.Setenv("SENSE_EMBEDDINGS", "")
			}

			if tt.cfgYAML != "" {
				senseDir := filepath.Join(root, ".sense")
				if err := os.MkdirAll(senseDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(senseDir, "config.yml"), []byte(tt.cfgYAML), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			got := EmbeddingsEnabled(root)
			if got != tt.want {
				t.Errorf("EmbeddingsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunStatusParseErrorExit1(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := RunStatus([]string{"--bogus-flag"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: t.TempDir()})
	if code != ExitGeneralError {
		t.Errorf("exit = %d, want %d for an unknown flag", code, ExitGeneralError)
	}
}

// TestRunStatusSenseDirEnv pins the SENSE_DIR override: when the env
// var is set, buildCLIStatusResponse reads the index from there
// rather than <dir>/.sense.
func TestRunStatusSenseDirEnv(t *testing.T) {
	dir := t.TempDir()
	customDir := filepath.Join(dir, "elsewhere")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(customDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.WriteFile(ctx, &model.File{Path: "main.go", Language: "go", Hash: "h", Symbols: 1, IndexedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_ = adapter.Close()

	t.Setenv("SENSE_DIR", customDir)
	t.Setenv("SENSE_EMBEDDINGS", "false")
	var stdout, stderr bytes.Buffer
	// Dir points somewhere with no .sense — success proves SENSE_DIR won.
	code := RunStatus(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Files:") {
		t.Errorf("expected rendered status from SENSE_DIR index, got:\n%s", stdout.String())
	}
}

func TestComputeHealthStaleFilesDegraded(t *testing.T) {
	ctx := context.Background()
	db := healthTestDB(t)

	stale := 3
	resp := mcpio.StatusResponse{
		Version:   &mcpio.StatusVersion{SchemaCurrent: true, EmbeddingModelCurrent: true},
		Freshness: mcpio.Freshness{StaleFilesSeen: &stale},
	}
	resp.Index.Symbols = 0 // skip the embedding-percentage branch
	h := computeHealth(ctx, db, t.TempDir(), resp)

	if h.verdict != "degraded" {
		t.Errorf("verdict = %q, want degraded", h.verdict)
	}
	if h.detail != "3 stale files" {
		t.Errorf("detail = %q, want '3 stale files'", h.detail)
	}
}

func TestQueryLangBreakdownQueryError(t *testing.T) {
	got := queryLangBreakdown(context.Background(), closedQueryDB(t))
	if len(got) != 0 {
		t.Errorf("queryLangBreakdown on closed DB = %v, want empty map", got)
	}
}

func TestEmbeddingDebtCLIError(t *testing.T) {
	if got := embeddingDebtCLI(context.Background(), closedQueryDB(t)); got != -1 {
		t.Errorf("embeddingDebtCLI on closed DB = %d, want -1", got)
	}
}

func TestAbs(t *testing.T) {
	if got := abs(-5); got != 5 {
		t.Errorf("abs(-5) = %d, want 5", got)
	}
	if got := abs(7); got != 7 {
		t.Errorf("abs(7) = %d, want 7", got)
	}
}

func TestCountStaleFilesCLIQueryError(t *testing.T) {
	if got := countStaleFilesCLI(context.Background(), closedQueryDB(t), t.TempDir()); got != 0 {
		t.Errorf("countStaleFilesCLI on closed DB = %d, want 0", got)
	}
}

// TestCountStaleFilesCLIUnparseableTimestamp drives the time.Parse
// failure continue: a row with a garbage indexed_at is skipped rather
// than counted or fatal.
func TestCountStaleFilesCLIUnparseableTimestamp(t *testing.T) {
	ctx := context.Background()
	db := healthTestDB(t)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_files VALUES (1, 'main.go', 'go', 'h', 1, 'not-a-timestamp')`)
	if got := countStaleFilesCLI(ctx, db, t.TempDir()); got != 0 {
		t.Errorf("countStaleFilesCLI with bad timestamp = %d, want 0", got)
	}
}

func TestBuildVersionInfoError(t *testing.T) {
	if got := buildVersionInfo(context.Background(), closedQueryDB(t)); got != nil {
		t.Errorf("buildVersionInfo on closed DB = %+v, want nil", got)
	}
}

func TestQueryLifetimeCountersError(t *testing.T) {
	if got := queryLifetimeCounters(context.Background(), closedQueryDB(t)); got != nil {
		t.Errorf("queryLifetimeCounters on closed DB = %+v, want nil", got)
	}
}

// TestComputeCLIFreshnessLastUpdate drives the branch where the last
// file update (MAX(indexed_at)) is meaningfully newer-or-older than
// the recorded last_scan_at, so LastUpdate is reported separately.
func TestComputeCLIFreshnessLastUpdate(t *testing.T) {
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
	defer func() { _ = adapter.Close() }()

	// File indexed two hours ago; last_scan_at recorded as "now". The
	// >60s gap between scan age and update age trips the LastUpdate
	// reporting branch.
	if _, err := adapter.WriteFile(ctx, &model.File{Path: "main.go", Language: "go", Hash: "h", Symbols: 1, IndexedAt: time.Now().Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.DB().ExecContext(ctx,
		`INSERT INTO sense_meta (key, value) VALUES ('last_scan_at', ?)`,
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	f := computeCLIFreshness(ctx, adapter.DB(), dir)
	if f.LastScan == nil {
		t.Fatal("expected LastScan to be set from last_scan_at meta")
	}
	if f.LastUpdate == nil {
		t.Error("expected LastUpdate to be reported when file update age differs from scan age by >60s")
	}
}

// TestRenderStatusHumanFullResponse exercises the optional render
// branches: embeddings-disabled line, separate LastUpdate line, and
// the Pending line.
func TestRenderStatusHumanFullResponse(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "false")
	dir := t.TempDir()

	lastScan := "2026-06-05T00:00:00Z"
	lastUpdate := "2026-06-05T01:00:00Z"
	var scanAge, updateAge int64 = 120, 60
	stale := 2
	watching := true
	pending := 4

	resp := mcpio.StatusResponse{
		Freshness: mcpio.Freshness{
			LastScan:              &lastScan,
			IndexAgeSeconds:       &scanAge,
			LastUpdate:            &lastUpdate,
			IndexUpdateAgeSeconds: &updateAge,
			StaleFilesSeen:        &stale,
			Watching:              &watching,
			Pending:               &pending,
		},
		Version: &mcpio.StatusVersion{
			Binary: "test", Schema: sqlite.SchemaVersion, SchemaCurrent: true,
			EmbeddingModel: "m", EmbeddingModelCurrent: true,
		},
	}
	resp.Index.Path = ".sense/index.db"
	resp.Index.SizeBytes = 2048
	resp.Index.Files = 3
	resp.Index.Symbols = 10

	var stdout bytes.Buffer
	renderStatusHuman(IO{Stdout: &stdout, Dir: dir}, resp, healthInfo{verdict: "degraded", detail: "2 stale files"})
	out := stdout.String()
	for _, want := range []string{
		"Embeddings: disabled",
		"Last update:",
		"Stale files: 2",
		"Watching:    yes",
		"Pending:     4 symbols awaiting embeddings",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestComputeCLIFreshnessWatchingAndPending(t *testing.T) {
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
	defer func() { _ = adapter.Close() }()

	fid, err := adapter.WriteFile(ctx, &model.File{Path: "main.go", Language: "go", Hash: "h", Symbols: 1, IndexedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "Main", Qualified: "main.Main", Kind: "function", LineStart: 1, LineEnd: 1}); err != nil {
		t.Fatal(err)
	}

	// Simulate a running `sense mcp` watcher holding the single-writer lock.
	release, ok := freshen.AcquireWriterLock(dir)
	if !ok {
		t.Fatal("test setup: should acquire writer lock")
	}
	defer release()

	f := computeCLIFreshness(ctx, adapter.DB(), dir)
	if f.Watching == nil || !*f.Watching {
		t.Error("expected Watching=true when a watcher holds the lock")
	}
	if f.Pending == nil {
		t.Error("expected Pending to be reported")
	} else if *f.Pending != 1 {
		t.Errorf("pending = %d, want 1 (one unembedded symbol)", *f.Pending)
	}
}
