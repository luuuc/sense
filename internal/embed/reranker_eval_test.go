package embed

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupRerankerCandidate(t *testing.T, name string) *ONNXReranker {
	t.Helper()

	dir := testdataDir()
	modelPath := filepath.Join(dir, "rerankers", name, "model.onnx")
	vocabPath := filepath.Join(dir, "vocab.txt")

	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("model not downloaded; run internal/embed/testdata/rerankers/download.sh")
	}
	if _, err := os.Stat(vocabPath); err != nil {
		t.Skip("vocab not downloaded")
	}

	libPath := ortLibPath()
	if _, err := os.Stat(libPath); err != nil && !filepath.IsAbs(libPath) {
		t.Skipf("ONNX Runtime not found at %s", libPath)
	}
	if err := InitORTLibrary(libPath); err != nil {
		t.Fatalf("init ORT: %v", err)
	}

	mb, err := os.ReadFile(modelPath)
	if err != nil {
		t.Fatalf("read model: %v", err)
	}
	vb, err := os.ReadFile(vocabPath)
	if err != nil {
		t.Fatalf("read vocab: %v", err)
	}

	r, err := NewONNXReranker(mb, vb, 0)
	if err != nil {
		t.Fatalf("create reranker %s: %v", name, err)
	}
	t.Cleanup(func() { _ = r.Close() })

	return r
}

func TestCrossEncoderEvaluation(t *testing.T) {
	candidates := []string{"ms-marco-MiniLM-L-6-v2", "ms-marco-MiniLM-L-12-v2"}

	for _, name := range candidates {
		t.Run(name, func(t *testing.T) {
			r := setupRerankerCandidate(t, name)

			// Warmup
			_, _ = r.Score("warmup query", []string{"warmup document text"})

			t.Run("latency_sequential", func(t *testing.T) {
				docs := make([]string, 50)
				for i := range docs {
					docs[i] = fmt.Sprintf("method Auth::Login#call\ndef call(email, password)\n  validate_credentials(email, password)\nend variant_%d", i)
				}
				start := time.Now()
				for _, doc := range docs {
					_, err := r.Score("user authentication and password validation", []string{doc})
					if err != nil {
						t.Fatalf("score: %v", err)
					}
				}
				elapsed := time.Since(start)
				msPerPair := float64(elapsed.Microseconds()) / 50000.0
				t.Logf("50 sequential: %v total, %.2f ms/pair", elapsed, msPerPair)
				if elapsed > 5*time.Second {
					t.Errorf("latency %v exceeds 5s budget", elapsed)
				}
			})

			t.Run("latency_batch", func(t *testing.T) {
				docs := make([]string, 50)
				for i := range docs {
					docs[i] = fmt.Sprintf("method Auth::Login#call\ndef call(email, password)\n  validate_credentials(email, password)\nend variant_%d", i)
				}
				start := time.Now()
				_, err := r.Score("user authentication and password validation", docs)
				if err != nil {
					t.Fatalf("score: %v", err)
				}
				elapsed := time.Since(start)
				t.Logf("50 batched: %v", elapsed)
			})

			t.Run("quality", func(t *testing.T) {
				var totalSep float64
				wins := 0
				for _, tc := range separationCases {
					targetScores, err := r.Score(tc.query, []string{tc.targetRich})
					if err != nil {
						t.Fatalf("score target: %v", err)
					}
					noiseScores, err := r.Score(tc.query, []string{tc.noiseRich})
					if err != nil {
						t.Fatalf("score noise: %v", err)
					}
					sep := float64(targetScores[0] - noiseScores[0])
					totalSep += sep

					pass := "PASS"
					if sep <= 0 {
						pass = "FAIL"
					} else {
						wins++
					}

					t.Logf("%-50s target=%8.4f noise=%8.4f sep=%8.4f %s",
						tc.name, targetScores[0], noiseScores[0], sep, pass)
				}
				n := len(separationCases)
				t.Logf("average separation: %.4f  wins: %d/%d", totalSep/float64(n), wins, n)

				if wins < n/2 {
					t.Errorf("cross-encoder won only %d/%d separation cases", wins, n)
				}
			})
		})
	}
}

func TestTokenizePair(t *testing.T) {
	vocabPath := filepath.Join(testdataDir(), "vocab.txt")
	vocabBytes, err := os.ReadFile(vocabPath)
	if err != nil {
		t.Skip("vocab not downloaded")
	}

	tok := newTokenizer(vocabBytes, MaxSequenceLength)
	result := tok.TokenizePair("hello world", "this is a test document")

	if result.InputIDs[0] != tok.clsID {
		t.Errorf("first token should be [CLS] (%d), got %d", tok.clsID, result.InputIDs[0])
	}

	var sepPositions []int
	for i, id := range result.InputIDs {
		if id == tok.sepID && i > 0 {
			sepPositions = append(sepPositions, i)
		}
	}
	if len(sepPositions) < 2 {
		t.Fatalf("expected 2 [SEP] tokens, found %d", len(sepPositions))
	}

	for i := 0; i <= sepPositions[0]; i++ {
		if result.TokenTypeIDs[i] != 0 {
			t.Errorf("position %d: token_type_id = %d, want 0 (query segment)", i, result.TokenTypeIDs[i])
		}
	}

	for i := sepPositions[0] + 1; i <= sepPositions[1]; i++ {
		if result.TokenTypeIDs[i] != 1 {
			t.Errorf("position %d: token_type_id = %d, want 1 (doc segment)", i, result.TokenTypeIDs[i])
		}
	}

	lastNonPad := 0
	for i, m := range result.AttentionMask {
		if m == 1 {
			lastNonPad = i
		}
	}
	if lastNonPad < sepPositions[1] {
		t.Error("attention mask ends before second [SEP]")
	}
	if lastNonPad < MaxSequenceLength-1 && result.AttentionMask[lastNonPad+1] != 0 {
		t.Error("padding positions should have attention mask = 0")
	}
}

func TestTokenizePairTruncation(t *testing.T) {
	vocabPath := filepath.Join(testdataDir(), "vocab.txt")
	vocabBytes, err := os.ReadFile(vocabPath)
	if err != nil {
		t.Skip("vocab not downloaded")
	}

	// Use a very short max length to force truncation.
	tok := newTokenizer(vocabBytes, 16)
	result := tok.TokenizePair(
		"this is a fairly long query that should get truncated",
		"this is an even longer document that should definitely get truncated at the boundary",
	)

	// Count non-padding tokens.
	nonPad := 0
	for _, m := range result.AttentionMask {
		if m == 1 {
			nonPad++
		}
	}
	if nonPad > 16 {
		t.Errorf("non-padding tokens %d exceeds max length 16", nonPad)
	}

	// Must have [CLS] and two [SEP] tokens.
	if result.InputIDs[0] != tok.clsID {
		t.Error("missing [CLS]")
	}
	sepCount := 0
	for i, id := range result.InputIDs {
		if id == tok.sepID && i > 0 {
			sepCount++
		}
	}
	if sepCount < 2 {
		t.Errorf("expected 2 [SEP] tokens, found %d", sepCount)
	}
}
