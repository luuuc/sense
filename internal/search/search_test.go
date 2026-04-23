package search_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

// seedFusionIndex creates a small index with symbols, edges, and
// embeddings suitable for testing RRF fusion behavior.
func seedFusionIndex(t *testing.T, ctx context.Context, a *sqlite.Adapter) {
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

	seedFusionIndex(t, ctx, a)

	embeddings, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vectorIdx := search.BuildHNSWIndex(embeddings)

	engine := search.NewEngine(a, vectorIdx, &paymentQueryEmbedder{})

	results, symbolCount, err := engine.Search(ctx, search.Options{
		Query: "payment",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	if symbolCount < 3 {
		t.Errorf("expected at least 3 symbols searched, got %d", symbolCount)
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

	seedFusionIndex(t, ctx, a)

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
			Kind: "function", LineStart: i * 10, LineEnd: i*10 + 5,
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

func TestFusionMinScore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedFusionIndex(t, ctx, a)

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

	seedFusionIndex(t, ctx, a)

	embeddings, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vectorIdx := search.BuildHNSWIndex(embeddings)
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
