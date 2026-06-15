package scan

import (
	"reflect"
	"testing"

	"github.com/luuuc/sense/internal/model"
)

func TestBuildAncestry(t *testing.T) {
	h := &harness{
		pendingEdges: []pendingEdge{
			// inherits edges → recorded as child → [parent].
			{SourceQualified: "Sub", TargetName: "Base", Kind: model.EdgeInherits},
			{SourceQualified: "Sub2", TargetName: "Base", Kind: model.EdgeInherits},
			// A reopened class with a divergent superclass yields a multi-parent
			// slice (rare, but the map must hold it).
			{SourceQualified: "Sub", TargetName: "OtherBase", Kind: model.EdgeInherits},
			// Non-inherits edges are ignored.
			{SourceQualified: "Sub", TargetName: "Mixin", Kind: model.EdgeIncludes},
			{SourceQualified: "Caller#m", TargetName: "Base#perform", Kind: model.EdgeCalls},
			// Edges with an empty endpoint are skipped.
			{SourceQualified: "", TargetName: "Base", Kind: model.EdgeInherits},
			{SourceQualified: "Sub3", TargetName: "", Kind: model.EdgeInherits},
		},
	}

	got := h.buildAncestry()
	want := map[string][]string{
		"Sub":  {"Base", "OtherBase"},
		"Sub2": {"Base"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildAncestry() = %#v, want %#v", got, want)
	}
}

func TestBuildAncestryEmpty(t *testing.T) {
	h := &harness{}
	if got := h.buildAncestry(); len(got) != 0 {
		t.Errorf("buildAncestry() on no edges = %#v, want empty", got)
	}
}
