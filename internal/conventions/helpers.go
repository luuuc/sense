package conventions

import (
	"path"
	"sort"
	"strings"

	"github.com/luuuc/sense/internal/extract"
)

// dropSyntheticSymbols removes the synthetic cross-language/framework symbols
// (i18n keys, route helpers, turbo channels, importmap entries, ruby-core
// shims) the extractors emit for resolution. They are plumbing, not project
// declarations, keyed on the qualified-name prefixes owned by the extract
// package. It drops the full synthetic set via extract.IsSyntheticQualified;
// search and dead-code drop only the narrower subset that reaches their output.
// It returns a new slice rather than filtering in place, leaving the caller's
// input untouched.
func dropSyntheticSymbols(symbols []symbolRow) []symbolRow {
	out := make([]symbolRow, 0, len(symbols))
	for _, s := range symbols {
		if extract.IsSyntheticQualified(s.qualified) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func indexSymbols(symbols []symbolRow) map[int64]symbolRow {
	m := make(map[int64]symbolRow, len(symbols))
	for _, s := range symbols {
		m[s.id] = s
	}
	return m
}

func extractSuffix(name string) string {
	// CamelCase suffix: "CheckoutService" -> "Service"
	// Snake_case suffix: "checkout_service" -> "_service"
	if idx := strings.LastIndex(name, "_"); idx > 0 && idx < len(name)-1 {
		return name[idx:]
	}
	// CamelCase: find last uppercase letter that starts a word
	lastUpper := -1
	for i := len(name) - 1; i > 0; i-- {
		if name[i] >= 'A' && name[i] <= 'Z' {
			lastUpper = i
			break
		}
	}
	if lastUpper > 0 {
		return name[lastUpper:]
	}
	return ""
}

// ambiguousTargetNames returns the bare names shared by more than one distinct
// target id. Such names need qualifying so the rendered conventions do not read
// as duplicate lines (e.g. several "Base" classes in different namespaces each
// inherited by a different family). nameByID holds one entry per distinct target.
func ambiguousTargetNames(nameByID map[int64]string) map[string]bool {
	counts := map[string]int{}
	for _, name := range nameByID {
		counts[name]++
	}
	out := map[string]bool{}
	for name, n := range counts {
		if n > 1 {
			out[name] = true
		}
	}
	return out
}

// baseLabel renders a base/mixin target name, qualifying it only when the bare
// name is ambiguous (shared by several distinct targets) and a distinct qualified
// name is available. This keeps the common case terse while disambiguating
// collisions, so "extend Base" lines do not read as duplicates. In the rare case
// where the qualified names also collide (two "Foo::Base" in different files), it
// falls back to the bare name rather than render a misleading partial path; that
// residual duplicate is accepted as not worth a path-suffix escalation here.
func baseLabel(name, qualified string, ambiguous map[string]bool) string {
	if ambiguous[name] && qualified != "" && qualified != name {
		return qualified
	}
	return name
}

// categoryOrder ranks categories by how much they reveal a project's own
// architecture versus generic, framework-default structure the caller already
// knows. Inheritance, framework idioms, design patterns, and composition carry
// the project's character and lead; naming/architecture-layers/structure are
// progressively more generic and trail, so they yield first to the response's
// token budget. This order is part of the tool's contract.
func categoryOrder(c Category) int {
	switch c {
	case CategoryInheritance:
		return 0
	case CategoryFramework:
		return 1
	case CategoryDesignPattern:
		return 2
	case CategoryComposition:
		return 3
	case CategoryKeyTypes:
		return 4
	case CategoryNaming:
		return 5
	case CategoryArchitecture:
		return 6
	case CategoryStructure:
		return 7
	case CategoryTesting:
		return 8
	}
	return 9
}

func hasMatchingExample(examples []Example, domain string) bool {
	for _, e := range examples {
		if strings.Contains(e.Path, domain) {
			return true
		}
	}
	return false
}

// sortExamples orders examples for alphabetical grouping by Path, with a Name
// tiebreak so same-Path examples have a defined order. It is Path-first by
// intent (the one exception to the EdgeCount-first lessExample family), so it
// keeps its own inline cascade rather than sharing lessExample.
func sortExamples(examples []Example) {
	sort.Slice(examples, func(i, j int) bool {
		a, b := examples[i], examples[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Name < b.Name
	})
}

// lessExample is the shared EdgeCount-first total order for ranking examples by
// importance: EdgeCount descending, then Name ascending, then Path ascending.
// Every cascade falls through on equality so sort.Slice is well-defined (strict
// weak ordering). This is the single correct cascade for the EdgeCount-first
// sites to copy; sortExamples (Path-first) keeps its own shape by intent.
func lessExample(a, b Example) bool {
	if a.EdgeCount != b.EdgeCount {
		return a.EdgeCount > b.EdgeCount
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.Path < b.Path
}

// pickRepresentatives is the shared pick: dedupe by Name+Path, sort by the
// EdgeCount-first total order, and take the top `limit` examples. The two public
// renderers build on it — PickRepresentatives projects the raw Names (a lookup
// key into sense_symbols), RepresentativeLabels disambiguates collisions for
// display. They must select the identical set, so the pick lives here once.
func pickRepresentatives(examples []Example, limit int) []Example {
	if len(examples) == 0 {
		return nil
	}
	// Dedupe by Name+Path so the sort below is a true total order: detectNaming
	// emits many examples sharing a (Name, Path), and enrichEdgeCounts collapses
	// them to the same EdgeCount, so EdgeCount/Name/Path can all tie and a bare
	// tiebreak would still leave map order showing. Dedupe makes Name+Path unique
	// by construction, then sort EdgeCount → Name → Path.
	sorted := dedupeExamples(examples)
	sort.Slice(sorted, func(i, j int) bool {
		return lessExample(sorted[i], sorted[j])
	})
	n := limit
	if n > len(sorted) {
		n = len(sorted)
	}
	return sorted[:n]
}

// PickRepresentatives returns the raw Names of the top representatives. These
// are symbol/file names used as a lookup key (lookupInstanceSnippets keys on
// sense_symbols.name), so they stay bare even when two share a name — use
// RepresentativeLabels for anything shown to a reader.
func PickRepresentatives(examples []Example, limit int) []string {
	picked := pickRepresentatives(examples, limit)
	if len(picked) == 0 {
		return nil
	}
	names := make([]string, len(picked))
	for i, e := range picked {
		names[i] = e.Name
	}
	return names
}

// RepresentativeLabels returns display labels for the top representatives: bare
// Name when unique within the picked set, else the Name with the shortest
// trailing path segments that distinguish it from its same-named siblings, e.g.
// "_add_modal.html.erb (categories/categorizations)". The same convention can
// surface the same displayed name from several distinct files; a bare repeat
// reads as one example shown N times rather than N real files.
func RepresentativeLabels(examples []Example, limit int) []string {
	picked := pickRepresentatives(examples, limit)
	return disambiguateLabels(picked)
}

func topNames(examples []Example) string {
	return strings.Join(RepresentativeLabels(examples, maxDescriptionNames), ", ")
}

// disambiguateLabels labels each picked example: bare Name when its Name is
// unique in the set, else Name + " (suffix)" where suffix is the shortest
// trailing directory segments of its Path that distinguish it from its
// same-named siblings. dedupeExamples guarantees every picked example has a
// distinct (Name, Path), so a group's full Paths are always distinct — when the
// per-member dir suffixes still collide (a dir suffix and a full-Path fallback
// live in different string spaces, so a shallow member's fallback can equal a
// deeper sibling's suffix), escalateColliding relabels the whole group with full
// Paths, guaranteeing every label in a group is unique.
func disambiguateLabels(picked []Example) []string {
	groups := make(map[string][]int, len(picked))
	for i, e := range picked {
		groups[e.Name] = append(groups[e.Name], i)
	}
	labels := make([]string, len(picked))
	for i, e := range picked {
		group := groups[e.Name]
		if len(group) == 1 {
			labels[i] = e.Name
			continue
		}
		labels[i] = e.Name + " (" + uniqueDirSuffix(e.Path, siblingPaths(picked, group, i)) + ")"
	}
	escalateColliding(picked, groups, labels)
	return labels
}

// siblingPaths returns the Paths of the group members other than i.
func siblingPaths(picked []Example, group []int, i int) []string {
	paths := make([]string, 0, len(group)-1)
	for _, j := range group {
		if j != i {
			paths = append(paths, picked[j].Path)
		}
	}
	return paths
}

// uniqueDirSuffix returns the shortest join of trailing directory segments of
// target that no sibling shares at the same depth. The depth grows one segment
// at a time (one segment is not always enough: two files can share a trailing
// dir, e.g. .../categories/categorizations and .../navigations/categorizations).
// If the directory segments are exhausted without distinguishing target — its
// only dir suffix is shared by a deeper sibling, or a sibling differs only in
// basename — the full target Path is returned as the fallback.
func uniqueDirSuffix(target string, siblings []string) string {
	segs := dirSegments(target)
	for k := 1; k <= len(segs); k++ {
		cand := suffixOf(segs, k)
		collision := false
		for _, s := range siblings {
			other := dirSegments(s)
			if len(other) >= k && suffixOf(other, k) == cand {
				collision = true
				break
			}
		}
		if !collision {
			return cand
		}
	}
	return target
}

// escalateColliding is the airtight backstop. A dir-suffix label and a full-Path
// fallback occupy different string spaces, so a shallow member's fallback can
// still equal a deeper sibling's dir suffix (e.g. fallback "foo/bar" vs suffix
// "foo/bar"). If any same-named group still holds a duplicate label, relabel that
// whole group with full Paths, distinct by dedupeExamples. It only fires on the
// rare residual collision, leaving the minimal dir-suffix labels untouched.
func escalateColliding(picked []Example, groups map[string][]int, labels []string) {
	for _, group := range groups {
		if len(group) < 2 || labelsDistinct(group, labels) {
			continue
		}
		for _, i := range group {
			labels[i] = picked[i].Name + " (" + picked[i].Path + ")"
		}
	}
}

// labelsDistinct reports whether the labels at the given indices are all unique.
func labelsDistinct(group []int, labels []string) bool {
	seen := make(map[string]bool, len(group))
	for _, i := range group {
		if seen[labels[i]] {
			return false
		}
		seen[labels[i]] = true
	}
	return true
}

// dirSegments splits the directory portion of a path into segments, dropping the
// basename. A path with no directory (root-level file) yields no segments.
func dirSegments(p string) []string {
	dir := path.Dir(p)
	if dir == "." || dir == "/" || dir == "" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(dir, "/"), "/")
}

// suffixOf joins the last k segments with "/".
func suffixOf(segs []string, k int) string {
	return strings.Join(segs[len(segs)-k:], "/")
}

func extractFileSuffix(basename string) string {
	// "checkout_service.rb" -> "_service.rb"
	// "orders_controller.rb" -> "_controller.rb"
	ext := path.Ext(basename)
	nameOnly := strings.TrimSuffix(basename, ext)
	if idx := strings.LastIndex(nameOnly, "_"); idx > 0 {
		return nameOnly[idx:] + ext
	}
	return ""
}

func countParents(symbols []symbolRow) int {
	n := 0
	for _, s := range symbols {
		if s.parentID == nil && (s.kind == "class" || s.kind == "struct") {
			n++
		}
	}
	return n
}

func countByKind(symbols []symbolRow, kinds ...string) int {
	kindSet := map[string]bool{}
	for _, k := range kinds {
		kindSet[k] = true
	}
	n := 0
	for _, s := range symbols {
		if kindSet[s.kind] {
			n++
		}
	}
	return n
}

func safeStrength(instances, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(instances) / float64(total)
}

func pluralize(kind string) string {
	if strings.HasSuffix(kind, "ss") || strings.HasSuffix(kind, "ch") ||
		strings.HasSuffix(kind, "sh") || strings.HasSuffix(kind, "x") {
		return kind + "es"
	}
	if strings.HasSuffix(kind, "s") {
		return kind + "es"
	}
	return kind + "s"
}

func enrichEdgeCounts(conventions []Convention, symbols []symbolRow, edges []edgeRow, filePathByID map[int64]string) {
	type namePathKey struct{ name, path string }
	inbound := make(map[int64]int, len(symbols))
	for _, e := range edges {
		inbound[e.targetID]++
	}
	lookup := make(map[namePathKey]int, len(symbols))
	for _, s := range symbols {
		key := namePathKey{s.name, filePathByID[s.fileID]}
		if c := inbound[s.id]; c > lookup[key] {
			lookup[key] = c
		}
	}
	for ci := range conventions {
		for ei := range conventions[ci].Examples {
			ex := &conventions[ci].Examples[ei]
			if ex.EdgeCount != 0 {
				continue
			}
			ex.EdgeCount = lookup[namePathKey{ex.Name, ex.Path}]
		}
	}
}

func dedupeExamples(examples []Example) []Example {
	seen := map[string]bool{}
	var out []Example
	for _, e := range examples {
		key := e.Name + ":" + e.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out
}
