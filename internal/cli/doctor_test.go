package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/sqlite"
)

func TestFormatAge(t *testing.T) {
	tests := []struct {
		seconds int64
		want    string
	}{
		{0, "0 seconds ago"},
		{30, "30 seconds ago"},
		{59, "59 seconds ago"},
		{60, "1 minutes ago"},
		{120, "2 minutes ago"},
		{3599, "59 minutes ago"},
		{3600, "1 hours ago"},
		{7200, "2 hours ago"},
		{86399, "23 hours ago"},
		{86400, "1 days ago"},
		{172800, "2 days ago"},
	}
	for _, tt := range tests {
		got := formatAge(&tt.seconds)
		if got != tt.want {
			t.Errorf("formatAge(%d) = %q, want %q", tt.seconds, got, tt.want)
		}
	}

	if got := formatAge(nil); got != "unknown" {
		t.Errorf("formatAge(nil) = %q, want %q", got, "unknown")
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{13000000, "12.4 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.bytes)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestCountOrphanedEdges(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	_, err = db.ExecContext(ctx, `
		CREATE TABLE sense_files (
			id INTEGER PRIMARY KEY, path TEXT UNIQUE, language TEXT, hash TEXT, symbols INTEGER, indexed_at TEXT
		);
		CREATE TABLE sense_symbols (
			id INTEGER PRIMARY KEY, file_id INTEGER, name TEXT, qualified TEXT, kind TEXT,
			visibility TEXT, parent_id INTEGER, line_start INTEGER, line_end INTEGER,
			docstring TEXT, complexity INTEGER, snippet TEXT, UNIQUE(file_id, qualified)
		);
		CREATE TABLE sense_edges (
			id INTEGER PRIMARY KEY, source_id INTEGER, target_id INTEGER, kind TEXT,
			file_id INTEGER, line INTEGER, confidence REAL
		);
	`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert a valid file and symbol
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_files VALUES (1, 'a.go', 'go', 'abc', 1, '2026-01-01T00:00:00Z')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_symbols VALUES (1, 1, 'Foo', 'Foo', 'function', 'public', NULL, 1, 5, NULL, NULL, NULL)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_symbols VALUES (2, 1, 'Bar', 'Bar', 'function', 'public', NULL, 6, 10, NULL, NULL, NULL)`)

	// Valid edge
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_edges VALUES (1, 1, 2, 'calls', 1, 3, 1.0)`)

	if got := countOrphanedEdges(ctx, db); got != 0 {
		t.Errorf("countOrphanedEdges with valid edges = %d, want 0", got)
	}

	// Orphaned edge (target_id 99 doesn't exist)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_edges VALUES (2, 1, 99, 'calls', 1, 4, 1.0)`)

	if got := countOrphanedEdges(ctx, db); got != 1 {
		t.Errorf("countOrphanedEdges with one orphan = %d, want 1", got)
	}

	// Orphaned edge (source_id 88 doesn't exist)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_edges VALUES (3, 88, 2, 'calls', 1, 5, 1.0)`)

	if got := countOrphanedEdges(ctx, db); got != 2 {
		t.Errorf("countOrphanedEdges with two orphans = %d, want 2", got)
	}
}

func TestDoctorExitCodeOnFailure(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	// No index → should fail
	code := RunDoctor(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Errorf("doctor with no index: exit=%d, want %d", code, ExitGeneralError)
	}
}

func createTestDB(t *testing.T, dir string, schemaVer int) *sql.DB {
	t.Helper()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(senseDir, "index.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `
		CREATE TABLE sense_files (id INTEGER PRIMARY KEY, path TEXT UNIQUE, language TEXT, hash TEXT, symbols INTEGER, indexed_at TEXT);
		CREATE TABLE sense_symbols (id INTEGER PRIMARY KEY, file_id INTEGER, name TEXT, qualified TEXT, kind TEXT, visibility TEXT, parent_id INTEGER, line_start INTEGER, line_end INTEGER, docstring TEXT, complexity INTEGER, snippet TEXT, UNIQUE(file_id, qualified));
		CREATE TABLE sense_edges (id INTEGER PRIMARY KEY, source_id INTEGER, target_id INTEGER, kind TEXT, file_id INTEGER, line INTEGER, confidence REAL);
		CREATE TABLE sense_embeddings (symbol_id INTEGER PRIMARY KEY, vector BLOB);
		CREATE TABLE sense_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
	`)
	_, _ = db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVer))
	return db
}

func TestDoctorSchemaMismatch(t *testing.T) {
	dir := t.TempDir()
	db := createTestDB(t, dir, sqlite.SchemaVersion+1)
	_ = db.Close()

	t.Setenv("SENSE_EMBEDDINGS", "false")
	var stdout, stderr bytes.Buffer
	code := RunDoctor(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Errorf("doctor with schema mismatch: exit=%d, want %d", code, ExitGeneralError)
	}
	if !strings.Contains(stdout.String(), "Schema version mismatch") {
		t.Errorf("expected schema mismatch message, got: %s", stdout.String())
	}
}

func TestDoctorModelMismatch(t *testing.T) {
	dir := t.TempDir()
	db := createTestDB(t, dir, sqlite.SchemaVersion)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_meta VALUES ('embedding_model', 'old-model')`)
	_ = db.Close()

	t.Setenv("SENSE_EMBEDDINGS", "false")
	var stdout, stderr bytes.Buffer
	code := RunDoctor(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Errorf("doctor with model mismatch: exit=%d, want %d", code, ExitGeneralError)
	}
	if !strings.Contains(stdout.String(), "Embedding model mismatch") {
		t.Errorf("expected model mismatch message, got: %s", stdout.String())
	}
}

func TestDoctorStaleFilesWarning(t *testing.T) {
	dir := t.TempDir()
	db := createTestDB(t, dir, sqlite.SchemaVersion)
	ctx := context.Background()

	// Create a file that will be stale
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Set indexed_at to past so file is stale
	past := time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_files VALUES (1, 'main.go', 'go', 'abc', 1, ?)`, past)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_symbols VALUES (1, 1, 'main', 'main', 'function', 'public', NULL, 1, 1, NULL, NULL, NULL)`)
	_ = db.Close()

	t.Setenv("SENSE_EMBEDDINGS", "false")
	var stdout, stderr bytes.Buffer
	code := RunDoctor(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Errorf("doctor with stale files warning: exit=%d, want %d", code, ExitSuccess)
	}
	if !strings.Contains(stdout.String(), "stale file") {
		t.Errorf("expected stale files warning, got: %s", stdout.String())
	}
}

func TestDoctorStaleFilesFail(t *testing.T) {
	dir := t.TempDir()
	db := createTestDB(t, dir, sqlite.SchemaVersion)
	ctx := context.Background()

	// Create 11 stale files to trigger fail status
	for i := 0; i < 11; i++ {
		fname := fmt.Sprintf("file%d.go", i)
		fpath := filepath.Join(dir, fname)
		if err := os.WriteFile(fpath, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
		_, _ = db.ExecContext(ctx, `INSERT INTO sense_files VALUES (?, ?, 'go', 'abc', 1, ?)`, i+1, fname, past)
		_, _ = db.ExecContext(ctx, `INSERT INTO sense_symbols VALUES (?, ?, 'main', 'main', 'function', 'public', NULL, 1, 1, NULL, NULL, NULL)`, i+1)
	}
	_ = db.Close()

	t.Setenv("SENSE_EMBEDDINGS", "false")
	var stdout, stderr bytes.Buffer
	code := RunDoctor(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Errorf("doctor with many stale files: exit=%d, want %d", code, ExitGeneralError)
	}
	if !strings.Contains(stdout.String(), "stale files") {
		t.Errorf("expected stale files fail message, got: %s", stdout.String())
	}
}

func TestDoctorEmbeddingCompletenessWarn(t *testing.T) {
	dir := t.TempDir()
	db := createTestDB(t, dir, sqlite.SchemaVersion)
	ctx := context.Background()

	// 10 symbols, 9 embeddings = 90%
	for i := 0; i < 10; i++ {
		qualified := fmt.Sprintf("Foo%d", i)
		_, _ = db.ExecContext(ctx, `INSERT INTO sense_symbols VALUES (?, 1, 'Foo', ?, 'function', 'public', NULL, 1, 1, NULL, NULL, NULL)`, i+1, qualified)
	}
	for i := 0; i < 9; i++ {
		_, _ = db.ExecContext(ctx, `INSERT INTO sense_embeddings VALUES (?, X'00')`, i+1)
	}
	_ = db.Close()

	var stdout, stderr bytes.Buffer
	code := RunDoctor(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Errorf("doctor with embedding warning: exit=%d, want %d", code, ExitSuccess)
	}
	if !strings.Contains(stdout.String(), "Embeddings incomplete") {
		t.Errorf("expected embedding completeness warning, got: %s", stdout.String())
	}
}

func TestDoctorEmbeddingCompletenessFail(t *testing.T) {
	dir := t.TempDir()
	db := createTestDB(t, dir, sqlite.SchemaVersion)
	ctx := context.Background()

	// 10 symbols, 5 embeddings = 50%
	for i := 0; i < 10; i++ {
		qualified := fmt.Sprintf("Foo%d", i)
		_, _ = db.ExecContext(ctx, `INSERT INTO sense_symbols VALUES (?, 1, 'Foo', ?, 'function', 'public', NULL, 1, 1, NULL, NULL, NULL)`, i+1, qualified)
	}
	for i := 0; i < 5; i++ {
		_, _ = db.ExecContext(ctx, `INSERT INTO sense_embeddings VALUES (?, X'00')`, i+1)
	}
	_ = db.Close()

	var stdout, stderr bytes.Buffer
	code := RunDoctor(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Errorf("doctor with embedding fail: exit=%d, want %d", code, ExitGeneralError)
	}
	if !strings.Contains(stdout.String(), "Embeddings incomplete") {
		t.Errorf("expected embedding completeness fail message, got: %s", stdout.String())
	}
}

func TestDoctorUnknownExtensions(t *testing.T) {
	dir := t.TempDir()
	db := createTestDB(t, dir, sqlite.SchemaVersion)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_files VALUES (1, 'main.xyz', 'xyz', 'abc', 1, '2026-01-01T00:00:00Z')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_symbols VALUES (1, 1, 'main', 'main', 'function', 'public', NULL, 1, 1, NULL, NULL, NULL)`)
	_ = db.Close()

	t.Setenv("SENSE_EMBEDDINGS", "false")
	var stdout, stderr bytes.Buffer
	code := RunDoctor(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Errorf("doctor with unknown extensions: exit=%d, want %d", code, ExitSuccess)
	}
	if !strings.Contains(stdout.String(), "Unknown extensions") {
		t.Errorf("expected unknown extensions warning, got: %s", stdout.String())
	}
}

func TestDoctorJSONOutput(t *testing.T) {
	dir := t.TempDir()
	db := createTestDB(t, dir, sqlite.SchemaVersion)
	_ = db.Close()

	t.Setenv("SENSE_EMBEDDINGS", "false")
	var stdout, stderr bytes.Buffer
	code := RunDoctor([]string{"--json"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Errorf("doctor with JSON flag: exit=%d, want %d", code, ExitSuccess)
	}
	var resp doctorResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON output: %v\noutput: %s", err, stdout.String())
	}
	if len(resp.Checks) == 0 {
		t.Error("expected non-empty checks in JSON response")
	}
}

func TestDoctorSENSE_DIR(t *testing.T) {
	dir := t.TempDir()
	customSenseDir := filepath.Join(dir, "custom-sense")
	if err := os.MkdirAll(customSenseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(customSenseDir, "index.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `
		CREATE TABLE sense_files (id INTEGER PRIMARY KEY, path TEXT UNIQUE, language TEXT, hash TEXT, symbols INTEGER, indexed_at TEXT);
		CREATE TABLE sense_symbols (id INTEGER PRIMARY KEY, file_id INTEGER, name TEXT, qualified TEXT, kind TEXT, visibility TEXT, parent_id INTEGER, line_start INTEGER, line_end INTEGER, docstring TEXT, complexity INTEGER, snippet TEXT, UNIQUE(file_id, qualified));
		CREATE TABLE sense_edges (id INTEGER PRIMARY KEY, source_id INTEGER, target_id INTEGER, kind TEXT, file_id INTEGER, line INTEGER, confidence REAL);
		CREATE TABLE sense_embeddings (symbol_id INTEGER PRIMARY KEY, vector BLOB);
		CREATE TABLE sense_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
	`)
	_, _ = db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", sqlite.SchemaVersion))
	_ = db.Close()

	t.Setenv("SENSE_DIR", customSenseDir)
	t.Setenv("SENSE_EMBEDDINGS", "false")
	var stdout, stderr bytes.Buffer
	code := RunDoctor(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Errorf("doctor with SENSE_DIR: exit=%d, want %d", code, ExitSuccess)
	}
	if !strings.Contains(stdout.String(), customSenseDir) {
		t.Errorf("expected custom sense dir in output, got: %s", stdout.String())
	}
}

func TestDoctorOrphanedEdgesFail(t *testing.T) {
	dir := t.TempDir()
	db := createTestDB(t, dir, sqlite.SchemaVersion)
	ctx := context.Background()

	// One valid symbol, plus an edge whose target_id points to a missing symbol.
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_files VALUES (1, 'a.go', 'go', 'abc', 1, '2026-01-01T00:00:00Z')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_symbols VALUES (1, 1, 'Foo', 'Foo', 'function', 'public', NULL, 1, 5, NULL, NULL, NULL)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_edges VALUES (1, 1, 99, 'calls', 1, 3, 1.0)`)
	_ = db.Close()

	t.Setenv("SENSE_EMBEDDINGS", "false")
	var stdout, stderr bytes.Buffer
	code := RunDoctor(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Errorf("doctor with orphaned edges: exit=%d, want %d", code, ExitGeneralError)
	}
	if !strings.Contains(stdout.String(), "orphaned edges") {
		t.Errorf("expected orphaned edges fail message, got: %s", stdout.String())
	}
}

func TestDoctorParseErrorExit1(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := RunDoctor([]string{"--bogus-flag"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: t.TempDir()})
	if code != ExitGeneralError {
		t.Errorf("exit = %d, want %d for an unknown flag", code, ExitGeneralError)
	}
}

// TestDoctorEmbeddingCompletenessNoSymbols drives the "No symbols to
// embed" pass branch: embeddings enabled but the index holds zero
// symbols.
func TestDoctorEmbeddingCompletenessNoSymbols(t *testing.T) {
	dir := t.TempDir()
	db := createTestDB(t, dir, sqlite.SchemaVersion)
	_ = db.Close()

	t.Setenv("SENSE_EMBEDDINGS", "true")
	var stdout, stderr bytes.Buffer
	code := RunDoctor(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Errorf("exit=%d, want %d\n%s", code, ExitSuccess, stdout.String())
	}
	if !strings.Contains(stdout.String(), "No symbols to embed") {
		t.Errorf("expected 'No symbols to embed', got:\n%s", stdout.String())
	}
}

// TestDoctorEmbeddingCompletenessComplete drives the pct==100 pass
// branch: every symbol has an embedding.
func TestDoctorEmbeddingCompletenessComplete(t *testing.T) {
	dir := t.TempDir()
	db := createTestDB(t, dir, sqlite.SchemaVersion)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		_, _ = db.ExecContext(ctx, `INSERT INTO sense_symbols VALUES (?, 1, 'Foo', ?, 'function', 'public', NULL, 1, 1, NULL, NULL, NULL)`, i+1, fmt.Sprintf("Foo%d", i))
		_, _ = db.ExecContext(ctx, `INSERT INTO sense_embeddings VALUES (?, X'00')`, i+1)
	}
	_ = db.Close()

	t.Setenv("SENSE_EMBEDDINGS", "true")
	var stdout, stderr bytes.Buffer
	code := RunDoctor(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Errorf("exit=%d, want %d\n%s", code, ExitSuccess, stdout.String())
	}
	if !strings.Contains(stdout.String(), "complete (4/4)") {
		t.Errorf("expected 'complete (4/4)', got:\n%s", stdout.String())
	}
}

func TestFindUnknownExtensionsQueryError(t *testing.T) {
	if got := findUnknownExtensions(context.Background(), closedQueryDB(t)); got != nil {
		t.Errorf("findUnknownExtensions on closed DB = %v, want nil", got)
	}
}

func TestDoctorPassesOnHealthyIndex(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(senseDir, "index.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `
		CREATE TABLE sense_files (id INTEGER PRIMARY KEY, path TEXT UNIQUE, language TEXT, hash TEXT, symbols INTEGER, indexed_at TEXT);
		CREATE TABLE sense_symbols (id INTEGER PRIMARY KEY, file_id INTEGER, name TEXT, qualified TEXT, kind TEXT, visibility TEXT, parent_id INTEGER, line_start INTEGER, line_end INTEGER, docstring TEXT, complexity INTEGER, snippet TEXT, UNIQUE(file_id, qualified));
		CREATE TABLE sense_edges (id INTEGER PRIMARY KEY, source_id INTEGER, target_id INTEGER, kind TEXT, file_id INTEGER, line INTEGER, confidence REAL);
		CREATE TABLE sense_embeddings (symbol_id INTEGER PRIMARY KEY, vector BLOB);
		CREATE TABLE sense_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
	`)
	_, _ = db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", sqlite.SchemaVersion))
	_ = db.Close()

	t.Setenv("SENSE_EMBEDDINGS", "false")
	var stdout, stderr bytes.Buffer
	code := RunDoctor(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Errorf("doctor on healthy index: exit=%d, want 0\nstdout: %s", code, stdout.String())
	}
}
