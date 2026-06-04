package conventions

import (
	"strings"
	"testing"
)

// TestDetectGoInterfaces pins the Go interface-contract convention: an interface
// satisfied by two or more struct/class types is surfaced as a polymorphic
// dispatch point. Before this test the detector sat at 45.8%, its entire
// output-building loop was never reached by any fixture (none had an interface
// target with multiple implementors). The inputs below also exercise every
// guard: non-inherits edges, unknown endpoints, non-interface targets,
// non-struct/class sources, and a below-threshold interface.
func TestDetectGoInterfaces(t *testing.T) {
	// Interfaces.
	reader := symbolRow{id: 1, fileID: 10, name: "Reader", kind: "interface"}
	writer := symbolRow{id: 2, fileID: 10, name: "Writer", kind: "interface"}
	closer := symbolRow{id: 7, fileID: 10, name: "Closer", kind: "interface"}
	// Implementors of Reader (3: two structs + one class, both kinds count).
	fileStore := symbolRow{id: 3, fileID: 11, name: "FileStore", kind: "struct"}
	memStore := symbolRow{id: 4, fileID: 12, name: "MemStore", kind: "struct"}
	s3Store := symbolRow{id: 5, fileID: 13, name: "S3Store", kind: "class"}
	// Lone implementor of Writer (below the 2-instance threshold).
	solo := symbolRow{id: 6, fileID: 14, name: "Solo", kind: "struct"}
	// Plain class hierarchy, target is not an interface, must be ignored.
	base := symbolRow{id: 8, fileID: 15, name: "Base", kind: "class"}
	derived := symbolRow{id: 9, fileID: 16, name: "Derived", kind: "class"}

	symbols := []symbolRow{reader, writer, closer, fileStore, memStore, s3Store, solo, base, derived}
	symbolByID := indexSymbols(symbols)
	filePathByID := map[int64]string{
		10: "io.go",
		11: "a_store.go",
		12: "b_store.go",
		13: "c_store.go",
		14: "solo.go",
		15: "base.go",
		16: "derived.go",
	}

	edges := []edgeRow{
		// Reader is satisfied by three types.
		{sourceID: 3, targetID: 1, kind: "inherits"},
		{sourceID: 4, targetID: 1, kind: "inherits"},
		{sourceID: 5, targetID: 1, kind: "inherits"},
		// Writer has a single implementor, below threshold, skipped.
		{sourceID: 6, targetID: 2, kind: "inherits"},
		// Source is an interface, not a struct/class, skipped.
		{sourceID: 7, targetID: 1, kind: "inherits"},
		// Target is a class, not an interface, skipped.
		{sourceID: 9, targetID: 8, kind: "inherits"},
		// Non-inherits edge, skipped.
		{sourceID: 3, targetID: 1, kind: "calls"},
		// Unknown endpoints, skipped.
		{sourceID: 999, targetID: 1, kind: "inherits"},
		{sourceID: 3, targetID: 999, kind: "inherits"},
	}

	out := detectGoInterfaces(symbols, edges, symbolByID, filePathByID)

	if len(out) != 1 {
		t.Fatalf("expected exactly 1 interface convention (Reader), got %d: %+v", len(out), out)
	}
	c := out[0]
	if c.Category != CategoryFramework {
		t.Errorf("category = %q, want %q", c.Category, CategoryFramework)
	}
	if c.KeySymbol != "Reader" {
		t.Errorf("KeySymbol = %q, want %q", c.KeySymbol, "Reader")
	}
	if c.Instances != 3 {
		t.Errorf("instances = %d, want 3", c.Instances)
	}
	// Total is the count of all struct/class symbols: FileStore, MemStore, Solo
	// (structs) + S3Store, Base, Derived (classes) = 6.
	if c.Total != 6 {
		t.Errorf("total = %d, want 6 (struct+class symbols)", c.Total)
	}
	wantStrength := 3.0 / 6.0
	if c.Strength != wantStrength {
		t.Errorf("strength = %v, want %v", c.Strength, wantStrength)
	}
	if !strings.Contains(c.Description, "Interface contract: Reader is satisfied by 3 types") {
		t.Errorf("description = %q, missing interface-contract phrasing", c.Description)
	}
	if !strings.Contains(c.Description, "polymorphic dispatch point") {
		t.Errorf("description = %q, missing dispatch-point phrasing", c.Description)
	}
	// Examples are the three implementors, sorted by path.
	if len(c.Examples) != 3 {
		t.Fatalf("examples len = %d, want 3", len(c.Examples))
	}
	wantNames := []string{"FileStore", "MemStore", "S3Store"}
	for i, want := range wantNames {
		if c.Examples[i].Name != want {
			t.Errorf("examples[%d].Name = %q, want %q", i, c.Examples[i].Name, want)
		}
	}
}

// TestDetectGoInterfacesBelowThreshold confirms a single implementor produces no
// convention, an interface is only a "contract" once multiple types satisfy it.
func TestDetectGoInterfacesBelowThreshold(t *testing.T) {
	iface := symbolRow{id: 1, fileID: 10, name: "Stringer", kind: "interface"}
	impl := symbolRow{id: 2, fileID: 11, name: "OnlyOne", kind: "struct"}
	symbols := []symbolRow{iface, impl}
	symbolByID := indexSymbols(symbols)
	filePathByID := map[int64]string{10: "io.go", 11: "only.go"}
	edges := []edgeRow{{sourceID: 2, targetID: 1, kind: "inherits"}}

	if out := detectGoInterfaces(symbols, edges, symbolByID, filePathByID); len(out) != 0 {
		t.Errorf("expected no convention for a single implementor, got %d: %+v", len(out), out)
	}
}
