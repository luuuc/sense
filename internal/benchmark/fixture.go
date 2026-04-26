package benchmark

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

type Fixture struct {
	Adapter   *sqlite.Adapter
	SymbolIDs []int64
	FileIDs   []int64
}

func BuildFixture(ctx context.Context, adapter *sqlite.Adapter, symbolCount int) (*Fixture, error) {
	rng := rand.New(rand.NewSource(42))
	fix := &Fixture{Adapter: adapter}

	langs := []struct {
		lang string
		ext  string
		dir  string
	}{
		{"go", ".go", "internal/"},
		{"ruby", ".rb", "app/"},
		{"python", ".py", "lib/"},
		{"typescript", ".ts", "src/"},
	}

	kinds := []model.SymbolKind{
		model.KindClass, model.KindFunction, model.KindMethod,
		model.KindConstant, model.KindInterface,
	}

	filesPerLang := max(symbolCount/20, 1)
	now := time.Now().UTC()

	err := adapter.InTx(ctx, func() error {
		for _, l := range langs {
			for f := 0; f < filesPerLang; f++ {
				fid, err := adapter.WriteFile(ctx, &model.File{
					Path:      fmt.Sprintf("%s%s_%d%s", l.dir, l.lang, f, l.ext),
					Language:  l.lang,
					Hash:      fmt.Sprintf("hash_%s_%d", l.lang, f),
					Symbols:   symbolCount / (len(langs) * filesPerLang),
					IndexedAt: now,
				})
				if err != nil {
					return fmt.Errorf("write file: %w", err)
				}
				fix.FileIDs = append(fix.FileIDs, fid)
			}
		}

		symsPerFile := symbolCount / len(fix.FileIDs)
		if symsPerFile < 1 {
			symsPerFile = 1
		}

		for i, fid := range fix.FileIDs {
			langIdx := i / filesPerLang
			if langIdx >= len(langs) {
				langIdx = len(langs) - 1
			}

			for s := 0; s < symsPerFile && len(fix.SymbolIDs) < symbolCount; s++ {
				kind := kinds[rng.Intn(len(kinds))]
				name := fmt.Sprintf("Symbol%d", len(fix.SymbolIDs))
				qualified := fmt.Sprintf("%s.%s", langs[langIdx].dir, name)

				sid, err := adapter.WriteSymbol(ctx, &model.Symbol{
					FileID:    fid,
					Name:      name,
					Qualified: qualified,
					Kind:      kind,
					LineStart: s*10 + 1,
					LineEnd:   s*10 + 9,
					Snippet:   fmt.Sprintf("func %s() {}", name),
				})
				if err != nil {
					return fmt.Errorf("write symbol: %w", err)
				}
				fix.SymbolIDs = append(fix.SymbolIDs, sid)
			}
		}

		for i := 1; i < len(fix.SymbolIDs); i++ {
			targetIdx := rng.Intn(i)
			src := fix.SymbolIDs[i]
			if _, err := adapter.WriteEdge(ctx, &model.Edge{
				SourceID:   &src,
				TargetID:   fix.SymbolIDs[targetIdx],
				Kind:       model.EdgeCalls,
				FileID:     fix.FileIDs[i%len(fix.FileIDs)],
				Confidence: 0.8 + rng.Float64()*0.2,
			}); err != nil {
				return fmt.Errorf("write edge: %w", err)
			}
		}

		hubIdx := 0
		for i := 0; i < min(50, len(fix.SymbolIDs)-1); i++ {
			src := fix.SymbolIDs[rng.Intn(len(fix.SymbolIDs)-1)+1]
			if _, err := adapter.WriteEdge(ctx, &model.Edge{
				SourceID:   &src,
				TargetID:   fix.SymbolIDs[hubIdx],
				Kind:       model.EdgeCalls,
				FileID:     fix.FileIDs[0],
				Confidence: 1.0,
			}); err != nil {
				return fmt.Errorf("write hub edge: %w", err)
			}
		}

		for i := 0; i < len(fix.SymbolIDs); i++ {
			vec := make([]float32, 384)
			for j := range vec {
				vec[j] = rng.Float32()*2 - 1
			}
			norm := float32(0)
			for _, v := range vec {
				norm += v * v
			}
			norm = float32(math.Sqrt(float64(norm)))
			for j := range vec {
				vec[j] /= norm
			}

			blob := make([]byte, len(vec)*4)
			for j, v := range vec {
				binary.LittleEndian.PutUint32(blob[j*4:], math.Float32bits(v))
			}
			if err := adapter.WriteEmbedding(ctx, fix.SymbolIDs[i], blob); err != nil {
				return fmt.Errorf("write embedding: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return fix, nil
}
