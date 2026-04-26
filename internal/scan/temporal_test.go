package scan

import (
	"reflect"
	"sort"
	"testing"

	"github.com/luuuc/sense/internal/model"
)

func TestParseGitLogOutput(t *testing.T) {
	raw := []byte(`abc123def456abc123def456abc123def456abcd
internal/scan/scan.go
internal/model/edge.go

def456abc123def456abc123def456abc123abcd
internal/scan/scan.go
internal/blast/engine.go
internal/model/edge.go

eee456abc123def456abc123def456abc123abcd
internal/scan/scan.go
internal/blast/engine.go
`)

	commits := parseGitLogOutput(raw)
	if len(commits) != 3 {
		t.Fatalf("expected 3 commits, got %d", len(commits))
	}
	want := [][]string{
		{"internal/scan/scan.go", "internal/model/edge.go"},
		{"internal/scan/scan.go", "internal/blast/engine.go", "internal/model/edge.go"},
		{"internal/scan/scan.go", "internal/blast/engine.go"},
	}
	if !reflect.DeepEqual(commits, want) {
		t.Errorf("commits mismatch:\ngot:  %v\nwant: %v", commits, want)
	}
}

func TestParseGitLogOutputEmpty(t *testing.T) {
	commits := parseGitLogOutput([]byte{})
	if len(commits) != 0 {
		t.Errorf("expected 0 commits from empty input, got %d", len(commits))
	}
}

func TestParseGitLogOutputNoFiles(t *testing.T) {
	raw := []byte("abc123def456abc123def456abc123def456abcd\n\n")
	commits := parseGitLogOutput(raw)
	if len(commits) != 0 {
		t.Errorf("expected 0 commits (no files), got %d", len(commits))
	}
}

func TestIsHexString(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"abc123", true},
		{"0000000000000000000000000000000000000000", true},
		{"xyz", false},
		{"ABC", false},
		{"", true},
	}
	for _, c := range cases {
		if got := isHexString(c.in); got != c.want {
			t.Errorf("isHexString(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMakePairKey(t *testing.T) {
	k1 := makePairKey("b.rb", "a.rb")
	k2 := makePairKey("a.rb", "b.rb")
	if k1 != k2 {
		t.Errorf("expected symmetric pair keys, got %v and %v", k1, k2)
	}
	if k1.a != "a.rb" || k1.b != "b.rb" {
		t.Errorf("expected sorted keys (a.rb, b.rb), got (%s, %s)", k1.a, k1.b)
	}
}

func TestCountCoChanges(t *testing.T) {
	indexed := map[string]bool{
		"pkg/a/file.go":  true,
		"pkg/b/file.go":  true,
		"pkg/a/other.go": true,
		"pkg/c/file.go":  true,
	}

	commits := [][]string{
		{"pkg/a/file.go", "pkg/b/file.go"},
		{"pkg/a/file.go", "pkg/b/file.go", "pkg/c/file.go"},
		{"pkg/a/file.go", "pkg/b/file.go"},
		{"pkg/a/file.go", "pkg/a/other.go"}, // same directory — should not count
		{"pkg/c/file.go", "untracked.go"},    // untracked not in indexed
	}

	pairs, fileCounts := countCoChanges(commits, indexed)

	// pkg/a/file.go ↔ pkg/b/file.go: 3 co-changes
	abKey := makePairKey("pkg/a/file.go", "pkg/b/file.go")
	if pairs[abKey] != 3 {
		t.Errorf("a↔b co-changes = %d, want 3", pairs[abKey])
	}

	// pkg/a/file.go ↔ pkg/c/file.go: 1 co-change
	acKey := makePairKey("pkg/a/file.go", "pkg/c/file.go")
	if pairs[acKey] != 1 {
		t.Errorf("a↔c co-changes = %d, want 1", pairs[acKey])
	}

	// pkg/b/file.go ↔ pkg/c/file.go: 1 co-change
	bcKey := makePairKey("pkg/b/file.go", "pkg/c/file.go")
	if pairs[bcKey] != 1 {
		t.Errorf("b↔c co-changes = %d, want 1", pairs[bcKey])
	}

	// Same-directory pair should not appear.
	sameDir := makePairKey("pkg/a/file.go", "pkg/a/other.go")
	if pairs[sameDir] != 0 {
		t.Errorf("same-directory pair should be 0, got %d", pairs[sameDir])
	}

	if fileCounts["pkg/a/file.go"] != 4 {
		t.Errorf("file count for pkg/a/file.go = %d, want 4", fileCounts["pkg/a/file.go"])
	}
	if fileCounts["pkg/b/file.go"] != 3 {
		t.Errorf("file count for pkg/b/file.go = %d, want 3", fileCounts["pkg/b/file.go"])
	}
}

func TestCountCoChangesUnindexedFilesIgnored(t *testing.T) {
	indexed := map[string]bool{"a/x.go": true}
	commits := [][]string{
		{"a/x.go", "b/untracked.go"},
	}
	pairs, fileCounts := countCoChanges(commits, indexed)
	if len(pairs) != 0 {
		t.Errorf("expected no pairs with unindexed files, got %d", len(pairs))
	}
	if fileCounts["a/x.go"] != 1 {
		t.Errorf("file count for a/x.go = %d, want 1", fileCounts["a/x.go"])
	}
}

func TestPickRepresentative(t *testing.T) {
	t.Run("prefers class over function", func(t *testing.T) {
		symbols := []model.Symbol{
			{ID: 1, Name: "helper", Kind: "function"},
			{ID: 2, Name: "Widget", Kind: "class"},
			{ID: 3, Name: "doThing", Kind: "method"},
		}
		conn := map[int64]int{1: 5, 2: 1, 3: 3}
		rep := pickRepresentative(symbols, conn)
		if rep.Name != "Widget" {
			t.Errorf("expected Widget (class), got %s (%s)", rep.Name, rep.Kind)
		}
	})

	t.Run("highest connectivity class wins", func(t *testing.T) {
		symbols := []model.Symbol{
			{ID: 1, Name: "ClassA", Kind: "class"},
			{ID: 2, Name: "ClassB", Kind: "class"},
		}
		conn := map[int64]int{1: 3, 2: 7}
		rep := pickRepresentative(symbols, conn)
		if rep.Name != "ClassB" {
			t.Errorf("expected ClassB (higher connectivity), got %s", rep.Name)
		}
	})

	t.Run("falls back to highest connectivity function", func(t *testing.T) {
		symbols := []model.Symbol{
			{ID: 1, Name: "helper", Kind: "function"},
			{ID: 2, Name: "main", Kind: "function"},
			{ID: 3, Name: "init", Kind: "function"},
		}
		conn := map[int64]int{1: 1, 2: 5, 3: 2}
		rep := pickRepresentative(symbols, conn)
		if rep.Name != "main" {
			t.Errorf("expected main (highest connectivity), got %s", rep.Name)
		}
	})

	t.Run("tie-breaks by lowest ID", func(t *testing.T) {
		symbols := []model.Symbol{
			{ID: 5, Name: "B", Kind: "function"},
			{ID: 3, Name: "A", Kind: "function"},
		}
		conn := map[int64]int{5: 2, 3: 2}
		rep := pickRepresentative(symbols, conn)
		if rep.ID != 3 {
			t.Errorf("expected ID 3 (lower), got %d", rep.ID)
		}
	})

	t.Run("module kind treated as class-level", func(t *testing.T) {
		symbols := []model.Symbol{
			{ID: 1, Name: "BigFunc", Kind: "function"},
			{ID: 2, Name: "MyModule", Kind: "module"},
		}
		conn := map[int64]int{1: 10, 2: 1}
		rep := pickRepresentative(symbols, conn)
		if rep.Name != "MyModule" {
			t.Errorf("expected MyModule (module kind), got %s", rep.Name)
		}
	})
}

func TestCountCoChangesStrength(t *testing.T) {
	indexed := map[string]bool{
		"a/x.go": true,
		"b/y.go": true,
	}

	// 5 co-changes out of 5 changes each → strength 1.0
	commits := make([][]string, 5)
	for i := range commits {
		commits[i] = []string{"a/x.go", "b/y.go"}
	}

	pairs, fileCounts := countCoChanges(commits, indexed)
	key := makePairKey("a/x.go", "b/y.go")

	coChanges := pairs[key]
	if coChanges != 5 {
		t.Errorf("co-changes = %d, want 5", coChanges)
	}

	maxChanges := fileCounts["a/x.go"]
	if fileCounts["b/y.go"] > maxChanges {
		maxChanges = fileCounts["b/y.go"]
	}
	strength := float64(coChanges) / float64(maxChanges)
	if strength != 1.0 {
		t.Errorf("strength = %f, want 1.0", strength)
	}
}

func TestCountCoChangesMinThresholdFiltering(t *testing.T) {
	indexed := map[string]bool{
		"a/x.go": true,
		"b/y.go": true,
		"c/z.go": true,
	}

	// a↔b: 3 co-changes (meets threshold)
	// a↔c: 2 co-changes (below threshold)
	commits := [][]string{
		{"a/x.go", "b/y.go", "c/z.go"},
		{"a/x.go", "b/y.go", "c/z.go"},
		{"a/x.go", "b/y.go"},
	}

	pairs, _ := countCoChanges(commits, indexed)

	type countedPair struct {
		key   pairKey
		count int
	}
	var significant []countedPair
	for k, v := range pairs {
		if v >= minCoChanges {
			significant = append(significant, countedPair{k, v})
		}
	}
	sort.Slice(significant, func(i, j int) bool {
		return significant[i].key.a < significant[j].key.a
	})

	if len(significant) != 1 {
		t.Fatalf("expected 1 significant pair, got %d: %v", len(significant), significant)
	}
	if significant[0].key != makePairKey("a/x.go", "b/y.go") {
		t.Errorf("expected a/x.go↔b/y.go, got %v", significant[0].key)
	}
}
