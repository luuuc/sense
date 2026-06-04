package scan

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestCloseParsersClosesEach exercises the loop body of closeParsers
// (scan.go:540-542), which other tests never hit because RunIncremental
// is the only call site that populates h.parsers and the standard scan
// path through walkTree uses parseFileStandalone instead.
func TestCloseParsersClosesEach(_ *testing.T) {
	h := &harness{parsers: map[string]*sitter.Parser{}}
	for i := 0; i < 3; i++ {
		h.parsers[string(rune('a'+i))] = sitter.NewParser()
	}
	h.closeParsers()
}

// TestParseFileCoreReadError covers the os.ReadFile error path
// (scan.go:805-808) without depending on filesystem permission tricks.
func TestParseFileCoreReadError(t *testing.T) {
	var got string
	po := parseOpts{
		ctx:           context.Background(),
		maxFileSizeKB: 0,
		warnf: func(_ warningKind, format string, _ ...any) {
			got = format
		},
		parserFor: func(extract.Extractor) (*sitter.Parser, bool) { return nil, false },
	}
	fr := parseFileCore(po, "/definitely/nonexistent/path.go", "path.go", func(string) bool { return false })
	if fr != nil {
		t.Errorf("expected nil result on read error, got %+v", fr)
	}
	if !strings.Contains(got, "%s") {
		t.Errorf("expected warn format with path placeholder, got %q", got)
	}
}

// TestParseFileCoreFileTooLarge covers the size-cap warning
// (scan.go:795-797). The test writes a 2KB file then runs with a 1KB cap.
func TestParseFileCoreFileTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.go")
	if err := os.WriteFile(path, []byte(strings.Repeat("// x\n", 600)), 0o644); err != nil {
		t.Fatal(err)
	}

	var warnedKind warningKind
	po := parseOpts{
		ctx:           context.Background(),
		maxFileSizeKB: 1,
		warnf: func(kind warningKind, _ string, _ ...any) {
			warnedKind = kind
		},
		parserFor: func(extract.Extractor) (*sitter.Parser, bool) { return nil, false },
	}
	fr := parseFileCore(po, path, "big.go", func(string) bool { return false })
	if fr != nil {
		t.Errorf("expected nil result for over-cap file, got %+v", fr)
	}
	if warnedKind != warnFileTooLarge {
		t.Errorf("warn kind = %v, want %v", warnedKind, warnFileTooLarge)
	}
}

// TestParseFileCoreStatError covers the os.Stat error path
// (scan.go:790-793) — non-existent file with size cap enabled.
func TestParseFileCoreStatError(t *testing.T) {
	var warnedKind warningKind
	po := parseOpts{
		ctx:           context.Background(),
		maxFileSizeKB: 1,
		warnf: func(kind warningKind, _ string, _ ...any) {
			warnedKind = kind
		},
		parserFor: func(extract.Extractor) (*sitter.Parser, bool) { return nil, false },
	}
	fr := parseFileCore(po, "/definitely/missing/file.go", "file.go", func(string) bool { return false })
	if fr != nil {
		t.Errorf("expected nil for stat error, got %+v", fr)
	}
	if warnedKind != warnMetaError {
		t.Errorf("warn kind = %v, want %v", warnedKind, warnMetaError)
	}
}

// TestParseFileCoreContextCancelled covers the ctx.Err() short-circuit
// (scan.go:801-803).
func TestParseFileCoreContextCancelled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	if err := os.WriteFile(path, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	po := parseOpts{
		ctx:           ctx,
		maxFileSizeKB: 0,
		warnf:         func(warningKind, string, ...any) {},
		parserFor:     func(extract.Extractor) (*sitter.Parser, bool) { return nil, false },
	}
	fr := parseFileCore(po, path, "x.go", func(string) bool { return false })
	if fr != nil {
		t.Errorf("expected nil under cancelled ctx, got %+v", fr)
	}
}

// TestParseFileCoreUnknownExtension covers the early return when no
// extractor is registered for the file's extension (scan.go:785-787).
// Catches the simple ext == "" / unknown ext path without filesystem I/O.
func TestParseFileCoreUnknownExtension(t *testing.T) {
	po := parseOpts{
		ctx:       context.Background(),
		warnf:     func(warningKind, string, ...any) {},
		parserFor: func(extract.Extractor) (*sitter.Parser, bool) { return nil, false },
	}
	fr := parseFileCore(po, "/tmp/notes.unknownext", "notes.unknownext", func(string) bool { return false })
	if fr != nil {
		t.Errorf("expected nil for unsupported extension, got %+v", fr)
	}
}

// TestParseFileCoreParserSetupFailure covers the parserFor returning nil
// (scan.go:824-827) — the standalone variant emits a warning and returns
// nil when SetLanguage fails on the per-call parser.
func TestParseFileCoreParserSetupFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	if err := os.WriteFile(path, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	po := parseOpts{
		ctx:       context.Background(),
		warnf:     func(warningKind, string, ...any) {},
		parserFor: func(extract.Extractor) (*sitter.Parser, bool) { return nil, false },
	}
	fr := parseFileCore(po, path, "x.go", func(string) bool { return false })
	if fr != nil {
		t.Errorf("expected nil when parserFor returns nil, got %+v", fr)
	}
}

// TestMigrateEmbeddingModelReadMetaError covers the ReadMeta error path
// in migrateEmbeddingModel (embed.go:137-139) via a closed adapter.
func TestMigrateEmbeddingModelReadMetaError(t *testing.T) {
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = adapter.Close()

	h := &harness{ctx: ctx, idx: adapter, out: io.Discard, warn: io.Discard}
	if _, err := h.migrateEmbeddingModel(); err == nil {
		t.Fatal("expected error from migrateEmbeddingModel on closed adapter")
	}
}

// TestEmbedSymbolsEmptyChangedFiles covers the early return when no
// files changed in this scan (embed.go:253-256).
func TestEmbedSymbolsEmptyChangedFiles(t *testing.T) {
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	h := &harness{ctx: ctx, idx: adapter, out: io.Discard, warn: io.Discard}
	if err := h.embedSymbols(); err != nil {
		t.Fatalf("embedSymbols with no changed files: %v", err)
	}
}

// TestEmbedPendingNoSymbols covers the early-return when an index has
// no symbols missing embeddings (embed.go:163-165).
func TestEmbedPendingNoSymbols(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	adapter, err := sqlite.Open(ctx, filepath.Join(tmp, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	n, err := EmbedPending(ctx, adapter, tmp)
	if err != nil {
		t.Fatalf("EmbedPending: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 embedded, got %d", n)
	}
}

// TestEmbedPendingQueryError covers the SymbolsWithoutEmbeddings error
// path (embed.go:160-162) via a closed adapter.
func TestEmbedPendingQueryError(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	adapter, err := sqlite.Open(ctx, filepath.Join(tmp, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = adapter.Close()

	if _, err := EmbedPending(ctx, adapter, tmp); err == nil {
		t.Fatal("expected error from EmbedPending on closed adapter")
	}
}

// TestParseFileCoreHashSkip covers the skip-by-unchanged-hash early
// return (scan.go:812-814).
func TestParseFileCoreHashSkip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	if err := os.WriteFile(path, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	po := parseOpts{
		ctx:       context.Background(),
		warnf:     func(warningKind, string, ...any) {},
		parserFor: func(extract.Extractor) (*sitter.Parser, bool) { return nil, false },
	}
	fr := parseFileCore(po, path, "x.go", func(string) bool { return true })
	if fr != nil {
		t.Errorf("expected nil when skip returns true, got %+v", fr)
	}
}

func TestAddWarning(t *testing.T) {
	wc := newWarningCollector()
	p := &progress{
		out:     &bytes.Buffer{},
		enabled: false,
		done:    make(chan struct{}),
	}
	p.phase.Store("")

	h := &harness{
		collector: wc,
		progress:  p,
	}

	h.addWarning(warnParseFailed, "test.go (%s)", "broken syntax")

	if wc.count() != 1 {
		t.Errorf("warning count = %d, want 1", wc.count())
	}
	if p.warnings.Load() != 1 {
		t.Errorf("progress warnings = %d, want 1", p.warnings.Load())
	}
}
