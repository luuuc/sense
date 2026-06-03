package scan_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/scan/scantest"
	"github.com/luuuc/sense/internal/sqlite"
)

// coChange builds a Commit that rewrites the same two cross-directory
// files at revision i. The pair lives in pkg/ and lib/ so temporal
// coupling (which ignores same-directory pairs) can form an edge between
// them. They are Ruby classes with a real inherits edge (B < A) so each
// file's representative symbol carries non-temporal connectivity —
// exercising representativeSymbols' inbound and outbound connectivity
// queries, not just its empty-graph path.
func coChange(i int) scantest.Commit {
	return scantest.Commit{
		Files: map[string]string{
			"pkg/a.rb": fmt.Sprintf("# rev %d\nclass A\n  def hi; end\nend\n", i),
			"lib/b.rb": fmt.Sprintf("# rev %d\nclass B < A\n  def yo; end\nend\n", i),
		},
		Message: fmt.Sprintf("co-change %d", i),
	}
}

func temporalEdgeCount(t *testing.T, adapter *sqlite.Adapter) int {
	t.Helper()
	edges, err := adapter.EdgesOfKind(context.Background(), model.EdgeTemporal)
	if err != nil {
		t.Fatalf("EdgesOfKind: %v", err)
	}
	return len(edges)
}

// TestExtractTemporalCoupling_whenPairCoChangesAboveThreshold_writesBidirectionalEdges
// is the positive path the negative cases never reach: a cross-directory
// pair that co-changes four times (≥ minCoChanges) with both files indexed
// drives the full pipeline — significant-pair selection, representativeSymbols,
// clearTemporalEdges, and the bidirectional edge write. It asserts the
// observable graph: one temporal edge each way, carrying the co-change count
// on Line and strength 1.0 on Confidence (4 co-changes / 4 max changes).
func TestExtractTemporalCoupling_whenPairCoChangesAboveThreshold_writesBidirectionalEdges(t *testing.T) {
	repo := scantest.NewRepo(t, nil)
	commits := make([]scantest.Commit, 4)
	for i := range commits {
		commits[i] = coChange(i)
	}
	repo.WithGitHistory(commits)

	ctx := context.Background()
	_, adapter := repo.Scan(scan.Options{})

	edges, err := adapter.EdgesOfKind(ctx, model.EdgeTemporal)
	if err != nil {
		t.Fatalf("EdgesOfKind: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("temporal edges = %d, want 2 (one each way for the pkg/a.rb ↔ lib/b.rb pair)", len(edges))
	}

	// Both directions present: the two edges are mirror images.
	a, b := edges[0], edges[1]
	if a.SourceID == nil || b.SourceID == nil {
		t.Fatalf("temporal edges must have source symbols, got %+v and %+v", a, b)
	}
	if *a.SourceID != b.TargetID || *b.SourceID != a.TargetID {
		t.Errorf("expected mirrored edges, got A(src=%d,tgt=%d) B(src=%d,tgt=%d)",
			*a.SourceID, a.TargetID, *b.SourceID, b.TargetID)
	}
	for _, e := range edges {
		if e.Kind != model.EdgeTemporal {
			t.Errorf("edge kind = %q, want %q", e.Kind, model.EdgeTemporal)
		}
		if e.Confidence != 1.0 {
			t.Errorf("edge confidence = %v, want 1.0 (4 co-changes / 4 max changes)", e.Confidence)
		}
		if e.Line == nil || *e.Line != 4 {
			t.Errorf("edge Line = %v, want 4 (co-change count)", e.Line)
		}
	}
}

// TestExtractTemporalCoupling_multiplePairsExerciseRankingAndStrength drives
// the branches a single symmetric pair never reaches:
//   - two significant pairs, so the deterministic sort comparator runs;
//   - an asymmetric change count, so the "other file changed more" branch
//     in the strength denominator fires;
//   - a co-changing file with no symbols (comment-only), so both the
//     skip-file-without-symbols path in representativeSymbols and the
//     skip-pair-without-a-representative path in the edge loop run.
//
// The graph assertion stays observable: only the A↔B pair (both files have
// a class) yields edges; the pair involving the symbol-less file is dropped.
func TestExtractTemporalCoupling_multiplePairsExerciseRankingAndStrength(t *testing.T) {
	repo := scantest.NewRepo(t, nil)

	classA := func(i int) string { return fmt.Sprintf("# rev %d\nclass A\n  def hi; end\nend\n", i) }
	classB := func(i int) string { return fmt.Sprintf("# rev %d\nclass B < A\n  def yo; end\nend\n", i) }
	notesOnly := func(i int) string { return fmt.Sprintf("# rev %d — comments only, no symbols\n", i) }

	var commits []scantest.Commit
	// 4 commits couple x/a.rb ↔ z/b.rb (A↔B co-change = 4).
	for i := 0; i < 4; i++ {
		commits = append(commits, scantest.Commit{
			Files:   map[string]string{"x/a.rb": classA(i), "z/b.rb": classB(i)},
			Message: fmt.Sprintf("ab %d", i),
		})
	}
	// 4 commits couple z/b.rb ↔ w/notes.rb (B↔notes co-change = 4), which
	// pushes z/b.rb's total change count to 8 — higher than its partner in
	// either pair, exercising the asymmetric-strength branch.
	for i := 0; i < 4; i++ {
		commits = append(commits, scantest.Commit{
			Files:   map[string]string{"z/b.rb": classB(i + 4), "w/notes.rb": notesOnly(i)},
			Message: fmt.Sprintf("bn %d", i),
		})
	}
	repo.WithGitHistory(commits)

	_, adapter := repo.Scan(scan.Options{})

	// Only A↔B produces edges; B↔notes is dropped because notes.rb has no
	// representative symbol. Bidirectional ⇒ exactly 2 temporal edges.
	if got := temporalEdgeCount(t, adapter); got != 2 {
		t.Errorf("temporal edges = %d, want 2 (only the A↔B pair anchors symbols)", got)
	}
}

// TestExtractTemporalCoupling_sharedFirstFileSortsBySecond covers the
// deterministic ordering's secondary tiebreak: two significant pairs that
// share their first (lexically smaller) file must be ordered by their
// second file. One hub file (a/hub.rb) co-changes with two leaves in
// other directories, producing pairs (a/hub.rb, m/x.rb) and
// (a/hub.rb, n/y.rb) — equal on the first element, so the comparator falls
// through to comparing the second.
func TestExtractTemporalCoupling_sharedFirstFileSortsBySecond(t *testing.T) {
	repo := scantest.NewRepo(t, nil)
	hub := func(i int) string { return fmt.Sprintf("# rev %d\nclass Hub\n  def go; end\nend\n", i) }
	leaf := func(name string, i int) string {
		return fmt.Sprintf("# rev %d\nclass %s < Hub\nend\n", i, name)
	}
	var commits []scantest.Commit
	for i := 0; i < 4; i++ {
		commits = append(commits, scantest.Commit{
			Files:   map[string]string{"a/hub.rb": hub(i), "m/x.rb": leaf("X", i)},
			Message: fmt.Sprintf("hx %d", i),
		})
	}
	for i := 0; i < 4; i++ {
		commits = append(commits, scantest.Commit{
			Files:   map[string]string{"a/hub.rb": hub(i + 4), "n/y.rb": leaf("Y", i)},
			Message: fmt.Sprintf("hy %d", i),
		})
	}
	repo.WithGitHistory(commits)

	_, adapter := repo.Scan(scan.Options{})

	// Two pairs, both bidirectional ⇒ 4 temporal edges.
	if got := temporalEdgeCount(t, adapter); got != 4 {
		t.Errorf("temporal edges = %d, want 4 (hub↔x and hub↔y, both directions)", got)
	}
}

// TestExtractTemporalCoupling_isIdempotentAcrossRescans pins clearTemporalEdges'
// purpose: a second scan must not double the temporal edges. Without the
// clear-before-recompute step the count would grow each run.
func TestExtractTemporalCoupling_isIdempotentAcrossRescans(t *testing.T) {
	repo := scantest.NewRepo(t, nil)
	commits := make([]scantest.Commit, 4)
	for i := range commits {
		commits[i] = coChange(i)
	}
	repo.WithGitHistory(commits)

	_, firstAdapter := repo.Scan(scan.Options{})
	if got := temporalEdgeCount(t, firstAdapter); got != 2 {
		t.Fatalf("temporal edges after first scan = %d, want 2 (baseline for the idempotency check)", got)
	}

	_, adapter := repo.Scan(scan.Options{})
	if got := temporalEdgeCount(t, adapter); got != 2 {
		t.Errorf("temporal edges after rescan = %d, want 2 (clearTemporalEdges must prevent doubling)", got)
	}
}

// TestExtractTemporalCoupling_whenCoChangesBelowThreshold_writesNoEdges
// exercises the "drop pairs below minCoChanges" branch: a cross-directory
// pair that co-changes only twice (below the threshold of three) must
// produce zero temporal edges even though it is structurally eligible.
func TestExtractTemporalCoupling_whenCoChangesBelowThreshold_writesNoEdges(t *testing.T) {
	repo := scantest.NewRepo(t, nil)
	repo.WithGitHistory([]scantest.Commit{coChange(0), coChange(1)})

	_, adapter := repo.Scan(scan.Options{})

	if got := temporalEdgeCount(t, adapter); got != 0 {
		t.Errorf("expected 0 temporal edges below threshold, got %d", got)
	}
}

// TestExtractTemporalCoupling_whenNoIndexedFiles_returnsEarly covers the
// len(indexedFiles)==0 branch: commits touching only unindexed files (plain
// text) leave temporal coupling nothing to anchor against, so it must
// short-circuit cleanly.
func TestExtractTemporalCoupling_whenNoIndexedFiles_returnsEarly(t *testing.T) {
	repo := scantest.NewRepo(t, nil)
	var commits []scantest.Commit
	for i := 0; i < 4; i++ {
		commits = append(commits, scantest.Commit{
			Files: map[string]string{
				"docs/a.txt":  fmt.Sprintf("v%d\n", i),
				"notes/b.txt": fmt.Sprintf("v%d\n", i),
			},
			Message: fmt.Sprintf("co-change %d", i),
		})
	}
	repo.WithGitHistory(commits)

	_, adapter := repo.Scan(scan.Options{})

	if got := temporalEdgeCount(t, adapter); got != 0 {
		t.Errorf("expected 0 temporal edges when no files are indexed, got %d", got)
	}
}

// TestExtractTemporalCoupling_whenRepoHasNoHistory_writesNoEdges exercises
// the early return when parseGitLog yields no commits (a git repo with an
// indexed file but no commits yet).
func TestExtractTemporalCoupling_whenRepoHasNoHistory_writesNoEdges(t *testing.T) {
	repo := scantest.NewRepo(t, nil)
	repo.WithGitHistory(nil) // init only, no commits
	repo.Write("pkg/a.go", "package pkg\nfunc A() {}\n")

	_, adapter := repo.Scan(scan.Options{})

	if got := temporalEdgeCount(t, adapter); got != 0 {
		t.Errorf("expected 0 temporal edges with no commits, got %d", got)
	}
}
