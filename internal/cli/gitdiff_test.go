package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, out, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, out, err)
		}
	}
	return dir
}

// commit stages everything in dir and records a commit with the given message.
func commit(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", msg}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, out, err)
		}
	}
}

func TestGitDiffHunksValidRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	dir := initGitRepo(t)
	hunks, err := GitDiffHunks(context.Background(), dir, "HEAD~0")
	if err != nil {
		t.Fatalf("GitDiffHunks: %v", err)
	}
	if len(hunks) != 0 {
		t.Errorf("expected no diff for HEAD~0, got %v", hunks)
	}
}

func TestGitDiffHunksReportsChangedLines(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	dir := initGitRepo(t)
	// Grow the file, then edit a single line, so the hunk is tight to the
	// change rather than spanning the whole file.
	body := "package main\n\nfunc A() {}\nfunc B() {}\nfunc C() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	commit(t, dir, "grow file")
	edited := "package main\n\nfunc A() {}\nfunc B2() {}\nfunc C() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	hunks, err := GitDiffHunks(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatalf("GitDiffHunks: %v", err)
	}
	ranges, ok := hunks["hello.go"]
	if !ok {
		t.Fatalf("expected hello.go in hunks, got %v", hunks)
	}
	// Only line 4 changed; the range must not span the whole 5-line file.
	if len(ranges) != 1 || ranges[0].Start != 4 || ranges[0].End != 4 {
		t.Errorf("ranges = %v, want a single [4,4] span", ranges)
	}
}

func TestGitDiffHunksBadRefReturnsError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	dir := initGitRepo(t)
	_, err := GitDiffHunks(context.Background(), dir, "nonexistent-ref-abc123")
	if err == nil {
		t.Fatal("expected error for bad ref")
	}
}

func TestGitDiffHunksFlagInjectionBlocked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	dir := initGitRepo(t)

	// These payloads would be dangerous without --end-of-options. With it, git
	// interprets them as literal ref names (which don't exist) and errors —
	// not a flag-injection side effect.
	payloads := []string{
		"--upload-pack=evil",
		"--output=/tmp/evil",
		"-p",
		"--stat",
		"--no-index",
	}
	for _, p := range payloads {
		t.Run(p, func(t *testing.T) {
			_, err := GitDiffHunks(context.Background(), dir, p)
			if err == nil {
				t.Errorf("flag-like ref %q should error (bad revision), not be treated as a flag", p)
			}
		})
	}
}

func TestGitDiffHunksNoGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := GitDiffHunks(context.Background(), t.TempDir(), "HEAD")
	if err == nil {
		t.Error("expected error when git not on PATH")
	}
}

func TestParseDiffHunks(t *testing.T) {
	cases := []struct {
		name string
		diff string
		want map[string][]LineRange
	}{
		{
			name: "single edit",
			diff: "diff --git a/f.rb b/f.rb\n--- a/f.rb\n+++ b/f.rb\n@@ -4 +4 @@\n-old\n+new\n",
			want: map[string][]LineRange{"f.rb": {{4, 4}}},
		},
		{
			name: "multi-line addition with count",
			diff: "+++ b/app/x.rb\n@@ -10,0 +11,3 @@\n+a\n+b\n+c\n",
			want: map[string][]LineRange{"app/x.rb": {{11, 13}}},
		},
		{
			name: "pure deletion widens to bracket the gap",
			diff: "+++ b/app/x.rb\n@@ -5,3 +4,0 @@\n",
			want: map[string][]LineRange{"app/x.rb": {{4, 5}}},
		},
		{
			name: "two files, multiple hunks",
			diff: "+++ b/a.rb\n@@ -1 +1 @@\n+++ b/b.rb\n@@ -2,2 +2,2 @@\n@@ -9 +9,0 @@\n",
			want: map[string][]LineRange{
				"a.rb": {{1, 1}},
				"b.rb": {{2, 3}, {9, 10}},
			},
		},
		{
			name: "deletion target /dev/null contributes nothing",
			diff: "+++ /dev/null\n@@ -1,5 +0,0 @@\n",
			want: map[string][]LineRange{},
		},
		{
			name: "new file added covers all its lines",
			diff: "+++ b/new.rb\n@@ -0,0 +1,4 @@\n+l1\n+l2\n+l3\n+l4\n",
			want: map[string][]LineRange{"new.rb": {{1, 4}}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseDiffHunks(c.diff)
			if len(got) != len(c.want) {
				t.Fatalf("parseDiffHunks(%q) = %v, want %v", c.diff, got, c.want)
			}
			for path, wantRanges := range c.want {
				gotRanges := got[path]
				if len(gotRanges) != len(wantRanges) {
					t.Fatalf("path %q ranges = %v, want %v", path, gotRanges, wantRanges)
				}
				for i := range wantRanges {
					if gotRanges[i] != wantRanges[i] {
						t.Errorf("path %q range %d = %v, want %v", path, i, gotRanges[i], wantRanges[i])
					}
				}
			}
		})
	}
}

func TestNewFilePath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"+++ b/app/x.rb", "app/x.rb"},
		{"+++ a/app/x.rb", "app/x.rb"},
		{"+++ app/x.rb", "app/x.rb"},       // no prefix (diff.noprefix)
		{"+++ /dev/null", ""},              // deletion target
		{"+++ b/x.rb\t2026-01-01", "x.rb"}, // trailing tab metadata dropped
	}
	for _, c := range cases {
		if got := newFilePath(c.in); got != c.want {
			t.Errorf("newFilePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNewSideRange(t *testing.T) {
	cases := []struct {
		header string
		want   LineRange
		wantOK bool
	}{
		{"@@ -4 +4 @@", LineRange{4, 4}, true},         // count omitted defaults to 1
		{"@@ -10,0 +11,3 @@", LineRange{11, 13}, true}, // explicit count
		{"@@ -5,3 +4,0 @@", LineRange{4, 5}, true},     // deletion widens
		{"@@ -1,2 +0,3 @@", LineRange{1, 3}, true},     // start < 1 clamps to 1, end follows
		{"@@ -1 +x,2 @@", LineRange{}, false},          // non-numeric start
		{"@@ -1 +5,y @@", LineRange{}, false},          // non-numeric count
		{"@@ -1 @@", LineRange{}, false},               // no new-side field
	}
	for _, c := range cases {
		got, ok := newSideRange(c.header)
		if ok != c.wantOK || got != c.want {
			t.Errorf("newSideRange(%q) = (%v, %v), want (%v, %v)", c.header, got, ok, c.want, c.wantOK)
		}
	}
}

func TestSymbolsInChangedLinesChunksManyPaths(t *testing.T) {
	// More changed paths than the 500-path query chunk forces a second query
	// iteration; the overlapping symbol must still be found across batches.
	ctx := context.Background()
	dir := t.TempDir()
	adapter, err := sqlite.Open(ctx, filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	file := model.File{Path: "app/real.rb", Language: "ruby", Hash: "h", IndexedAt: time.Now()}
	fid, err := adapter.WriteFile(ctx, &file)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sym := model.Symbol{FileID: fid, Name: "thing", Qualified: "Real#thing", Kind: "method", LineStart: 3, LineEnd: 7}
	sid, err := adapter.WriteSymbol(ctx, &sym)
	if err != nil {
		t.Fatalf("WriteSymbol: %v", err)
	}

	hunks := map[string][]LineRange{"app/real.rb": {{4, 4}}}
	for i := 0; i < 600; i++ {
		hunks[fmt.Sprintf("app/bogus_%d.rb", i)] = []LineRange{{1, 1}}
	}
	got, err := SymbolsInChangedLines(ctx, adapter.DB(), hunks)
	if err != nil {
		t.Fatalf("SymbolsInChangedLines: %v", err)
	}
	if len(got) != 1 || got[0] != sid {
		t.Fatalf("got %v, want only [%d] across %d chunked paths", got, sid, len(hunks))
	}
}

func TestSymbolsInChangedLinesScanError(t *testing.T) {
	// A row whose line_start is not an integer fails the scan and surfaces an
	// error rather than being silently skipped. We corrupt the column directly
	// (SQLite is dynamically typed) to drive the scan-error path.
	ctx := context.Background()
	dir := t.TempDir()
	adapter, err := sqlite.Open(ctx, filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	file := model.File{Path: "app/x.rb", Language: "ruby", Hash: "h", IndexedAt: time.Now()}
	fid, err := adapter.WriteFile(ctx, &file)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sym := model.Symbol{FileID: fid, Name: "m", Qualified: "X#m", Kind: "method", LineStart: 1, LineEnd: 9}
	sid, err := adapter.WriteSymbol(ctx, &sym)
	if err != nil {
		t.Fatalf("WriteSymbol: %v", err)
	}
	if _, err := adapter.DB().ExecContext(ctx, "UPDATE sense_symbols SET line_start='not-an-int' WHERE id=?", sid); err != nil {
		t.Fatalf("corrupt line_start: %v", err)
	}

	_, qerr := SymbolsInChangedLines(ctx, adapter.DB(), map[string][]LineRange{"app/x.rb": {{1, 1}}})
	if qerr == nil {
		t.Error("expected a scan error for a non-integer line_start")
	}
}

func TestSymbolsInChangedLinesQueryError(t *testing.T) {
	// A DB without the sense schema surfaces the query error rather than
	// silently returning nothing.
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rawDB := db.DB()
	_ = db.Close() // close so the query fails on a closed handle
	_, qerr := SymbolsInChangedLines(context.Background(), rawDB, map[string][]LineRange{"x.rb": {{1, 1}}})
	if qerr == nil {
		t.Error("expected an error querying a closed database")
	}
}

func TestOverlapsAny(t *testing.T) {
	ranges := []LineRange{{10, 12}, {20, 20}}
	cases := []struct {
		symStart, symEnd int
		want             bool
	}{
		{1, 5, false},   // before all ranges
		{8, 10, true},   // touches a range's lower edge
		{12, 30, true},  // spans both ranges
		{13, 19, false}, // gap between ranges
		{20, 20, true},  // exact single-line match
		{20, 19, true},  // unknown end (symEnd < symStart) treated as [20,20]
		{30, 40, false}, // after all ranges
	}
	for _, c := range cases {
		if got := overlapsAny(c.symStart, c.symEnd, ranges); got != c.want {
			t.Errorf("overlapsAny(%d,%d,%v) = %v, want %v", c.symStart, c.symEnd, ranges, got, c.want)
		}
	}
}

func TestSymbolsInChangedLinesEmpty(t *testing.T) {
	ids, err := SymbolsInChangedLines(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty ids, got %v", ids)
	}
}

// TestSymbolsInChangedLinesSelectsOverlapping is the heart of the diff-blast
// fix: a one-line change to a file with many symbols must seed only the
// symbol whose span overlaps the change, not every symbol in the file.
func TestSymbolsInChangedLinesSelectsOverlapping(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	adapter, err := sqlite.Open(ctx, filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	file := model.File{Path: "config/routes/admin_routes.rb", Language: "ruby", Hash: "h", IndexedAt: time.Now()}
	fid, err := adapter.WriteFile(ctx, &file)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Three route helpers at disjoint line spans, mimicking a hub file.
	syms := []model.Symbol{
		{FileID: fid, Name: "admin_users", Qualified: "route:admin_users", Kind: "method", LineStart: 1, LineEnd: 5},
		{FileID: fid, Name: "admin_orders", Qualified: "route:admin_orders", Kind: "method", LineStart: 10, LineEnd: 14},
		{FileID: fid, Name: "admin_reports", Qualified: "route:admin_reports", Kind: "method", LineStart: 20, LineEnd: 24},
	}
	ids := make([]int64, len(syms))
	for i := range syms {
		id, werr := adapter.WriteSymbol(ctx, &syms[i])
		if werr != nil {
			t.Fatalf("WriteSymbol: %v", werr)
		}
		ids[i] = id
	}

	// Two disjoint changes (lines 2 and 11) seed exactly the two helpers they
	// fall inside — never the third at lines 20-24. Returned ids are ascending.
	hunks := map[string][]LineRange{"config/routes/admin_routes.rb": {{2, 2}, {11, 11}}}
	got, err := SymbolsInChangedLines(ctx, adapter.DB(), hunks)
	if err != nil {
		t.Fatalf("SymbolsInChangedLines: %v", err)
	}
	if len(got) != 2 || got[0] != ids[0] || got[1] != ids[1] {
		t.Fatalf("got %v, want [%d %d] (the two overlapping helpers, not admin_reports)", got, ids[0], ids[1])
	}

	// A path Sense has no symbols for (a locale .yml, a migration) seeds nothing.
	none, err := SymbolsInChangedLines(ctx, adapter.DB(), map[string][]LineRange{"config/locales/en.yml": {{1, 100}}})
	if err != nil {
		t.Fatalf("SymbolsInChangedLines (unindexed): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected no symbols for an unindexed path, got %v", none)
	}
}
