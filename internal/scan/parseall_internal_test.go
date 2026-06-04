package scan

import (
	"path/filepath"
	"testing"
)

// TestParseAllFilesPreservesEntryOrder asserts the contract the parallel parse
// phase rests on: results[i] always corresponds to entries[i], regardless of the
// order the bounded worker pool happens to finish in. Each file is given a
// distinct class name, and the test checks every result slot reports the symbol
// from its own entry — so a reordering bug (e.g. a channel-collect that returns
// results in completion order) would surface as a mismatch. Runs under `-race`
// in `make cover`, which also exercises the concurrent results[i] writes.
func TestParseAllFilesPreservesEntryOrder(t *testing.T) {
	dir := t.TempDir()
	const n = 25
	entries := make([]walkEntry, n)
	for i := 0; i < n; i++ {
		rel := filepath.Join("pkg", className(i)+".rb")
		writeSource(t, dir, rel, "class "+className(i)+"\nend\n")
		entries[i] = walkEntry{path: filepath.Join(dir, rel), rel: rel}
	}

	h := newWalkHarness(newClosedAdapter(t)) // parse phase never touches the store
	t.Cleanup(h.closeParsers)

	results, err := h.parseAllFiles(entries, nil)
	if err != nil {
		t.Fatalf("parseAllFiles: %v", err)
	}
	if len(results) != n {
		t.Fatalf("results length = %d, want %d", len(results), n)
	}

	for i, fr := range results {
		if fr == nil {
			t.Errorf("results[%d] is nil; want a parse result for %s", i, entries[i].rel)
			continue
		}
		if fr.Rel != entries[i].rel {
			t.Errorf("results[%d].Rel = %q, want %q — result/entry order drifted", i, fr.Rel, entries[i].rel)
		}
		if len(fr.Symbols) == 0 || fr.Symbols[0].Qualified != className(i) {
			t.Errorf("results[%d] for %s did not yield class %q (symbols=%v)", i, entries[i].rel, className(i), fr.Symbols)
		}
	}
}

// className returns a stable per-index class name (Cls000, Cls001, ...), unique
// per file so a misaligned result is unambiguous.
func className(i int) string {
	return "Cls" + string(rune('A'+i/26)) + string(rune('A'+i%26))
}

// TestCollectPathsWalkErrorPropagates covers collectPaths' WalkDir-error return:
// walking a path that does not exist surfaces the error rather than returning an
// empty entry set that would silently index nothing.
func TestCollectPathsWalkErrorPropagates(t *testing.T) {
	h := newWalkHarness(newClosedAdapter(t))
	if _, err := h.collectPaths(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("expected an error walking a missing root")
	}
}

// TestPreloadHashesErrorPropagates covers preloadHashes' error return: a closed
// index can't serve the hash map, and the wrapped error must surface so the walk
// fails loudly instead of treating every file as changed.
func TestPreloadHashesErrorPropagates(t *testing.T) {
	h := newWalkHarness(newClosedAdapter(t))
	if _, err := h.preloadHashes(); err == nil {
		t.Fatal("expected an error loading hashes from a closed index")
	}
}
