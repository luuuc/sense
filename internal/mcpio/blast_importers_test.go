package mcpio

import (
	"context"
	"testing"

	"github.com/luuuc/sense/internal/blast"
)

// TestImporterGroupNeverFlipsTheVerdict is named for the failure it prevents, not the
// field it touches. Adding importer rows to the radius was tried and measured: at
// min_confidence 0.3 it took total_affected 12 -> 45 and flipped completeness from
// `complete` to `partial`, which is the "Sense is unsure, re-grep everything" behaviour
// that cost us once already. The group must be inert with respect to both numbers.
func TestImporterGroupNeverFlipsTheVerdict(t *testing.T) {
	base := blast.Result{TotalAffected: 0}
	withGroup := blast.Result{
		TotalAffected:            0,
		ImportersNotReached:      []string{"src/Widgets/Alpha.php", "src/Widgets/Beta.php"},
		ImportersNotReachedCount: 2,
	}

	// A real caller always supplies a FileLookup; nil panics at the subject-file
	// lookup, so the stub keeps this test about the verdict rather than about nils.
	files := func(int64) (string, bool) { return "src/Facades/Thing.php", true }

	plain := BuildBlastResponse(context.Background(), base, files, nil)
	grouped := BuildBlastResponse(context.Background(), withGroup, files, nil)

	if plain.TotalAffected != grouped.TotalAffected {
		t.Errorf("total_affected moved: %d -> %d", plain.TotalAffected, grouped.TotalAffected)
	}
	if plain.Completeness == nil || grouped.Completeness == nil {
		t.Fatal("completeness missing")
	}
	if plain.Completeness.Verdict != grouped.Completeness.Verdict {
		t.Errorf("verdict moved: %q -> %q", plain.Completeness.Verdict, grouped.Completeness.Verdict)
	}
	if plain.Completeness.Resolved != grouped.Completeness.Resolved {
		t.Errorf("resolved moved: %d -> %d", plain.Completeness.Resolved, grouped.Completeness.Resolved)
	}
	if len(grouped.ImportersNotReached) != 2 || grouped.ImportersNotReachedCount != 2 {
		t.Errorf("the group itself did not surface: %v / %d",
			grouped.ImportersNotReached, grouped.ImportersNotReachedCount)
	}
	if grouped.ImportersNote == "" {
		t.Error("a populated group must carry its shared note")
	}
	if plain.ImportersNote != "" {
		t.Error("an empty group must not carry a note")
	}
}
