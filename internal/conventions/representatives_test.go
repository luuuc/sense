package conventions

import (
	"reflect"
	"testing"
)

// TestPickRepresentativesDropsRepeatedNames models the dominant maket defect:
// detectNaming emits many examples sharing a (Name, Path) — e.g. *_bar.html.erb,
// where one name repeats with the same enriched EdgeCount. Before dedupe the
// top-3 showed the same name three times; after dedupe the repeats collapse and
// the next distinct names surface into the window. The input is ordered so the
// pre-fix SliceStable-by-EdgeCount returns the repeats deterministically, making
// this a reliable (not flaky) regression.
func TestPickRepresentativesDropsRepeatedNames(t *testing.T) {
	examples := []Example{
		{Name: "_horizontal_bar", Path: "a.erb", EdgeCount: 5},
		{Name: "_horizontal_bar", Path: "a.erb", EdgeCount: 5},
		{Name: "_horizontal_bar", Path: "a.erb", EdgeCount: 5},
		{Name: "_vertical_bar", Path: "b.erb", EdgeCount: 5},
		{Name: "_priority_actions", Path: "c.erb", EdgeCount: 3},
	}
	got := PickRepresentatives(examples, 3)
	want := []string{"_horizontal_bar", "_vertical_bar", "_priority_actions"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PickRepresentatives = %v, want %v (no repeated names, distinct names surfaced)", got, want)
	}
}

// TestPickRepresentativesTiebreak asserts the EdgeCount → Name → Path cascade
// for distinct examples that share an EdgeCount: equal weight falls through to
// Name ascending, and equal (EdgeCount, Name) falls through to Path ascending.
func TestPickRepresentativesTiebreak(t *testing.T) {
	examples := []Example{
		{Name: "User", Path: "z.go", EdgeCount: 10},
		{Name: "Dup", Path: "b.go", EdgeCount: 10},
		{Name: "Item", Path: "a.go", EdgeCount: 10},
		{Name: "Dup", Path: "a.go", EdgeCount: 10},
		{Name: "Order", Path: "m.go", EdgeCount: 7},
	}
	got := PickRepresentatives(examples, 5)
	// EdgeCount desc: the four 10s before the 7. Among the 10s, Name asc puts the
	// two "Dup" first, broken by Path asc (a.go before b.go), then Item, then User.
	want := []string{"Dup", "Dup", "Item", "User", "Order"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PickRepresentatives = %v, want %v", got, want)
	}
}
