package conventions

import (
	"reflect"
	"testing"
)

// TestLessExampleStrictWeakOrdering exercises each branch of the shared
// EdgeCount → Name → Path cascade and confirms it is a strict weak ordering:
// irreflexive (a is not less than itself) and antisymmetric (a<b and b<a are
// never both true). Each branch is checked in isolation so a regression in one
// tier cannot hide behind another.
func TestLessExampleStrictWeakOrdering(t *testing.T) {
	hi := Example{Name: "M", Path: "m", EdgeCount: 10}
	lo := Example{Name: "M", Path: "m", EdgeCount: 1}
	// EdgeCount branch: higher count sorts first.
	if !lessExample(hi, lo) || lessExample(lo, hi) {
		t.Errorf("EdgeCount branch broken: less(hi,lo)=%v less(lo,hi)=%v", lessExample(hi, lo), lessExample(lo, hi))
	}

	nameA := Example{Name: "A", Path: "z", EdgeCount: 5}
	nameB := Example{Name: "B", Path: "a", EdgeCount: 5}
	// Name branch: equal EdgeCount falls through to Name ascending (Path ignored).
	if !lessExample(nameA, nameB) || lessExample(nameB, nameA) {
		t.Errorf("Name branch broken: less(A,B)=%v less(B,A)=%v", lessExample(nameA, nameB), lessExample(nameB, nameA))
	}

	pathA := Example{Name: "X", Path: "a", EdgeCount: 5}
	pathB := Example{Name: "X", Path: "b", EdgeCount: 5}
	// Path branch: equal EdgeCount and Name falls through to Path ascending.
	if !lessExample(pathA, pathB) || lessExample(pathB, pathA) {
		t.Errorf("Path branch broken: less(a,b)=%v less(b,a)=%v", lessExample(pathA, pathB), lessExample(pathB, pathA))
	}

	// Irreflexive: nothing is less than itself.
	if lessExample(pathA, pathA) {
		t.Error("lessExample(x, x) must be false")
	}
}

// TestDetectRailsCallbacksExampleOrder pins detectRailsCallbacks' own output
// order over a fixture where three classes tie on EdgeCount (two callbacks
// each), so the cascade must fall through to Name ascending. The classes are
// fed in non-alphabetical order so map iteration alone would not produce the
// asserted result.
func TestDetectRailsCallbacksExampleOrder(t *testing.T) {
	p := func(id int64) *int64 { return &id }
	symbols := []symbolRow{
		{id: 1, fileID: 101, name: "Zebra", kind: "class"},
		{id: 2, fileID: 102, name: "Apple", kind: "class"},
		{id: 3, fileID: 103, name: "Mango", kind: "class"},
		{id: 11, fileID: 101, name: "before_save", kind: "method", parentID: p(1)},
		{id: 12, fileID: 101, name: "after_save", kind: "method", parentID: p(1)},
		{id: 21, fileID: 102, name: "before_save", kind: "method", parentID: p(2)},
		{id: 22, fileID: 102, name: "after_save", kind: "method", parentID: p(2)},
		{id: 31, fileID: 103, name: "before_save", kind: "method", parentID: p(3)},
		{id: 32, fileID: 103, name: "after_save", kind: "method", parentID: p(3)},
	}
	filePathByID := map[int64]string{101: "zebra.rb", 102: "apple.rb", 103: "mango.rb"}

	out := detectRailsCallbacks(symbols, nil, indexSymbols(symbols), filePathByID)
	if len(out) != 1 {
		t.Fatalf("expected 1 convention, got %d", len(out))
	}
	got := exampleNames(out[0].Examples)
	want := []string{"Apple", "Mango", "Zebra"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("callback example order = %v, want %v (EdgeCount tie → Name asc)", got, want)
	}
}

// TestDetectScopesExampleOrder pins detectScopes' order over three classes that
// tie on scope count, asserting the Name-ascending fall-through.
func TestDetectScopesExampleOrder(t *testing.T) {
	p := func(id int64) *int64 { return &id }
	symbols := []symbolRow{
		{id: 1, fileID: 101, name: "Zebra", kind: "class"},
		{id: 2, fileID: 102, name: "Apple", kind: "class"},
		{id: 3, fileID: 103, name: "Mango", kind: "class"},
		{id: 11, fileID: 101, name: "active", kind: "method", parentID: p(1)},
		{id: 12, fileID: 101, name: "recent", kind: "method", parentID: p(1)},
		{id: 21, fileID: 102, name: "active", kind: "method", parentID: p(2)},
		{id: 22, fileID: 102, name: "recent", kind: "method", parentID: p(2)},
		{id: 31, fileID: 103, name: "active", kind: "method", parentID: p(3)},
		{id: 32, fileID: 103, name: "recent", kind: "method", parentID: p(3)},
	}
	filePathByID := map[int64]string{101: "zebra.rb", 102: "apple.rb", 103: "mango.rb"}
	// Each class calls its own scope methods, marking them scope targets.
	edges := []edgeRow{
		{sourceID: 1, targetID: 11, kind: "calls"}, {sourceID: 1, targetID: 12, kind: "calls"},
		{sourceID: 2, targetID: 21, kind: "calls"}, {sourceID: 2, targetID: 22, kind: "calls"},
		{sourceID: 3, targetID: 31, kind: "calls"}, {sourceID: 3, targetID: 32, kind: "calls"},
	}

	out := detectScopes(symbols, edges, indexSymbols(symbols), filePathByID)
	if len(out) != 1 {
		t.Fatalf("expected 1 convention, got %d", len(out))
	}
	got := exampleNames(out[0].Examples)
	want := []string{"Apple", "Mango", "Zebra"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scope example order = %v, want %v (EdgeCount tie → Name asc)", got, want)
	}
}

// TestDetectGoMiddlewareExampleOrder pins detectGoMiddleware's order over three
// factories that tie on call count, asserting the Name-ascending fall-through.
func TestDetectGoMiddlewareExampleOrder(t *testing.T) {
	symbols := []symbolRow{
		{id: 1, fileID: 10, name: "Use", kind: "method"},
		{id: 2, fileID: 11, name: "Zlog", kind: "function"},
		{id: 3, fileID: 12, name: "Auth", kind: "function"},
		{id: 4, fileID: 13, name: "Cors", kind: "function"},
	}
	filePathByID := map[int64]string{10: "router.go", 11: "zlog.go", 12: "auth.go", 13: "cors.go"}
	// Each factory is called twice by the router, so all three tie on count.
	edges := []edgeRow{
		{sourceID: 1, targetID: 2, kind: "calls"}, {sourceID: 1, targetID: 2, kind: "calls"},
		{sourceID: 1, targetID: 3, kind: "calls"}, {sourceID: 1, targetID: 3, kind: "calls"},
		{sourceID: 1, targetID: 4, kind: "calls"}, {sourceID: 1, targetID: 4, kind: "calls"},
	}

	out := detectGoMiddleware(symbols, edges, indexSymbols(symbols), filePathByID)
	if len(out) != 1 {
		t.Fatalf("expected 1 convention, got %d", len(out))
	}
	got := exampleNames(out[0].Examples)
	want := []string{"Auth", "Cors", "Zlog"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("middleware example order = %v, want %v (EdgeCount tie → Name asc)", got, want)
	}
}

// TestSortExamplesPathThenName pins the Path-first exception: examples group by
// Path ascending, and same-Path examples fall through to Name ascending. The
// input is ordered so a Path-only sort would leave the same-Path pair in the
// wrong order, making this fail before the Name tiebreak was added.
func TestSortExamplesPathThenName(t *testing.T) {
	examples := []Example{
		{Name: "Zeta", Path: "a.go"},
		{Name: "Alpha", Path: "a.go"},
		{Name: "Mid", Path: "b.go"},
	}
	sortExamples(examples)
	got := exampleNames(examples)
	want := []string{"Alpha", "Zeta", "Mid"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortExamples order = %v, want %v (Path asc, then Name asc)", got, want)
	}
}

// TestDetectKeyTypesCandidateOrder pins the count → name → path cascade on
// detectKeyTypes' candidate sort. Three types tie on reference count, so the
// fall-through to Name ascending must hold; a same-named pair in two files
// proves the Path tier decides which file's type surfaces (a.go over z.go). The
// symbols are fed in a non-sorted order so a count-only sort would fail.
func TestDetectKeyTypesCandidateOrder(t *testing.T) {
	symbols := []symbolRow{
		{id: 1, fileID: 10, name: "Zeta", kind: "struct"},
		{id: 2, fileID: 11, name: "Alpha", kind: "struct"},
		{id: 3, fileID: 12, name: "Order", kind: "struct"}, // z.go
		{id: 4, fileID: 13, name: "Order", kind: "struct"}, // a.go
	}
	filePathByID := map[int64]string{10: "zeta.go", 11: "alpha.go", 12: "z.go", 13: "a.go"}
	// Two inbound edges per type, so all four candidates tie on count.
	edges := []edgeRow{
		{sourceID: 99, targetID: 1, kind: "calls"}, {sourceID: 98, targetID: 1, kind: "calls"},
		{sourceID: 99, targetID: 2, kind: "calls"}, {sourceID: 98, targetID: 2, kind: "calls"},
		{sourceID: 99, targetID: 3, kind: "calls"}, {sourceID: 98, targetID: 3, kind: "calls"},
		{sourceID: 99, targetID: 4, kind: "calls"}, {sourceID: 98, targetID: 4, kind: "calls"},
	}

	out := detectKeyTypes(symbols, edges, filePathByID, nil)
	if len(out) != 1 {
		t.Fatalf("expected 1 convention, got %d", len(out))
	}
	got := exampleNames(out[0].Examples)
	want := []string{"Alpha", "Order", "Zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("key-type example order = %v, want %v (count tie → name asc)", got, want)
	}
	// The Path tier broke the "Order" name tie: a.go wins over z.go.
	for _, e := range out[0].Examples {
		if e.Name == "Order" && e.Path != "a.go" {
			t.Errorf("Order surfaced from %q, want a.go (Path tiebreak)", e.Path)
		}
	}
}

func exampleNames(examples []Example) []string {
	names := make([]string, len(examples))
	for i, e := range examples {
		names[i] = e.Name
	}
	return names
}
