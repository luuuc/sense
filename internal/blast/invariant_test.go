package blast

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
)

// Name-collision resolutions are stamped at extract.ConfidenceNameCollision
// specifically so blast's BFS ignores them. That only holds while the stamp
// stays below the traversal floor — guard the cross-package invariant here so
// a future change to either constant fails loudly instead of silently
// re-admitting guesses into impact analysis.
func TestNameCollisionBelowTraversalFloor(t *testing.T) {
	if extract.ConfidenceNameCollision >= defaultMinConfidence {
		t.Fatalf("ConfidenceNameCollision (%v) must stay below blast defaultMinConfidence (%v)",
			extract.ConfidenceNameCollision, defaultMinConfidence)
	}
}
