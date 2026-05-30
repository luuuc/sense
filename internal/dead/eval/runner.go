package eval

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/dead"
	"github.com/luuuc/sense/internal/scan"
)

// Fixture is one labeled case: a small synthetic project (relative path →
// source) and the ground-truth verdicts for symbols it defines. Keeping
// each case its own project isolates framework detection and edge
// resolution so a label means exactly what it says.
type Fixture struct {
	Name  string
	Files map[string]string
	Want  []Sym
}

// FixtureResult pairs a fixture with the verdicts the engine produced for
// it, for per-case diagnostics.
type FixtureResult struct {
	Name string
	Got  map[string]Verdict
}

// Materialize writes a fixture's files under root, creating parent
// directories as needed.
func Materialize(root string, files map[string]string) error {
	for rel, src := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

// Classify scans an already-materialized project at root (writing its index
// under senseDir) and returns the observable per-symbol verdicts. It is the
// bridge from "source on disk" to the verdict map the scorer consumes.
func Classify(ctx context.Context, root, senseDir string) (map[string]Verdict, error) {
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Sense:    senseDir,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		return nil, fmt.Errorf("scan %s: %w", root, err)
	}
	return classifyDB(ctx, filepath.Join(senseDir, "index.db"))
}

// classifyDB opens an existing index and runs the decision layer, returning
// the observable per-symbol verdicts. Split from Classify so the decision
// layer can be scored against a hand-crafted index without a scan.
func classifyDB(ctx context.Context, dbPath string) (map[string]Verdict, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Limit high enough that nothing is truncated — the corpus is small and
	// a dropped candidate would silently distort the score.
	res, err := dead.FindDead(ctx, db, dead.Options{Limit: 100000})
	if err != nil {
		return nil, fmt.Errorf("find dead: %w", err)
	}
	return VerdictsFrom(res), nil
}

// RunCorpus materializes, scans, and scores every fixture under workdir
// (each in its own subdirectory) and returns the aggregate Report plus the
// per-fixture verdicts. The aggregate Score is taken over the union of all
// fixtures' labels, so DeadPrecision is the harness's single headline
// number: the fraction of the engine's `dead` calls that were true.
func RunCorpus(ctx context.Context, workdir string, corpus []Fixture) (Report, []FixtureResult, error) {
	got := make(map[string]Verdict)
	var want []Sym
	results := make([]FixtureResult, 0, len(corpus))

	for i, f := range corpus {
		root := filepath.Join(workdir, fmt.Sprintf("fixture-%02d", i))
		senseDir := filepath.Join(root, ".sense")
		if err := Materialize(root, f.Files); err != nil {
			return Report{}, nil, fmt.Errorf("fixture %q: %w", f.Name, err)
		}
		v, err := Classify(ctx, root, senseDir)
		if err != nil {
			return Report{}, nil, fmt.Errorf("fixture %q: %w", f.Name, err)
		}
		results = append(results, FixtureResult{Name: f.Name, Got: v})
		for k, val := range v {
			got[k] = val
		}
		want = append(want, f.Want...)
	}

	return Score(got, want), results, nil
}
