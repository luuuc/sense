package eval

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"path/filepath"
	"sort"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/dead"
	"github.com/luuuc/sense/internal/scan"
)

// Shared plumbing for the per-language subset gates (cargo.go, staticcheck.go).
// Each oracle binds the same invariant — Sense `dead` ⊆ oracle findings — so the
// Sense side of the join and the violation filter live here once; the files per
// oracle keep only what differs: how the oracle runs and how its output parses.

// symRef identifies a symbol by repo-relative file and bare name — the join key
// between Sense's `dead` set and an oracle's findings. Keyed on name+file rather
// than line/column so the two tools' slightly different position reporting never
// causes a spurious mismatch.
type symRef struct {
	File string
	Name string
}

// DeadSymbol is one symbol Sense earns the `dead` verdict for, carrying the
// fields needed to join against an oracle and to report a violation legibly.
type DeadSymbol struct {
	Qualified string
	File      string
	Name      string
}

// deadSymbols scans repoRoot with Sense and returns the lang symbols it earns
// the `dead` verdict for — the left side of the subset gate.
func deadSymbols(ctx context.Context, repoRoot, senseDir, lang string) ([]DeadSymbol, error) {
	if _, err := scan.Run(ctx, scan.Options{
		Root:     repoRoot,
		Sense:    senseDir,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		return nil, fmt.Errorf("scan %s: %w", repoRoot, err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(senseDir, "index.db"))
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}
	defer func() { _ = db.Close() }()

	res, err := dead.FindDead(ctx, db, dead.Options{Language: lang, Limit: 1000000})
	if err != nil {
		return nil, fmt.Errorf("find dead: %w", err)
	}
	var out []DeadSymbol
	for _, f := range res.Findings {
		if f.Verdict != dead.VerdictDead {
			continue
		}
		out = append(out, DeadSymbol{
			Qualified: f.Symbol.Qualified,
			File:      normalizeRel(f.Symbol.File),
			Name:      f.Symbol.Name,
		})
	}
	return out, nil
}

// falseDeads returns the Sense `dead` symbols absent from the oracle set — the
// gate violations. An empty result is the only passing state: every symbol Sense
// called `dead` is one the oracle independently agrees is unused. Order is
// stable for legible reporting.
func falseDeads(senseDead []DeadSymbol, oracle map[symRef]struct{}) []DeadSymbol {
	var out []DeadSymbol
	for _, d := range senseDead {
		if _, ok := oracle[symRef{File: d.File, Name: d.Name}]; !ok {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Name < out[j].Name
	})
	return out
}
