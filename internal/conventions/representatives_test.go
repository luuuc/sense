package conventions

import (
	"reflect"
	"strings"
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

// TestRepresentativeLabelsUniqueStayBare asserts the no-collision path: distinct
// names render bare, with no parenthetical suffix.
func TestRepresentativeLabelsUniqueStayBare(t *testing.T) {
	examples := []Example{
		{Name: "User", Path: "app/models/user.rb", EdgeCount: 10},
		{Name: "Order", Path: "app/models/order.rb", EdgeCount: 9},
		{Name: "Item", Path: "app/models/item.rb", EdgeCount: 8},
	}
	got := RepresentativeLabels(examples, 5)
	want := []string{"User", "Order", "Item"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RepresentativeLabels = %v, want %v", got, want)
	}
}

// TestRepresentativeLabelsTwoSegmentCollision is the headline maket case: four
// files share the basename Name _add_modal.html.erb. One trailing segment is
// enough for the two that sit under unique dirs, but the two under a
// categorizations/ dir must extend to a second segment to separate. The picked
// set is ordered by the EdgeCount→Name→Path cascade, so equal-weight equal-name
// examples emerge in Path-ascending order — the want order reflects that.
func TestRepresentativeLabelsTwoSegmentCollision(t *testing.T) {
	examples := []Example{
		{Name: "_add_modal.html.erb", Path: "app/views/admin/catalog/categories/specifications/_add_modal.html.erb", EdgeCount: 4},
		{Name: "_add_modal.html.erb", Path: "app/views/admin/catalog/categories/categorizations/_add_modal.html.erb", EdgeCount: 4},
		{Name: "_add_modal.html.erb", Path: "app/views/admin/catalog/navigations/categorizations/_add_modal.html.erb", EdgeCount: 4},
		{Name: "_add_modal.html.erb", Path: "app/views/admin/catalog/specification_definitions/specification_values/_add_modal.html.erb", EdgeCount: 4},
	}
	got := RepresentativeLabels(examples, 5)
	want := []string{
		"_add_modal.html.erb (categories/categorizations)",
		"_add_modal.html.erb (specifications)",
		"_add_modal.html.erb (navigations/categorizations)",
		"_add_modal.html.erb (specification_values)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RepresentativeLabels =\n %v\nwant\n %v", got, want)
	}
}

// TestRepresentativeLabelsSymbolCollision covers a symbol-name collision (same
// class in two trees): one trailing segment collides (both "controllers"), so
// the suffix extends to "app/controllers" vs "test/controllers".
func TestRepresentativeLabelsSymbolCollision(t *testing.T) {
	examples := []Example{
		{Name: "AcceptancesController", Path: "app/controllers/acceptances_controller.rb", EdgeCount: 5},
		{Name: "AcceptancesController", Path: "test/controllers/acceptances_controller.rb", EdgeCount: 5},
	}
	got := RepresentativeLabels(examples, 5)
	want := []string{
		"AcceptancesController (app/controllers)",
		"AcceptancesController (test/controllers)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RepresentativeLabels = %v, want %v", got, want)
	}
}

// TestRepresentativeLabelsPreserveSet is the no-counts-change property: labeling
// is pure decoration over the picked set. For any input, RepresentativeLabels
// selects exactly the same examples in the same order as PickRepresentatives —
// each label is the raw name, optionally with a " (suffix)" appended, never a
// drop, reorder, or substitution. This keeps Instances/Total honest: the four
// colliding files stay four instances; only their displayed names change.
func TestRepresentativeLabelsPreserveSet(t *testing.T) {
	fixtures := [][]Example{
		{
			{Name: "User", Path: "app/models/user.rb", EdgeCount: 10},
			{Name: "Order", Path: "app/models/order.rb", EdgeCount: 9},
		},
		{
			{Name: "X", Path: "a/controllers/x.rb", EdgeCount: 5},
			{Name: "X", Path: "b/controllers/x.rb", EdgeCount: 5},
			{Name: "Y", Path: "c/y.rb", EdgeCount: 4},
		},
		{
			{Name: "_modal.erb", Path: "views/a/categories/_modal.erb", EdgeCount: 3},
			{Name: "_modal.erb", Path: "views/b/categories/_modal.erb", EdgeCount: 3},
			{Name: "_modal.erb", Path: "views/c/_modal.erb", EdgeCount: 3},
		},
	}
	for fi, examples := range fixtures {
		names := PickRepresentatives(examples, 5)
		labels := RepresentativeLabels(examples, 5)
		if len(labels) != len(names) {
			t.Fatalf("fixture %d: %d labels, %d names — set size changed", fi, len(labels), len(names))
		}
		for i, label := range labels {
			base := label
			if idx := strings.Index(label, " ("); idx >= 0 {
				base = label[:idx]
			}
			if base != names[i] {
				t.Errorf("fixture %d index %d: label %q has base %q, want raw name %q", fi, i, label, base, names[i])
			}
		}
	}
}

// TestRepresentativeLabelsBasenameFallback covers siblings that share a Name and
// an entire directory, differing only in basename. Directory segments cannot
// distinguish them, so the rule falls back to the full Path — unique by
// construction (dedupeExamples guarantees distinct (Name, Path)).
func TestRepresentativeLabelsBasenameFallback(t *testing.T) {
	examples := []Example{
		{Name: "Widget", Path: "app/models/widget_a.rb", EdgeCount: 6},
		{Name: "Widget", Path: "app/models/widget_b.rb", EdgeCount: 6},
	}
	got := RepresentativeLabels(examples, 5)
	want := []string{
		"Widget (app/models/widget_a.rb)",
		"Widget (app/models/widget_b.rb)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RepresentativeLabels = %v, want %v", got, want)
	}
}

// TestRepresentativeLabelsStrictSuffixDir covers a shallow member whose only dir
// suffix is shared by a deeper sibling: "categorizations" (depth 1) sits inside
// "categories/categorizations" (depth 2). The shallow member cannot extend, so it
// falls back to its full Path; the deep member separates at depth 2. The labels
// are unambiguous (no escalation), just asymmetric — this pins that degradation.
func TestRepresentativeLabelsStrictSuffixDir(t *testing.T) {
	examples := []Example{
		{Name: "_modal.erb", Path: "categorizations/_modal.erb", EdgeCount: 3},
		{Name: "_modal.erb", Path: "categories/categorizations/_modal.erb", EdgeCount: 3},
	}
	got := RepresentativeLabels(examples, 5)
	// Path-ascending: "categories/..." sorts before "categorizations/..." ('e'<'z').
	want := []string{
		"_modal.erb (categories/categorizations)",
		"_modal.erb (categorizations/_modal.erb)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RepresentativeLabels =\n %v\nwant\n %v", got, want)
	}
}

// TestRepresentativeLabelsEscalatesCrossNamespaceCollision covers the residual
// collision the escalation backstop exists for: a shallow member's full-Path
// fallback ("foo/bar") equals a deeper member's dir suffix ("foo/bar"). Without
// the backstop both would render "Thing (foo/bar)"; with it, the whole group
// relabels to full Paths, which are distinct by construction.
func TestRepresentativeLabelsEscalatesCrossNamespaceCollision(t *testing.T) {
	examples := []Example{
		{Name: "Thing", Path: "foo/bar", EdgeCount: 1},
		{Name: "Thing", Path: "p/foo/bar/qux.go", EdgeCount: 1},
		{Name: "Thing", Path: "x/foo/baz.go", EdgeCount: 1},
		{Name: "Thing", Path: "z/bar/m.go", EdgeCount: 1},
	}
	got := RepresentativeLabels(examples, 5)
	// Path-ascending order; every member shown as its full Path, all distinct.
	want := []string{
		"Thing (foo/bar)",
		"Thing (p/foo/bar/qux.go)",
		"Thing (x/foo/baz.go)",
		"Thing (z/bar/m.go)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RepresentativeLabels =\n %v\nwant\n %v", got, want)
	}
	// The invariant the backstop guarantees: no two labels in the group collide.
	seen := map[string]bool{}
	for _, l := range got {
		if seen[l] {
			t.Fatalf("duplicate label %q survived escalation", l)
		}
		seen[l] = true
	}
}

// TestRepresentativeLabelsRootLevelFallback covers same-named root-level files
// (no directory to distinguish them): dirSegments yields nothing, so the suffix
// is the full Path.
func TestRepresentativeLabelsRootLevelFallback(t *testing.T) {
	examples := []Example{
		{Name: "Config", Path: "config_a.go", EdgeCount: 2},
		{Name: "Config", Path: "config_b.go", EdgeCount: 2},
	}
	got := RepresentativeLabels(examples, 5)
	want := []string{"Config (config_a.go)", "Config (config_b.go)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RepresentativeLabels = %v, want %v", got, want)
	}
}
