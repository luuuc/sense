package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
)

// panicTreeExtractor is a tree-sitter-backed extractor (not a RawExtractor)
// whose Extract panics. It carries a real Go grammar and a unique extension so
// a real parse routes a `.gopanic` file through the parser path, parses it
// successfully, then into safeExtract — the branch a RawExtractor never
// exercises. It proves parseFileCore turns a tree-sitter extractor panic into a
// per-file parse-failed warning and a nil result.
type panicTreeExtractor struct{}

func (panicTreeExtractor) Extract(*sitter.Tree, []byte, string, extract.Emitter) error {
	panic("extract boom")
}
func (panicTreeExtractor) Grammar() *sitter.Language { return grammars.Go() }
func (panicTreeExtractor) Language() string          { return "gopanic" }
func (panicTreeExtractor) Extensions() []string      { return []string{".gopanic"} }
func (panicTreeExtractor) Tier() extract.Tier        { return extract.TierBasic }

func init() { extract.Register(panicTreeExtractor{}) }

// TestParseFileCoreSafeExtractRecoversPanic covers the safeExtract error path
// inside parseFileCore (parse.go:91-94): a non-raw extractor that panics during
// Extract yields a parse-failed warning and a nil result, not a crash. The file
// parses fine (real Go grammar) so control reaches safeExtract, whose recover()
// converts the panic; parseFileCore then warns and returns nil. This is the
// tree-sitter twin of the RawExtractor panic test, which only reaches
// safeExtractRaw.
func TestParseFileCoreSafeExtractRecoversPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "boom.gopanic")
	if err := os.WriteFile(path, []byte("package p\n\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var warnedKind warningKind
	var warned bool
	po := parseOpts{
		ctx:           context.Background(),
		maxFileSizeKB: 0,
		warnf: func(kind warningKind, _ string, _ ...any) {
			warnedKind = kind
			warned = true
		},
		parserFor: func(ex extract.Extractor) (*sitter.Parser, bool) {
			p := sitter.NewParser()
			if err := p.SetLanguage(ex.Grammar()); err != nil {
				p.Close()
				t.Fatalf("set language: %v", err)
			}
			return p, true // parseFileCore closes it
		},
	}

	fr := parseFileCore(po, path, "boom.gopanic", func(string) bool { return false })
	if fr != nil {
		t.Errorf("expected nil result when the extractor panics, got %+v", fr)
	}
	if !warned {
		t.Fatal("expected a warning from the recovered extractor panic")
	}
	if warnedKind != warnParseFailed {
		t.Errorf("warn kind = %v, want %v", warnedKind, warnParseFailed)
	}
}

// TestParseFileCoreNilParseTree covers the nil-parse-tree guard
// (parse.go:81-84): when the parser yields no tree, parseFileCore warns with a
// parse-failed kind and returns nil rather than dereferencing the tree. A parser
// returned with no language bound produces a nil tree, which is exactly the
// degenerate state the guard defends against.
func TestParseFileCoreNilParseTree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.gopanic")
	if err := os.WriteFile(path, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var warnedKind warningKind
	var warned bool
	po := parseOpts{
		ctx:           context.Background(),
		maxFileSizeKB: 0,
		warnf: func(kind warningKind, _ string, _ ...any) {
			warnedKind = kind
			warned = true
		},
		parserFor: func(extract.Extractor) (*sitter.Parser, bool) {
			// A parser with no language bound returns nil from Parse.
			return sitter.NewParser(), true
		},
	}

	fr := parseFileCore(po, path, "x.gopanic", func(string) bool { return false })
	if fr != nil {
		t.Errorf("expected nil result for a nil parse tree, got %+v", fr)
	}
	if !warned || warnedKind != warnParseFailed {
		t.Errorf("expected a parse-failed warning, warned=%v kind=%v", warned, warnedKind)
	}
}
