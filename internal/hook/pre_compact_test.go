package hook

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/sqlite"
)

func TestPreCompactEmptyIndex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".sense", "index.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = adapter.Close()

	var buf bytes.Buffer
	code := Run("pre-compact", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if buf.String() != "{}\n" {
		t.Errorf("empty index should produce {}, got %q", buf.String())
	}
}

func TestPreCompactQueryFailsOnClosedAdapter(t *testing.T) {
	dir := indexedDir(t)
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = adapter.Close()

	_, err = handlePreCompact(ctx, nil, adapter, dir)
	if err == nil {
		t.Fatal("expected error when querying closed adapter")
	}
}

func TestTopHubsQueryFailsOnClosedAdapter(t *testing.T) {
	dir := indexedDir(t)
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = adapter.Close()

	_, err = topHubs(ctx, adapter, 5)
	if err == nil {
		t.Fatal("expected error when querying topHubs on closed adapter")
	}
}

func TestPreCompactEdgeCountQueryError(t *testing.T) {
	dir := indexedDir(t)
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	// Drop sense_edges so the symbol COUNT succeeds but the edge COUNT
	// query fails, exercising the edgeCount error return.
	if _, err := adapter.DB().Exec("DROP TABLE sense_edges"); err != nil {
		t.Fatalf("drop sense_edges: %v", err)
	}

	if _, err := handlePreCompact(ctx, nil, adapter, dir); err == nil {
		t.Fatal("expected error when edge count query fails")
	}
}

func TestPreCompactTopHubsError(t *testing.T) {
	dir := indexedDir(t)
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	// Recreate sense_edges without the target_id column. The symbol and
	// edge COUNT(*) queries still succeed (so we pass the symbolCount==0
	// guard), but topHubs references target_id and fails, exercising the
	// topHubs error return in handlePreCompact.
	db := adapter.DB()
	if _, err := db.Exec("DROP TABLE sense_edges"); err != nil {
		t.Fatalf("drop sense_edges: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE sense_edges (id INTEGER PRIMARY KEY, source_id INTEGER)"); err != nil {
		t.Fatalf("recreate sense_edges: %v", err)
	}
	if _, err := db.Exec("INSERT INTO sense_edges (id, source_id) VALUES (1, 1)"); err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	if _, err := handlePreCompact(ctx, nil, adapter, dir); err == nil {
		t.Fatal("expected error when topHubs query fails")
	}
}

func TestPreCompactPopulated(t *testing.T) {
	dir := indexedDir(t)
	var buf bytes.Buffer
	code := Run("pre-compact", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if buf.String() == "{}\n" {
		t.Error("populated index should produce a message, not {}")
	}
	if !strings.Contains(buf.String(), "Sense Index Summary") {
		t.Errorf("message should contain 'Sense Index Summary', got %q", buf.String())
	}
}
