package conventions

import (
	"strings"
	"testing"
)

// TestSuffixGuardRejectsAcronymTails pins the acronym-tail rejection with
// gin's own names: AsciiJSON/BSON/IndentedJSON/JsonpJSON share only the last
// letter of an acronym, and "classes use *N naming convention" is not a
// convention. The
// word-shaped survivors (*Binding) must be untouched — they are the proof the
// guard does not over-cut.
func TestSuffixGuardRejectsAcronymTails(t *testing.T) {
	symbols := []symbolRow{
		{id: 1, fileID: 1, name: "AsciiJSON", kind: "class"},
		{id: 2, fileID: 2, name: "BSON", kind: "class"},
		{id: 3, fileID: 3, name: "IndentedJSON", kind: "class"},
		{id: 4, fileID: 4, name: "JsonpJSON", kind: "class"},
		{id: 5, fileID: 5, name: "bsonBinding", kind: "class"},
		{id: 6, fileID: 6, name: "formBinding", kind: "class"},
		{id: 7, fileID: 7, name: "jsonBinding", kind: "class"},
	}
	paths := fileMap(map[int64]string{
		1: "render/json.go", 2: "render/bson.go", 3: "render/indented.go", 4: "render/jsonp.go",
		5: "binding/bson.go", 6: "binding/form.go", 7: "binding/json.go",
	})

	convs := detectSymbolSuffixNaming(symbols, paths)
	hasBinding := false
	for _, c := range convs {
		if strings.Contains(c.Description, "*N naming") {
			t.Errorf("acronym tail leaked as a convention: %q", c.Description)
		}
		if strings.Contains(c.Description, "*Binding") {
			hasBinding = true
		}
	}
	if !hasBinding {
		t.Errorf("word-shaped *Binding suffix must survive the guard, got: %v", convs)
	}
}

// TestSuffixGuardRejectsCapitalDigitTails pins the *L2 shape: MIMEXML2-style
// constants share a capital+digit tail, not a word.
func TestSuffixGuardRejectsCapitalDigitTails(t *testing.T) {
	symbols := []symbolRow{
		{id: 1, fileID: 1, name: "MIMEXML2", kind: "constant"},
		{id: 2, fileID: 1, name: "MIMEYAML2", kind: "constant"},
		{id: 3, fileID: 1, name: "OtherXML2", kind: "constant"},
		{id: 4, fileID: 1, name: "DebugMode", kind: "constant"},
		{id: 5, fileID: 1, name: "ReleaseMode", kind: "constant"},
		{id: 6, fileID: 1, name: "TestMode", kind: "constant"},
	}
	paths := fileMap(map[int64]string{1: "mime.go"})
	convs := detectSymbolSuffixNaming(symbols, paths)
	hasMode := false
	for _, c := range convs {
		if strings.Contains(c.Description, "*L2") {
			t.Errorf("capital+digit tail leaked as a convention: %q", c.Description)
		}
		if strings.Contains(c.Description, "*Mode") {
			hasMode = true
		}
	}
	if !hasMode {
		t.Errorf("word-shaped *Mode suffix must survive the guard, got: %v", convs)
	}
}

// TestSuffixGuardRejectsShortSnakeTokens pins the snake_case branch of the
// guard: a one-character _x token is not a word, however many symbols share
// it, while a real snake_case suffix survives.
func TestSuffixGuardRejectsShortSnakeTokens(t *testing.T) {
	symbols := []symbolRow{
		{id: 1, fileID: 1, name: "alpha_x", kind: "function"},
		{id: 2, fileID: 1, name: "beta_x", kind: "function"},
		{id: 3, fileID: 1, name: "gamma_x", kind: "function"},
		{id: 4, fileID: 2, name: "checkout_service", kind: "function"},
		{id: 5, fileID: 2, name: "payment_service", kind: "function"},
		{id: 6, fileID: 2, name: "shipping_service", kind: "function"},
	}
	paths := fileMap(map[int64]string{1: "lib/x.rb", 2: "lib/services.rb"})
	convs := detectSymbolSuffixNaming(symbols, paths)
	hasService := false
	for _, c := range convs {
		if strings.Contains(c.Description, "*_x naming") {
			t.Errorf("one-character snake token leaked as a convention: %q", c.Description)
		}
		if strings.Contains(c.Description, "*_service") {
			hasService = true
		}
	}
	if !hasService {
		t.Errorf("real snake_case suffix *_service must survive the guard, got: %v", convs)
	}
}

// TestIsWordShapedSuffix pins the guard's contract as an independently usable
// helper: word-shaped CamelCase and snake_case suffixes pass, acronym
// fragments and bare tails do not.
func TestIsWordShapedSuffix(t *testing.T) {
	cases := []struct {
		suffix string
		want   bool
	}{
		{"Binding", true},
		{"Func", true},
		{"Mode", true},
		{"Er", true},
		{"N", false},
		{"L", false},
		{"L2", false},
		{"XML", false},
		{"", false},
		{"_service", true},
		{"_to", true},
		{"_KEY", true},
		{"_v2", true},
		{"_42", false},
		{"_\u00e9", false},
		{"_x", false},
		{"_", false},
	}
	for _, tc := range cases {
		if got := isWordShapedSuffix(tc.suffix); got != tc.want {
			t.Errorf("isWordShapedSuffix(%q) = %v, want %v", tc.suffix, got, tc.want)
		}
	}
}

// TestFileSuffixNamingCountsFiles pins the file-count semantics: the "N files
// use *suffix" sentence must count FILES. Four interface symbols in one
// *_nomsgpack.go file are one file, and one file is not a convention — the
// row disappears below minInstances instead of reporting four symbol tallies
// over a single exemplar (gin's defect, in miniature).
func TestFileSuffixNamingCountsFiles(t *testing.T) {
	symbols := []symbolRow{
		// Four interfaces in ONE build-tag twin file.
		{id: 1, fileID: 1, name: "Binding", kind: "interface"},
		{id: 2, fileID: 1, name: "BindingBody", kind: "interface"},
		{id: 3, fileID: 1, name: "BindingUri", kind: "interface"},
		{id: 4, fileID: 1, name: "StructValidator", kind: "interface"},
		// Interface-bearing files without the suffix, for the denominator.
		{id: 5, fileID: 2, name: "Render", kind: "interface"},
		{id: 6, fileID: 3, name: "HTMLRender", kind: "interface"},
	}
	paths := fileMap(map[int64]string{
		1: "binding/binding_nomsgpack.go", 2: "render/render.go", 3: "render/html.go",
	})
	for _, c := range detectFileSuffixNaming(symbols, paths) {
		if strings.Contains(c.Description, "_nomsgpack.go") {
			t.Errorf("single-file suffix reported as a multi-instance convention: %q", c.Description)
		}
	}
}

// TestFileSuffixNamingHonestCounts pins the positive path: three suffix files
// among five class-bearing files reads "3 of 5", both tallies distinct files,
// however many symbols each file holds.
func TestFileSuffixNamingHonestCounts(t *testing.T) {
	symbols := []symbolRow{
		// Two classes per suffix file: symbol tallies would say 6.
		{id: 1, fileID: 1, name: "A", kind: "class"},
		{id: 2, fileID: 1, name: "B", kind: "class"},
		{id: 3, fileID: 2, name: "C", kind: "class"},
		{id: 4, fileID: 2, name: "D", kind: "class"},
		{id: 5, fileID: 3, name: "E", kind: "class"},
		{id: 6, fileID: 3, name: "F", kind: "class"},
		// Two class-bearing files without the suffix: denominator = 5 files,
		// where symbol tallies would say 8.
		{id: 7, fileID: 4, name: "G", kind: "class"},
		{id: 8, fileID: 4, name: "H", kind: "class"},
		{id: 9, fileID: 5, name: "I", kind: "class"},
	}
	paths := fileMap(map[int64]string{
		1: "app/a_service.rb", 2: "app/b_service.rb", 3: "app/c_service.rb",
		4: "app/models.rb", 5: "app/other.rb",
	})
	convs := detectFileSuffixNaming(symbols, paths)
	if len(convs) != 1 {
		t.Fatalf("expected exactly the *_service.rb convention, got %d: %v", len(convs), convs)
	}
	c := convs[0]
	if c.Instances != 3 || c.Total != 5 {
		t.Errorf("instances/total = %d/%d, want 3/5 (files, not symbols)", c.Instances, c.Total)
	}
	if !strings.Contains(c.Description, "3 of 5") {
		t.Errorf("description must say 3 of 5 files: %q", c.Description)
	}
	if !strings.Contains(c.Description, "files use *_service.rb") {
		t.Errorf("description must keep the files wording: %q", c.Description)
	}
	if len(c.Examples) != 3 {
		t.Errorf("examples must list one entry per file, got %d", len(c.Examples))
	}
}
