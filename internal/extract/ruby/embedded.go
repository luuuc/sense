package ruby

import (
	"bytes"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
)

// ExtractEmbeddedCalls parses a Ruby source fragment and emits the calls edges
// its expressions imply, each sourced from scopeQualified and shifted by
// lineOffset so the edge's line number points at the original (e.g. ERB) line
// rather than a line inside the synthetic fragment.
//
// It is the cross-package entry point the ERB extractor uses to understand the
// embedded Ruby inside `<% %>` / `<%= %>` tags with the language's own grammar
// instead of a receiver-stripping regex: receiverless calls resolve to
// `self.<name>`, constant receivers to `Const.<name>`, and method chains and
// blocks resolve by the same logic the whole-file walker uses
// (`@cart.items.each { |i| i.listing.title }` emits the chain's calls, not a
// single bare token).
//
// Parsing is tolerant by design. An embedded snippet is often not standalone-
// valid Ruby — a `<% if x %> … <% end %>` conditional is split across tags, so
// a single tag's content is a dangling `if x` or a dangling `end`. tree-sitter
// still produces a best-effort tree with ERROR nodes; emission proceeds over
// whatever resolved and a parse error never aborts the caller's file scan.
//
// No cross-tag local/instance-variable type map is available, so a chained
// receiver whose type can't be inferred within the fragment emits the trailing
// method name at unresolved confidence for the resolver's name fallback — the
// same policy the whole-file walker applies to an unknown receiver.
//
// lineOffset is added to each emitted edge's (1-indexed) fragment line. For a
// tag whose content lives on ERB line N, pass N-1 so a fragment-line-1 call
// maps back to line N.
func ExtractEmbeddedCalls(src []byte, lineOffset int, scopeQualified string, emit extract.Emitter) error {
	// Empty / whitespace-only tags (`<% %>`, comment bodies the caller already
	// stripped) carry no Ruby — skip the parse entirely. Templates have many
	// of these, so the short-circuit is a real cost saving, not just a guard.
	if len(bytes.TrimSpace(src)) == 0 {
		return nil
	}

	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(grammars.Ruby()); err != nil {
		return err
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		// A nil tree (not merely a tree with ERROR nodes) means the parser
		// could not produce anything usable. Skip rather than fail — a single
		// unparseable tag must never abort the surrounding template scan.
		return nil
	}
	defer tree.Close()

	root := tree.RootNode()
	w := &walker{
		source:            src,
		emit:              offsetEmitter{inner: emit, offset: lineOffset},
		filePath:          scopeQualified,
		classInstanceVars: make(map[string]map[string]string),
		returnTypes:       map[string]string{},
		emittedCallbacks:  make(map[string]bool),
		emittedSynthetics: make(map[string]bool),
		pkgBindings:       make(map[string]string),
	}

	localTypes := buildLocalTypeMap(root, src)

	// Call nodes: emit each, but skip those nested inside a block — emitCall
	// walks block bodies itself, so a top-level walk that also descended into
	// blocks would double-emit the block's inner calls.
	if err := extract.WalkNamedDescendants(root, "call", func(c *sitter.Node) error {
		if isInsideBlock(c) {
			return nil
		}
		return w.emitCall(c, scopeQualified, nil, localTypes, nil)
	}); err != nil {
		return err
	}

	// Bare receiverless calls (`current_user`, `render_widget`) parse as
	// identifier nodes, not call nodes; emitBareIdentifierCalls picks them up.
	return w.emitBareIdentifierCalls(root, scopeQualified, extract.ConfidenceDynamic, nil)
}

// offsetEmitter wraps an extract.Emitter, shifting every emitted edge's line
// (and every emitted symbol's line span) by a fixed offset. It lets the
// fragment walker run with the fragment's own 1-indexed line numbers while the
// edges it produces report the original file's lines.
type offsetEmitter struct {
	inner  extract.Emitter
	offset int
}

func (o offsetEmitter) Symbol(s extract.EmittedSymbol) error {
	s.LineStart += o.offset
	s.LineEnd += o.offset
	return o.inner.Symbol(s)
}

func (o offsetEmitter) Edge(e extract.EmittedEdge) error {
	if e.Line != nil {
		shifted := *e.Line + o.offset
		e.Line = &shifted
	}
	return o.inner.Edge(e)
}
