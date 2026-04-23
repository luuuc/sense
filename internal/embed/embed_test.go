package embed

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testdataDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata")
}

func ortLibPath() string {
	if p := os.Getenv("ORT_LIB_PATH"); p != "" {
		return p
	}
	if runtime.GOOS == "darwin" {
		candidates := []string{
			"/opt/homebrew/lib/libonnxruntime.dylib",
			"/usr/local/lib/libonnxruntime.dylib",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	return "libonnxruntime.so"
}

func setupEmbedder(t *testing.T) *ONNXEmbedder {
	t.Helper()

	dir := testdataDir()
	modelPath := filepath.Join(dir, "model.onnx")
	vocabPath := filepath.Join(dir, "vocab.txt")

	if _, err := os.Stat(modelPath); err != nil {
		t.Skip("model not downloaded; run internal/embed/testdata/download.sh")
	}
	if _, err := os.Stat(vocabPath); err != nil {
		t.Skip("vocab not downloaded; run internal/embed/testdata/download.sh")
	}

	libPath := ortLibPath()
	if _, err := os.Stat(libPath); err != nil && !filepath.IsAbs(libPath) {
		t.Skipf("ONNX Runtime not found at %s; install onnxruntime or set ORT_LIB_PATH", libPath)
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

	emb, err := NewONNXEmbedder(modelBytes, vocabBytes, 0)
	if err != nil {
		t.Fatalf("create embedder: %v", err)
	}
	t.Cleanup(func() { _ = emb.Close() })

	return emb
}

func cosineSimilarity(a, b []float32) float64 {
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func TestEmbedDimensions(t *testing.T) {
	emb := setupEmbedder(t)

	vecs, err := emb.Embed(context.Background(), []EmbedInput{
		{QualifiedName: "Auth::Login#call", Kind: "method", ParentName: "Auth::Login", Snippet: "def call(email, password)"},
	})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 1 {
		t.Fatalf("expected 1 vector, got %d", len(vecs))
	}
	if len(vecs[0]) != Dimensions {
		t.Fatalf("expected %d dimensions, got %d", Dimensions, len(vecs[0]))
	}
}

func TestGoldenPairSimilarity(t *testing.T) {
	emb := setupEmbedder(t)

	inputs := []EmbedInput{
		// 0: authentication login
		{QualifiedName: "Auth::Login#call", Kind: "method", ParentName: "Auth::Login", Snippet: "def call(email, password)"},
		// 1: authentication verify (semantically similar to 0)
		{QualifiedName: "Auth::Verify#execute", Kind: "method", ParentName: "Auth::Verify", Snippet: "def execute(token)"},
		// 2: file parser (semantically different from 0 and 1)
		{QualifiedName: "CSV::Parser#parse", Kind: "method", ParentName: "CSV::Parser", Snippet: "def parse(input_stream, delimiter)"},
		// 3: http request handler (different domain)
		{QualifiedName: "HTTP::Router#dispatch", Kind: "method", ParentName: "HTTP::Router", Snippet: "def dispatch(request, response)"},
		// 4: user authentication (similar to 0)
		{QualifiedName: "Users::Authenticate", Kind: "function", ParentName: "Users", Snippet: "func Authenticate(username string, password string) (*User, error)"},
	}

	vecs, err := emb.Embed(context.Background(), inputs)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	// Auth::Login should be more similar to Auth::Verify and Users::Authenticate
	// than to CSV::Parser or HTTP::Router
	simLoginVerify := cosineSimilarity(vecs[0], vecs[1])
	simLoginCSV := cosineSimilarity(vecs[0], vecs[2])
	simLoginHTTP := cosineSimilarity(vecs[0], vecs[3])
	simLoginAuth := cosineSimilarity(vecs[0], vecs[4])

	t.Logf("Login↔Verify:  %.4f", simLoginVerify)
	t.Logf("Login↔CSV:     %.4f", simLoginCSV)
	t.Logf("Login↔HTTP:    %.4f", simLoginHTTP)
	t.Logf("Login↔Auth:    %.4f", simLoginAuth)

	if simLoginVerify <= simLoginCSV {
		t.Errorf("Login should be more similar to Verify (%.4f) than CSV (%.4f)", simLoginVerify, simLoginCSV)
	}
	if simLoginAuth <= simLoginCSV {
		t.Errorf("Login should be more similar to Authenticate (%.4f) than CSV (%.4f)", simLoginAuth, simLoginCSV)
	}
	if simLoginAuth <= simLoginHTTP {
		t.Errorf("Login should be more similar to Authenticate (%.4f) than HTTP (%.4f)", simLoginAuth, simLoginHTTP)
	}
}

func TestBatchProcessing(t *testing.T) {
	emb := setupEmbedder(t)

	// Generate more inputs than BatchSize to test batching
	inputs := make([]EmbedInput, BatchSize+5)
	for i := range inputs {
		inputs[i] = EmbedInput{
			QualifiedName: "Pkg::Method" + string(rune('A'+i%26)),
			Kind:          "method",
			Snippet:       "def method_body",
		}
	}

	vecs, err := emb.Embed(context.Background(), inputs)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != len(inputs) {
		t.Fatalf("expected %d vectors, got %d", len(inputs), len(vecs))
	}
	for i, v := range vecs {
		if len(v) != Dimensions {
			t.Errorf("vector %d: expected %d dims, got %d", i, Dimensions, len(v))
		}
	}

	// Determinism: re-embedding the same inputs produces identical vectors.
	vecs2, err := emb.Embed(context.Background(), inputs)
	if err != nil {
		t.Fatalf("second embed: %v", err)
	}
	for i := range vecs {
		for j := range vecs[i] {
			if vecs[i][j] != vecs2[i][j] {
				t.Fatalf("non-deterministic: vector %d differs at dim %d (%.6f vs %.6f)", i, j, vecs[i][j], vecs2[i][j])
			}
		}
	}
}

func TestTruncationPreservesSemantics(t *testing.T) {
	emb := setupEmbedder(t)

	short := EmbedInput{
		QualifiedName: "Auth::Login#call",
		Kind:          "method",
		ParentName:    "Auth::Login",
		Snippet:       "def call(email, password)",
	}
	// Long snippet that will be truncated at 128 tokens
	long := EmbedInput{
		QualifiedName: "Auth::Login#call",
		Kind:          "method",
		ParentName:    "Auth::Login",
		Snippet:       "def call(email, password) validate_email(email) user = User.find_by(email: email) return nil unless user return nil unless user.authenticate(password) session = Session.create(user: user, expires_at: 24.hours.from_now) notify_login(user) AuditLog.record(event: :login, user: user) session",
	}
	unrelated := EmbedInput{
		QualifiedName: "CSV::Parser#parse",
		Kind:          "method",
		ParentName:    "CSV::Parser",
		Snippet:       "def parse(input_stream, delimiter)",
	}

	vecs, err := emb.Embed(context.Background(), []EmbedInput{short, long, unrelated})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	// The truncated long version should still be more similar to the
	// short version than to an unrelated symbol.
	simShortLong := cosineSimilarity(vecs[0], vecs[1])
	simShortUnrelated := cosineSimilarity(vecs[0], vecs[2])

	t.Logf("Short↔Long (truncated): %.4f", simShortLong)
	t.Logf("Short↔Unrelated:        %.4f", simShortUnrelated)

	if simShortLong <= simShortUnrelated {
		t.Errorf("truncated version (%.4f) should be more similar than unrelated (%.4f)", simShortLong, simShortUnrelated)
	}
}

func TestTokenizer(t *testing.T) {
	vocabPath := filepath.Join(testdataDir(), "vocab.txt")
	vocabBytes, err := os.ReadFile(vocabPath)
	if err != nil {
		t.Skip("vocab not downloaded")
	}

	tok := newTokenizer(vocabBytes, 128)

	result := tok.Tokenize("hello world")
	if result.InputIDs[0] != tok.clsID {
		t.Errorf("first token should be [CLS] (%d), got %d", tok.clsID, result.InputIDs[0])
	}
	if result.AttentionMask[0] != 1 {
		t.Error("attention mask should be 1 for [CLS]")
	}

	// Find the [SEP] position
	sepFound := false
	for i, id := range result.InputIDs {
		if id == tok.sepID && i > 0 {
			sepFound = true
			break
		}
	}
	if !sepFound {
		t.Error("[SEP] token not found in output")
	}

	// Padding positions should have mask = 0
	lastNonPad := 0
	for i, m := range result.AttentionMask {
		if m == 1 {
			lastNonPad = i
		}
	}
	if lastNonPad >= 127 {
		t.Error("expected padding in short sequence")
	}
	if result.AttentionMask[lastNonPad+1] != 0 {
		t.Error("padding positions should have attention mask = 0")
	}
}

func TestEmbedDeterminism(t *testing.T) {
	dir := testdataDir()
	modelPath := filepath.Join(dir, "model.onnx")
	vocabPath := filepath.Join(dir, "vocab.txt")
	if _, err := os.Stat(modelPath); err != nil {
		t.Skip("model not downloaded; run internal/embed/testdata/download.sh")
	}

	libPath := ortLibPath()
	if _, err := os.Stat(libPath); err != nil && !filepath.IsAbs(libPath) {
		t.Skipf("ONNX Runtime not found at %s", libPath)
	}
	if err := InitORTLibrary(libPath); err != nil {
		t.Fatalf("init ORT: %v", err)
	}

	mb, _ := os.ReadFile(modelPath)
	vb, _ := os.ReadFile(vocabPath)

	inputs := []EmbedInput{
		{QualifiedName: "Auth::Login#call", Kind: "method", ParentName: "Auth::Login", Snippet: "def call(email, password)"},
		{QualifiedName: "CSV::Parser#parse", Kind: "method", ParentName: "CSV::Parser", Snippet: "def parse(stream)"},
		{QualifiedName: "HTTP::Router#dispatch", Kind: "function", Snippet: "func dispatch(req, res)"},
	}

	// 1-worker: all cores
	emb1, err := NewONNXEmbedder(mb, vb, 0)
	if err != nil {
		t.Fatalf("create 1-worker embedder: %v", err)
	}
	vecs1, err := emb1.Embed(context.Background(), inputs)
	_ = emb1.Close()
	if err != nil {
		t.Fatalf("embed (1 worker): %v", err)
	}

	// N-worker: 1 thread per session (simulates parallelEmbed's thread partitioning)
	emb2, err := NewONNXEmbedder(mb, vb, 1)
	if err != nil {
		t.Fatalf("create N-worker embedder: %v", err)
	}
	vecs2, err := emb2.Embed(context.Background(), inputs)
	_ = emb2.Close()
	if err != nil {
		t.Fatalf("embed (N worker): %v", err)
	}

	for i := range vecs1 {
		for j := range vecs1[i] {
			if vecs1[i][j] != vecs2[i][j] {
				t.Fatalf("determinism violation: vec[%d][%d] = %v (all-cores) vs %v (1-thread)",
					i, j, vecs1[i][j], vecs2[i][j])
			}
		}
	}
}

func BenchmarkEmbed(b *testing.B) {
	dir := testdataDir()
	modelPath := filepath.Join(dir, "model.onnx")
	vocabPath := filepath.Join(dir, "vocab.txt")
	if _, err := os.Stat(modelPath); err != nil {
		b.Skip("model not downloaded; run internal/embed/testdata/download.sh")
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
	emb, err := NewONNXEmbedder(mb, vb, 0)
	if err != nil {
		b.Fatalf("create embedder: %v", err)
	}
	defer func() { _ = emb.Close() }()

	inputs := make([]EmbedInput, BatchSize)
	for i := range inputs {
		inputs[i] = EmbedInput{
			QualifiedName: "Pkg::Method",
			Kind:          "method",
			Snippet:       "def method_body(arg1, arg2)",
		}
	}

	b.ResetTimer()
	for range b.N {
		if _, err := emb.Embed(context.Background(), inputs); err != nil {
			b.Fatalf("embed: %v", err)
		}
	}
}

func TestFormatInput(t *testing.T) {
	tests := []struct {
		in   EmbedInput
		want string
	}{
		{
			in:   EmbedInput{QualifiedName: "Foo#bar", Kind: "method", ParentName: "Foo", Snippet: "def bar"},
			want: "method Foo#bar parent: Foo def bar",
		},
		{
			in:   EmbedInput{QualifiedName: "main", Kind: "function", Snippet: "func main()"},
			want: "function main func main()",
		},
		{
			in:   EmbedInput{QualifiedName: "Config", Kind: "class"},
			want: "class Config",
		},
	}
	for _, tt := range tests {
		got := FormatInput(tt.in)
		if got != tt.want {
			t.Errorf("FormatInput(%+v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
