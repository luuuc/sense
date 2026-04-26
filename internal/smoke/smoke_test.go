package smoke_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/conventions"
	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

var update = flag.Bool("update", false, "regenerate ground-truth.json")

type GroundTruth struct {
	Symbols     SymbolTruth      `json:"symbols"`
	Edges       EdgeTruth        `json:"edges"`
	Search      []SearchTruth    `json:"search"`
	Blast       []BlastTruth     `json:"blast"`
	Conventions ConventionsTruth `json:"conventions"`
	Performance PerformanceTruth `json:"performance"`
}

type SymbolTruth struct {
	MinTotal   int            `json:"min_total"`
	ByLanguage map[string]int `json:"by_language"`
}

type EdgeTruth struct {
	Required []RequiredEdge `json:"required"`
	MinTotal int            `json:"min_total"`
}

type RequiredEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

type SearchTruth struct {
	Query          string `json:"query"`
	ExpectedInTop3 string `json:"expected_in_top3"`
}

type BlastTruth struct {
	Symbol           string `json:"symbol"`
	MinDirectCallers int    `json:"min_direct_callers"`
}

type ConventionsTruth struct {
	MinDetected int `json:"min_detected"`
}

type PerformanceTruth struct {
	ScanMaxSeconds int `json:"scan_max_seconds"`
	QueryMaxMs     int `json:"query_max_ms"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "testdata", "smoke")
}

func groundTruthPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "testdata", "smoke", "ground-truth.json")
}

func loadGroundTruth(t *testing.T) GroundTruth {
	t.Helper()
	data, err := os.ReadFile(groundTruthPath(t))
	if err != nil {
		t.Fatalf("read ground-truth.json: %v", err)
	}
	var gt GroundTruth
	if err := json.Unmarshal(data, &gt); err != nil {
		t.Fatalf("parse ground-truth.json: %v", err)
	}
	return gt
}

func TestSmoke(t *testing.T) {
	root := fixtureRoot(t)
	senseDir := t.TempDir()
	ctx := context.Background()

	scanStart := time.Now()
	res, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Sense:    senseDir,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	})
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	scanDuration := time.Since(scanStart)

	dbPath := filepath.Join(senseDir, "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	if *update {
		generateGroundTruth(t, ctx, adapter, res)
		return
	}

	gt := loadGroundTruth(t)

	t.Run("symbols", func(t *testing.T) {
		if res.Symbols < gt.Symbols.MinTotal {
			t.Fatalf("symbol count %d < minimum %d", res.Symbols, gt.Symbols.MinTotal)
		}

		allSymbols, err := adapter.Query(ctx, index.Filter{})
		if err != nil {
			t.Fatalf("query symbols: %v", err)
		}

		fileLangMap := buildFileLangMap(ctx, t, adapter)

		langCounts := map[string]int{}
		for _, sym := range allSymbols {
			lang := fileLangMap[sym.FileID]
			langCounts[lang]++
		}

		for lang, expected := range gt.Symbols.ByLanguage {
			actual := langCounts[lang]
			if actual < expected {
				t.Fatalf("language %q: symbol count %d < expected %d", lang, actual, expected)
			}
		}
	})

	t.Run("edges", func(t *testing.T) {
		if res.Edges < gt.Edges.MinTotal {
			t.Fatalf("edge count %d < minimum %d", res.Edges, gt.Edges.MinTotal)
		}

		allSymbols, err := adapter.Query(ctx, index.Filter{})
		if err != nil {
			t.Fatalf("query symbols: %v", err)
		}
		byQualified := map[string]model.Symbol{}
		for _, s := range allSymbols {
			byQualified[s.Qualified] = s
		}

		type edgeKey struct {
			sourceID int64
			targetID int64
			kind     model.EdgeKind
		}
		edgeSet := map[edgeKey]bool{}
		rows, err := adapter.DB().QueryContext(ctx,
			`SELECT source_id, target_id, kind FROM sense_edges`)
		if err != nil {
			t.Fatalf("query all edges: %v", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var k edgeKey
			if err := rows.Scan(&k.sourceID, &k.targetID, &k.kind); err != nil {
				t.Fatalf("scan edge row: %v", err)
			}
			edgeSet[k] = true
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate edges: %v", err)
		}

		for _, req := range gt.Edges.Required {
			srcSym, srcOK := byQualified[req.Source]
			tgtSym, tgtOK := byQualified[req.Target]
			if !srcOK {
				t.Fatalf("required edge source %q not found in symbols", req.Source)
			}
			if !tgtOK {
				t.Fatalf("required edge target %q not found in symbols", req.Target)
			}

			k := edgeKey{sourceID: srcSym.ID, targetID: tgtSym.ID, kind: model.EdgeKind(req.Kind)}
			if !edgeSet[k] {
				t.Fatalf("required edge %s -[%s]-> %s not found", req.Source, req.Kind, req.Target)
			}
		}
	})

	t.Run("search", func(t *testing.T) {
		engine := search.NewEngine(adapter, nil, nil)

		for _, sq := range gt.Search {
			queryStart := time.Now()
			results, _, _, err := engine.Search(ctx, search.Options{
				Query: sq.Query,
				Limit: 10,
			})
			queryDuration := time.Since(queryStart)

			if err != nil {
				t.Fatalf("search %q failed: %v", sq.Query, err)
			}

			top3 := results
			if len(top3) > 3 {
				top3 = top3[:3]
			}

			found := false
			for _, r := range top3 {
				if r.Name == sq.ExpectedInTop3 || r.Qualified == sq.ExpectedInTop3 {
					found = true
					break
				}
			}
			if !found {
				names := make([]string, len(top3))
				for i, r := range top3 {
					names[i] = r.Qualified
				}
				t.Fatalf("search %q: expected %q in top 3, got %v", sq.Query, sq.ExpectedInTop3, names)
			}

			if gt.Performance.QueryMaxMs > 0 {
				maxDur := time.Duration(gt.Performance.QueryMaxMs) * time.Millisecond
				if queryDuration > maxDur {
					t.Logf("PERF WARNING: search %q took %s, baseline is %s", sq.Query, queryDuration, maxDur)
				}
			}
		}
	})

	t.Run("blast", func(t *testing.T) {
		allSymbols, err := adapter.Query(ctx, index.Filter{})
		if err != nil {
			t.Fatalf("query symbols: %v", err)
		}
		byQualified := map[string]model.Symbol{}
		for _, s := range allSymbols {
			byQualified[s.Qualified] = s
		}

		db := adapter.DB()

		for _, bt := range gt.Blast {
			sym, ok := byQualified[bt.Symbol]
			if !ok {
				t.Fatalf("blast target %q not found in symbols", bt.Symbol)
			}

			result, err := blast.Compute(ctx, db, []int64{sym.ID}, blast.Options{
				MaxHops:      3,
				IncludeTests: true,
			})
			if err != nil {
				t.Fatalf("blast %q failed: %v", bt.Symbol, err)
			}

			if len(result.DirectCallers) < bt.MinDirectCallers {
				t.Fatalf("blast %q: direct callers %d < minimum %d",
					bt.Symbol, len(result.DirectCallers), bt.MinDirectCallers)
			}
		}
	})

	t.Run("conventions", func(t *testing.T) {
		db := adapter.DB()
		convs, _, err := conventions.Detect(ctx, db, conventions.Options{})
		if err != nil {
			t.Fatalf("conventions detection failed: %v", err)
		}

		if len(convs) < gt.Conventions.MinDetected {
			t.Fatalf("conventions detected %d < minimum %d", len(convs), gt.Conventions.MinDetected)
		}
	})

	t.Run("performance", func(t *testing.T) {
		if gt.Performance.ScanMaxSeconds > 0 {
			maxDur := time.Duration(gt.Performance.ScanMaxSeconds) * time.Second
			if scanDuration > maxDur {
				t.Logf("PERF WARNING: scan took %s, baseline is %s", scanDuration, maxDur)
			}
		}
	})
}

func buildFileLangMap(ctx context.Context, t *testing.T, adapter *sqlite.Adapter) map[int64]string {
	t.Helper()
	rows, err := adapter.DB().QueryContext(ctx, "SELECT id, language FROM sense_files")
	if err != nil {
		t.Fatalf("query file languages: %v", err)
	}
	defer func() { _ = rows.Close() }()
	m := map[int64]string{}
	for rows.Next() {
		var id int64
		var lang string
		if err := rows.Scan(&id, &lang); err != nil {
			t.Fatalf("scan file row: %v", err)
		}
		m[id] = lang
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate file rows: %v", err)
	}
	return m
}

func generateGroundTruth(t *testing.T, ctx context.Context, adapter *sqlite.Adapter, res *scan.Result) {
	t.Helper()

	allSymbols, err := adapter.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("query symbols: %v", err)
	}

	fileLang := buildFileLangMap(ctx, t, adapter)

	langCounts := map[string]int{}
	for _, s := range allSymbols {
		langCounts[fileLang[s.FileID]]++
	}

	// Representative edges: one per edge kind per language where possible.
	representativeEdges := []RequiredEdge{
		{Source: "smoke.OrderService.Process", Target: "smoke.PaymentGateway.Charge", Kind: "calls"},
		{Source: "Order", Target: "Order#validate_total", Kind: "calls"},
		{Source: "smoke.OrderService", Target: "smoke.BaseService", Kind: "includes"},
		{Source: "Order", Target: "Trackable", Kind: "includes"},
		{Source: "smoke.OrderService", Target: "smoke.Notifier", Kind: "inherits"},
		{Source: "Order", Target: "ApplicationRecord", Kind: "inherits"},
		{Source: "Order", Target: "LineItem", Kind: "composes"},
		{Source: "smoke.TestOrderProcess", Target: "smoke.OrderService", Kind: "tests"},
		{Source: "OrderTest", Target: "Order", Kind: "tests"},
	}

	db := adapter.DB()
	convs, _, err := conventions.Detect(ctx, db, conventions.Options{})
	if err != nil {
		t.Fatalf("detect conventions: %v", err)
	}

	gt := GroundTruth{
		Symbols: SymbolTruth{
			MinTotal:   len(allSymbols),
			ByLanguage: langCounts,
		},
		Edges: EdgeTruth{
			Required: representativeEdges,
			MinTotal: res.Edges,
		},
		Search: []SearchTruth{
			{Query: "PaymentGateway", ExpectedInTop3: "PaymentGateway"},
			{Query: "validate_total", ExpectedInTop3: "validate_total"},
		},
		Blast: []BlastTruth{
			{Symbol: "smoke.OrderService.Process", MinDirectCallers: 2},
		},
		Conventions: ConventionsTruth{
			MinDetected: len(convs),
		},
		Performance: PerformanceTruth{
			ScanMaxSeconds: 10,
			QueryMaxMs:     500,
		},
	}

	data, err := json.MarshalIndent(gt, "", "  ")
	if err != nil {
		t.Fatalf("marshal ground truth: %v", err)
	}

	if err := os.WriteFile(groundTruthPath(t), data, 0o644); err != nil {
		t.Fatalf("write ground-truth.json: %v", err)
	}

	t.Logf("generated ground-truth.json: %d symbols, %d edges, %d conventions", len(allSymbols), res.Edges, len(convs))

	engine := search.NewEngine(adapter, nil, nil)
	for _, sq := range gt.Search {
		results, _, _, err := engine.Search(ctx, search.Options{Query: sq.Query, Limit: 5})
		if err != nil {
			t.Logf("REVIEW search %q: error: %v", sq.Query, err)
			continue
		}
		t.Logf("REVIEW search %q expecting %q in top 3:", sq.Query, sq.ExpectedInTop3)
		for i, r := range results {
			t.Logf("  #%d %s (%s) score=%.4f", i+1, r.Qualified, r.Kind, r.Score)
		}
	}
}
