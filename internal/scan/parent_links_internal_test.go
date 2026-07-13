package scan

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/luuuc/sense/internal/sqlite"
)

// TestPickParent pins the resolution rule: same file beats same directory,
// same directory beats nothing, cross-directory never binds, and within a
// tier the smallest (path, line, id) wins — independent of candidate order.
func TestPickParent(t *testing.T) {
	pending := pendingParent{SymbolID: 99, ParentQualified: "mvcc.Store", FileID: 10, Dir: "server/mvcc"}

	sameFile := sqlite.ContainerRef{ID: 1, Qualified: "mvcc.Store", FileID: 10, Path: "server/mvcc/kvstore.go", Line: 40}
	sameDirA := sqlite.ContainerRef{ID: 2, Qualified: "mvcc.Store", FileID: 11, Path: "server/mvcc/a_store.go", Line: 12}
	sameDirB := sqlite.ContainerRef{ID: 3, Qualified: "mvcc.Store", FileID: 12, Path: "server/mvcc/b_store.go", Line: 5}
	sameDirALater := sqlite.ContainerRef{ID: 4, Qualified: "mvcc.Store", FileID: 13, Path: "server/mvcc/a_store.go", Line: 30}
	crossDir := sqlite.ContainerRef{ID: 5, Qualified: "mvcc.Store", FileID: 20, Path: "tools/mvcc/store.go", Line: 3}

	cases := []struct {
		name       string
		candidates []sqlite.ContainerRef
		wantID     int64
		wantOK     bool
	}{
		{"no candidates", nil, 0, false},
		{"cross-directory never binds", []sqlite.ContainerRef{crossDir}, 0, false},
		{"same file wins over same dir", []sqlite.ContainerRef{sameDirA, sameFile, crossDir}, 1, true},
		{"same dir wins over cross dir", []sqlite.ContainerRef{crossDir, sameDirB}, 3, true},
		{"same dir: smallest path wins", []sqlite.ContainerRef{sameDirB, sameDirA}, 2, true},
		{"same path: smallest line wins", []sqlite.ContainerRef{sameDirALater, sameDirA}, 2, true},
		{"order independence", []sqlite.ContainerRef{crossDir, sameDirALater, sameDirB, sameDirA}, 2, true},
		{"order independence reversed", []sqlite.ContainerRef{sameDirA, sameDirB, sameDirALater, crossDir}, 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := pickParent(pending, tc.candidates)
			if ok != tc.wantOK || id != tc.wantID {
				t.Errorf("pickParent = (%d, %v), want (%d, %v)", id, ok, tc.wantID, tc.wantOK)
			}
		})
	}
}

// TestLessContainerRefTotalOrder pins the final id key: identical
// (path, line) still yields one deterministic winner.
func TestLessContainerRefTotalOrder(t *testing.T) {
	a := &sqlite.ContainerRef{ID: 7, Path: "x/a.go", Line: 3}
	b := &sqlite.ContainerRef{ID: 9, Path: "x/a.go", Line: 3}
	if !lessContainerRef(a, b) || lessContainerRef(b, a) {
		t.Errorf("id tiebreak not a strict total order")
	}
}

// errInjectedContainers is the sentinel the fault store below returns.
var errInjectedContainers = errors.New("injected ContainerRefs failure")

// faultContainerStore embeds the real adapter and fails exactly one named
// method, ContainerRefs — the seam substitution that drives the parent
// pass's error branch in deriveIncremental (rollback_internal_test.go sets
// the pattern).
type faultContainerStore struct {
	*sqlite.Adapter
}

func (f *faultContainerStore) ContainerRefs(_ context.Context) ([]sqlite.ContainerRef, error) {
	return nil, errInjectedContainers
}

// TestDeriveIncrementalParentLinkError pins the incremental pipeline's
// error wrap: a failing container load surfaces as a resolve-parent-links
// error instead of being swallowed.
func TestDeriveIncrementalParentLinkError(t *testing.T) {
	dir := t.TempDir()
	adapter := openIndex(t, dir)
	t.Cleanup(func() { _ = adapter.Close() })

	h := &harness{
		ctx:       context.Background(),
		idx:       &faultContainerStore{Adapter: adapter},
		out:       io.Discard,
		warn:      io.Discard,
		progress:  newProgress(io.Discard, true),
		collector: newWarningCollector(),
		seenPaths: map[string]bool{},
		pendingParents: []pendingParent{
			{SymbolID: 1, ParentQualified: "mvcc.Store", FileID: 1, Dir: "server"},
		},
	}

	var phases PhaseTiming
	err := h.deriveIncremental(false, &phases)
	if err == nil || !errors.Is(err, errInjectedContainers) {
		t.Fatalf("deriveIncremental error = %v, want wrapped injected ContainerRefs failure", err)
	}
}

// errInjectedLangQuery drives the naming pass's error branch after the
// parent pass succeeds, pinning deriveIncremental's downstream wrap.
var errInjectedLangQuery = errors.New("injected FileIDsByLanguage failure")

type faultLangStore struct {
	*sqlite.Adapter
}

func (f *faultLangStore) FileIDsByLanguage(_ context.Context, _ string) (map[int64]bool, error) {
	return nil, errInjectedLangQuery
}

func TestDeriveIncrementalNamingError(t *testing.T) {
	dir := t.TempDir()
	adapter := openIndex(t, dir)
	t.Cleanup(func() { _ = adapter.Close() })

	h := &harness{
		ctx:       context.Background(),
		idx:       &faultLangStore{Adapter: adapter},
		out:       io.Discard,
		warn:      io.Discard,
		progress:  newProgress(io.Discard, true),
		collector: newWarningCollector(),
		seenPaths: map[string]bool{},
	}

	var phases PhaseTiming
	err := h.deriveIncremental(false, &phases)
	if err == nil || !errors.Is(err, errInjectedLangQuery) {
		t.Fatalf("deriveIncremental error = %v, want wrapped injected language-query failure", err)
	}
}
