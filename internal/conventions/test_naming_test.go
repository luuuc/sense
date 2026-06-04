package conventions

import (
	"strings"
	"testing"
)

// TestDetectTestingNamingPatterns drives detectTesting across every test-file
// naming family it classifies, Go's _test.go, JS/TS .test.*, Python test_*,
// and the JUnit/xUnit *Test.* / *Tests.* forms, so each classification branch
// and its match-closure runs. A below-threshold family and a test file with a
// missing path entry exercise the two skip paths. tests edges are supplied
// directly so the IsTestPath fallback is bypassed.
func TestDetectTestingNamingPatterns(t *testing.T) {
	filePathByID := map[int64]string{}
	var symbols []symbolRow
	var edges []edgeRow

	// add registers a test file: a symbol in that file plus a tests edge from
	// it, so the file id lands in detectTesting's testFileIDs set.
	id := int64(0)
	add := func(path string) {
		id++
		fid := id
		symbols = append(symbols, symbolRow{id: id, fileID: fid})
		filePathByID[fid] = path
		edges = append(edges, edgeRow{sourceID: id, kind: "tests"})
	}

	for _, p := range []string{"a_test.go", "b_test.go", "c_test.go"} {
		add(p)
	}
	for _, p := range []string{"x.test.ts", "y.test.ts", "z.test.ts"} {
		add(p)
	}
	for _, p := range []string{"test_a.py", "test_b.py", "test_c.py"} {
		add(p)
	}
	for _, p := range []string{"AThing.Test.cs", "BThing.Test.cs", "CThing.Test.cs"} {
		add(p)
	}
	for _, p := range []string{"OneTests.kt", "TwoTests.kt", "ThreeTests.kt"} {
		add(p)
	}
	// Below threshold (2 < minInstances): a recognized family that is
	// classified but never emitted, exercises the count-skip branch.
	for _, p := range []string{"p.test.jsx", "q.test.jsx"} {
		add(p)
	}
	// A test file whose id has no path entry: exercises the skip branches.
	id++
	symbols = append(symbols, symbolRow{id: id, fileID: id})
	edges = append(edges, edgeRow{sourceID: id, kind: "tests"})

	symbolByID := indexSymbols(symbols)
	out := detectTesting(nil, edges, filePathByID, symbolByID)

	// Every above-threshold family produces a Testing convention.
	wantLabels := []string{"_test.go", ".test.ts", "test_*", "*Test.cs", "*Tests.kt"}
	for _, label := range wantLabels {
		found := false
		for _, c := range out {
			if c.Category != CategoryTesting {
				t.Errorf("category = %q, want %q", c.Category, CategoryTesting)
			}
			if strings.Contains(c.Description, label) {
				found = true
				if c.Instances != 3 {
					t.Errorf("%s instances = %d, want 3", label, c.Instances)
				}
			}
		}
		if !found {
			t.Errorf("expected a testing convention for %q, got %d conventions", label, len(out))
		}
	}
	// The 2-instance .test.jsx family is below threshold and must not appear.
	for _, c := range out {
		if strings.Contains(c.Description, ".test.jsx") {
			t.Errorf("below-threshold .test.jsx family should not be emitted: %q", c.Description)
		}
	}
}

// TestClassifyTestFile pins the per-name classifier directly, including the
// names that fit no test family: a name with no extension and an ordinary
// source file.
func TestClassifyTestFile(t *testing.T) {
	classified := []struct {
		base  string
		label string
	}{
		{"adapter_test.go", "_test.go"},
		{"button.test.tsx", ".test.tsx"},
		{"test_views.py", "test_*"},
		{"UserTest.java", "*Test.java"},
		{"ConfigTests.kt", "*Tests.kt"},
	}
	for _, tc := range classified {
		label, matches, ok := classifyTestFile(tc.base)
		if !ok {
			t.Errorf("classifyTestFile(%q) ok = false, want classified as %q", tc.base, tc.label)
			continue
		}
		if label != tc.label {
			t.Errorf("classifyTestFile(%q) label = %q, want %q", tc.base, label, tc.label)
		}
		if !matches(tc.base) {
			t.Errorf("classifyTestFile(%q) matcher rejects its own base", tc.base)
		}
	}

	for _, base := range []string{"README", "main.go", "handler.rb"} {
		if _, _, ok := classifyTestFile(base); ok {
			t.Errorf("classifyTestFile(%q) ok = true, want unclassified", base)
		}
	}
}

// TestDetectTestingBelowThreshold confirms that too few test files yields no
// conventions at all, the whole-package gate before any classification.
func TestDetectTestingBelowThreshold(t *testing.T) {
	filePathByID := map[int64]string{1: "a_test.go", 2: "b_test.go"}
	symbols := []symbolRow{{id: 1, fileID: 1}, {id: 2, fileID: 2}}
	edges := []edgeRow{
		{sourceID: 1, kind: "tests"},
		{sourceID: 2, kind: "tests"},
	}
	symbolByID := indexSymbols(symbols)
	if out := detectTesting(nil, edges, filePathByID, symbolByID); out != nil {
		t.Errorf("expected nil for fewer than %d test files, got %+v", minInstances, out)
	}
}
