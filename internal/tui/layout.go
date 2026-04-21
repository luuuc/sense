package tui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"

	"github.com/luuuc/sense/internal/sqlite"
)

// LayoutNode is a positioned symbol for the TUI graph.
type LayoutNode struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Qualified  string  `json:"qualified"`
	Kind       string  `json:"kind"`
	Language   string  `json:"language"`
	FileID     int64   `json:"file_id"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Centrality int     `json:"centrality"`
}

// LayoutEdge is a positioned edge for the TUI graph.
type LayoutEdge struct {
	SourceID   int64   `json:"source_id"`
	TargetID   int64   `json:"target_id"`
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence"`
}

// Layout is the cached graph layout written to .sense/layout.json.
type Layout struct {
	GraphHash string       `json:"graph_hash"`
	Nodes     []LayoutNode `json:"nodes"`
	Edges     []LayoutEdge `json:"edges"`
}

// LoadLayout reads the cached layout from .sense/layout.json.
// Returns nil, nil if the file doesn't exist.
func LoadLayout(senseDir string) (*Layout, error) {
	path := filepath.Join(senseDir, "layout.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read layout: %w", err)
	}
	var l Layout
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}
	return &l, nil
}

// ComputeAndCacheLayout loads the graph from SQLite, computes a force-directed
// layout, and writes it to .sense/layout.json. Skips computation if the graph
// hash hasn't changed. Returns the layout.
func ComputeAndCacheLayout(ctx context.Context, adapter *sqlite.Adapter, senseDir string) (*Layout, error) {
	nodes, edges, err := loadGraph(ctx, adapter)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		l := &Layout{GraphHash: "empty"}
		return l, writeLayout(senseDir, l)
	}

	hash := graphHash(nodes, edges)

	existing, _ := LoadLayout(senseDir)
	if existing != nil && existing.GraphHash == hash {
		return existing, nil
	}

	applyFruchtermanReingold(nodes, edges)

	l := &Layout{
		GraphHash: hash,
		Nodes:     nodes,
		Edges:     edges,
	}
	if err := writeLayout(senseDir, l); err != nil {
		return nil, err
	}
	return l, nil
}

func writeLayout(senseDir string, l *Layout) error {
	data, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("marshal layout: %w", err)
	}
	path := filepath.Join(senseDir, "layout.json")
	return os.WriteFile(path, data, 0o644)
}

func loadGraph(ctx context.Context, adapter *sqlite.Adapter) ([]LayoutNode, []LayoutEdge, error) {
	db := adapter.DB()

	rows, err := db.QueryContext(ctx, `
		SELECT s.id, s.name, s.qualified, s.kind, f.language, s.file_id
		FROM sense_symbols s
		JOIN sense_files f ON f.id = s.file_id
		ORDER BY s.id ASC`)
	if err != nil {
		return nil, nil, fmt.Errorf("load symbols: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var nodes []LayoutNode
	var symbolIDs []int64
	for rows.Next() {
		var n LayoutNode
		if err := rows.Scan(&n.ID, &n.Name, &n.Qualified, &n.Kind, &n.Language, &n.FileID); err != nil {
			return nil, nil, fmt.Errorf("scan symbol: %w", err)
		}
		nodes = append(nodes, n)
		symbolIDs = append(symbolIDs, n.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	centrality, err := adapter.InboundEdgeCounts(ctx, symbolIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("load centrality: %w", err)
	}
	for i := range nodes {
		nodes[i].Centrality = centrality[nodes[i].ID]
	}

	edgeRows, err := db.QueryContext(ctx, `
		SELECT source_id, target_id, kind, confidence
		FROM sense_edges
		WHERE source_id IS NOT NULL
		ORDER BY id ASC`)
	if err != nil {
		return nil, nil, fmt.Errorf("load edges: %w", err)
	}
	defer func() { _ = edgeRows.Close() }()

	var edges []LayoutEdge
	for edgeRows.Next() {
		var e LayoutEdge
		if err := edgeRows.Scan(&e.SourceID, &e.TargetID, &e.Kind, &e.Confidence); err != nil {
			return nil, nil, fmt.Errorf("scan edge: %w", err)
		}
		edges = append(edges, e)
	}
	if err := edgeRows.Err(); err != nil {
		return nil, nil, err
	}

	return nodes, edges, nil
}

func graphHash(nodes []LayoutNode, edges []LayoutEdge) string {
	h := sha256.New()
	for _, n := range nodes {
		_, _ = fmt.Fprintf(h, "n:%d:%s\n", n.ID, n.Qualified)
	}
	for _, e := range edges {
		_, _ = fmt.Fprintf(h, "e:%d:%d:%s\n", e.SourceID, e.TargetID, e.Kind)
	}
	return fmt.Sprintf("%x", h.Sum(nil)[:16])
}

// applyFruchtermanReingold runs the Fruchterman-Reingold force-directed layout
// algorithm. Positions are normalized to [0,1] with margin. Uses a seeded RNG
// for deterministic output.
func applyFruchtermanReingold(nodes []LayoutNode, edges []LayoutEdge) {
	n := len(nodes)
	if n == 0 {
		return
	}
	if n == 1 {
		nodes[0].X = 0.5
		nodes[0].Y = 0.5
		return
	}

	idxByID := make(map[int64]int, n)
	for i, node := range nodes {
		idxByID[node.ID] = i
	}

	// Seeded RNG for deterministic layout.
	rng := rand.New(rand.NewPCG(42, 0))

	xs := make([]float64, n)
	ys := make([]float64, n)
	for i := range n {
		xs[i] = rng.Float64()
		ys[i] = rng.Float64()
	}

	area := 1.0
	k := math.Sqrt(area / float64(n))

	iterations := 300
	if n > 1000 {
		iterations = 150
	}
	if n > 5000 {
		iterations = 80
	}

	temp := 0.1
	cooling := temp / float64(iterations)

	dxs := make([]float64, n)
	dys := make([]float64, n)

	for range iterations {
		for i := range n {
			dxs[i] = 0
			dys[i] = 0
		}

		// Repulsive forces between all node pairs.
		// For large graphs, only consider nearby pairs (Barnes-Hut approximation
		// would be better, but this cutoff keeps it simple within appetite).
		cutoff := k * 6
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				dx := xs[i] - xs[j]
				dy := ys[i] - ys[j]
				dist := math.Sqrt(dx*dx + dy*dy)
				if dist > cutoff {
					continue
				}
				if dist < 1e-6 {
					dist = 1e-6
				}
				force := (k * k) / dist
				fx := (dx / dist) * force
				fy := (dy / dist) * force
				dxs[i] += fx
				dys[i] += fy
				dxs[j] -= fx
				dys[j] -= fy
			}
		}

		// Attractive forces along edges.
		for _, e := range edges {
			si, ok1 := idxByID[e.SourceID]
			ti, ok2 := idxByID[e.TargetID]
			if !ok1 || !ok2 {
				continue
			}
			dx := xs[si] - xs[ti]
			dy := ys[si] - ys[ti]
			dist := math.Sqrt(dx*dx + dy*dy)
			if dist < 1e-6 {
				continue
			}
			force := (dist * dist) / k
			fx := (dx / dist) * force
			fy := (dy / dist) * force
			dxs[si] -= fx
			dys[si] -= fy
			dxs[ti] += fx
			dys[ti] += fy
		}

		// Apply displacements capped by temperature.
		for i := range n {
			disp := math.Sqrt(dxs[i]*dxs[i] + dys[i]*dys[i])
			if disp < 1e-6 {
				continue
			}
			scale := math.Min(disp, temp) / disp
			xs[i] += dxs[i] * scale
			ys[i] += dys[i] * scale
		}

		temp -= cooling
		if temp < 0 {
			temp = 0
		}
	}

	// Normalize to [margin, 1-margin].
	normalize(xs, ys, nodes)
}

func normalize(xs, ys []float64, nodes []LayoutNode) {
	const margin = 0.05
	minX, maxX := xs[0], xs[0]
	minY, maxY := ys[0], ys[0]
	for i := 1; i < len(xs); i++ {
		if xs[i] < minX {
			minX = xs[i]
		}
		if xs[i] > maxX {
			maxX = xs[i]
		}
		if ys[i] < minY {
			minY = ys[i]
		}
		if ys[i] > maxY {
			maxY = ys[i]
		}
	}
	rangeX := maxX - minX
	rangeY := maxY - minY
	if rangeX < 1e-9 {
		rangeX = 1
	}
	if rangeY < 1e-9 {
		rangeY = 1
	}
	usable := 1.0 - 2*margin
	for i := range nodes {
		nodes[i].X = margin + ((xs[i]-minX)/rangeX)*usable
		nodes[i].Y = margin + ((ys[i]-minY)/rangeY)*usable
	}

	// Stable output: sort by ID so JSON order is deterministic.
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].ID < nodes[j].ID
	})
}
