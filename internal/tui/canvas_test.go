package tui

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

var updateGolden = os.Getenv("UPDATE_GOLDEN") != ""

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with UPDATE_GOLDEN=1 to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("output mismatch for %s\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

// testGraph is the fixed 5-node, 4-edge graph used across all visual tests.
// Positions are pre-seeded (not computed by layout) so output is deterministic.
type testNode struct {
	ID        int64
	Name      string
	X, Y      float64
	Centrality int
}

type testEdge struct {
	SourceIdx int
	TargetIdx int
}

func fixedTestGraph() ([]testNode, []testEdge) {
	nodes := []testNode{
		{ID: 1, Name: "App", X: 0.5, Y: 0.5, Centrality: 4},      // hub
		{ID: 2, Name: "UserService", X: 0.2, Y: 0.2, Centrality: 2},
		{ID: 3, Name: "DB", X: 0.8, Y: 0.3, Centrality: 3},
		{ID: 4, Name: "Auth", X: 0.3, Y: 0.8, Centrality: 1},
		{ID: 5, Name: "Logger", X: 0.7, Y: 0.8, Centrality: 0},   // leaf
	}
	edges := []testEdge{
		{0, 1}, // App -> UserService
		{0, 2}, // App -> DB
		{1, 2}, // UserService -> DB
		{0, 3}, // App -> Auth
	}
	return nodes, edges
}

// renderTestGraph renders the fixed graph at the given terminal cell size.
func renderTestGraph(cols, rows int) string {
	nodes, edges := fixedTestGraph()
	c := NewCanvas(cols, rows)

	for _, e := range edges {
		src := nodes[e.SourceIdx]
		tgt := nodes[e.TargetIdx]
		x0 := int(src.X * float64(c.Width-1))
		y0 := int(src.Y * float64(c.Height-1))
		x1 := int(tgt.X * float64(c.Width-1))
		y1 := int(tgt.Y * float64(c.Height-1))
		c.DrawLine(x0, y0, x1, y1)
	}

	for _, n := range nodes {
		x := int(n.X * float64(c.Width-1))
		y := int(n.Y * float64(c.Height-1))
		radius := 1 + n.Centrality
		c.DrawCircle(x, y, radius)
	}

	return c.Render()
}

func TestGolden_SmallViewport(t *testing.T) {
	got := renderTestGraph(40, 12)
	assertGolden(t, "graph_small", got)
}

func TestGolden_MediumViewport(t *testing.T) {
	got := renderTestGraph(80, 24)
	assertGolden(t, "graph_medium", got)
}

func TestGolden_LargeViewport(t *testing.T) {
	got := renderTestGraph(120, 36)
	assertGolden(t, "graph_large", got)
}

func TestAnimationOrdering(t *testing.T) {
	nodes, _ := fixedTestGraph()

	sorted := make([]testNode, len(nodes))
	copy(sorted, nodes)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Centrality > sorted[j].Centrality
	})

	wantOrder := []string{"App", "DB", "UserService", "Auth", "Logger"}
	for i, n := range sorted {
		if n.Name != wantOrder[i] {
			t.Errorf("reveal step %d: got %s, want %s", i, n.Name, wantOrder[i])
		}
	}
}

func TestCanvas_Empty(t *testing.T) {
	c := NewCanvas(10, 5)
	if got := c.Render(); got != "" {
		t.Errorf("empty canvas should render empty string, got %q", got)
	}
}

func TestCanvas_ZeroDimensions(t *testing.T) {
	for _, tc := range []struct{ cols, rows int }{{0, 0}, {-1, -1}, {0, 5}, {5, 0}} {
		c := NewCanvas(tc.cols, tc.rows)
		if got := c.Render(); got != "" {
			t.Errorf("NewCanvas(%d,%d).Render() = %q, want empty", tc.cols, tc.rows, got)
		}
	}
}

func TestCanvas_SingleDot(t *testing.T) {
	c := NewCanvas(5, 3)
	c.Set(0, 0)
	got := c.Render()
	if got != "⠁" {
		t.Errorf("single dot at (0,0): got %q, want %q", got, "⠁")
	}
}

func TestCanvas_OutOfBounds(t *testing.T) {
	c := NewCanvas(5, 3)
	c.Set(-1, 0)
	c.Set(0, -1)
	c.Set(100, 0)
	c.Set(0, 100)
	if got := c.Render(); got != "" {
		t.Errorf("out-of-bounds sets should be no-ops, got %q", got)
	}
}

func TestCanvas_AllDotsInCell(t *testing.T) {
	c := NewCanvas(1, 1)
	for x := 0; x < 2; x++ {
		for y := 0; y < 4; y++ {
			c.Set(x, y)
		}
	}
	got := c.Render()
	if got != "⣿" {
		t.Errorf("all dots: got %q, want %q", got, "⣿")
	}
}

func TestCanvas_Line(t *testing.T) {
	c := NewCanvas(10, 3)
	c.DrawLine(0, 0, 19, 11)
	got := c.Render()
	assertGolden(t, "line_diagonal", got)
}

func TestCanvas_Circle(t *testing.T) {
	c := NewCanvas(10, 5)
	c.DrawCircle(10, 10, 5)
	got := c.Render()
	assertGolden(t, "circle", got)
}
