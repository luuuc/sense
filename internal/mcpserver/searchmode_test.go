package mcpserver

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/metrics"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/profile"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

// lowConfEmbedder returns query vectors orthogonal to every seeded symbol
// embedding, forcing vector confidence into the low bucket so the shape
// floor (semantic mode) is distinguishable from the confidence ladder
// (keyword/identifier mode).
type lowConfEmbedder struct{}

func (lowConfEmbedder) Embed(_ context.Context, inputs []embed.EmbedInput) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i := range inputs {
		v := make([]float32, 384)
		v[200] = 0.9
		v[201] = 0.1
		out[i] = v
	}
	return out, nil
}

func (lowConfEmbedder) Close() error { return nil }

// setupVectorHandlers builds a handlers fixture backed by a vector index so
// the mode override's effect on fusion weights is observable.
func setupVectorHandlers(t *testing.T) *handlers {
	t.Helper()
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	fid, err := adapter.WriteFile(ctx, &model.File{
		Path: "app/auth.go", Language: "go", Hash: "h1", Symbols: 3, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Authenticate", "AuthToken", "AuthGuard"} {
		id, werr := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: fid, Name: name, Qualified: "auth." + name,
			Kind: "function", LineStart: 1, LineEnd: 5,
		})
		if werr != nil {
			t.Fatal(werr)
		}
		vec := make([]float32, 384)
		vec[10] = 0.9
		vec[11] = 0.1
		if eerr := adapter.WriteEmbedding(ctx, id, vectorBlob(vec)); eerr != nil {
			t.Fatal(eerr)
		}
	}

	embeddings, err := adapter.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vectorIdx := search.BuildFlatIndex(embeddings)
	engine := search.NewEngine(adapter, vectorIdx, lowConfEmbedder{})

	tracker := metrics.NewTracker(adapter.DB())
	t.Cleanup(func() { tracker.Close() })

	return &handlers{
		adapter:     adapter,
		db:          adapter.DB(),
		dir:         dir,
		search:      engine,
		tracker:     tracker,
		defaults:    profile.DefaultParams(),
		seenSymbols: make(map[int64]bool),
	}
}

func vectorBlob(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// TestHandleSearchModeAffectsFusionWeights proves the sense_search `mode`
// argument reaches the engine: on the same query, keyword mode reproduces
// the confidence ladder (low-confidence vector weight 0.3) while semantic
// mode floors the vector leg at 0.5.
func TestHandleSearchModeAffectsFusionWeights(t *testing.T) {
	h := setupVectorHandlers(t)
	ctx := context.Background()

	weightsFor := func(mode string) mcpio.FusionWeights {
		t.Helper()
		args := map[string]any{"query": "auth", "limit": 10}
		if mode != "" {
			args["mode"] = mode
		}
		result, err := h.handleSearch(ctx, toolReq(args))
		if err != nil {
			t.Fatalf("handleSearch(mode=%q): %v", mode, err)
		}
		if result.IsError {
			t.Fatalf("handleSearch(mode=%q) error: %s", mode, resultText(t, result))
		}
		var resp mcpio.SearchResponse
		if err := json.Unmarshal([]byte(resultText(t, result)), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return resp.FusionWeights
	}

	keyword := weightsFor(search.ModeKeyword)
	if keyword.Vector != 0.3 {
		t.Errorf("keyword mode vector weight = %v, want 0.3 (confidence ladder)", keyword.Vector)
	}

	semantic := weightsFor(search.ModeSemantic)
	if semantic.Vector != 0.5 {
		t.Errorf("semantic mode vector weight = %v, want 0.5 (shape floor)", semantic.Vector)
	}

	// Default (omitted mode) must behave as hybrid. "auth" is a single
	// identifier token, so the classifier picks Identifier → ladder 0.3.
	def := weightsFor("")
	if def.Vector != 0.3 {
		t.Errorf("default mode vector weight = %v, want 0.3 (hybrid → Identifier for a lone identifier)", def.Vector)
	}
}
