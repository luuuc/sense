package dead_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/dead"
	"github.com/luuuc/sense/internal/scan"
)

var updateGolden = flag.Bool("update-golden", false, "regenerate dead-golden.json")

type goldenEntry struct {
	Qualified string `json:"qualified"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
}

type goldenFile struct {
	Dead         []goldenEntry `json:"dead"`
	TotalSymbols int           `json:"total_symbols"`
	DeadCount    int           `json:"dead_count"`
}

func smokeRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "smoke"))
	if err != nil {
		t.Fatalf("resolve smoke root: %v", err)
	}
	return root
}

func goldenPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(smokeRoot(t), "dead-golden.json")
}

func scanSmoke(t *testing.T) *sql.DB {
	t.Helper()
	root := smokeRoot(t)
	senseDir := t.TempDir()

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Sense:    senseDir,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(senseDir, "index.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestDeadCodeGolden(t *testing.T) {
	db := scanSmoke(t)
	ctx := context.Background()

	result, err := dead.FindDead(ctx, db, dead.Options{Limit: 200})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	rolled := dead.Rollup(result.Dead)

	entries := make([]goldenEntry, len(rolled))
	for i, s := range rolled {
		entries[i] = goldenEntry{s.Qualified, s.Kind, s.File}
	}

	actual := goldenFile{
		Dead:         entries,
		TotalSymbols: result.TotalSymbols,
		DeadCount:    len(rolled),
	}

	if *updateGolden {
		data, err := json.MarshalIndent(actual, "", "  ")
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(goldenPath(t), append(data, '\n'), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated %s: %d dead out of %d total", goldenPath(t), actual.DeadCount, actual.TotalSymbols)
		return
	}

	data, err := os.ReadFile(goldenPath(t))
	if err != nil {
		t.Fatalf("read golden file (run with -update-golden to generate): %v", err)
	}
	var expected goldenFile
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatalf("parse golden: %v", err)
	}

	actualJSON, _ := json.MarshalIndent(actual, "", "  ")
	expectedJSON, _ := json.MarshalIndent(expected, "", "  ")

	if !bytes.Equal(actualJSON, expectedJSON) {
		t.Errorf("dead code output differs from golden file.\nGot:\n%s\nWant:\n%s\n\nRun with -update-golden to regenerate.",
			string(actualJSON), string(expectedJSON))
	}
}

func TestDeadCLIIntegration(t *testing.T) {
	db := scanSmoke(t)
	ctx := context.Background()

	result, err := dead.FindDead(ctx, db, dead.Options{Limit: 200})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	rolled := dead.Rollup(result.Dead)

	if len(rolled) == 0 {
		t.Fatal("expected dead symbols in smoke fixture, got none")
	}

	// Verify known-dead symbols are present.
	deadSet := map[string]bool{}
	for _, s := range rolled {
		deadSet[s.Qualified] = true
	}

	// PaymentGateway.Refund is never called in the smoke fixture.
	if !deadSet["smoke.PaymentGateway.Refund"] {
		t.Error("expected smoke.PaymentGateway.Refund to be dead")
	}

	// Verify known-alive symbols are absent.
	alive := []string{
		"smoke.OrderService.Process",
		"smoke.PaymentGateway.Charge",
		"Order",
		"ApplicationRecord",
		"Trackable",
	}
	for _, name := range alive {
		if deadSet[name] {
			t.Errorf("%s should be alive, but appears in dead set", name)
		}
	}

	// Test files should not appear.
	for _, s := range rolled {
		if s.File == "order_test.go" || s.File == "order_test.rb" {
			t.Errorf("test file symbol %q should be excluded", s.Qualified)
		}
	}
}
