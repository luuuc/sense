package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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
