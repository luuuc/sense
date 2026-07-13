package conventions

import "testing"

// TestGoEmbedDescriptionKindAware pins the composition wording split: struct
// embedders promote methods, interface embedders union contracts — an
// interface-sourced group must never read "structs embed".
func TestGoEmbedDescriptionKindAware(t *testing.T) {
	got := goEmbedDescription(3, "Resource", "A, B, C", "interface")
	if got != "3 interfaces embed Resource (interface embedding) (A, B, C)" {
		t.Errorf("interface wording wrong: %q", got)
	}
	got = goEmbedDescription(2, "Base", "X, Y", "class")
	if got != "2 structs embed Base (methods promoted) (X, Y)" {
		t.Errorf("struct wording wrong: %q", got)
	}
}

// TestDetectCompositionSkipsAndThreshold pins detectComposition's guards in
// one fixture: an edge with an unknown source, an edge with an unknown
// target, a test-file source, and a group below minInstances all contribute
// nothing, while a 3-strong interface-sourced Go group gets the kind-aware
// wording end to end.
func TestDetectCompositionSkipsAndThreshold(t *testing.T) {
	symbols := []symbolRow{
		{id: 1, fileID: 10, name: "Resource", qualified: "resource.Resource", kind: "interface"},
		{id: 2, fileID: 11, name: "PageA", qualified: "p.PageA", kind: "interface"},
		{id: 3, fileID: 12, name: "PageB", qualified: "p.PageB", kind: "interface"},
		{id: 4, fileID: 13, name: "PageC", qualified: "p.PageC", kind: "interface"},
		{id: 5, fileID: 14, name: "Lonely", qualified: "p.Lonely", kind: "class"},
		{id: 6, fileID: 15, name: "TestHelper", qualified: "p.TestHelper", kind: "class"},
	}
	symbolByID := map[int64]symbolRow{}
	for _, s := range symbols {
		symbolByID[s.id] = s
	}
	filePathByID := map[int64]string{
		10: "resource/resource.go", 11: "p/a.go", 12: "p/b.go", 13: "p/c.go",
		14: "p/lonely.go", 15: "p/helper_test.go",
	}
	edges := []edgeRow{
		{sourceID: 2, targetID: 1, kind: "includes"},
		{sourceID: 3, targetID: 1, kind: "includes"},
		{sourceID: 4, targetID: 1, kind: "includes"},
		{sourceID: 99, targetID: 1, kind: "includes"}, // unknown source: skipped
		{sourceID: 5, targetID: 98, kind: "includes"}, // unknown target: skipped
		{sourceID: 6, targetID: 1, kind: "includes"},  // test-file source: skipped
		{sourceID: 5, targetID: 2, kind: "includes"},  // group of 1 < minInstances
	}
	out := detectComposition(symbols, edges, symbolByID, filePathByID)
	if len(out) != 1 {
		t.Fatalf("expected exactly one composition convention, got %d: %+v", len(out), out)
	}
	want := "3 interfaces embed Resource (interface embedding) (PageA, PageB, PageC)"
	if out[0].Description != want {
		t.Errorf("kind-aware wording wrong:\n got %q\nwant %q", out[0].Description, want)
	}
	if out[0].Instances != 3 {
		t.Errorf("instances = %d, want 3 (skips must not count)", out[0].Instances)
	}
}
