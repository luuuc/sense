package resolve

// golang.go is the Go-specific resolution lane: import-path-verified binding.
// A Go extractor may annotate an edge with the import path its target's
// package was resolved from (the file's own import table). This lane turns
// that annotation into a proof-backed bind under ONE invariant:
//
//	a path-annotated target binds iff its import path lands in an indexed
//	directory; everything else drops.
//
// The invariant is Go-shaped (module paths from go.mod, package-per-directory
// layout), so it lives here and fires only for Go source files, mirroring how
// django.go namespaces Python framework rules. Without module evidence (no
// go.mod collected) the lane never fires and requests degrade to the legacy
// name-keyed lane exactly as before this file existed.

import (
	"path"
	"sort"
	"strings"

	"github.com/luuuc/sense/internal/model"
)

// GoModule is one collected go.mod: its declared module path and the
// repo-relative directory (slash-separated, "." for the root) it governs.
type GoModule struct {
	Path string
	Dir  string
}

// WithGoModules attaches the module table the path lane matches against and
// returns the Index for chaining. Modules sort longest-path-first so nested
// modules (a monorepo sub-go.mod) win over their parents. A module path
// declared by two different directories is ambiguity, never a pick: those
// paths divert to the legacy lane wholesale. Passing an empty slice (or not
// calling) leaves the lane inert.
func (ix *Index) WithGoModules(mods []GoModule) *Index {
	seen := map[string]string{}
	ambiguous := map[string]bool{}
	for _, m := range mods {
		if m.Path == "" {
			continue
		}
		if dir, dup := seen[m.Path]; dup && dir != m.Dir {
			ambiguous[m.Path] = true
			continue
		}
		seen[m.Path] = m.Dir
	}
	for _, m := range mods {
		if ambiguous[m.Path] {
			continue
		}
		ix.goModules = append(ix.goModules, GoModule{Path: m.Path, Dir: path.Clean(m.Dir)})
	}
	sort.Slice(ix.goModules, func(i, j int) bool {
		if len(ix.goModules[i].Path) != len(ix.goModules[j].Path) {
			return len(ix.goModules[i].Path) > len(ix.goModules[j].Path)
		}
		return ix.goModules[i].Path < ix.goModules[j].Path
	})
	ix.goAmbiguousModules = ambiguous
	// Dedup after the ambiguity pass: WithGoModules is exported, so the
	// same (path, dir) listed twice by ANY caller collapses to one entry
	// rather than counting as ambiguity (the scan walk itself visits each
	// go.mod once).
	deduped := ix.goModules[:0]
	var prev GoModule
	for i, m := range ix.goModules {
		if i > 0 && m == prev {
			continue
		}
		deduped = append(deduped, m)
		prev = m
	}
	ix.goModules = deduped
	return ix
}

// WithGoEmbeddings attaches the Go embedding adjacency (embedder qualified
// name → embedded types' qualified names) and returns the Index for
// chaining. Built by scan from resolved includes edges ONLY: satisfaction
// edges are inherits-kind and must never enter (a struct does not acquire
// methods from interfaces it satisfies; a fabricated hop would launder a
// shadow bind into the verified band). Nil leaves the walk a no-op.
func (ix *Index) WithGoEmbeddings(embeddings map[string][]string) *Index {
	ix.goEmbeddings = embeddings
	return ix
}

// resolveGoImportPath is the path lane. The bool handled reports whether the
// lane claimed the request: true means its answer is TERMINAL (a hit, or a
// drop that must not leaf-fall-back); false diverts to the legacy lane (no
// module table, or the path prefix-matches an ambiguous module).
func (ix *Index) resolveGoImportPath(req Request) (Result, bool, bool) {
	if len(ix.goModules) == 0 {
		return Result{}, false, false
	}
	for p := range ix.goAmbiguousModules {
		if importPathHasPrefix(req.TargetImportPath, p) {
			return Result{}, false, false
		}
	}
	var mod *GoModule
	for i := range ix.goModules {
		if importPathHasPrefix(req.TargetImportPath, ix.goModules[i].Path) {
			mod = &ix.goModules[i]
			break // longest-first order: first match is the winner
		}
	}
	if mod == nil {
		// Stdlib or third-party: provably not in the indexed tree. A wrong
		// edge misleads worse than a gap; the target does not exist here.
		return Result{External: true}, false, true
	}
	dir := path.Clean(path.Join(mod.Dir, strings.TrimPrefix(req.TargetImportPath, mod.Path)))
	matches := ix.dirScopedCandidates(dir, req)
	if len(matches) == 0 {
		// A Type.Method target whose method misses its own type may reach
		// the method through Go embedding (APIContext → Context → Base).
		if i := strings.LastIndex(req.TargetInPackage, "."); i > 0 {
			if r, ok := ix.resolveGoEmbedded(dir, req.TargetInPackage[:i], req.TargetInPackage[i+1:], req); ok {
				return r, true, true
			}
		}
		return Result{}, false, true
	}
	return pickBest(matches, req.SourceFileID, req.BaseConfidence), true, true
}

// resolveGoEmbedded walks the embedding chain of dir's `typeName` looking
// for `method`, reusing resolveInherited's mechanics: breadth-first,
// per-depth collection before pickBest decides (Go's shadowing rule: the
// shallowest declaration wins; two at the same depth are a compile-time
// ambiguity and ride the clamp), seen-guard for cycles, maxAncestryDepth as
// the runaway cap (deliberately deeper than satisfy's promotion depth 3;
// deeper is MORE correct for call binding; do not "align" them down).
func (ix *Index) resolveGoEmbedded(dir, typeName, method string, req Request) (Result, bool) {
	if len(ix.goEmbeddings) == 0 {
		return Result{}, false
	}
	seen := map[string]bool{}
	var frontier []string
	for _, pkg := range sortedKeys(ix.dirGoPackages[dir]) {
		q := pkg + "." + typeName
		if len(ix.byQualified[q]) > 0 {
			seen[q] = true
			frontier = append(frontier, ix.goEmbeddings[q]...)
		}
	}
	for depth := 0; depth < maxAncestryDepth && len(frontier) > 0; depth++ {
		var matches []model.SymbolRef
		var next []string
		for _, anc := range frontier {
			if seen[anc] {
				continue
			}
			seen[anc] = true
			matches = append(matches, ix.byQualified[anc+"."+method]...)
			next = append(next, ix.goEmbeddings[anc]...)
		}
		matches = filterByLanguage(matches, ix.fileLang[req.SourceFileID])
		matches = filterByTestDirection(matches, ix.fileIsTest[req.SourceFileID], ix.fileIsTest)
		if len(matches) > 0 {
			return pickBest(matches, req.SourceFileID, req.BaseConfidence), true
		}
		frontier = next
	}
	return Result{}, false
}

// sortedKeys returns a map's keys sorted, so candidate order never depends
// on map iteration.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// dirScopedCandidates finds symbols named <pkg>.<TargetInPackage> for each Go
// package clause seen in dir, keeping only symbols whose file lives in dir.
// Package names iterate sorted so candidate order (and pickBest's tie-break)
// never depends on map order. The standard language and test-direction gates
// apply: an import can only reach the directory's non-test package, and the
// test gate enforces exactly that for production sources.
func (ix *Index) dirScopedCandidates(dir string, req Request) []model.SymbolRef {
	pkgs := sortedKeys(ix.dirGoPackages[dir])
	var matches []model.SymbolRef
	for _, pkg := range pkgs {
		for _, m := range ix.byQualified[pkg+"."+req.TargetInPackage] {
			if ix.fileDir[m.FileID] == dir {
				matches = append(matches, m)
			}
		}
	}
	matches = filterByLanguage(matches, ix.fileLang[req.SourceFileID])
	return filterByTestDirection(matches, ix.fileIsTest[req.SourceFileID], ix.fileIsTest)
}

// importPathHasPrefix reports whether importPath is prefix or exactly equal
// to modPath at a path-segment boundary: corp/app matches corp/app/util but
// never corp/app2.
func importPathHasPrefix(importPath, modPath string) bool {
	return importPath == modPath || strings.HasPrefix(importPath, modPath+"/")
}
