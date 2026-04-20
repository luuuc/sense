package cli

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/sqlite"
	_ "modernc.org/sqlite"
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
