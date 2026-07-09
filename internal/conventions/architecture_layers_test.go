package conventions

import (
	"strings"
	"testing"
)

// archFixture builds gin's shape: a real production boundary (render/ calls
// json/, three calls, never reversed) plus a boundary that exists ONLY
// through test-origin edges — a test file's calls into a testdata/ fixture
// package. Known limit, deliberately not fixed here: IsTestPath classifies
// /testdata/ mid-path and test/-style prefixes but not a repo-root-relative
// "testdata/" prefix, so a production-source edge into root-level testdata
// still maps a layer; broadening that predicate is a product decision owned
// elsewhere, as is whether testdata is indexed at all.
func archFixture() ([]symbolRow, []edgeRow, map[int64]symbolRow, map[int64]string) {
	symbols := []symbolRow{
		// Production: render/ calls json/.
		{id: 1, fileID: 10, name: "renderJSON", kind: "function"},
		{id: 2, fileID: 10, name: "renderIndented", kind: "function"},
		{id: 3, fileID: 10, name: "renderAscii", kind: "function"},
		{id: 4, fileID: 11, name: "Marshal", kind: "function"},
		// Test scaffolding: binding_test.go calls into testdata/protoexample.
		{id: 5, fileID: 12, name: "TestBindingProto", kind: "function"},
		{id: 6, fileID: 12, name: "TestBindingProtoFail", kind: "function"},
		{id: 7, fileID: 12, name: "TestBindingProtoBody", kind: "function"},
		{id: 8, fileID: 13, name: "Test", kind: "class"},
	}
	filePathByID := map[int64]string{
		10: "render/render.go",
		11: "json/json.go",
		12: "binding/binding_test.go",
		13: "testdata/protoexample/test.pb.go",
	}
	edges := []edgeRow{
		{sourceID: 1, targetID: 4, kind: "calls"},
		{sourceID: 2, targetID: 4, kind: "calls"},
		{sourceID: 3, targetID: 4, kind: "calls"},
		{sourceID: 5, targetID: 8, kind: "calls"},
		{sourceID: 6, targetID: 8, kind: "calls"},
		{sourceID: 7, targetID: 8, kind: "calls"},
	}
	return symbols, edges, indexSymbols(symbols), filePathByID
}

// TestArchitectureLayersExcludeTestEdges pins the exclusion: a boundary
// whose calls all originate in a test file is not architecture and must not
// be reported as a layer law, while the production boundary keeps its exact
// count.
func TestArchitectureLayersExcludeTestEdges(t *testing.T) {
	symbols, edges, symbolByID, filePathByID := archFixture()
	convs := detectArchitectureLayers(symbols, edges, symbolByID, filePathByID)

	if len(convs) != 1 {
		t.Fatalf("expected exactly the production boundary, got %d: %+v", len(convs), convs)
	}
	c := convs[0]
	if !strings.Contains(c.Description, "render/ depends on json/ (3 calls, never reversed)") {
		t.Errorf("production boundary must survive with its exact count: %q", c.Description)
	}
	if c.Instances != 3 {
		t.Errorf("instances = %d, want 3 (production calls only)", c.Instances)
	}
	// The denominator is total cross-layer calls; with test-origin edges
	// excluded it is the same 3 production calls.
	if c.Total != 3 {
		t.Errorf("total = %d, want 3 (test-origin calls excluded from the population)", c.Total)
	}
	for _, ex := range c.Examples {
		if strings.Contains(ex.Path, "_test") || strings.Contains(ex.Path, "testdata") {
			t.Errorf("test-origin example leaked into a boundary: %+v", ex)
		}
	}
}

// TestArchitectureLayersProductionOnlyUnchanged pins the no-op path: with no
// test files in play, boundaries and counts are exactly what the edges say.
func TestArchitectureLayersProductionOnlyUnchanged(t *testing.T) {
	symbols := []symbolRow{
		{id: 1, fileID: 10, name: "A", kind: "function"},
		{id: 2, fileID: 10, name: "B", kind: "function"},
		{id: 3, fileID: 10, name: "C", kind: "function"},
		{id: 4, fileID: 11, name: "D", kind: "function"},
	}
	filePathByID := map[int64]string{10: "web/handlers.go", 11: "store/store.go"}
	edges := []edgeRow{
		{sourceID: 1, targetID: 4, kind: "calls"},
		{sourceID: 2, targetID: 4, kind: "calls"},
		{sourceID: 3, targetID: 4, kind: "calls"},
	}
	convs := detectArchitectureLayers(symbols, edges, indexSymbols(symbols), filePathByID)
	if len(convs) != 1 || convs[0].Instances != 3 {
		t.Fatalf("production-only boundary must be reported unchanged, got: %+v", convs)
	}
}

// TestArchitectureLayersUnsuppressesProductionLaw pins the one
// output-GROWING direction of the guard: a production boundary previously
// silenced because its only reverse call originated in a test file becomes
// unidirectional over production edges — "never reversed" is now the
// stronger, honest claim, and a new law rightly appears.
func TestArchitectureLayersUnsuppressesProductionLaw(t *testing.T) {
	symbols := []symbolRow{
		{id: 1, fileID: 10, name: "A", kind: "function"},
		{id: 2, fileID: 10, name: "B", kind: "function"},
		{id: 3, fileID: 10, name: "C", kind: "function"},
		{id: 4, fileID: 11, name: "Marshal", kind: "function"},
		// The lone reverse caller lives in a test file.
		{id: 5, fileID: 12, name: "TestRoundtrip", kind: "function"},
	}
	filePathByID := map[int64]string{
		10: "render/render.go", 11: "json/json.go", 12: "json/json_test.go",
	}
	edges := []edgeRow{
		{sourceID: 1, targetID: 4, kind: "calls"},
		{sourceID: 2, targetID: 4, kind: "calls"},
		{sourceID: 3, targetID: 4, kind: "calls"},
		// json -> render, test-origin: suppressed the law before the guard.
		{sourceID: 5, targetID: 1, kind: "calls"},
	}
	convs := detectArchitectureLayers(symbols, edges, indexSymbols(symbols), filePathByID)
	if len(convs) != 1 || !strings.Contains(convs[0].Description, "render/ depends on json/ (3 calls, never reversed)") {
		t.Fatalf("production law must appear once test-origin reverse noise is excluded, got: %+v", convs)
	}
}

// TestArchitectureLayersDropTestTargets pins the target side of the guard: a
// production call INTO a test-directory file maps no boundary and does not
// inflate the cross-layer population.
func TestArchitectureLayersDropTestTargets(t *testing.T) {
	symbols := []symbolRow{
		{id: 1, fileID: 10, name: "A", kind: "function"},
		{id: 2, fileID: 10, name: "B", kind: "function"},
		{id: 3, fileID: 10, name: "C", kind: "function"},
		{id: 4, fileID: 11, name: "Store", kind: "function"},
		{id: 5, fileID: 12, name: "Helper", kind: "function"},
	}
	filePathByID := map[int64]string{
		10: "web/handlers.go", 11: "store/store.go", 12: "test/support/helper.rb",
	}
	edges := []edgeRow{
		{sourceID: 1, targetID: 4, kind: "calls"},
		{sourceID: 2, targetID: 4, kind: "calls"},
		{sourceID: 3, targetID: 4, kind: "calls"},
		// Production calling into test support: no layer, no boundary.
		{sourceID: 1, targetID: 5, kind: "calls"},
	}
	convs := detectArchitectureLayers(symbols, edges, indexSymbols(symbols), filePathByID)
	if len(convs) != 1 {
		t.Fatalf("expected only the production boundary, got: %+v", convs)
	}
	if convs[0].Total != 3 {
		t.Errorf("total = %d, want 3 (call into test support excluded from the population)", convs[0].Total)
	}
}

// TestArchitectureLayersTestOnlyBoundaryVanishes pins the zero-edge outcome:
// when every call over a boundary is test-origin, the boundary drops below
// the emission threshold and disappears — no floor keeps the row alive.
func TestArchitectureLayersTestOnlyBoundaryVanishes(t *testing.T) {
	symbols := []symbolRow{
		{id: 1, fileID: 10, name: "TestX", kind: "function"},
		{id: 2, fileID: 10, name: "TestY", kind: "function"},
		{id: 3, fileID: 10, name: "TestZ", kind: "function"},
		{id: 4, fileID: 11, name: "Fixture", kind: "class"},
	}
	filePathByID := map[int64]string{10: "binding/binding_test.go", 11: "testdata/protoexample/example.go"}
	edges := []edgeRow{
		{sourceID: 1, targetID: 4, kind: "calls"},
		{sourceID: 2, targetID: 4, kind: "calls"},
		{sourceID: 3, targetID: 4, kind: "calls"},
	}
	if convs := detectArchitectureLayers(symbols, edges, indexSymbols(symbols), filePathByID); len(convs) != 0 {
		t.Fatalf("test-only boundary must not be reported, got: %+v", convs)
	}
}
