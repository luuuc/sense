package conventions

import (
	"strings"
	"testing"
)

// twinFixture builds the gin build-tag shape: one interface named
// binding.Binding defined in two files (mutually exclusive builds), with an
// implementor set per twin placed under the given directory. Implementor ids
// start at 100 for the first twin's set and 200 for the second's. Same names
// under the same dir yield identical (Name, Path) sets; same names under
// different dirs yield identical rendered descriptions over genuinely
// different sets.
func twinFixture(firstImpls []string, firstDir string, secondImpls []string, secondDir string) ([]symbolRow, []edgeRow, map[int64]symbolRow, map[int64]string) {
	twin1 := symbolRow{id: 1, fileID: 10, name: "Binding", qualified: "binding.Binding", kind: "interface"}
	twin2 := symbolRow{id: 2, fileID: 11, name: "Binding", qualified: "binding.Binding", kind: "interface"}
	symbols := []symbolRow{twin1, twin2}
	filePathByID := map[int64]string{
		10: "binding/binding.go",
		11: "binding/binding_nomsgpack.go",
	}
	var edges []edgeRow
	addImpls := func(names []string, dir string, base int64, target int64) {
		for i, n := range names {
			id := base + int64(i)
			fid := 1000 + id
			symbols = append(symbols, symbolRow{id: id, fileID: fid, name: n, kind: "struct"})
			filePathByID[fid] = dir + strings.ToLower(n) + ".go"
			edges = append(edges, edgeRow{sourceID: id, targetID: target, kind: "inherits"})
		}
	}
	addImpls(firstImpls, firstDir, 100, twin1.id)
	addImpls(secondImpls, secondDir, 200, twin2.id)
	return symbols, edges, indexSymbols(symbols), filePathByID
}

// TestDedupeMergesTwinRows pins the twin-file merge end-to-end at the
// detector level: two same-qualified-name interfaces with identical
// implementor sets emit two byte-identical framework rows (inheritance
// routes Go satisfaction away entirely), and dedupeRenderedRows merges the
// pair down to one row with the counts of one population, never summed.
func TestDedupeMergesTwinRows(t *testing.T) {
	impls := []string{"bsonBinding", "formBinding", "jsonBinding"}
	symbols, edges, symbolByID, filePathByID := twinFixture(impls, "binding/", nil, "")
	// Point the second twin's edges at the SAME implementor symbols so both
	// populations are the identical (Name, Path) set, as in gin where every
	// binding struct satisfies both build-tag variants.
	for _, id := range []int64{100, 101, 102} {
		edges = append(edges, edgeRow{sourceID: id, targetID: 2, kind: "inherits"})
	}

	var conventions []Convention
	conventions = append(conventions, detectInheritance(symbols, edges, symbolByID, filePathByID)...)
	conventions = append(conventions, detectGoInterfaces(symbols, edges, symbolByID, filePathByID)...)
	if len(conventions) != 2 {
		t.Fatalf("fixture must reproduce the defect (2 identical framework rows, 0 inheritance), got %d: %+v", len(conventions), conventions)
	}

	deduped := dedupeRenderedRows(conventions)
	if len(deduped) != 1 {
		t.Fatalf("expected twin framework rows merged to 1, got %d: %+v", len(deduped), deduped)
	}
	c := deduped[0]
	if c.Instances != 3 {
		t.Errorf("merged row counts must come from one population, got Instances=%d, want 3", c.Instances)
	}
	if strings.Contains(c.Description, "defined in") {
		t.Errorf("merged row must not be file-qualified: %s", c.Description)
	}
}

// TestDedupeMergesRubyTwinRows pins the inheritance-category merge with the
// re-opened-class shape: one Ruby class defined in two files (the same
// qualified name twice), the same three subclasses — two byte-identical
// inheritance rows merge to one.
func TestDedupeMergesRubyTwinRows(t *testing.T) {
	twin1 := symbolRow{id: 1, fileID: 10, name: "Base", qualified: "Billing::Base", kind: "class"}
	twin2 := symbolRow{id: 2, fileID: 11, name: "Base", qualified: "Billing::Base", kind: "class"}
	symbols := []symbolRow{twin1, twin2,
		{id: 3, fileID: 12, name: "Invoice", kind: "class"},
		{id: 4, fileID: 13, name: "Receipt", kind: "class"},
		{id: 5, fileID: 14, name: "Refund", kind: "class"},
	}
	filePathByID := map[int64]string{
		10: "app/models/billing/base.rb", 11: "lib/billing/base.rb",
		12: "app/models/invoice.rb", 13: "app/models/receipt.rb", 14: "app/models/refund.rb",
	}
	var edges []edgeRow
	for _, src := range []int64{3, 4, 5} {
		edges = append(edges,
			edgeRow{sourceID: src, targetID: 1, kind: "inherits"},
			edgeRow{sourceID: src, targetID: 2, kind: "inherits"})
	}

	conventions := detectInheritance(symbols, edges, indexSymbols(symbols), filePathByID)
	if len(conventions) != 2 || conventions[0].Description != conventions[1].Description {
		t.Fatalf("fixture must reproduce two identical inheritance rows, got: %+v", conventions)
	}
	deduped := dedupeRenderedRows(conventions)
	if len(deduped) != 1 {
		t.Fatalf("expected twin inheritance rows merged to 1, got %d: %+v", len(deduped), deduped)
	}
	if deduped[0].Instances != 3 {
		t.Errorf("merged row counts must come from one population, got %d", deduped[0].Instances)
	}
}

// TestDedupeQualifiesTrueDifference pins the qualify branch: two
// same-qualified-name interfaces with DIFFERENT implementor sets (same names,
// different files) must stay two rows, each qualified by the shortest
// distinguishing suffix of its defining file — never silently merged.
func TestDedupeQualifiesTrueDifference(t *testing.T) {
	names := []string{"bsonBinding", "formBinding", "jsonBinding"}
	symbols, edges, symbolByID, filePathByID := twinFixture(names, "binding/", names, "binding/internal/")

	conventions := detectGoInterfaces(symbols, edges, symbolByID, filePathByID)
	if len(conventions) != 2 {
		t.Fatalf("fixture must emit one row per twin, got %d", len(conventions))
	}
	if conventions[0].Description != conventions[1].Description {
		t.Fatalf("fixture must reproduce colliding descriptions, got %q vs %q", conventions[0].Description, conventions[1].Description)
	}

	deduped := dedupeRenderedRows(conventions)
	if len(deduped) != 2 {
		t.Fatalf("genuinely different populations must never merge, got %d rows", len(deduped))
	}
	if deduped[0].Description == deduped[1].Description {
		t.Fatalf("colliding rows must be qualified apart, both render %q", deduped[0].Description)
	}
	want := map[string]bool{"(defined in binding.go)": false, "(defined in binding_nomsgpack.go)": false}
	for _, c := range deduped {
		for suffix := range want {
			if strings.HasSuffix(c.Description, suffix) {
				want[suffix] = true
			}
		}
	}
	for suffix, found := range want {
		if !found {
			t.Errorf("expected a row qualified with %q, got %+v", suffix, deduped)
		}
	}
}

// TestDedupeLeavesDistinctRowsAlone pins the no-op path: a response with no
// rendered collisions comes back unchanged, in order.
func TestDedupeLeavesDistinctRowsAlone(t *testing.T) {
	conventions := []Convention{
		{Category: CategoryInheritance, Description: "a", Instances: 3},
		{Category: CategoryFramework, Description: "b", Instances: 2},
		// Same description in a different category is not a rendered collision:
		// the response groups rows by category.
		{Category: CategoryFramework, Description: "a", Instances: 2},
	}
	deduped := dedupeRenderedRows(conventions)
	if len(deduped) != 3 {
		t.Fatalf("expected all 3 rows untouched, got %d", len(deduped))
	}
	for i, want := range []string{"a", "b", "a"} {
		if deduped[i].Description != want {
			t.Errorf("row %d = %q, want %q (order must be preserved)", i, deduped[i].Description, want)
		}
	}
}

// TestDedupeMergeIsDeterministic pins the survivor choice: whichever detection
// order the twins arrive in, the merged row is the one with the smallest
// definingPath, so repeated runs render identically.
func TestDedupeMergeIsDeterministic(t *testing.T) {
	// The duplicated {A, p} entry pins set semantics: repeated members
	// collapse in the signature, so a detector emitting the same example
	// twice still merges with its twin.
	rowA := Convention{Category: CategoryFramework, Description: "x", Instances: 2,
		Examples:     []Example{{Name: "A", Path: "p"}, {Name: "A", Path: "p"}, {Name: "B", Path: "q"}},
		definingPath: "pkg/a.go"}
	rowB := rowA
	rowB.definingPath = "pkg/b.go"
	// Same set, different order: the merge must compare populations as sets,
	// so example order can never block it.
	rowB.Examples = []Example{{Name: "B", Path: "q"}, {Name: "A", Path: "p"}}

	for _, order := range [][]Convention{{rowA, rowB}, {rowB, rowA}} {
		deduped := dedupeRenderedRows(order)
		if len(deduped) != 1 {
			t.Fatalf("identical populations must merge, got %d", len(deduped))
		}
		if deduped[0].definingPath != "pkg/a.go" {
			t.Errorf("survivor must be the smallest definingPath, got %q", deduped[0].definingPath)
		}
	}
}

// TestDedupeTripleCollision pins the group shapes beyond a pair: four rows
// rendering identically, two of them one twin population — the twins merge
// to the smallest-definingPath survivor, and each of the three surviving
// populations is qualified against BOTH siblings, escalating past the shared
// basename to the distinguishing directory.
func TestDedupeTripleCollision(t *testing.T) {
	base := Convention{Category: CategoryFramework, Description: "y", Instances: 2}
	twin1, twin2, third, fourth := base, base, base, base
	twin1.Examples = []Example{{Name: "A", Path: "p"}, {Name: "B", Path: "q"}}
	twin1.definingPath = "pkg/v1/base.go"
	twin2.Examples = twin1.Examples
	twin2.definingPath = "pkg/v2/base.go"
	third.Examples = []Example{{Name: "A", Path: "p"}, {Name: "C", Path: "r"}}
	third.definingPath = "other/base.go"
	fourth.Examples = []Example{{Name: "D", Path: "s"}, {Name: "E", Path: "t"}}
	fourth.definingPath = "third/base.go"

	deduped := dedupeRenderedRows([]Convention{twin2, third, twin1, fourth})
	if len(deduped) != 3 {
		t.Fatalf("expected twins merged and true differences kept, got %d: %+v", len(deduped), deduped)
	}
	want := map[string]bool{
		"y (defined in v1/base.go)":    false,
		"y (defined in other/base.go)": false,
		"y (defined in third/base.go)": false,
	}
	for _, c := range deduped {
		if _, ok := want[c.Description]; !ok {
			t.Errorf("unexpected qualified description %q", c.Description)
			continue
		}
		want[c.Description] = true
	}
	for desc, found := range want {
		if !found {
			t.Errorf("missing qualified row %q in %+v", desc, deduped)
		}
	}
}

// TestDedupeQualifyWithoutProvenance pins the fallback: colliding
// true-difference rows whose detector recorded no defining file are left
// as-is rather than qualified with a fabricated path.
func TestDedupeQualifyWithoutProvenance(t *testing.T) {
	conventions := []Convention{
		{Category: CategoryNaming, Description: "n", Instances: 3, Examples: []Example{{Name: "A", Path: "p"}}},
		{Category: CategoryNaming, Description: "n", Instances: 4, Examples: []Example{{Name: "B", Path: "q"}}},
	}
	deduped := dedupeRenderedRows(conventions)
	if len(deduped) != 2 {
		t.Fatalf("different populations must not merge, got %d", len(deduped))
	}
	for _, c := range deduped {
		if strings.Contains(c.Description, "defined in") {
			t.Errorf("row without provenance must stay unqualified: %q", c.Description)
		}
	}
}

// TestDistinguishingPathSuffix pins the suffix growth: basename first, then
// trailing directories, full path when a sibling shares every suffix depth.
func TestDistinguishingPathSuffix(t *testing.T) {
	cases := []struct {
		target   string
		siblings []string
		want     string
	}{
		{"binding/binding.go", []string{"binding/binding_nomsgpack.go"}, "binding.go"},
		{"a/render/json.go", []string{"b/render/json.go"}, "a/render/json.go"},
		{"x/render/json.go", []string{"y/other/json.go"}, "render/json.go"},
		{"same/path.go", []string{"same/path.go"}, "same/path.go"},
	}
	for _, tc := range cases {
		if got := distinguishingPathSuffix(tc.target, tc.siblings); got != tc.want {
			t.Errorf("distinguishingPathSuffix(%q, %v) = %q, want %q", tc.target, tc.siblings, got, tc.want)
		}
	}
}
