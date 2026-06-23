package conventions

import (
	"testing"
)

func TestChunkIDsSingleChunk(t *testing.T) {
	ids := make([]int64, 100)
	for i := range ids {
		ids[i] = int64(i)
	}
	chunks := chunkIDs(ids)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}
	if len(chunks[0]) != 100 {
		t.Errorf("chunk[0] len = %d, want 100", len(chunks[0]))
	}
}

func TestChunkIDsMultipleChunks(t *testing.T) {
	ids := make([]int64, 1234)
	for i := range ids {
		ids[i] = int64(i)
	}
	chunks := chunkIDs(ids)
	if len(chunks) != 3 {
		t.Errorf("expected 3 chunks for 1234 ids, got %d", len(chunks))
	}
	// First two chunks should be full (500 each), last should have remainder
	if len(chunks[0]) != 500 {
		t.Errorf("chunk[0] len = %d, want 500", len(chunks[0]))
	}
	if len(chunks[1]) != 500 {
		t.Errorf("chunk[1] len = %d, want 500", len(chunks[1]))
	}
	if len(chunks[2]) != 234 {
		t.Errorf("chunk[2] len = %d, want 234", len(chunks[2]))
	}
}

func TestChunkIDsExactMultiple(t *testing.T) {
	ids := make([]int64, 1000)
	for i := range ids {
		ids[i] = int64(i)
	}
	chunks := chunkIDs(ids)
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks for 1000 ids, got %d", len(chunks))
	}
}

func TestChunkIDsEmpty(t *testing.T) {
	chunks := chunkIDs(nil)
	if len(chunks) != 1 || len(chunks[0]) != 0 {
		t.Errorf("expected 1 chunk with 0 items for nil input, got %d chunks", len(chunks))
	}
}

func TestSafeStrengthZeroTotal(t *testing.T) {
	if got := safeStrength(10, 0); got != 0 {
		t.Errorf("safeStrength(10, 0) = %v, want 0", got)
	}
}

func TestSafeStrengthNormal(t *testing.T) {
	got := safeStrength(3, 10)
	if got < 0.29 || got > 0.31 {
		t.Errorf("safeStrength(3, 10) = %v, want ~0.3", got)
	}
}

func TestSafeStrengthFull(t *testing.T) {
	if got := safeStrength(5, 5); got != 1.0 {
		t.Errorf("safeStrength(5, 5) = %v, want 1.0", got)
	}
}

func TestPluralizeBranches(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"class", "classes"},        // ends in "ss"
		{"match", "matches"},        // ends in "ch"
		{"flash", "flashes"},        // ends in "sh"
		{"box", "boxes"},            // ends in "x"
		{"bus", "buses"},            // ends in "s" (but not "ss")
		{"function", "functions"},   // default
		{"method", "methods"},       // default
		{"interface", "interfaces"}, // default (ends in "e")
	}
	for _, tt := range tests {
		got := pluralize(tt.input)
		if got != tt.want {
			t.Errorf("pluralize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCategoryOrderAllValues(t *testing.T) {
	tests := []struct {
		cat  Category
		want int
	}{
		{CategoryInheritance, 0},
		{CategoryFramework, 1},
		{CategoryDesignPattern, 2},
		{CategoryComposition, 3},
		{CategoryKeyTypes, 4},
		{CategoryNaming, 5},
		{CategoryArchitecture, 6},
		{CategoryStructure, 7},
		{CategoryTesting, 8},
		{Category("unknown"), 9}, // default branch
	}
	for _, tt := range tests {
		got := categoryOrder(tt.cat)
		if got != tt.want {
			t.Errorf("categoryOrder(%q) = %d, want %d", tt.cat, got, tt.want)
		}
	}
}

func TestCountByKindCoverage(t *testing.T) {
	symbols := []symbolRow{
		{kind: "class"}, {kind: "class"}, {kind: "interface"},
		{kind: "function"}, {kind: "struct"},
	}
	if got := countByKind(symbols, "class"); got != 2 {
		t.Errorf("countByKind(class) = %d, want 2", got)
	}
	if got := countByKind(symbols, "class", "interface"); got != 3 {
		t.Errorf("countByKind(class, interface) = %d, want 3", got)
	}
	if got := countByKind(symbols, "method"); got != 0 {
		t.Errorf("countByKind(method) = %d, want 0", got)
	}
}

func TestDedupeExamplesCoverage(t *testing.T) {
	examples := []Example{
		{Name: "Order", Path: "order.go"},
		{Name: "User", Path: "user.go"},
		{Name: "Order", Path: "order.go"},        // duplicate
		{Name: "Order", Path: "models/order.go"}, // different path -> not duplicate
	}
	got := dedupeExamples(examples)
	if len(got) != 3 {
		t.Errorf("dedupeExamples returned %d items, want 3", len(got))
	}
}

func TestHasMatchingExampleCoverage(t *testing.T) {
	examples := []Example{
		{Name: "Order", Path: "app/models/order.go"},
		{Name: "User", Path: "app/models/user.go"},
	}
	if !hasMatchingExample(examples, "models") {
		t.Error("should match 'models' domain")
	}
	if hasMatchingExample(examples, "controllers") {
		t.Error("should not match 'controllers' domain")
	}
	if hasMatchingExample(nil, "anything") {
		t.Error("empty examples should return false")
	}
}

func TestExtractSuffixCoverage(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"OrderService", "Service"},
		{"UserController", "Controller"},
		{"HTMLParser", "Parser"},
		{"checkout_service", "_service"},
		{"user_auth_handler", "_handler"},
		// Single-word names without inner uppercase or underscore return ""
		{"Config", ""},
		{"A", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractSuffix(tt.name)
		if got != tt.want {
			t.Errorf("extractSuffix(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestPickRepresentativesCoverage(t *testing.T) {
	if got := PickRepresentatives(nil, 5); got != nil {
		t.Errorf("PickRepresentatives(nil, 5) = %v, want nil", got)
	}

	examples := []Example{
		{Name: "A", Path: "a.go"},
		{Name: "B", Path: "b.go"},
		{Name: "C", Path: "c.go"},
	}
	got := PickRepresentatives(examples, 2)
	if len(got) != 2 {
		t.Errorf("PickRepresentatives(3, limit=2) = %d items, want 2", len(got))
	}
}
