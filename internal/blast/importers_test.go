package blast_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// writeFile and idOf come from engine_test.go, same package.
//
// setupFacadeGraph builds the minimal shape a Laravel facade produces: a class
// declaring only getFacadeAccessor(), and N files that `use` it and call a method
// it does not declare. The static call binds to nothing, so the ONLY edges in the
// index are `imports` - which is exactly why blast reported total_affected 0 while
// every importer was a real caller.
func setupFacadeGraph(t *testing.T, importers int) (*sql.DB, *sqlite.Adapter) {
	t.Helper()
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "src", "Facades", "Thing.php"), `<?php
namespace App\Facades;

class Thing
{
    protected static function getFacadeAccessor()
    {
        return 'thing';
    }
}
`)
	for i := 0; i < importers; i++ {
		name := fmt.Sprintf("Widget%d", i)
		writeFile(t, filepath.Join(root, "src", "Widgets", name+".php"), fmt.Sprintf(`<?php
namespace App\Widgets;

use App\Facades\Thing;

class %s
{
    public function render()
    {
        return Thing::go('%s');
    }
}
`, name, name))
	}

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, adapter
}

// TestImportersNotReachedAreNamed is the repro. Before this group existed, blast on a
// facade returned total_affected 0 with verdict "complete" while the index held an
// `imports` edge from every calling file. Measured on a real clone: 32 importers, 12
// affected, 22 unreported, and 22 of 22 made a genuine static call.
func TestImportersNotReachedAreNamed(t *testing.T) {
	db, adapter := setupFacadeGraph(t, 3)
	id := idOf(t, adapter, `App\Facades\Thing`)

	res, err := blast.Compute(context.Background(), db, []int64{id}, blast.Options{})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if len(res.ImportersNotReached) != 3 {
		t.Fatalf("ImportersNotReached = %d files, want 3 (the index holds an imports edge from each)",
			len(res.ImportersNotReached))
	}
	if res.ImportersNotReachedCount != 3 {
		t.Errorf("ImportersNotReachedCount = %d, want 3", res.ImportersNotReachedCount)
	}
	// The load-bearing invariant: this group is a WEAKER claim than a call edge, so it
	// must never inflate the radius. Anything else flips the completeness verdict and
	// reproduces the "Sense is unsure, re-grep everything" failure.
	if res.TotalAffected != 0 {
		t.Errorf("TotalAffected = %d, want 0: the importer group must never feed the radius",
			res.TotalAffected)
	}
}

// TestImportersSuppressedWhenOverCap pins the council's ruling that a truncated list is
// worse than none: a partial list is a prompt to go grep, which is the behaviour this
// group exists to prevent. Over the cap the group is ABSENT, and the count still tells
// the truth about how many exist.
func TestImportersSuppressedWhenOverCap(t *testing.T) {
	db, adapter := setupFacadeGraph(t, blast.ImportersCap+2)
	id := idOf(t, adapter, `App\Facades\Thing`)

	res, err := blast.Compute(context.Background(), db, []int64{id}, blast.Options{})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(res.ImportersNotReached) != 0 {
		t.Errorf("ImportersNotReached = %d rows past the cap, want 0 (suppressed, not truncated)",
			len(res.ImportersNotReached))
	}
	if res.ImportersNotReachedCount != blast.ImportersCap+2 {
		t.Errorf("ImportersNotReachedCount = %d, want %d: the count must stay honest when the list is suppressed",
			res.ImportersNotReachedCount, blast.ImportersCap+2)
	}
}

// TestNoImportersGroupWithoutImportEdges is the no-regress guard. `imports` is emitted
// by the PHP extractor only; every other language's blast output must be untouched.
func TestNoImportersGroupWithoutImportEdges(t *testing.T) {
	db, adapter := setupGraph(t) // the ruby fixture: calls edges, no imports
	id := idOf(t, adapter, "C#leaf")

	res, err := blast.Compute(context.Background(), db, []int64{id}, blast.Options{})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(res.ImportersNotReached) != 0 || res.ImportersNotReachedCount != 0 {
		t.Errorf("a graph with no imports edges produced an importer group: %d rows, count %d",
			len(res.ImportersNotReached), res.ImportersNotReachedCount)
	}
}

// TestImporterAlreadyReachedIsNotDoubleListed pins the per-FILE exclusion. PHP attributes
// an import to the file's namespace symbol, never the class symbol a call edge finds, so
// an earlier symbol-id exclusion matched nothing: on filament's FilamentAsset it reported
// all 32 importers instead of the 22 the caller lists had not already covered, re-listing
// rows that were sitting in the answer. Here Caller.php both imports the facade AND calls
// a method the facade really declares, so a call edge reaches it and it must NOT appear.
func TestImporterAlreadyReachedIsNotDoubleListed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "src", "Facades", "Thing.php"), `<?php
namespace App\Facades;

class Thing
{
    public static function declared()
    {
        return 1;
    }
}
`)
	// imports AND calls a declared method -> a real call edge reaches this file
	writeFile(t, filepath.Join(root, "src", "Widgets", "Caller.php"), `<?php
namespace App\Widgets;

use App\Facades\Thing;

class Caller
{
    public function run()
    {
        return Thing::declared();
    }
}
`)
	// imports only, calls an undeclared method -> no call edge, belongs in the group
	writeFile(t, filepath.Join(root, "src", "Widgets", "Importer.php"), `<?php
namespace App\Widgets;

use App\Facades\Thing;

class Importer
{
    public function run()
    {
        return Thing::undeclared();
    }
}
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{Root: root, Output: &bytes.Buffer{}, Warnings: io.Discard}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	res, err := blast.Compute(ctx, db, []int64{idOf(t, adapter, `App\Facades\Thing`)}, blast.Options{})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	for _, f := range res.ImportersNotReached {
		if strings.Contains(f, "Caller.php") {
			t.Errorf("Caller.php is reached by a call edge but was listed as an unreached importer: %v",
				res.ImportersNotReached)
		}
	}
}
