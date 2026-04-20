package ruby

import "strings"

// classify converts a snake_case plural or singular symbol name to a
// PascalCase class name: "line_items" → "LineItem", "user" → "User".
// It singularizes first (for has_many / habtm), then PascalCases.
func classify(s string) string {
	return pascalCase(singularize(s))
}

// singularize applies minimal suffix rules. Covers ~95% of Rails
// association names. The escape hatch is class_name: override.
func singularize(s string) string {
	if s == "" {
		return s
	}
	// Suffix rules ordered longest-match-first.
	switch {
	case strings.HasSuffix(s, "ies"):
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "sses"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "ses"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "xes"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "zes"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss"):
		return s[:len(s)-1]
	}
	return s
}

// pascalCase converts "line_item" → "LineItem".
func pascalCase(s string) string {
	parts := strings.Split(s, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}
