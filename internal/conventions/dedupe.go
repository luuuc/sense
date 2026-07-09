package conventions

import (
	"sort"
	"strings"
)

// dedupeRenderedRows resolves conventions that render identically. Twin
// symbols (e.g. a Go interface defined once per build tag) are indexed as
// distinct symbols, so detectors honestly emit one group per symbol — and the
// rows then render byte-identical. Rows whose (category, description) collide
// are merged into one when they describe the same population; when the
// populations genuinely differ, each row is qualified with the shortest path
// suffix of its target's defining file that tells the rows apart. Resolution
// is complete only when the defining files differ: colliding rows with no
// recorded provenance, or defined in the same file, keep their residual
// duplicate rather than a fabricated qualifier. It compacts the slice in
// place and mutates Descriptions; callers must not retain the input.
func dedupeRenderedRows(conventions []Convention) []Convention {
	type rowKey struct {
		category    Category
		description string
	}
	groups := map[rowKey][]int{}
	for i, c := range conventions {
		k := rowKey{c.Category, c.Description}
		groups[k] = append(groups[k], i)
	}
	drop := map[int]bool{}
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		survivors := mergeIdenticalPopulations(conventions, group)
		if len(survivors) < len(group) {
			kept := make(map[int]bool, len(survivors))
			for _, i := range survivors {
				kept[i] = true
			}
			for _, i := range group {
				if !kept[i] {
					drop[i] = true
				}
			}
		}
		if len(survivors) > 1 {
			qualifyByDefiningFile(conventions, survivors)
		}
	}
	if len(drop) == 0 {
		return conventions
	}
	out := conventions[:0]
	for i := range conventions {
		if drop[i] {
			continue
		}
		out = append(out, conventions[i])
	}
	return out
}

// mergeIdenticalPopulations answers one question: which group members
// survive? Members describing the same population — identical instance set
// (compared as a set of Name+Path, never as a bare count) and identical
// Instances/Total — collapse to one survivor each. Counts are kept from that
// one survivor, never summed: the populations are identical by the merge
// condition, so two rows overstate one fact. The survivor is the member with
// the smallest definingPath, keeping the merge deterministic across map
// iteration order. It returns the surviving indices in input order.
func mergeIdenticalPopulations(conventions []Convention, group []int) []int {
	type population struct {
		signature        string
		instances, total int
	}
	survivor := map[population]int{}
	for _, i := range group {
		c := conventions[i]
		p := population{exampleSetSignature(c.Examples), c.Instances, c.Total}
		if j, ok := survivor[p]; !ok || c.definingPath < conventions[j].definingPath {
			survivor[p] = i
		}
	}
	out := make([]int, 0, len(survivor))
	for _, i := range survivor {
		out = append(out, i)
	}
	sort.Ints(out)
	return out
}

// exampleSetSignature keys an example list as an order-insensitive set of
// (Name, Path) members, so population comparison is by set, never by count.
func exampleSetSignature(examples []Example) string {
	keys := make([]string, 0, len(examples))
	seen := map[string]bool{}
	for _, e := range examples {
		k := e.Name + "\x00" + e.Path
		if seen[k] {
			continue
		}
		seen[k] = true
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, "\x01")
}

// qualifyByDefiningFile relabels colliding rows that survived the merge —
// same rendered description over genuinely different populations — by
// appending the shortest trailing path suffix of each row's defining file
// that its siblings do not share (the 28-03 label precedent, one level up).
// A row without a recorded defining file is left as-is, and colliding rows
// defined in the same file receive the same qualifier: in both cases a
// residual duplicate is preferable to a fabricated distinction.
func qualifyByDefiningFile(conventions []Convention, group []int) {
	for _, i := range group {
		if conventions[i].definingPath == "" {
			continue
		}
		siblings := make([]string, 0, len(group)-1)
		for _, j := range group {
			if j != i && conventions[j].definingPath != "" {
				siblings = append(siblings, conventions[j].definingPath)
			}
		}
		suffix := distinguishingPathSuffix(conventions[i].definingPath, siblings)
		conventions[i].Description += " (defined in " + suffix + ")"
	}
}

// distinguishingPathSuffix returns the shortest join of trailing path
// segments of target that no sibling shares, growing one segment at a time;
// the full target path when nothing shorter distinguishes it. Unlike
// uniqueDirSuffix it keeps the basename — build-tag twins live in the same
// directory and differ only there.
func distinguishingPathSuffix(target string, siblings []string) string {
	sibSegs := make([][]string, len(siblings))
	for i, s := range siblings {
		sibSegs[i] = pathSegments(s)
	}
	return shortestUniqueSuffix(pathSegments(target), sibSegs, target)
}

// pathSegments splits a path into all its segments, basename included.
func pathSegments(p string) []string {
	return strings.Split(strings.TrimPrefix(p, "/"), "/")
}
