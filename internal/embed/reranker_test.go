package embed

import (
	"os"
	"path/filepath"
	"testing"
)

func setupReranker(t *testing.T) *ONNXReranker {
	t.Helper()

	dir := testdataDir()
	modelPath := filepath.Join(dir, "rerankers", "ms-marco-MiniLM-L-6-v2", "model.onnx")
	vocabPath := filepath.Join(dir, "vocab.txt")

	if _, err := os.Stat(modelPath); err != nil {
		t.Skip("reranker model not downloaded; run internal/embed/testdata/rerankers/download.sh")
	}
	if _, err := os.Stat(vocabPath); err != nil {
		t.Skip("vocab not downloaded; run internal/embed/testdata/download.sh")
	}

	libPath := ortLibPath()
	if _, err := os.Stat(libPath); err != nil && !filepath.IsAbs(libPath) {
		t.Skipf("ONNX Runtime not found at %s", libPath)
	}
	if err := InitORTLibrary(libPath); err != nil {
		t.Fatalf("init ORT: %v", err)
	}

	modelBytes, err := os.ReadFile(modelPath)
	if err != nil {
		t.Fatalf("read model: %v", err)
	}
	vocabBytes, err := os.ReadFile(vocabPath)
	if err != nil {
		t.Fatalf("read vocab: %v", err)
	}

	r, err := NewONNXReranker(modelBytes, vocabBytes, 0)
	if err != nil {
		t.Fatalf("create reranker: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	return r
}

func TestRerankerScore(t *testing.T) {
	r := setupReranker(t)

	scores, err := r.Score("user authentication", []string{
		"method Auth::Login#call\ndef call(email, password)",
		"method CSV::Parser#parse\ndef parse(input_stream)",
	})
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}

	t.Logf("auth query → auth doc: %.4f", scores[0])
	t.Logf("auth query → csv doc:  %.4f", scores[1])
}

func TestRerankerOrdering(t *testing.T) {
	r := setupReranker(t)

	scores, err := r.Score("user authentication and password validation", []string{
		"method Users::AuthenticateService#execute\ndef execute\n  user = User.find_by(email: params[:login])\n  return error unless user&.valid_password?(params[:password])",
		"method Users::UpdateService#execute\ndef execute\n  user.name = params[:name]\n  user.email = params[:email]\n  user.save",
		"method CSV::Parser#parse\ndef parse(input_stream, delimiter)",
	})
	if err != nil {
		t.Fatalf("score: %v", err)
	}

	if scores[0] <= scores[1] {
		t.Errorf("auth doc (%.4f) should score higher than update doc (%.4f)", scores[0], scores[1])
	}
	if scores[0] <= scores[2] {
		t.Errorf("auth doc (%.4f) should score higher than csv doc (%.4f)", scores[0], scores[2])
	}

	t.Logf("auth:   %.4f", scores[0])
	t.Logf("update: %.4f", scores[1])
	t.Logf("csv:    %.4f", scores[2])
}

func TestRerankerEmptyDocs(t *testing.T) {
	r := &ONNXReranker{}

	scores, err := r.Score("anything", nil)
	if err != nil {
		t.Fatalf("score nil: %v", err)
	}
	if scores != nil {
		t.Errorf("expected nil scores for nil docs, got %v", scores)
	}

	scores, err = r.Score("anything", []string{})
	if err != nil {
		t.Fatalf("score empty: %v", err)
	}
	if scores != nil {
		t.Errorf("expected nil scores for empty docs, got %v", scores)
	}
}

func TestRerankerBatching(t *testing.T) {
	r := setupReranker(t)

	// More docs than RerankerBatchSize to exercise multi-batch path.
	n := RerankerBatchSize + 3
	docs := make([]string, n)
	for i := range docs {
		docs[i] = "method Foo#bar\ndef bar(x)"
	}

	scores, err := r.Score("test query", docs)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if len(scores) != n {
		t.Fatalf("expected %d scores, got %d", n, len(scores))
	}
}

func TestRerankerDeterminism(t *testing.T) {
	r := setupReranker(t)

	docs := []string{
		"method Auth::Login#call\ndef call(email, password)",
		"method HTTP::Router#dispatch\ndef dispatch(req, res)",
	}

	scores1, err := r.Score("authentication", docs)
	if err != nil {
		t.Fatalf("first score: %v", err)
	}

	scores2, err := r.Score("authentication", docs)
	if err != nil {
		t.Fatalf("second score: %v", err)
	}

	for i := range scores1 {
		if scores1[i] != scores2[i] {
			t.Fatalf("non-deterministic: score[%d] = %.6f then %.6f", i, scores1[i], scores2[i])
		}
	}
}

func BenchmarkRerankerScore(b *testing.B) {
	dir := testdataDir()
	modelPath := filepath.Join(dir, "rerankers", "ms-marco-MiniLM-L-6-v2", "model.onnx")
	vocabPath := filepath.Join(dir, "vocab.txt")

	if _, err := os.Stat(modelPath); err != nil {
		b.Skip("reranker model not downloaded")
	}

	libPath := ortLibPath()
	if _, err := os.Stat(libPath); err != nil && !filepath.IsAbs(libPath) {
		b.Skipf("ONNX Runtime not found at %s", libPath)
	}
	if err := InitORTLibrary(libPath); err != nil {
		b.Fatalf("init ORT: %v", err)
	}

	mb, _ := os.ReadFile(modelPath)
	vb, _ := os.ReadFile(vocabPath)
	r, err := NewONNXReranker(mb, vb, 0)
	if err != nil {
		b.Fatalf("create reranker: %v", err)
	}
	defer func() { _ = r.Close() }()

	docs := make([]string, RerankerBatchSize)
	for i := range docs {
		docs[i] = "method Pkg::Method#call\ndef call(arg1, arg2)\n  process(arg1)\n  validate(arg2)"
	}

	b.ResetTimer()
	for range b.N {
		if _, err := r.Score("user authentication", docs); err != nil {
			b.Fatalf("score: %v", err)
		}
	}
}

// TestONNXRerankerCloseNilSession pins the defensive Close() path for
// a reranker that never finished construction (session unset). Without
// this guard a stale Close call would panic on r.session.Destroy.
func TestONNXRerankerCloseNilSession(t *testing.T) {
	r := &ONNXReranker{}
	if err := r.Close(); err != nil {
		t.Errorf("Close on nil-session reranker: %v", err)
	}
}
