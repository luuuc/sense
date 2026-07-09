package conventions

import (
	"strings"
	"testing"
)

// satisfactionFixture builds a Go interface with three production
// implementors and one implementor declared in a test file — the shape that
// produced gin's 18-vs-19 count drift between categories.
func satisfactionFixture() ([]symbolRow, []edgeRow, map[int64]symbolRow, map[int64]string) {
	// Go structs are indexed as kind "class" (the extractor's classification);
	// fixtures mirror the real index, not the Go keyword.
	symbols := []symbolRow{
		{id: 1, fileID: 10, name: "Render", qualified: "render.Render", kind: "interface"},
		{id: 2, fileID: 11, name: "JSON", kind: "class"},
		{id: 3, fileID: 12, name: "XML", kind: "class"},
		{id: 4, fileID: 13, name: "YAML", kind: "class"},
		{id: 5, fileID: 14, name: "fakeRender", kind: "class"},
	}
	filePathByID := map[int64]string{
		10: "render/render.go",
		11: "render/json.go",
		12: "render/xml.go",
		13: "render/yaml.go",
		14: "render/render_test.go",
	}
	edges := []edgeRow{
		{sourceID: 2, targetID: 1, kind: "inherits"},
		{sourceID: 3, targetID: 1, kind: "inherits"},
		{sourceID: 4, targetID: 1, kind: "inherits"},
		{sourceID: 5, targetID: 1, kind: "inherits"},
	}
	return symbols, edges, indexSymbols(symbols), filePathByID
}

// TestSatisfactionRoutedToFrameworkOnly pins the one-fact-one-category rule:
// inherits edges into a Go interface are satisfaction, owned by the framework
// interface-contract row; the inheritance category emits NOTHING for them,
// and the framework row applies the same test-source exclusion as
// detectInheritance, so cross-category count drift is structurally
// impossible.
func TestSatisfactionRoutedToFrameworkOnly(t *testing.T) {
	symbols, edges, symbolByID, filePathByID := satisfactionFixture()

	if got := detectInheritance(symbols, edges, symbolByID, filePathByID); len(got) != 0 {
		t.Fatalf("inheritance must not report Go interface satisfaction, got: %+v", got)
	}

	convs := detectGoInterfaces(symbols, edges, symbolByID, filePathByID)
	if len(convs) != 1 {
		t.Fatalf("expected one framework interface-contract row, got %d: %+v", len(convs), convs)
	}
	c := convs[0]
	if c.Instances != 3 {
		t.Errorf("instances = %d, want 3 (test-file implementor excluded)", c.Instances)
	}
	// Denominator matches the sentence: "N types" counts non-test
	// struct/class types (JSON, XML, YAML — fakeRender lives in a test file).
	if c.Total != 3 {
		t.Errorf("total = %d, want 3 (non-test struct/class types)", c.Total)
	}
	if !strings.Contains(c.Description, "3 types implement Render") {
		t.Errorf("description must use the implement wording, got %q", c.Description)
	}
	for _, banned := range []string{"extend", "base class", "satisfied by"} {
		if strings.Contains(c.Description, banned) {
			t.Errorf("description must not say %q over Go code: %q", banned, c.Description)
		}
	}
	for _, ex := range c.Examples {
		if ex.Name == "fakeRender" {
			t.Errorf("test-file implementor leaked into the contract row: %+v", c.Examples)
		}
	}
}

// TestInheritanceKeepsNonGoTargets pins the routing discriminator to the
// language, not the target kind alone: an interface-kind target declared
// outside a Go file stays in the inheritance category. Asserted with
// fixtures, not reasoning, per the pitch. This is a guard test (it never
// went red). Known asymmetry it documents rather than fixes: non-Go
// interface-kind targets (Rust traits, TS implements) still appear in BOTH
// inheritance and the framework contract row — one-category ownership for
// them is a separate behavioral change that needs its own bench.
func TestInheritanceKeepsNonGoTargets(t *testing.T) {
	cases := []struct {
		lang    string
		kind    string
		files   map[int64]string
		mention string
	}{
		{"ruby", "class", map[int64]string{
			10: "app/models/base.rb", 11: "app/models/a.rb", 12: "app/models/b.rb", 13: "app/models/c.rb",
		}, "Base"},
		{"python", "class", map[int64]string{
			10: "core/models.py", 11: "core/a.py", 12: "core/b.py", 13: "core/c.py",
		}, "Base"},
		{"typescript", "interface", map[int64]string{
			10: "src/base.ts", 11: "src/a.ts", 12: "src/b.ts", 13: "src/c.ts",
		}, "Base"},
	}
	for _, tc := range cases {
		symbols := []symbolRow{
			{id: 1, fileID: 10, name: "Base", qualified: "Base", kind: tc.kind},
			{id: 2, fileID: 11, name: "A", kind: "class"},
			{id: 3, fileID: 12, name: "B", kind: "class"},
			{id: 4, fileID: 13, name: "C", kind: "class"},
		}
		edges := []edgeRow{
			{sourceID: 2, targetID: 1, kind: "inherits"},
			{sourceID: 3, targetID: 1, kind: "inherits"},
			{sourceID: 4, targetID: 1, kind: "inherits"},
		}
		convs := detectInheritance(symbols, edges, indexSymbols(symbols), tc.files)
		if len(convs) != 1 || !strings.Contains(convs[0].Description, tc.mention) {
			t.Errorf("%s: inheritance must keep non-Go %s targets, got: %+v", tc.lang, tc.kind, convs)
		}
	}
}

// TestGoEmbeddingWording pins the composition vocabulary over Go code: struct
// embedding promotes methods, nothing is mixed in — while Ruby include
// targets keep the generic mixin wording.
func TestGoEmbeddingWording(t *testing.T) {
	build := func(targetPath string, srcPaths [3]string) ([]symbolRow, []edgeRow, map[int64]symbolRow, map[int64]string) {
		// Kind "class" mirrors how the extractor indexes Go structs; the
		// wording must still say structs, which is what they are in Go.
		symbols := []symbolRow{
			{id: 1, fileID: 10, name: "Base", qualified: "pkg.Base", kind: "class"},
			{id: 2, fileID: 11, name: "A", kind: "class"},
			{id: 3, fileID: 12, name: "B", kind: "class"},
			{id: 4, fileID: 13, name: "C", kind: "class"},
		}
		files := map[int64]string{10: targetPath, 11: srcPaths[0], 12: srcPaths[1], 13: srcPaths[2]}
		edges := []edgeRow{
			{sourceID: 2, targetID: 1, kind: "includes"},
			{sourceID: 3, targetID: 1, kind: "includes"},
			{sourceID: 4, targetID: 1, kind: "includes"},
		}
		return symbols, edges, indexSymbols(symbols), files
	}

	symbols, edges, symbolByID, files := build("pkg/base.go", [3]string{"pkg/a.go", "pkg/b.go", "pkg/c.go"})
	convs := detectComposition(symbols, edges, symbolByID, files)
	if len(convs) != 1 {
		t.Fatalf("expected one Go composition row, got %d: %+v", len(convs), convs)
	}
	if !strings.Contains(convs[0].Description, "3 structs embed Base (methods promoted)") {
		t.Errorf("Go embedding must use embed wording, got %q", convs[0].Description)
	}
	if strings.Contains(convs[0].Description, "mix in") {
		t.Errorf("mixin wording over Go code: %q", convs[0].Description)
	}

	symbols, edges, symbolByID, files = build("app/models/concerns/base.rb", [3]string{"app/a.rb", "app/b.rb", "app/c.rb"})
	// Ruby modules are the usual include target; kind stays as declared in
	// the fixture, only the language changes the wording.
	convs = detectComposition(symbols, edges, symbolByID, files)
	if len(convs) != 1 {
		t.Fatalf("expected one Ruby composition row, got %d: %+v", len(convs), convs)
	}
	if !strings.Contains(convs[0].Description, "mix in Base for shared behavior") {
		t.Errorf("non-Go composition must keep mixin wording, got %q", convs[0].Description)
	}
}
