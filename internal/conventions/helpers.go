package conventions

import (
	"path"
	"sort"
	"strings"
)

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

func categoryOrder(c Category) int {
	switch c {
	case CategoryInheritance:
		return 0
	case CategoryNaming:
		return 1
	case CategoryStructure:
		return 2
	case CategoryComposition:
		return 3
	case CategoryTesting:
		return 4
	case CategoryDesignPattern:
		return 5
	case CategoryFramework:
		return 6
	case CategoryArchitecture:
		return 7
	case CategoryKeyTypes:
		return 8
	}
	return 8
}

func hasMatchingExample(examples []Example, domain string) bool {
	for _, e := range examples {
		if strings.Contains(e.Path, domain) {
			return true
		}
	}
	return false
}

func sortExamples(examples []Example) {
	sort.Slice(examples, func(i, j int) bool {
		return examples[i].Path < examples[j].Path
	})
}

func PickRepresentatives(examples []Example, limit int) []string {
	if len(examples) == 0 {
		return nil
	}
	sorted := make([]Example, len(examples))
	copy(sorted, examples)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].EdgeCount > sorted[j].EdgeCount
	})
	n := limit
	if n > len(sorted) {
		n = len(sorted)
	}
	names := make([]string, n)
	for i := 0; i < n; i++ {
		names[i] = sorted[i].Name
	}
	return names
}

func topNames(examples []Example) string {
	return strings.Join(PickRepresentatives(examples, maxDescriptionNames), ", ")
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
