// Package ruby extracts Tier-Basic symbols and intra-file edges from
// Ruby source code via tree-sitter-ruby.
//
// Symbol kinds:
//   - class, module          → KindClass / KindModule
//   - def / def self.<name>  → KindMethod (qualified as Class#name / Class.name)
//   - CONST = …              → KindConstant (at any nesting level)
//
// Intra-file edges:
//   - class A < B            → inherits edge (A → B) when B is defined in
//                              the same file. Cross-file inheritance is
//                              dropped — 01-03 backfills it.
//   - include M / extend M   → includes edge (class → M) when M is
//                              defined in the same file.
//
// Qualified names follow 05-languages.md: A::B::C for classes/modules,
// A::B#m for instance methods, A::B.m for singleton methods, A::B::CONST
// for constants. Top-level symbols carry no leading separator.
package ruby

import (
	"slices"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
	"github.com/luuuc/sense/internal/model"
)

// Extractor is the Ruby implementation of extract.Extractor.
type Extractor struct{}

func (Extractor) Grammar() *sitter.Language { return grammars.Ruby() }
func (Extractor) Language() string          { return "ruby" }
func (Extractor) Extensions() []string      { return []string{".rb", ".rake", ".gemspec"} }
func (Extractor) Tier() extract.Tier        { return extract.TierBasic }

func (Extractor) Extract(tree *sitter.Tree, source []byte, _ string, emit extract.Emitter) error {
	w := &walker{source: source, emit: emit}
	return w.walk(tree.RootNode(), nil)
}

func init() { extract.Register(Extractor{}) }

// ---- walker ----

type walker struct {
	source []byte
	emit   extract.Emitter
}

// walk visits node and its children under the given class/module scope.
// scope is the chain of enclosing class/module qualified-name segments —
// e.g. ["App", "Services"] inside `module App; module Services; …`.
func (w *walker) walk(n *sitter.Node, scope []string) error {
	if n == nil {
		return nil
	}

	switch n.Kind() {
	case "class":
		return w.handleClassOrModule(n, scope, model.KindClass)
	case "module":
		return w.handleClassOrModule(n, scope, model.KindModule)
	case "method":
		return w.handleMethod(n, scope, false)
	case "singleton_method":
		return w.handleMethod(n, scope, true)
	case "assignment":
		if err := w.handleConstantAssignment(n, scope); err != nil {
			return err
		}
		return w.walkChildren(n, scope)
	case "call":
		if err := w.handleIncludeCall(n, scope); err != nil {
			return err
		}
		return w.walkChildren(n, scope)
	default:
		return w.walkChildren(n, scope)
	}
}

func (w *walker) walkChildren(n *sitter.Node, scope []string) error {
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		if err := w.walk(n.NamedChild(i), scope); err != nil {
			return err
		}
	}
	return nil
}

// handleClassOrModule emits the symbol, records inheritance (class only),
// and descends into the body with the class/module pushed onto the scope.
func (w *walker) handleClassOrModule(n *sitter.Node, scope []string, kind model.SymbolKind) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return w.walkChildren(n, scope) // malformed, keep walking
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return w.walkChildren(n, scope)
	}

	// A::B as a class name pushes both segments; the last segment is the
	// "name" (what grep will find), the full chain is the qualified name.
	segments := strings.Split(name, "::")
	newScope := append(slices.Clone(scope), segments...)
	qualified := strings.Join(newScope, "::")
	parent := strings.Join(scope, "::")

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            segments[len(segments)-1],
		Qualified:       qualified,
		Kind:            kind,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}

	// Inheritance: emit an edge when the superclass is a simple constant.
	// Target resolution to a symbol_id happens at write time — here we
	// just record the target's qualified name.
	if kind == model.KindClass {
		if sup := n.ChildByFieldName("superclass"); sup != nil {
			if target := superclassName(sup, w.source); target != "" {
				line := extract.Line(sup.StartPosition())
				if err := w.emit.Edge(extract.EmittedEdge{
					SourceQualified: qualified,
					TargetQualified: target,
					Kind:            model.EdgeInherits,
					Line:            &line,
					Confidence:      1.0,
				}); err != nil {
					return err
				}
			}
		}
	}

	if body := n.ChildByFieldName("body"); body != nil {
		return w.walkChildren(body, newScope)
	}
	return nil
}

// handleMethod emits a method symbol qualified either as Class#name
// (instance) or Class.name (singleton). For top-level methods the
// separator and parent are both empty — they become KindMethod with
// qualified=name, which matches how Ruby treats top-level defs (they
// get attached to Object at runtime, but we don't model Object here).
func (w *walker) handleMethod(n *sitter.Node, scope []string, singleton bool) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}

	parent := strings.Join(scope, "::")
	var qualified string
	switch {
	case parent == "":
		qualified = name
	case singleton:
		qualified = parent + "." + name
	default:
		qualified = parent + "#" + name
	}

	return w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindMethod,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	})
}

// handleConstantAssignment emits a KindConstant symbol when the LHS of
// an assignment is a single `constant` node (CAPS name). Nested scope
// resolutions on the LHS (A::B = …) and non-constant LHS are skipped
// — not wrong to record, just not what "constant" means structurally.
func (w *walker) handleConstantAssignment(n *sitter.Node, scope []string) error {
	lhs := n.ChildByFieldName("left")
	if lhs == nil || lhs.Kind() != "constant" {
		return nil
	}
	name := extract.Text(lhs, w.source)
	if name == "" {
		return nil
	}
	parent := strings.Join(scope, "::")
	qualified := name
	if parent != "" {
		qualified = parent + "::" + name
	}
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindConstant,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	})
}

// handleIncludeCall catches `include M`, `extend M`, `prepend M` at the
// class body level and emits an includes edge. tree-sitter-ruby models
// these as `call` nodes with `method` = identifier (no receiver).
func (w *walker) handleIncludeCall(n *sitter.Node, scope []string) error {
	if len(scope) == 0 {
		return nil // outside a class/module, include is top-level, ignore.
	}
	methodNode := n.ChildByFieldName("method")
	if methodNode == nil {
		return nil
	}
	switch extract.Text(methodNode, w.source) {
	case "include", "extend", "prepend":
	default:
		return nil
	}
	args := n.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	source := strings.Join(scope, "::")
	line := extract.Line(n.StartPosition())
	// Each argument becomes a separate edge. Only simple constants are
	// resolvable intra-file — skip anything else (dynamic include expressions).
	count := args.NamedChildCount()
	for i := uint(0); i < count; i++ {
		arg := args.NamedChild(i)
		if arg == nil {
			continue
		}
		target := ""
		switch arg.Kind() {
		case "constant":
			target = extract.Text(arg, w.source)
		case "scope_resolution":
			target = extract.Text(arg, w.source)
		}
		if target == "" {
			continue
		}
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: source,
			TargetQualified: target,
			Kind:            model.EdgeIncludes,
			Line:            &line,
			Confidence:      1.0,
		}); err != nil {
			return err
		}
	}
	return nil
}

// superclassName pulls the target class name from a `superclass` node.
// The node wraps its target (usually `constant` or `scope_resolution`).
func superclassName(sup *sitter.Node, source []byte) string {
	count := sup.NamedChildCount()
	for i := uint(0); i < count; i++ {
		c := sup.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "constant", "scope_resolution":
			return c.Utf8Text(source)
		}
	}
	return ""
}
