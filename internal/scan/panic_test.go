package scan

import (
	"strings"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

type panicExtractor struct{}

func (p panicExtractor) Extract(_ *sitter.Tree, _ []byte, _ string, _ extract.Emitter) error {
	panic("intentional panic")
}
func (p panicExtractor) Grammar() *sitter.Language { return nil }
func (p panicExtractor) Language() string          { return "panic" }
func (p panicExtractor) Extensions() []string      { return []string{".panic"} }
func (p panicExtractor) Tier() extract.Tier        { return extract.TierBasic }

type panicRawExtractor struct{}

func (p panicRawExtractor) ExtractRaw(_ []byte, _ string, _ extract.Emitter) error {
	panic("intentional panic")
}
func (p panicRawExtractor) Language() string     { return "panic" }
func (p panicRawExtractor) Extensions() []string { return []string{".panic"} }
func (p panicRawExtractor) Tier() extract.Tier   { return extract.TierBasic }

func TestSafeExtractPanic(t *testing.T) {
	c := &collector{}
	err := safeExtract(panicExtractor{}, nil, []byte("test"), "test.go", c)
	if err == nil {
		t.Fatal("expected error from panicking extractor")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("expected 'panicked' in error, got: %v", err)
	}
}

func TestSafeExtractRawPanic(t *testing.T) {
	c := &collector{}
	err := safeExtractRaw(panicRawExtractor{}, []byte("test"), "test.go", c)
	if err == nil {
		t.Fatal("expected error from panicking raw extractor")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("expected 'panicked' in error, got: %v", err)
	}
}
