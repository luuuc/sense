package conventions

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/luuuc/sense/internal/mcpio"
)

// isTestFile reports whether the file with the given id is a test file. The
// domain-structure detectors skip test files so test scaffolding (TestCase
// subclasses, *_test.rb naming, test/ grouping) does not drown the real domain
// patterns; test-file naming is reported separately by detectTesting.
//
// This intentionally uses the path predicate mcpio.IsTestPath directly, the same
// signal detectKeyTypes trusts, rather than collectTestFileIDs' "tests"-edge set
// (which detectTesting uses). The path predicate is the simpler, more complete
// signal for exclusion: it catches every test file, not only those with a
// resolved tests edge. The two notions can diverge in principle; for exclusion
// the more inclusive one is the right default.
func isTestFile(filePathByID map[int64]string, fileID int64) bool {
	return mcpio.IsTestPath(filePathByID[fileID])
}

// domainKindCounts counts symbols per kind, excluding those in test files. It is
// the denominator for the domain-structure detectors so test scaffolding does
// not dilute the prevalence of domain conventions.
func domainKindCounts(symbols []symbolRow, filePathByID map[int64]string) map[string]int {
	counts := map[string]int{}
	for _, s := range symbols {
		if isTestFile(filePathByID, s.fileID) {
			continue
		}
		counts[s.kind]++
	}
	return counts
}

func detectInheritance(symbols []symbolRow, edges []edgeRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Convention {

	type inheritGroup struct {
		targetID        int64
		targetName      string
		targetQualified string
		sourceKind      string
		count           int
		examples        []Example
	}

	type groupKey struct {
		targetID   int64
		sourceKind string
	}
	groups := map[groupKey]*inheritGroup{}

	for _, e := range edges {
		if e.kind != "inherits" {
			continue
		}
		src, ok := symbolByID[e.sourceID]
		if !ok {
			continue
		}
		if isTestFile(filePathByID, src.fileID) {
			continue
		}
		tgt, ok := symbolByID[e.targetID]
		if !ok {
			continue
		}
		key := groupKey{targetID: e.targetID, sourceKind: src.kind}
		if g, exists := groups[key]; exists {
			g.count++
			g.examples = append(g.examples, Example{Name: src.name, Path: filePathByID[src.fileID]})
		} else {
			groups[key] = &inheritGroup{
				targetID:        e.targetID,
				targetName:      tgt.name,
				targetQualified: tgt.qualified,
				sourceKind:      src.kind,
				count:           1,
				examples:        []Example{{Name: src.name, Path: filePathByID[src.fileID]}},
			}
		}
	}

	kindCounts := domainKindCounts(symbols, filePathByID)

	nameByID := map[int64]string{}
	for _, g := range groups {
		if g.count >= minInstances {
			nameByID[g.targetID] = g.targetName
		}
	}
	ambiguous := ambiguousTargetNames(nameByID)

	var out []Convention
	for _, g := range groups {
		if g.count < minInstances {
			continue
		}
		// total is always >= g.count here: every group source is a non-test
		// symbol of g.sourceKind, so domainKindCounts has counted it. safeStrength
		// keeps the ratio well-defined without an unreachable zero guard.
		total := kindCounts[g.sourceKind]
		sortExamples(g.examples)
		label := baseLabel(g.targetName, g.targetQualified, ambiguous)
		out = append(out, Convention{
			Category:    CategoryInheritance,
			Description: fmt.Sprintf("%d %s extend %s as a base class (%s)", g.count, pluralize(g.sourceKind), label, topNames(g.examples)),
			Instances:   g.count,
			Total:       total,
			Strength:    safeStrength(g.count, total),
			Examples:    g.examples,
			KeySymbol:   g.targetQualified,
		})
	}
	return out
}

// detectNaming reports symbol-name and file-name suffix conventions. It is split
// into the two passes so each stays within the complexity budget; both exclude
// test files so test scaffolding does not dilute the domain naming patterns.
func detectNaming(symbols []symbolRow, filePathByID map[int64]string) []Convention {
	var out []Convention
	out = append(out, detectSymbolSuffixNaming(symbols, filePathByID)...)
	out = append(out, detectFileSuffixNaming(symbols, filePathByID)...)
	return out
}

// detectSymbolSuffixNaming finds top-level symbols whose names share a CamelCase
// or snake_case suffix (e.g. *Service), excluding test files.
func detectSymbolSuffixNaming(symbols []symbolRow, filePathByID map[int64]string) []Convention {
	type kindSuffix struct {
		kind   string
		suffix string
	}
	suffixCounts := map[kindSuffix]int{}
	suffixExamples := map[kindSuffix][]Example{}
	kindCounts := map[string]int{}

	for _, s := range symbols {
		if s.parentID != nil {
			continue
		}
		if isTestFile(filePathByID, s.fileID) {
			continue
		}
		kindCounts[s.kind]++

		suffix := extractSuffix(s.name)
		if suffix == "" {
			continue
		}
		ks := kindSuffix{kind: s.kind, suffix: suffix}
		suffixCounts[ks]++
		suffixExamples[ks] = append(suffixExamples[ks], Example{Name: s.name, Path: filePathByID[s.fileID]})
	}

	var out []Convention
	for ks, count := range suffixCounts {
		if count < minInstances {
			continue
		}
		total := kindCounts[ks.kind]
		if total == 0 {
			continue
		}
		ex := suffixExamples[ks]
		sortExamples(ex)
		out = append(out, Convention{
			Category:    CategoryNaming,
			Description: fmt.Sprintf("%s use *%s naming convention (%s — %d of %d)", pluralize(ks.kind), ks.suffix, topNames(ex), count, total),
			Instances:   count,
			Total:       total,
			Strength:    float64(count) / float64(total),
			Examples:    ex,
		})
	}
	return out
}

// detectFileSuffixNaming finds top-level symbols whose file basenames share a
// suffix (e.g. *_controller.rb), excluding test files.
func detectFileSuffixNaming(symbols []symbolRow, filePathByID map[int64]string) []Convention {
	type kindFileSuffix struct {
		kind   string
		suffix string
	}
	fileSuffixCounts := map[kindFileSuffix]int{}
	fileSuffixExamples := map[kindFileSuffix][]Example{}
	kindFileCounts := map[string]int{}

	for _, s := range symbols {
		if s.parentID != nil {
			continue
		}
		fp, ok := filePathByID[s.fileID]
		if !ok {
			continue
		}
		if isTestFile(filePathByID, s.fileID) {
			continue
		}
		base := path.Base(fp)
		kindFileCounts[s.kind]++
		fileSuffix := extractFileSuffix(base)
		if fileSuffix == "" {
			continue
		}
		kfs := kindFileSuffix{kind: s.kind, suffix: fileSuffix}
		fileSuffixCounts[kfs]++
		fileSuffixExamples[kfs] = append(fileSuffixExamples[kfs], Example{Name: base, Path: fp})
	}

	var out []Convention
	for kfs, count := range fileSuffixCounts {
		if count < minInstances {
			continue
		}
		total := kindFileCounts[kfs.kind]
		if total == 0 {
			continue
		}
		ex := fileSuffixExamples[kfs]
		sortExamples(ex)
		out = append(out, Convention{
			Category:    CategoryNaming,
			Description: fmt.Sprintf("%s files use *%s naming convention (%s — %d of %d)", kfs.kind, kfs.suffix, topNames(ex), count, total),
			Instances:   count,
			Total:       total,
			Strength:    float64(count) / float64(total),
			Examples:    ex,
		})
	}
	return out
}

func detectStructure(symbols []symbolRow, filePathByID map[int64]string) []Convention {
	type kindDir struct {
		kind string
		dir  string
	}
	dirCounts := map[kindDir]int{}
	dirExamples := map[kindDir][]Example{}
	kindCounts := map[string]int{}

	for _, s := range symbols {
		if s.parentID != nil {
			continue
		}
		fp, ok := filePathByID[s.fileID]
		if !ok {
			continue
		}
		if isTestFile(filePathByID, s.fileID) {
			continue
		}
		dir := path.Dir(fp)
		kindCounts[s.kind]++
		kd := kindDir{kind: s.kind, dir: dir}
		dirCounts[kd]++
		dirExamples[kd] = append(dirExamples[kd], Example{Name: s.name, Path: fp})
	}

	var out []Convention
	for kd, count := range dirCounts {
		if count < minInstances {
			continue
		}
		total := kindCounts[kd.kind]
		if total == 0 {
			continue
		}
		ex := dirExamples[kd]
		sortExamples(ex)
		out = append(out, Convention{
			Category:    CategoryStructure,
			Description: fmt.Sprintf("%s are grouped in %s/ (%s — %d of %d)", pluralize(kd.kind), kd.dir, topNames(ex), count, total),
			Instances:   count,
			Total:       total,
			Strength:    float64(count) / float64(total),
			Examples:    ex,
		})
	}
	return out
}

func detectComposition(symbols []symbolRow, edges []edgeRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Convention {

	type groupKey struct {
		targetID   int64
		sourceKind string
	}
	type compGroup struct {
		targetID        int64
		targetName      string
		targetQualified string
		sourceKind      string
		count           int
		examples        []Example
	}
	groups := map[groupKey]*compGroup{}

	for _, e := range edges {
		if e.kind != "includes" && e.kind != "composes" {
			continue
		}
		src, ok := symbolByID[e.sourceID]
		if !ok {
			continue
		}
		if isTestFile(filePathByID, src.fileID) {
			continue
		}
		tgt, ok := symbolByID[e.targetID]
		if !ok {
			continue
		}
		key := groupKey{targetID: e.targetID, sourceKind: src.kind}
		if g, exists := groups[key]; exists {
			g.count++
			g.examples = append(g.examples, Example{Name: src.name, Path: filePathByID[src.fileID]})
		} else {
			groups[key] = &compGroup{
				targetID:        e.targetID,
				targetName:      tgt.name,
				targetQualified: tgt.qualified,
				sourceKind:      src.kind,
				count:           1,
				examples:        []Example{{Name: src.name, Path: filePathByID[src.fileID]}},
			}
		}
	}

	kindCounts := domainKindCounts(symbols, filePathByID)

	nameByID := map[int64]string{}
	for _, g := range groups {
		if g.count >= minInstances {
			nameByID[g.targetID] = g.targetName
		}
	}
	ambiguous := ambiguousTargetNames(nameByID)

	var out []Convention
	var serializerExamples []Example
	for _, g := range groups {
		if g.count < minInstances {
			continue
		}
		// total is always >= g.count here (see detectInheritance): the group
		// source is a counted non-test symbol, so safeStrength needs no zero guard.
		total := kindCounts[g.sourceKind]
		sortExamples(g.examples)
		if strings.HasSuffix(g.targetName, "Serializer") {
			serializerExamples = append(serializerExamples, g.examples...)
			continue
		}
		label := baseLabel(g.targetName, g.targetQualified, ambiguous)
		out = append(out, Convention{
			Category:    CategoryComposition,
			Description: fmt.Sprintf("%d %s mix in %s for shared behavior (%s)", g.count, pluralize(g.sourceKind), label, topNames(g.examples)),
			Instances:   g.count,
			Total:       total,
			Strength:    safeStrength(g.count, total),
			Examples:    g.examples,
			KeySymbol:   g.targetQualified,
		})
	}
	if len(serializerExamples) >= minInstances {
		sortExamples(serializerExamples)
		totalClasses := kindCounts["class"]
		out = append(out, Convention{
			Category:    CategoryComposition,
			Description: fmt.Sprintf("Serializer composition: %d classes use custom serializers (%s)", len(serializerExamples), topNames(serializerExamples)),
			Instances:   len(serializerExamples),
			Total:       totalClasses,
			Strength:    safeStrength(len(serializerExamples), totalClasses),
			Examples:    serializerExamples,
		})
	}
	return out
}

// testPattern is a test-file naming family: a display label (e.g. "_test.go",
// "*Test.java"), a predicate matching base names in that family, and how many
// test files fell into it.
type testPattern struct {
	label   string
	matches func(base string) bool
	count   int
}

func detectTesting(_ []symbolRow, edges []edgeRow, filePathByID map[int64]string, symbolByID map[int64]symbolRow) []Convention {
	testFileIDs := collectTestFileIDs(edges, filePathByID, symbolByID)
	if len(testFileIDs) < minInstances {
		return nil
	}
	patterns := classifyTestFiles(testFileIDs, filePathByID)
	return buildTestingConventions(patterns, testFileIDs, filePathByID)
}

// collectTestFileIDs returns the set of files that are tests: those that are the
// source of a "tests" edge, or, when no such edges exist, those whose path
// matches a test-file pattern.
func collectTestFileIDs(edges []edgeRow, filePathByID map[int64]string, symbolByID map[int64]symbolRow) map[int64]struct{} {
	testFileIDs := map[int64]struct{}{}
	for _, e := range edges {
		if e.kind != "tests" {
			continue
		}
		if src, ok := symbolByID[e.sourceID]; ok {
			testFileIDs[src.fileID] = struct{}{}
		}
	}
	if len(testFileIDs) == 0 {
		for fid, fp := range filePathByID {
			if mcpio.IsTestPath(fp) {
				testFileIDs[fid] = struct{}{}
			}
		}
	}
	return testFileIDs
}

// classifyTestFiles groups the given test files by naming family, keyed and
// deduplicated by label.
func classifyTestFiles(testFileIDs map[int64]struct{}, filePathByID map[int64]string) map[string]*testPattern {
	patterns := map[string]*testPattern{}
	for fid := range testFileIDs {
		fp, ok := filePathByID[fid]
		if !ok {
			continue
		}
		label, matches, ok := classifyTestFile(path.Base(fp))
		if !ok {
			continue
		}
		if p, exists := patterns[label]; exists {
			p.count++
		} else {
			patterns[label] = &testPattern{label: label, matches: matches, count: 1}
		}
	}
	return patterns
}

// classifyTestFile maps a base filename to its naming family, a label and a
// matcher, or ok=false when it fits no known test pattern. The precedence
// (_test → .test. → test_ → *Test(s).ext) is behavior; keep it in sync with
// IsTestPath, both must agree on what is a test file.
func classifyTestFile(base string) (label string, matches func(string) bool, ok bool) {
	if idx := strings.LastIndex(base, "_test"); idx >= 0 {
		ext := base[idx:]
		return ext, func(b string) bool { return strings.Contains(b, ext) }, true
	}
	if idx := strings.LastIndex(base, ".test."); idx >= 0 {
		ext := base[idx:]
		return ext, func(b string) bool { return strings.Contains(b, ext) }, true
	}
	if strings.HasPrefix(base, "test_") {
		return "test_*", func(b string) bool { return strings.HasPrefix(b, "test_") }, true
	}
	dot := strings.LastIndex(base, ".")
	if dot <= 0 {
		return "", nil, false
	}
	name := base[:dot]
	fileExt := base[dot:]
	switch {
	case strings.HasSuffix(name, "Tests"):
		return "*Tests" + fileExt, func(b string) bool {
			d := strings.LastIndex(b, ".")
			return d > 0 && strings.HasSuffix(b[:d], "Tests") && b[d:] == fileExt
		}, true
	case strings.HasSuffix(name, "Test"):
		return "*Test" + fileExt, func(b string) bool {
			d := strings.LastIndex(b, ".")
			return d > 0 && strings.HasSuffix(b[:d], "Test") && b[d:] == fileExt
		}, true
	}
	return "", nil, false
}

// buildTestingConventions emits one Testing convention per above-threshold
// naming family, gathering its example files.
func buildTestingConventions(patterns map[string]*testPattern, testFileIDs map[int64]struct{}, filePathByID map[int64]string) []Convention {
	total := len(testFileIDs)
	var out []Convention
	for _, p := range patterns {
		if p.count < minInstances {
			continue
		}
		var ex []Example
		for fid := range testFileIDs {
			fp, ok := filePathByID[fid]
			if !ok {
				continue
			}
			if p.matches(path.Base(fp)) {
				ex = append(ex, Example{Name: path.Base(fp), Path: fp})
			}
		}
		sortExamples(ex)
		out = append(out, Convention{
			Category:    CategoryTesting,
			Description: fmt.Sprintf("Test files use *%s naming convention (%s — %d of %d test files)", p.label, topNames(ex), p.count, total),
			Instances:   p.count,
			Total:       total,
			Strength:    float64(p.count) / float64(total),
			Examples:    ex,
		})
	}
	return out
}

func detectKeyTypes(symbols []symbolRow, edges []edgeRow, filePathByID map[int64]string, existing []Convention) []Convention {
	typeKinds := map[string]bool{"struct": true, "class": true, "interface": true, "type": true}
	inbound := make(map[int64]int)
	for _, e := range edges {
		inbound[e.targetID]++
	}

	surfaced := map[string]bool{}
	for _, c := range existing {
		for _, ex := range c.Examples {
			surfaced[ex.Name] = true
		}
	}

	type candidate struct {
		sym   symbolRow
		path  string
		count int
	}
	var candidates []candidate
	for _, s := range symbols {
		if !typeKinds[s.kind] {
			continue
		}
		fp := filePathByID[s.fileID]
		if mcpio.IsTestPath(fp) {
			continue
		}
		c := inbound[s.id]
		if c == 0 {
			continue
		}
		candidates = append(candidates, candidate{sym: s, path: fp, count: c})
	}

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.count != b.count {
			return a.count > b.count
		}
		if a.sym.name != b.sym.name {
			return a.sym.name < b.sym.name
		}
		return a.path < b.path
	})

	const maxKeyTypes = 8
	var examples []Example
	for _, c := range candidates {
		if surfaced[c.sym.name] {
			continue
		}
		if len(examples) >= maxKeyTypes {
			break
		}
		examples = append(examples, Example{
			Name:      c.sym.name,
			Path:      c.path,
			Kind:      c.sym.kind,
			EdgeCount: c.count,
		})
		surfaced[c.sym.name] = true
	}

	if len(examples) == 0 {
		return nil
	}

	var parts []string
	for _, e := range examples {
		parts = append(parts, fmt.Sprintf("%s (%d refs)", e.Name, e.EdgeCount))
	}
	totalTypes := countByKind(symbols, "struct", "class", "interface", "type")
	return []Convention{{
		Category:    CategoryKeyTypes,
		Description: fmt.Sprintf("Key domain types: %s — most-referenced types in the codebase", strings.Join(parts, ", ")),
		Instances:   len(examples),
		Total:       totalTypes,
		Strength:    1.0,
		Examples:    examples,
	}}
}
