package scan_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/scan"
)

// TestScan_SatisfyViaEmbeddedMethod proves the embedded-method promotion
// path of interface satisfaction: a struct that declares no methods of its
// own but embeds another struct whose method set satisfies an interface must
// still earn the inherits edge. This is the only path that exercises
// promoteEmbeddedMethodSets (loading the includes edges and walking them) and
// the recursive promoteEmbeddedMethods hop that finds the embedded struct in
// the struct map — the plain "struct declares the methods directly" fixture
// never touches it.
func TestScan_SatisfyViaEmbeddedMethod(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "reader.go"), `package mylib

type Reader interface {
	Read() string
}
`)

	// Base declares Read; Derived embeds Base and declares nothing. Derived
	// satisfies Reader only after Base's Read is promoted across the embedding.
	writeFile(t, filepath.Join(root, "impl.go"), `package mylib

type Base struct{}

func (b *Base) Read() string { return "base" }

type Derived struct {
	Base
}
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Derived → Reader inherits edge must exist purely via the embedded method.
	var count int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM sense_edges e
		JOIN sense_symbols s ON s.id = e.source_id
		JOIN sense_symbols t ON t.id = e.target_id
		WHERE e.kind = 'inherits'
		  AND s.name = 'Derived'
		  AND t.name = 'Reader'`).Scan(&count)
	if err != nil {
		t.Fatalf("query inherits edge: %v", err)
	}
	if count == 0 {
		t.Error("expected Derived → Reader inherits edge via promoted embedded method")
	}
}
