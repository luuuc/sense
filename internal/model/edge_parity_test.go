package model_test

import (
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/model"
)

// TestAllEdgeKindsIsComplete keeps the vocabulary list honest. A kind constant that
// exists but is missing from AllEdgeKinds would make every parity check below vacuous:
// consumers would be measured against an incomplete vocabulary and pass by omission.
//
// Kept as an explicit expectation rather than reflection over the package, because the
// point is that a human adding a constant must also add it here and then face the
// consumer parity test.
func TestAllEdgeKindsIsComplete(t *testing.T) {
	want := map[model.EdgeKind]bool{
		model.EdgeCalls: true, model.EdgeImports: true, model.EdgeInherits: true,
		model.EdgeIncludes: true, model.EdgeTests: true, model.EdgeComposes: true,
		model.EdgeTemporal: true, model.EdgeReferences: true,
	}
	if len(model.AllEdgeKinds) != len(want) {
		t.Fatalf("AllEdgeKinds has %d kinds, expected %d: a new constant must be added to "+
			"AllEdgeKinds, and every consumer must then include or exempt it",
			len(model.AllEdgeKinds), len(want))
	}
	for _, k := range model.AllEdgeKinds {
		if !want[k] {
			t.Errorf("AllEdgeKinds contains unexpected kind %q", k)
		}
		delete(want, k)
	}
	for k := range want {
		t.Errorf("AllEdgeKinds is missing %q", k)
	}
}

func TestSQLKindList(t *testing.T) {
	got := model.SQLKindList([]model.EdgeKind{model.EdgeCalls, model.EdgeImports})
	if got != "'calls','imports'" {
		t.Errorf("SQLKindList = %q, want %q", got, "'calls','imports'")
	}
	if model.SQLKindList(nil) != "" {
		t.Errorf("SQLKindList(nil) = %q, want empty", model.SQLKindList(nil))
	}
	// Every kind renders as a quoted literal, so a generated IN-list is never malformed.
	all := model.SQLKindList(model.AllEdgeKinds)
	if strings.Count(all, "'") != 2*len(model.AllEdgeKinds) {
		t.Errorf("SQLKindList(all) = %q:each kind needs exactly two quotes", all)
	}
}
