package scan_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// These tests pin import-aware Go resolution end to end: a cross-package
// declared receiver heals through the embedding chain into the verified
// band; a stdlib-typed field stops fabricating a composes edge into a
// same-basename local package; a stdlib qualifier call stops binding at all;
// and an incremental rescan of the caller file alone preserves the healed
// band (the maps must build from persisted state, not pending edges only).

func writeImportAwareRepo(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"go.mod": "module corp/app\n\ngo 1.22\n",
		"services/context/base.go": `package context

type Base struct{}

func (b *Base) FormString(key string) string { return key }
`,
		"services/context/context.go": `package context

type Context struct {
	*Base
}
`,
		"services/context/api.go": `package context

type APIContext struct {
	*Context
}
`,
		// A local package whose basename collides with stdlib context: the
		// fabrication magnet.
		"modules/logimpl/logger.go": `package logimpl

import "context"

type LoggerImpl struct {
	ctx context.Context
}
`,
		"routers/caller.go": `package routers

import (
	"fmt"

	sctx "corp/app/services/context"
)

func ListHooks(ctx *sctx.APIContext) {
	v := ctx.FormString("type")
	fmt.Println(v)
}
`,
	}
	for rel, content := range files {
		writeFile(t, filepath.Join(root, rel), content)
	}
}

func openIndex(t *testing.T, root string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// edgeConfidence returns the confidence of the edge source→target of kind,
// or -1 when no such edge exists.
func edgeConfidence(t *testing.T, db *sql.DB, source, target, kind string) float64 {
	t.Helper()
	row := db.QueryRow(`
		SELECT e.confidence FROM sense_edges e
		JOIN sense_symbols s ON e.source_id = s.id
		JOIN sense_symbols tg ON e.target_id = tg.id
		WHERE s.qualified = ? AND tg.qualified = ? AND e.kind = ?`,
		source, target, kind)
	var conf float64
	if err := row.Scan(&conf); err != nil {
		if err == sql.ErrNoRows {
			return -1
		}
		t.Fatalf("edge query: %v", err)
	}
	return conf
}

func TestImportAwareGoResolutionEndToEnd(t *testing.T) {
	root := t.TempDir()
	writeImportAwareRepo(t, root)
	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	db := openIndex(t, root)

	// The heal: ctx *sctx.APIContext, FormString declared on Base: a
	// two-hop embedding walk binds it in the verified band.
	if conf := edgeConfidence(t, db, "routers.ListHooks", "context.Base.FormString", "calls"); conf < 0.7 {
		t.Errorf("declared-receiver call = %v, want verified band (>= 0.7)", conf)
	}

	// The fabrication kill: LoggerImpl's field is STDLIB context.Context;
	// no composes edge may reach the local shadow package.
	var n int
	if err := db.QueryRow(`
		SELECT count(*) FROM sense_edges e
		JOIN sense_symbols s ON e.source_id = s.id
		JOIN sense_symbols tg ON e.target_id = tg.id
		WHERE s.qualified = 'logimpl.LoggerImpl' AND tg.qualified = 'context.Context'
		AND e.kind = 'composes'`).Scan(&n); err != nil {
		t.Fatalf("composes query: %v", err)
	}
	if n != 0 {
		t.Errorf("stdlib field fabricated %d composes edge(s) into the local shadow", n)
	}

	// The qualifier kill: fmt.Println must bind nothing.
	if err := db.QueryRow(`
		SELECT count(*) FROM sense_edges e
		JOIN sense_symbols s ON e.source_id = s.id
		WHERE s.qualified = 'routers.ListHooks' AND e.kind = 'calls'
		AND e.target_id IN (SELECT id FROM sense_symbols WHERE name = 'Println')`).Scan(&n); err != nil {
		t.Fatalf("println query: %v", err)
	}
	if n != 0 {
		t.Errorf("stdlib qualifier call bound %d edge(s)", n)
	}
}

func TestImportAwareGoResolutionSurvivesIncremental(t *testing.T) {
	root := t.TempDir()
	writeImportAwareRepo(t, root)
	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Touch ONLY the caller file: the embedding chain and module table live
	// in untouched files, so a pending-only map would starve and the healed
	// edge would regress to the demoted band (the load-bearing incremental
	// leg; mutant M3's kill case).
	writeFile(t, filepath.Join(root, "routers/caller.go"), `package routers

import (
	"fmt"

	sctx "corp/app/services/context"
)

func ListHooks(ctx *sctx.APIContext) {
	v := ctx.FormString("kind")
	fmt.Println(v, "edited")
}
`)
	ctx := context.Background()
	idx, err := sqlite.Open(ctx, filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = idx.Close() }()
	matcher, err := ignore.Build(root, nil)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}
	if _, err := scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:    root,
		Idx:     idx,
		Matcher: matcher,
		Changed: []string{"routers/caller.go"},
	}); err != nil {
		t.Fatalf("RunIncremental: %v", err)
	}

	db := openIndex(t, root)
	if conf := edgeConfidence(t, db, "routers.ListHooks", "context.Base.FormString", "calls"); conf < 0.7 {
		t.Errorf("healed band regressed to %v on incremental rescan of the caller alone", conf)
	}
}
