package search_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"strings"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

// seedFusionIndex creates a small index with symbols, edges, and
// embeddings suitable for testing RRF fusion behavior.
func seedFusionIndex(ctx context.Context, t *testing.T, a *sqlite.Adapter) {
	t.Helper()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "payment.go", Language: "go",
		Hash: "a1", Symbols: 3, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Symbol 1: matches keyword "payment" AND has a payment-related embedding
	sid1, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "ProcessPayment", Qualified: "pkg.ProcessPayment",
		Kind: "function", LineStart: 1, LineEnd: 20,
		Docstring: "processes payment transactions",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Symbol 2: matches keyword "payment" but NOT semantically related
	sid2, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "PaymentConfig", Qualified: "pkg.PaymentConfig",
		Kind: "type", LineStart: 25, LineEnd: 30,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Symbol 3: semantically related to payment but no keyword match
	sid3, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "ChargeCard", Qualified: "pkg.ChargeCard",
		Kind: "function", LineStart: 35, LineEnd: 50,
		Docstring: "charges a credit card via Stripe",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add edges to make ProcessPayment a hub node
	fid2, err := a.WriteFile(ctx, &model.File{
		Path: "caller.go", Language: "go",
		Hash: "b1", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	caller1, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid2, Name: "HandleOrder", Qualified: "pkg.HandleOrder",
		Kind: "function", LineStart: 1, LineEnd: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	caller2, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid2, Name: "HandleRefund", Qualified: "pkg.HandleRefund",
		Kind: "function", LineStart: 15, LineEnd: 25,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Two callers → ProcessPayment is a hub
	for _, callerID := range []int64{caller1, caller2} {
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: &callerID, TargetID: sid1,
			Kind: model.EdgeCalls, FileID: fid2, Confidence: 1.0,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Write embeddings: sid1 and sid3 are semantically close (payment-related),
	// sid2 is distant (config, not payment processing)
	paymentVec := make([]float32, 384)
	paymentVec[0] = 0.9
	paymentVec[1] = 0.1

	configVec := make([]float32, 384)
	configVec[100] = 0.9
	configVec[101] = 0.1

	chargeVec := make([]float32, 384)
	chargeVec[0] = 0.85
	chargeVec[1] = 0.15
	chargeVec[2] = 0.1

	for id, vec := range map[int64][]float32{sid1: paymentVec, sid2: configVec, sid3: chargeVec} {
		blob := vectorToBlob(vec)
		if err := a.WriteEmbedding(ctx, id, blob); err != nil {
			t.Fatal(err)
		}
	}
}

func vectorToBlob(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// paymentQueryEmbedder returns vectors close to the payment embedding
// and distant from the config embedding.
type paymentQueryEmbedder struct{}

func (p *paymentQueryEmbedder) Embed(_ context.Context, inputs []embed.EmbedInput) ([][]float32, error) {
	vecs := make([][]float32, len(inputs))
	for i := range inputs {
		vec := make([]float32, 384)
		vec[0] = 0.9
		vec[1] = 0.1
		vecs[i] = vec
	}
	return vecs, nil
}

func (p *paymentQueryEmbedder) Close() error { return nil }

func TestFusionBothBackendsRankHigher(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(ctx, t, a)

	embeddings, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vectorIdx := search.BuildFlatIndex(embeddings)

	engine := search.NewEngine(a, vectorIdx, &paymentQueryEmbedder{})

	results, meta, err := engine.Search(ctx, search.Options{
		Query: "payment",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	if meta.SymbolCount < 3 {
		t.Errorf("expected at least 3 symbols searched, got %d", meta.SymbolCount)
	}

	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}

	// ProcessPayment should rank highest: it matches keyword AND vector,
	// and has graph centrality (2 callers).
	if results[0].Qualified != "pkg.ProcessPayment" {
		t.Errorf("expected ProcessPayment to rank first, got %q", results[0].Qualified)
	}

	t.Logf("results:")
	for i, r := range results {
		t.Logf("  %d. %s (score=%.6f)", i+1, r.Qualified, r.Score)
	}
}

func TestFusionKeywordOnly(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(ctx, t, a)

	// No vector index, no embedder → keyword-only
	engine := search.NewEngine(a, nil, nil)

	results, _, err := engine.Search(ctx, search.Options{
		Query: "payment",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(results) == 0 {
		t.Fatal("expected keyword results, got none")
	}

	// All results should have "payment" in name or docstring
	for _, r := range results {
		t.Logf("keyword-only: %s (score=%.6f)", r.Qualified, r.Score)
	}
}

func TestFusionCentralityBreaksTie(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "tie.go", Language: "go",
		Hash: "t1", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Two symbols with identical keyword relevance (both match "handler")
	sidHub, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "HubHandler", Qualified: "pkg.HubHandler",
		Kind: "function", LineStart: 1, LineEnd: 10,
		Docstring: "handler with many callers",
	})
	if err != nil {
		t.Fatal(err)
	}
	sidLeaf, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "LeafHandler", Qualified: "pkg.LeafHandler",
		Kind: "function", LineStart: 15, LineEnd: 25,
		Docstring: "handler with no callers",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Give HubHandler 5 callers, LeafHandler gets none.
	fid2, err := a.WriteFile(ctx, &model.File{
		Path: "callers.go", Language: "go",
		Hash: "c1", Symbols: 5, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		callerID, err := a.WriteSymbol(ctx, &model.Symbol{
			FileID: fid2, Name: fmt.Sprintf("Caller%d", i),
			Qualified: fmt.Sprintf("pkg.Caller%d", i),
			Kind:      "function", LineStart: i * 10, LineEnd: i*10 + 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: &callerID, TargetID: sidHub,
			Kind: model.EdgeCalls, FileID: fid2, Confidence: 1.0,
		}); err != nil {
			t.Fatal(err)
		}
	}
	_ = sidLeaf // used via keyword search match

	engine := search.NewEngine(a, nil, nil)

	results, _, err := engine.Search(ctx, search.Options{
		Query: "handler",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	// Both match "handler" equally in FTS5, but HubHandler has centrality.
	if results[0].Qualified != "pkg.HubHandler" {
		t.Errorf("expected HubHandler to rank first due to centrality, got %q", results[0].Qualified)
	}

	t.Logf("centrality tie-break:")
	for i, r := range results {
		t.Logf("  %d. %s (score=%.6f)", i+1, r.Qualified, r.Score)
	}
}

func TestSearchRanksHubSymbolHigher(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "handler.go", Language: "go",
		Hash: "h1", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	sidHub, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "HandleRequest", Qualified: "pkg.HandleRequest",
		Kind: "function", LineStart: 1, LineEnd: 20,
		Docstring: "handles incoming request",
	})
	if err != nil {
		t.Fatal(err)
	}
	sidHelper, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "HandleRequestHelper", Qualified: "pkg.HandleRequestHelper",
		Kind: "function", LineStart: 25, LineEnd: 40,
		Docstring: "helper for handling request setup",
	})
	if err != nil {
		t.Fatal(err)
	}

	callerFile, err := a.WriteFile(ctx, &model.File{
		Path: "callers.go", Language: "go",
		Hash: "c1", Symbols: 32, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Hub: 30 inbound edges.
	for i := range 30 {
		callerID, err := a.WriteSymbol(ctx, &model.Symbol{
			FileID: callerFile, Name: fmt.Sprintf("Caller%d", i),
			Qualified: fmt.Sprintf("pkg.Caller%d", i),
			Kind:      "function", LineStart: i * 10, LineEnd: i*10 + 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: &callerID, TargetID: sidHub,
			Kind: model.EdgeCalls, FileID: callerFile, Confidence: 1.0,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Helper: 2 inbound edges.
	for i := range 2 {
		callerID, err := a.WriteSymbol(ctx, &model.Symbol{
			FileID: callerFile, Name: fmt.Sprintf("HelperCaller%d", i),
			Qualified: fmt.Sprintf("pkg.HelperCaller%d", i),
			Kind:      "function", LineStart: 300 + i*10, LineEnd: 305 + i*10,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: &callerID, TargetID: sidHelper,
			Kind: model.EdgeCalls, FileID: callerFile, Confidence: 1.0,
		}); err != nil {
			t.Fatal(err)
		}
	}

	engine := search.NewEngine(a, nil, nil)
	results, _, err := engine.Search(ctx, search.Options{
		Query: "handle request",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	if results[0].Qualified != "pkg.HandleRequest" {
		t.Errorf("hub symbol (30 callers) should rank above helper (2 callers); got %q first", results[0].Qualified)
	}

	if results[0].References != 30 {
		t.Errorf("hub symbol references = %d, want 30", results[0].References)
	}

	t.Logf("hub vs helper ranking:")
	for i, r := range results[:min(5, len(results))] {
		t.Logf("  %d. %s (score=%.4f, refs=%d)", i+1, r.Qualified, r.Score, r.References)
	}
}

func TestFusionMinScore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(ctx, t, a)

	engine := search.NewEngine(a, nil, nil)

	results, _, err := engine.Search(ctx, search.Options{
		Query:    "payment",
		Limit:    10,
		MinScore: 999, // absurdly high — should filter everything
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results with high min_score, got %d", len(results))
	}
}

func TestNormalizeScoresSpread(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(ctx, t, a)

	embeddings, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vectorIdx := search.BuildFlatIndex(embeddings)
	engine := search.NewEngine(a, vectorIdx, &paymentQueryEmbedder{})

	results, _, err := engine.Search(ctx, search.Options{
		Query: "payment",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatal("need at least 2 results to test normalization")
	}

	// Top result should be close to 1.0 after normalization.
	if results[0].Score < 0.9 {
		t.Errorf("top result score = %.4f, want >= 0.9 after normalization", results[0].Score)
	}
	// Scores should be differentiated (not all 0.02 like before normalization).
	if results[0].Score == results[len(results)-1].Score {
		t.Error("normalized scores should be differentiated, but top == bottom")
	}

	t.Logf("normalized scores:")
	for i, r := range results {
		t.Logf("  %d. %s (score=%.4f)", i+1, r.Qualified, r.Score)
	}
}

func TestKindWeightsDemotesModules(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "mod.rb", Language: "ruby",
		Hash: "m1", Symbols: 3, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// A module symbol that would normally rank first via keyword match.
	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Payments", Qualified: "Payments",
		Kind: "module", LineStart: 1, LineEnd: 50,
		Docstring: "payment processing module namespace",
	}); err != nil {
		t.Fatal(err)
	}
	// A method symbol with a weaker keyword match but specific kind.
	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "process_payment", Qualified: "Payments#process_payment",
		Kind: "method", LineStart: 10, LineEnd: 20,
		Docstring: "processes a single payment",
	}); err != nil {
		t.Fatal(err)
	}
	// A class symbol with a weaker keyword match.
	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "PaymentService", Qualified: "Payments::PaymentService",
		Kind: "class", LineStart: 25, LineEnd: 45,
		Docstring: "payment service class",
	}); err != nil {
		t.Fatal(err)
	}

	engine := search.NewEngine(a, nil, nil)
	results, _, err := engine.Search(ctx, search.Options{
		Query: "payment",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(results) == 0 {
		t.Fatal("no results returned")
	}

	if results[0].Kind == "module" {
		t.Errorf("module %q ranks first; kind weights should demote it below non-module results",
			results[0].Qualified)
	}

	hasModule := false
	for _, r := range results {
		if r.Kind == "module" {
			hasModule = true
		}
	}
	if !hasModule {
		t.Error("module symbol missing from results entirely — test setup issue")
	}
}

func TestSearchDemotesTestSymbols(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "handler.go", Language: "go",
		Hash: "h1", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	sidReal, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "HandleRequest", Qualified: "pkg.HandleRequest",
		Kind: "function", LineStart: 1, LineEnd: 20,
		Docstring: "handles incoming request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "TestHandleRequest", Qualified: "pkg.TestHandleRequest",
		Kind: "function", LineStart: 25, LineEnd: 40,
		Docstring: "test for handle request",
	}); err != nil {
		t.Fatal(err)
	}

	callerFile, err := a.WriteFile(ctx, &model.File{
		Path: "callers.go", Language: "go",
		Hash: "c1", Symbols: 10, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		callerID, err := a.WriteSymbol(ctx, &model.Symbol{
			FileID: callerFile, Name: fmt.Sprintf("Caller%d", i),
			Qualified: fmt.Sprintf("pkg.Caller%d", i),
			Kind:      "function", LineStart: i * 10, LineEnd: i*10 + 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: &callerID, TargetID: sidReal,
			Kind: model.EdgeCalls, FileID: callerFile, Confidence: 1.0,
		}); err != nil {
			t.Fatal(err)
		}
	}

	engine := search.NewEngine(a, nil, nil)
	results, _, err := engine.Search(ctx, search.Options{
		Query: "handle request",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	if results[0].Qualified != "pkg.HandleRequest" {
		t.Errorf("HandleRequest should rank above TestHandleRequest; got %q first", results[0].Qualified)
	}

	t.Logf("test demotion ranking:")
	for i, r := range results[:min(5, len(results))] {
		t.Logf("  %d. %s (score=%.4f)", i+1, r.Qualified, r.Score)
	}
}

func TestSearchModeKeyword(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(ctx, t, a)

	engine := search.NewEngine(a, nil, nil)
	_, meta, err := engine.Search(ctx, search.Options{Query: "payment", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if meta.Mode != search.ModeKeyword {
		t.Errorf("mode = %q, want %q", meta.Mode, search.ModeKeyword)
	}
}

func TestSearchModeHybrid(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(ctx, t, a)

	embeddings, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vectorIdx := search.BuildFlatIndex(embeddings)
	engine := search.NewEngine(a, vectorIdx, &paymentQueryEmbedder{})

	_, meta, err := engine.Search(ctx, search.Options{Query: "payment", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if meta.Mode != search.ModeHybrid {
		t.Errorf("mode = %q, want %q", meta.Mode, search.ModeHybrid)
	}
}

func TestSearchModeUpgradeViaSetVectors(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(ctx, t, a)

	engine := search.NewEngine(a, nil, &paymentQueryEmbedder{})

	_, meta1, err := engine.Search(ctx, search.Options{Query: "payment", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if meta1.Mode != search.ModeKeyword {
		t.Errorf("before SetVectors: mode = %q, want %q", meta1.Mode, search.ModeKeyword)
	}

	embeddings, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vectorIdx := search.BuildFlatIndex(embeddings)
	engine.SetVectors(vectorIdx)

	_, meta2, err := engine.Search(ctx, search.Options{Query: "payment", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if meta2.Mode != search.ModeHybrid {
		t.Errorf("after SetVectors: mode = %q, want %q", meta2.Mode, search.ModeHybrid)
	}
}

func TestPathWeightsDemotesMigrationsAndScripts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	appFile, err := a.WriteFile(ctx, &model.File{
		Path: "app/models/post.rb", Language: "ruby",
		Hash: "a1", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	migrateFile, err := a.WriteFile(ctx, &model.File{
		Path: "db/migrate/20190101_create_posts.rb", Language: "ruby",
		Hash: "m1", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	scriptFile, err := a.WriteFile(ctx, &model.File{
		Path: "script/import_scripts/import_posts.rb", Language: "ruby",
		Hash: "s1", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: appFile, Name: "Post", Qualified: "Post",
		Kind: "class", LineStart: 1, LineEnd: 100,
		Docstring: "post moderation flagging",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: migrateFile, Name: "MigrateOldModeratorPosts", Qualified: "MigrateOldModeratorPosts#down",
		Kind: "method", LineStart: 1, LineEnd: 20,
		Docstring: "post moderation flagging",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: scriptFile, Name: "import_post", Qualified: "ImportScripts::GenericDatabase#import_post",
		Kind: "method", LineStart: 1, LineEnd: 30,
		Docstring: "post moderation flagging",
	}); err != nil {
		t.Fatal(err)
	}

	engine := search.NewEngine(a, nil, nil)
	results, _, err := engine.Search(ctx, search.Options{
		Query: "post moderation flagging",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(results) < 3 {
		t.Fatalf("expected at least 3 results, got %d", len(results))
	}

	if results[0].Qualified != "Post" {
		t.Errorf("app code %q should rank first, got %q", "Post", results[0].Qualified)
	}

	has := map[string]bool{}
	for _, r := range results {
		has[r.Qualified] = true
		t.Logf("  %s (score=%.4f, kind=%s)", r.Qualified, r.Score, r.Kind)
	}
	for _, want := range []string{"MigrateOldModeratorPosts#down", "ImportScripts::GenericDatabase#import_post"} {
		if !has[want] {
			t.Errorf("demoted symbol %q missing from results — should be present but ranked lower", want)
		}
	}
}

func TestFusionWeightsKeywordOnly(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(ctx, t, a)

	engine := search.NewEngine(a, nil, nil)
	_, meta, err := engine.Search(ctx, search.Options{Query: "payment", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if meta.KeywordWeight != 1.0 {
		t.Errorf("keyword weight = %v, want 1.0", meta.KeywordWeight)
	}
	if meta.VectorWeight != 0.0 {
		t.Errorf("vector weight = %v, want 0.0", meta.VectorWeight)
	}
}

func TestFusionWeightsHybrid(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(ctx, t, a)
	embeddings, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vectorIdx := search.BuildFlatIndex(embeddings)
	engine := search.NewEngine(a, vectorIdx, &paymentQueryEmbedder{})

	_, meta, err := engine.Search(ctx, search.Options{Query: "payment", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if meta.KeywordWeight+meta.VectorWeight == 0 {
		t.Error("both weights are zero in hybrid mode")
	}
	if meta.KeywordWeight < meta.VectorWeight {
		t.Logf("keyword=%.2f vector=%.2f — keyword-biased (low vector confidence)", meta.KeywordWeight, meta.VectorWeight)
	} else {
		t.Logf("keyword=%.2f vector=%.2f", meta.KeywordWeight, meta.VectorWeight)
	}
}

// lowConfidenceEmbedder returns vectors orthogonal to all indexed symbols,
// ensuring cosine similarity is near zero and triggering the weight floor.
type lowConfidenceEmbedder struct{}

func (l *lowConfidenceEmbedder) Embed(_ context.Context, inputs []embed.EmbedInput) ([][]float32, error) {
	vecs := make([][]float32, len(inputs))
	for i := range inputs {
		vec := make([]float32, 384)
		vec[200] = 0.9
		vec[201] = 0.1
		vecs[i] = vec
	}
	return vecs, nil
}

func (l *lowConfidenceEmbedder) Close() error { return nil }

func TestLowConfidenceVectorFloor(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(ctx, t, a)

	embeddings, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vectorIdx := search.BuildFlatIndex(embeddings)
	// Use an embedder that produces vectors orthogonal to the index,
	// guaranteeing low vector confidence (< 0.4).
	engine := search.NewEngine(a, vectorIdx, &lowConfidenceEmbedder{})

	_, meta, err := engine.Search(ctx, search.Options{Query: "payment", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}

	// With low confidence, vector weight should be 0.3 and keyword 0.7.
	if meta.VectorWeight != 0.3 {
		t.Errorf("vector weight = %v, want 0.3", meta.VectorWeight)
	}
	if meta.KeywordWeight != 0.7 {
		t.Errorf("keyword weight = %v, want 0.7", meta.KeywordWeight)
	}
	t.Logf("low confidence weights: kw=%.2f vec=%.2f", meta.KeywordWeight, meta.VectorWeight)
}

// TestNaturalLanguageQueryFloorsVectorWeight drives the full Search path
// with a low-confidence embedder (vectors orthogonal to the index, so
// confidence lands in the < 0.4 bucket) on a NATURAL-LANGUAGE query. The
// confidence ladder alone would return vector weight 0.3; the shape floor
// must lift it to 0.5. This proves the shape (not just cosine confidence)
// steers the weighting end-to-end, including propagation through
// expandQuery's sub-queries.
func TestNaturalLanguageQueryFloorsVectorWeight(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(ctx, t, a)
	embeddings, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vectorIdx := search.BuildFlatIndex(embeddings)
	engine := search.NewEngine(a, vectorIdx, &lowConfidenceEmbedder{})

	// Natural-language query: stopword "the" + multiple plain words, no
	// identifier-shaped tokens → NaturalLanguage.
	_, meta, err := engine.Search(ctx, search.Options{
		Query: "prevent the user from charging their own card", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	if meta.Shape != "natural_language" {
		t.Fatalf("shape = %q, want natural_language", meta.Shape)
	}
	if meta.VectorWeight != 0.5 {
		t.Errorf("vector weight = %v, want 0.5 (floored despite low confidence)", meta.VectorWeight)
	}
	if meta.KeywordWeight != 0.5 {
		t.Errorf("keyword weight = %v, want 0.5", meta.KeywordWeight)
	}
	t.Logf("NL floored weights: kw=%.2f vec=%.2f", meta.KeywordWeight, meta.VectorWeight)
}

// TestGenericTokenPenaltyEndToEnd seeds a corpus where "prevent" is a
// high-frequency generic token and "listing" is a rare domain token, then
// drives the full keyword-only Search path with a natural-language query.
// The hits that matched ONLY the generic token ("preventClose" etc.) must
// be demoted below the domain match ("ListingGuard"), reproducing the
// headline fix in miniature.
func TestGenericTokenPenaltyEndToEnd(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "app/ui.js", Language: "javascript",
		Hash: "h1", Symbols: 40, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, qualified string) {
		t.Helper()
		if _, err := a.WriteSymbol(ctx, &model.Symbol{
			FileID: fid, Name: name, Qualified: qualified,
			Kind: "function", LineStart: 1, LineEnd: 2,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Six symbols whose only query overlap is the generic token "prevent".
	preventNames := []string{
		"preventClose", "preventEscape", "preventResize",
		"preventScroll", "preventDefault", "preventEditing",
	}
	for _, n := range preventNames {
		write(n, "ui."+n)
	}
	// One rare domain symbol matching the non-generic token "listing".
	write("ListingGuard", "shop.ListingGuard")
	// Filler symbols (none containing the query terms) to make "prevent"
	// land above the 5% generic threshold and "listing" below it: 7 named
	// symbols + 33 fillers = 40 total → prevent 6/40=0.15 (generic),
	// listing 1/40=0.025 (specific).
	for i := 0; i < 33; i++ {
		write(fmt.Sprintf("Widget%d", i), fmt.Sprintf("ui.Widget%d", i))
	}

	// Keyword-only engine (no embedder) isolates the keyword-leg defect.
	engine := search.NewEngine(a, nil, nil)
	results, meta, err := engine.Search(ctx, search.Options{
		Query: "prevent adding own listing", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if meta.Shape != "natural_language" {
		t.Fatalf("shape = %q, want natural_language", meta.Shape)
	}

	rank := map[string]int{}
	for i, r := range results {
		rank[r.Name] = i
	}
	listingRank, ok := rank["ListingGuard"]
	if !ok {
		t.Fatalf("ListingGuard absent from results: %+v", names(results))
	}
	for _, n := range preventNames {
		if pr, ok := rank[n]; ok && pr < listingRank {
			t.Errorf("generic-only hit %q (rank %d) outranked domain match ListingGuard (rank %d)", n, pr, listingRank)
		}
	}
	if listingRank != 0 {
		t.Logf("ListingGuard at rank %d (top result is %q)", listingRank, results[0].Name)
	}
}

// TestGenericTokenPenaltyDemotesGenericOnlyHits pins the generic-token
// penalty: a keyword-only hit that matched solely on a high-frequency token
// ("prevent" → preventClose) must rank below the genuine domain match
// ("ListingGuard") for the query. This is the demotion the score-domain
// passes exist to deliver, asserted on the live pipeline.
func TestGenericTokenPenaltyDemotesGenericOnlyHits(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "app/ui.js", Language: "javascript",
		Hash: "h1", Symbols: 40, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, qualified string) {
		t.Helper()
		if _, err := a.WriteSymbol(ctx, &model.Symbol{
			FileID: fid, Name: name, Qualified: qualified,
			Kind: "function", LineStart: 1, LineEnd: 2,
		}); err != nil {
			t.Fatal(err)
		}
	}
	preventNames := []string{
		"preventClose", "preventEscape", "preventResize",
		"preventScroll", "preventDefault", "preventEditing",
	}
	for _, n := range preventNames {
		write(n, "ui."+n)
	}
	write("ListingGuard", "shop.ListingGuard")
	for i := 0; i < 33; i++ {
		write(fmt.Sprintf("Widget%d", i), fmt.Sprintf("ui.Widget%d", i))
	}

	engine := search.NewEngine(a, nil, nil)

	results, _, err := engine.Search(ctx, search.Options{
		Query: "prevent adding own listing", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	rank := map[string]int{}
	for i, r := range results {
		rank[r.Name] = i
	}
	listingRank, ok := rank["ListingGuard"]
	if !ok {
		t.Fatalf("ListingGuard absent from results: %v", names(results))
	}
	for _, n := range preventNames {
		if pr, ok := rank[n]; ok && pr < listingRank {
			t.Errorf("generic-token penalty failed: generic-only %q (rank %d) above ListingGuard (rank %d)", n, pr, listingRank)
		}
	}
}

func names(rs []search.Result) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

// TestSearchModeOverridesShape drives the full Search path under a
// low-confidence embedder and asserts each mode's effect on the resolved
// fusion weights: keyword mode reproduces the pre-shape ranking (vector
// 0.3 in the low bucket) even on an NL query; semantic mode floors the
// vector leg (0.5) even on an identifier query; hybrid is the default and
// defers to the classifier.
func TestSearchModeOverridesShape(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(ctx, t, a)
	embeddings, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vectorIdx := search.BuildFlatIndex(embeddings)
	engine := search.NewEngine(a, vectorIdx, &lowConfidenceEmbedder{})

	const nlQuery = "prevent the user from charging their own card"
	const identQuery = "ProcessPayment"

	tests := []struct {
		name      string
		mode      string
		query     string
		wantShape string
		wantVec   float64
		wantKw    float64
	}{
		{"NL query, default mode is hybrid → classifier → NL floor", "", nlQuery, "natural_language", 0.5, 0.5},
		{"NL query, hybrid → classifier → NL floor", search.ModeHybrid, nlQuery, "natural_language", 0.5, 0.5},
		{"NL query, keyword mode reproduces pre-shape ranking", search.ModeKeyword, nlQuery, "identifier", 0.3, 0.7},
		{"identifier query, hybrid → Identifier (pre-shape)", search.ModeHybrid, identQuery, "identifier", 0.3, 0.7},
		{"identifier query, semantic mode floors the vector leg", search.ModeSemantic, identQuery, "natural_language", 0.5, 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, meta, err := engine.Search(ctx, search.Options{Query: tt.query, Mode: tt.mode, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if meta.Shape != tt.wantShape {
				t.Errorf("shape = %q, want %q", meta.Shape, tt.wantShape)
			}
			if meta.VectorWeight != tt.wantVec {
				t.Errorf("vector weight = %v, want %v", meta.VectorWeight, tt.wantVec)
			}
			if meta.KeywordWeight != tt.wantKw {
				t.Errorf("keyword weight = %v, want %v", meta.KeywordWeight, tt.wantKw)
			}
		})
	}
}

func TestGraphEnrichmentBoostsCallees(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	// Create a hub function that calls a leaf function.
	fid, err := a.WriteFile(ctx, &model.File{
		Path: "handler.go", Language: "go",
		Hash: "h1", Symbols: 3, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	hub, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "ServeHTTP", Qualified: "pkg.ServeHTTP",
		Kind: "function", LineStart: 1, LineEnd: 20,
		Docstring: "serves HTTP requests",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Callee that also matches keyword but with lower rank.
	callee, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "HandleRoute", Qualified: "pkg.HandleRoute",
		Kind: "function", LineStart: 25, LineEnd: 40,
		Docstring: "handles a route",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Unrelated symbol that matches keyword.
	_, err = a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "HTTPConfig", Qualified: "pkg.HTTPConfig",
		Kind: "type", LineStart: 45, LineEnd: 50,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Edge: ServeHTTP calls HandleRoute.
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: &hub, TargetID: callee,
		Kind: model.EdgeCalls, FileID: fid, Confidence: 1.0,
	}); err != nil {
		t.Fatal(err)
	}

	engine := search.NewEngine(a, nil, nil)
	results, _, err := engine.Search(ctx, search.Options{Query: "HTTP", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}

	// HandleRoute should be boosted because it's a callee of ServeHTTP (top result).
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	// Find HandleRoute's position.
	var handleRouteScore float64
	var httpConfigScore float64
	for _, r := range results {
		switch r.Name {
		case "HandleRoute":
			handleRouteScore = r.Score
		case "HTTPConfig":
			httpConfigScore = r.Score
		}
	}

	if handleRouteScore <= httpConfigScore {
		t.Errorf("HandleRoute (callee of top result) score=%.4f should be > HTTPConfig score=%.4f",
			handleRouteScore, httpConfigScore)
	}

	// Every result must carry honest provenance — never empty, never the
	// old hardcoded "structural" placeholder.
	valid := map[string]bool{
		search.SourceKeyword: true, search.SourceVector: true,
		search.SourceHybrid: true, search.SourceGraph: true,
	}
	for _, r := range results {
		if !valid[r.Source] {
			t.Errorf("result %s has invalid source %q", r.Qualified, r.Source)
		}
	}
	t.Logf("enrichment result:")
	for i, r := range results {
		t.Logf("  %d. %s (score=%.4f)", i+1, r.Qualified, r.Score)
	}
}

// errEmbedder always fails, exercising the embed error path in Search.
type errEmbedder struct{}

func (e *errEmbedder) Embed(_ context.Context, _ []embed.EmbedInput) ([][]float32, error) {
	return nil, fmt.Errorf("boom")
}

func (e *errEmbedder) Close() error { return nil }

// TestSearchEmbedError verifies that an embedder failure surfaces as a
// wrapped "search embed" error rather than silently degrading.
func TestSearchEmbedError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(ctx, t, a)

	embeddings, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vectorIdx := search.BuildFlatIndex(embeddings)

	engine := search.NewEngine(a, vectorIdx, &errEmbedder{})

	_, _, err = engine.Search(ctx, search.Options{Query: "payment", Limit: 10})
	if err == nil || !strings.Contains(err.Error(), "search embed") {
		t.Fatalf("expected wrapped embed error, got %v", err)
	}
}

// TestSearchDocumentFrequencyError verifies that a DocumentFrequency
// failure (here, a dropped FTS table) surfaces as a wrapped "search df"
// error. The semantic mode pins a non-Identifier shape so the DF lookup
// always runs.
func TestSearchDocumentFrequencyError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(ctx, t, a)

	// Drop the FTS table so DocumentFrequency's MATCH query errors while
	// SymbolCount (which reads sense_symbols) still succeeds.
	if _, err := a.DB().ExecContext(ctx, "DROP TABLE sense_symbols_fts"); err != nil {
		t.Fatal(err)
	}

	engine := search.NewEngine(a, nil, nil)

	_, _, err = engine.Search(ctx, search.Options{
		Query: "payment",
		Mode:  search.ModeSemantic,
		Limit: 10,
	})
	if err == nil || !strings.Contains(err.Error(), "search df") {
		t.Fatalf("expected wrapped df error, got %v", err)
	}
}
