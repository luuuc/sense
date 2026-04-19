package scan_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/scan"
)

// updateCrossFile regenerates the expected.golden.json files under
// testdata/crossfile/. Enable by passing `-update` to `go test
// ./internal/scan` — the flag name matches the convention set by
// `internal/extract`, and package-scoped test binaries keep the two
// uses independent. Commit the resulting diffs deliberately.
var updateCrossFile = flag.Bool(
	"update",
	false,
	"regenerate cross-file-fixture goldens instead of asserting against them",
)

// crossFileRoot is the parent directory holding one subdirectory per
// named topology. Each subdir is self-contained: its source files
// are scanned, its expected.golden.json captures the cross-file
// edges that should result.
const crossFileRoot = "testdata/crossfile"

// TestCrossFileFixtures pins pitch 01-03's four named cross-file
// topologies as fixtures with golden-file assertions:
//
//	call_across_files    — Caller calls into Callee in a sibling file.
//	inherit_across_files — Child extends Base from a sibling file.
//	include_across_files — Widget mixes in a module from a sibling.
//	tests_across_files   — _test.go sibling lands `tests` edges on impl symbols.
//
// Each fixture runs through the full scan pipeline (parse → extract
// → resolve → tests-associate) and the golden captures only the
// cross-file edges — intra-file edges are filtered out so the
// assertion speaks precisely to the topology under test.
func TestCrossFileFixtures(t *testing.T) {
	entries, err := os.ReadDir(crossFileRoot)
	if err != nil {
		t.Fatalf("read %s: %v", crossFileRoot, err)
	}
	var fixtures []string
	for _, e := range entries {
		if e.IsDir() {
			fixtures = append(fixtures, e.Name())
		}
	}
	if len(fixtures) == 0 {
		t.Fatalf("no fixtures under %s", crossFileRoot)
	}
	sort.Strings(fixtures)

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			runCrossFileFixture(t, name)
		})
	}
}

func runCrossFileFixture(t *testing.T, name string) {
	t.Helper()

	fixtureDir := filepath.Join(crossFileRoot, name)
	goldenPath := filepath.Join(fixtureDir, "expected.golden.json")

	// Scan with a throwaway .sense directory so the source tree
	// stays free of index artefacts. Root still points at the
	// fixture so tree-walking picks up the source files as-is.
	senseDir := t.TempDir()
	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     fixtureDir,
		Sense:    senseDir,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	got, err := loadCrossFileEdges(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatalf("load cross-file edges: %v", err)
	}

	if *updateCrossFile {
		writeCrossFileGolden(t, goldenPath, got)
		return
	}

	wantBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v — run `go test ./internal/scan -update` after reviewing", goldenPath, err)
	}
	var want crossFileOutput
	if err := json.Unmarshal(wantBytes, &want); err != nil {
		t.Fatalf("parse golden %s: %v", goldenPath, err)
	}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		wantJSON, _ := json.MarshalIndent(want, "", "  ")
		t.Errorf("%s mismatch\n--- want\n%s\n--- got\n%s", name, wantJSON, gotJSON)
	}
}

// crossFileOutput is the on-disk golden shape. One sorted list of
// cross-file edges with qualified names and file paths on both
// endpoints so a reader can understand the topology without running
// queries. Intra-file edges are excluded by loadCrossFileEdges.
type crossFileOutput struct {
	Edges []crossFileEdge `json:"edges"`
}

type crossFileEdge struct {
	Source     string  `json:"source"`
	SourceFile string  `json:"source_file"`
	Target     string  `json:"target"`
	TargetFile string  `json:"target_file"`
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence"`
}

// loadCrossFileEdges opens the scanned index and returns every
// resolved edge whose source and target live in different files.
// Intra-file edges are dropped so a fixture's golden asserts
// precisely the cross-file contract the topology was designed to
// prove.
func loadCrossFileEdges(ctx context.Context, dbPath string) (crossFileOutput, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		return crossFileOutput{}, err
	}
	defer func() { _ = db.Close() }()

	const q = `
		SELECT
			ss.qualified, fs.path,
			st.qualified, ft.path,
			e.kind, e.confidence
		FROM sense_edges e
		JOIN sense_symbols ss ON ss.id = e.source_id
		JOIN sense_symbols st ON st.id = e.target_id
		JOIN sense_files   fs ON fs.id = ss.file_id
		JOIN sense_files   ft ON ft.id = st.file_id
		WHERE fs.id != ft.id
		ORDER BY ss.qualified, st.qualified, e.kind`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return crossFileOutput{}, err
	}
	defer func() { _ = rows.Close() }()

	out := crossFileOutput{Edges: []crossFileEdge{}}
	for rows.Next() {
		var e crossFileEdge
		if err := rows.Scan(&e.Source, &e.SourceFile, &e.Target, &e.TargetFile, &e.Kind, &e.Confidence); err != nil {
			return crossFileOutput{}, err
		}
		out.Edges = append(out.Edges, e)
	}
	return out, rows.Err()
}

func writeCrossFileGolden(t *testing.T, path string, out crossFileOutput) {
	t.Helper()
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden %s: %v", path, err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write golden %s: %v", path, err)
	}
}
