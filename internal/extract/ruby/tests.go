package ruby

import (
	"fmt"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// rspecDSLMethods is the set of RSpec DSL method names that create
// test scopes when called with a block.
var rspecDSLMethods = map[string]bool{
	"it": true, "describe": true, "context": true,
	"before": true, "after": true, "around": true,
	"let": true, "expect": true,
}

// rspecMatcherMethods is the set of RSpec matcher chain methods that
// should not be emitted as calls edges — they are DSL sugar, not
// application method invocations.
var rspecMatcherMethods = map[string]bool{
	"to": true, "not_to": true, "to_not": true,
	"eq": true, "be": true, "be_nil": true, "be_empty": true,
	"be_valid": true, "be_present": true, "be_a": true,
	"raise_error": true, "change": true, "receive": true,
	"have_received": true, "match": true, "include": true,
	"contain_exactly": true, "start_with": true, "end_with": true,
}

// handleTestBlock processes an RSpec DSL call that has a block body.
// It builds a synthetic scope name, walks the block for calls/identifiers,
// and recurses into nested test blocks.  Returns true to signal the node
// was consumed.
func (w *walker) handleTestBlock(n *sitter.Node, scope []string, methodName string) (bool, error) {
	// Emit the conventional tests edge for top-level describe with a
	// constant argument (e.g. `describe Order do … end`).
	if methodName == "describe" && len(scope) == 0 {
		if err := w.emitDescribeEdge(n); err != nil {
			return true, err
		}
	}

	block := getBlockChild(n)
	if block == nil {
		// No block — but the arguments may contain calls we still want to
		// capture (e.g. `expect(TopicCreator.create(...))`).
		synthetic := w.buildSyntheticSource(scope)
		return true, extract.WalkNamedDescendants(n, "call", func(c *sitter.Node) error {
			if w.isInsideNestedTestBlock(c, n) {
				return nil
			}
			methodNode := c.ChildByFieldName("method")
			if methodNode != nil && rspecDSLMethods[extract.Text(methodNode, w.source)] {
				return nil
			}
			return w.emitTestCall(c, synthetic, scope)
		})
	}

	segment := w.buildTestScopeSegment(n, methodName)
	if segment == "" {
		// Unnamed / unresolvable block — fall back to file-level scope.
		return true, w.walkTestBlockWithFallback(block, scope)
	}

	// Push segment and cap depth at 3.
	w.testScope = append(w.testScope, segment)
	if len(w.testScope) > 3 {
		w.testScope = w.testScope[:len(w.testScope)-1]
		return true, w.walkTestBlockWithFallback(block, scope)
	}
	defer func() {
		w.testScope = w.testScope[:len(w.testScope)-1]
	}()

	synthetic := w.buildSyntheticSource(scope)
	body := getBlockBody(block)
	if body == nil {
		return true, nil
	}
	return true, w.walkTestBody(body, scope, synthetic)
}

// buildTestScopeSegment extracts a scope segment from a test DSL call.
// Returns "" when the block should fall back to file-level scope.
func (w *walker) buildTestScopeSegment(n *sitter.Node, methodName string) string {
	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return ""
	}
	first := args.NamedChild(0)
	if first == nil {
		return ""
	}

	switch methodName {
	case "describe", "context":
		return w.describeScopeSegment(first, methodName)
	case "it":
		return w.itScopeSegment(first)
	default:
		// before / after / around / let / expect rarely carry a descriptive
		// string arg; fall back to file-level scope.
		return ""
	}
}

// describeScopeSegment derives a scope segment from a describe/context
// argument: a constant or scope-resolution becomes the class name; a plain
// string becomes a method ("#m"/".m") or sanitized descriptive segment.
func (w *walker) describeScopeSegment(first *sitter.Node, methodName string) string {
	switch first.Kind() {
	case "constant", "scope_resolution":
		return extract.Text(first, w.source)
	case "string":
		if hasInterpolation(first) {
			return ""
		}
		desc := extractStringValue(first, w.source)
		if desc == "" {
			return ""
		}
		if strings.HasPrefix(desc, "#") || strings.HasPrefix(desc, ".") {
			return desc
		}
		return methodName + "_" + sanitizeDesc(desc)
	default:
		return ""
	}
}

// itScopeSegment derives a "#it_…" scope segment from an `it` example's
// descriptive string argument.
func (w *walker) itScopeSegment(first *sitter.Node) string {
	if first.Kind() != "string" {
		return ""
	}
	if hasInterpolation(first) {
		return ""
	}
	desc := extractStringValue(first, w.source)
	if desc == "" {
		return ""
	}
	return "#it_" + sanitizeDesc(desc)
}

// buildSyntheticSource joins the class/module scope with the test-scope
// stack into a single synthetic qualified name.
func (w *walker) buildSyntheticSource(scope []string) string {
	classScope := strings.Join(scope, "::")
	testScope := strings.Join(w.testScope, "#")

	if classScope == "" && testScope == "" {
		return ""
	}
	if classScope == "" {
		return testScope
	}
	if testScope == "" {
		return classScope
	}
	return classScope + "#" + testScope
}

// walkTestBody walks a test block body emitting calls edges with the
// given synthetic source.  It also recurses into nested test blocks.
// A single tree walk handles both emission and recursion.
func (w *walker) walkTestBody(body *sitter.Node, scope []string, source string) error {
	if err := extract.WalkNamedDescendants(body, "call", func(c *sitter.Node) error {
		if w.isInsideNestedTestBlock(c, body) {
			return nil
		}
		methodNode := c.ChildByFieldName("method")
		if methodNode == nil {
			return nil
		}
		methodName := extract.Text(methodNode, w.source)
		if rspecDSLMethods[methodName] {
			// Recurse into nested test block.
			_, err := w.handleTestBlock(c, scope, methodName)
			return err
		}
		return w.emitTestCall(c, source, scope)
	}); err != nil {
		return err
	}
	// Emit edges for bare identifiers in statement and value positions.
	return w.emitBareIdentifierCalls(body, source, extract.ConfidenceTests, nil)
}

// isInsideNestedTestBlock returns true if n sits inside a test DSL call
// that is a descendant of body (i.e., a nested test block).
func (w *walker) isInsideNestedTestBlock(n, body *sitter.Node) bool {
	bodyID := body.Id()
	for p := n.Parent(); p != nil && p.Id() != bodyID; p = p.Parent() {
		if p.Kind() == "call" {
			methodNode := p.ChildByFieldName("method")
			if methodNode != nil && rspecDSLMethods[extract.Text(methodNode, w.source)] {
				return true
			}
		}
	}
	return false
}

// walkTestBlockWithFallback walks a test block body using the file path
// as the source scope (file-level fallback).
func (w *walker) walkTestBlockWithFallback(block *sitter.Node, scope []string) error {
	body := getBlockBody(block)
	if body == nil {
		return nil
	}
	source := w.fileLevelScope(block)
	return w.walkTestBody(body, scope, source)
}

// fileLevelScope returns a file-level synthetic scope like "test.rb#L42".
func (w *walker) fileLevelScope(n *sitter.Node) string {
	base := w.filePath
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	line := extract.Line(n.StartPosition())
	return fmt.Sprintf("%s#L%d", base, line)
}

// hasInterpolation returns true if a string node contains interpolation.
func hasInterpolation(n *sitter.Node) bool {
	if n.Kind() != "string" {
		return false
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c != nil && c.Kind() == "interpolation" {
			return true
		}
	}
	return false
}

// sanitizeDesc turns a human-readable block description into a safe
// scope segment: spaces → underscores, strip non-alphanumerics.
func sanitizeDesc(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('_')
		default:
			// Drop punctuation.
		}
	}
	return b.String()
}

// emitTestCall delegates to emitCall but substitutes ConfidenceTests and
// skips RSpec matcher noise (eq, be_valid, raise_error, etc.).
func (w *walker) emitTestCall(n *sitter.Node, source string, scope []string) error {
	methodNode := n.ChildByFieldName("method")
	if methodNode == nil {
		return nil
	}
	if rspecMatcherMethods[extract.Text(methodNode, w.source)] {
		return nil
	}
	return w.emitCallWithConfidence(n, source, scope, nil, nil, extract.ConfidenceTests)
}

// emitDescribeEdge detects RSpec.describe/describe with a constant
// argument and emits a tests edge to the referenced class.
func (w *walker) emitDescribeEdge(n *sitter.Node) error {
	// For RSpec.describe, the receiver is "RSpec". For bare describe, no receiver.
	// Both are valid — just need the first arg to be a constant.
	if recv := n.ChildByFieldName("receiver"); recv != nil {
		if extract.Text(recv, w.source) != "RSpec" {
			return nil
		}
	}

	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return nil
	}
	first := args.NamedChild(0)
	if first == nil {
		return nil
	}

	var target string
	switch first.Kind() {
	case "constant":
		target = extract.Text(first, w.source)
	case "scope_resolution":
		target = extract.Text(first, w.source)
	default:
		return nil
	}
	if target == "" {
		return nil
	}

	line := extract.Line(n.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: target + "Test",
		TargetQualified: target,
		Kind:            model.EdgeTests,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}
