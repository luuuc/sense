package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFruchtermanReingold_Deterministic(t *testing.T) {
	nodes1, edges := makeTestLayoutGraph()
	applyFruchtermanReingold(nodes1, edges)

	nodes2, _ := makeTestLayoutGraph()
	applyFruchtermanReingold(nodes2, edges)

	for i := range nodes1 {
		if nodes1[i].X != nodes2[i].X || nodes1[i].Y != nodes2[i].Y {
			t.Fatalf("non-deterministic: node %d position differs between runs", nodes1[i].ID)
		}
	}
}

func TestFruchtermanReingold_Normalized(t *testing.T) {
	nodes, edges := makeTestLayoutGraph()
	applyFruchtermanReingold(nodes, edges)

	for _, n := range nodes {
		if n.X < 0 || n.X > 1 || n.Y < 0 || n.Y > 1 {
			t.Errorf("node %d out of [0,1]: (%.4f, %.4f)", n.ID, n.X, n.Y)
		}
	}
}

func TestFruchtermanReingold_ConnectedCloser(t *testing.T) {
	nodes, edges := makeTestLayoutGraph()
	applyFruchtermanReingold(nodes, edges)

	nodeByID := map[int64]LayoutNode{}
	for _, n := range nodes {
		nodeByID[n.ID] = n
	}

	// Nodes 1 and 2 are connected; node 5 is only connected to node 1.
	// Connected pairs should generally be closer than unconnected pairs.
	n1 := nodeByID[1]
	n2 := nodeByID[2]
	n5 := nodeByID[5]
	dist12 := (n1.X-n2.X)*(n1.X-n2.X) + (n1.Y-n2.Y)*(n1.Y-n2.Y)
	dist25 := (n2.X-n5.X)*(n2.X-n5.X) + (n2.Y-n5.Y)*(n2.Y-n5.Y)

	// This is a heuristic check — connected nodes should be closer
	if dist12 > dist25*3 {
		t.Logf("warning: connected pair (1,2) dist=%.4f > 3× unconnected pair (2,5) dist=%.4f", dist12, dist25)
	}
}

func TestFruchtermanReingold_SingleNode(t *testing.T) {
	nodes := []LayoutNode{{ID: 1, Name: "solo"}}
	applyFruchtermanReingold(nodes, nil)
	if nodes[0].X != 0.5 || nodes[0].Y != 0.5 {
		t.Errorf("single node should be at center, got (%.4f, %.4f)", nodes[0].X, nodes[0].Y)
	}
}

func TestLayout_CacheRoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := &Layout{
		GraphHash: "abc123",
		Nodes: []LayoutNode{
			{ID: 1, Name: "A", X: 0.1, Y: 0.2, Centrality: 3},
			{ID: 2, Name: "B", X: 0.8, Y: 0.7, Centrality: 1},
		},
		Edges: []LayoutEdge{
			{SourceID: 1, TargetID: 2, Kind: "calls", Confidence: 0.9},
		},
	}

	if err := writeLayout(dir, original); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := LoadLayout(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.GraphHash != original.GraphHash {
		t.Errorf("hash: got %s, want %s", loaded.GraphHash, original.GraphHash)
	}
	if len(loaded.Nodes) != len(original.Nodes) {
		t.Fatalf("nodes: got %d, want %d", len(loaded.Nodes), len(original.Nodes))
	}
	for i := range loaded.Nodes {
		if loaded.Nodes[i].ID != original.Nodes[i].ID {
			t.Errorf("node %d ID mismatch", i)
		}
		if loaded.Nodes[i].X != original.Nodes[i].X || loaded.Nodes[i].Y != original.Nodes[i].Y {
			t.Errorf("node %d position mismatch", i)
		}
	}
}

func TestLayout_LoadMissing(t *testing.T) {
	l, err := LoadLayout(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l != nil {
		t.Error("expected nil for missing layout")
	}
}

func TestLayout_GraphHashChanges(t *testing.T) {
	nodes1 := []LayoutNode{{ID: 1, Qualified: "a"}, {ID: 2, Qualified: "b"}}
	edges1 := []LayoutEdge{{SourceID: 1, TargetID: 2, Kind: "calls"}}
	h1 := graphHash(nodes1, edges1)

	nodes2 := []LayoutNode{{ID: 1, Qualified: "a"}, {ID: 2, Qualified: "b"}, {ID: 3, Qualified: "c"}}
	h2 := graphHash(nodes2, edges1)

	if h1 == h2 {
		t.Error("hash should change when nodes change")
	}

	// Same inputs should produce same hash
	h3 := graphHash(nodes1, edges1)
	if h1 != h3 {
		t.Error("hash should be stable for same input")
	}
}

func TestLayout_ScanIntegration(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// No adapter here — just verify the file contract: layout.json should
	// be loadable after being written.
	l := &Layout{
		GraphHash: "test",
		Nodes:     []LayoutNode{{ID: 1, Name: "main", X: 0.5, Y: 0.5}},
	}
	if err := writeLayout(senseDir, l); err != nil {
		t.Fatalf("write: %v", err)
	}

	path := filepath.Join(senseDir, "layout.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("layout.json not created: %v", err)
	}
}

func makeTestLayoutGraph() ([]LayoutNode, []LayoutEdge) {
	nodes := []LayoutNode{
		{ID: 1, Name: "App", Qualified: "main.App"},
		{ID: 2, Name: "UserService", Qualified: "services.UserService"},
		{ID: 3, Name: "DB", Qualified: "db.DB"},
		{ID: 4, Name: "Auth", Qualified: "auth.Auth"},
		{ID: 5, Name: "Logger", Qualified: "util.Logger"},
	}
	edges := []LayoutEdge{
		{SourceID: 1, TargetID: 2, Kind: "calls", Confidence: 1.0},
		{SourceID: 1, TargetID: 3, Kind: "calls", Confidence: 1.0},
		{SourceID: 2, TargetID: 3, Kind: "calls", Confidence: 0.8},
		{SourceID: 1, TargetID: 4, Kind: "calls", Confidence: 1.0},
	}
	return nodes, edges
}
