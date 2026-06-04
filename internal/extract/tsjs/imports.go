package tsjs

// imports.go covers the module-boundary concerns: module-level constant
// tracking (the source of references edges), dynamic import() edges, and
// re-export handling. These are the edges that point outward from the
// file — to other modules or to module-scoped constants — kept apart from
// the core symbol walk in tsjs.go and the Stimulus idioms in frameworks.go.

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// collectModuleConstants pre-scans top-level const declarations for
// value-type constants (not arrow functions or class expressions) so
// function bodies can emit references edges.
func (w *walker) collectModuleConstants(root *sitter.Node) {
	if root == nil {
		return
	}
	count := root.NamedChildCount()
	for i := uint(0); i < count; i++ {
		n := root.NamedChild(i)
		if n == nil {
			continue
		}
		// Unwrap export_statement to reach the declaration.
		if n.Kind() == "export_statement" {
			if d := n.ChildByFieldName("declaration"); d != nil {
				n = d
			} else {
				continue
			}
		}
		if n.Kind() != "lexical_declaration" || !isConstDeclaration(n, w.source) {
			continue
		}
		w.collectConstBindings(n)
	}
}

// collectConstBindings records each value-typed const name in one
// lexical_declaration as a module binding (skipping function/class
// expressions and non-identifier destructuring patterns), so function
// bodies can later emit references edges to them.
func (w *walker) collectConstBindings(decl *sitter.Node) {
	for j := uint(0); j < decl.NamedChildCount(); j++ {
		d := decl.NamedChild(j)
		if d == nil || d.Kind() != "variable_declarator" {
			continue
		}
		nameNode := d.ChildByFieldName("name")
		if nameNode == nil || nameNode.Kind() != "identifier" {
			continue
		}
		name := extract.Text(nameNode, w.source)
		if name == "" {
			continue
		}
		// Only track value constants, not function/class expressions.
		if isFunctionOrClassValue(d) {
			continue
		}
		w.pkgBindings[name] = name
	}
}

// isFunctionOrClassValue reports whether a variable_declarator's value is
// a function or class expression — emitted as its own symbol elsewhere, so
// never tracked as a value constant.
func isFunctionOrClassValue(decl *sitter.Node) bool {
	v := decl.ChildByFieldName("value")
	if v == nil {
		return false
	}
	switch v.Kind() {
	case "arrow_function", "function_expression", "function",
		"generator_function", "class", "class_expression":
		return true
	}
	return false
}

// emitConstRefs walks a function body for identifiers that resolve to
// module-level constants and emits references edges.
func (w *walker) emitConstRefs(body *sitter.Node, sourceQualified string) error {
	if body == nil || len(w.pkgBindings) == 0 {
		return nil
	}
	seen := map[string]bool{}
	return extract.WalkNamedDescendants(body, "identifier", func(id *sitter.Node) error {
		name := extract.Text(id, w.source)
		if name == "" || seen[name] {
			return nil
		}
		targetQ, ok := w.pkgBindings[name]
		if !ok {
			return nil
		}
		if p := id.Parent(); p != nil && p.Kind() == "call_expression" {
			if fn := p.ChildByFieldName("function"); fn != nil && fn.Id() == id.Id() {
				return nil
			}
		}
		seen[name] = true
		line := extract.Line(id.StartPosition())
		return w.emit.Edge(extract.EmittedEdge{
			SourceQualified: sourceQualified,
			TargetQualified: targetQ,
			Kind:            model.EdgeReferences,
			Line:            &line,
			Confidence:      extract.ConfidenceStatic,
		})
	})
}

// walkDynamicImports scans the entire file for import() expressions
// and emits imports edges. This is a separate pass from the body walk
// because dynamic imports can appear at any nesting level, including
// inside non-function const initializers (e.g., React.lazy wrappers).
// Note: this revisits call_expression nodes already seen by walkBodyEdges.
// emitDynamicImport filters to import-callee only, so no duplicate edges,
// but the traversal overlaps. Combine with walkBodyEdges if profiling
// shows extraction is hot.
func (w *walker) walkDynamicImports(root *sitter.Node) error {
	return extract.WalkNamedDescendants(root, "call_expression", func(c *sitter.Node) error {
		return w.emitDynamicImport(c)
	})
}

func (w *walker) emitDynamicImport(call *sitter.Node) error {
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Kind() != "import" {
		return nil
	}
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	for i := uint(0); i < args.NamedChildCount(); i++ {
		child := args.NamedChild(i)
		if child == nil || child.Kind() != "string" {
			continue
		}
		frag := child.NamedChild(0)
		if frag == nil {
			continue
		}
		path := extract.Text(frag, w.source)
		if path == "" {
			continue
		}
		line := extract.Line(call.StartPosition())
		return w.emit.Edge(extract.EmittedEdge{
			TargetQualified: path,
			Kind:            model.EdgeImports,
			Line:            &line,
			Confidence:      extract.ConfidenceAmbiguous,
		})
	}
	return nil
}

// reexportSource returns the module path from a re-export statement
// (e.g., `export { Button } from "./Button"` → "./Button").
// Returns "" if this isn't a re-export.
func reexportSource(n *sitter.Node, source []byte) string {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		child := n.NamedChild(i)
		if child != nil && child.Kind() == "string" {
			if frag := child.NamedChild(0); frag != nil {
				return extract.Text(frag, source)
			}
		}
	}
	return ""
}

// handleReexport processes `export { X } from "./X"` and `export * from "./X"`.
// Emits an imports edge to the module path and symbols for each named
// re-export so they're discoverable in the barrel file.
func (w *walker) handleReexport(n *sitter.Node, modulePath string) error {
	line := extract.Line(n.StartPosition())
	if err := w.emit.Edge(extract.EmittedEdge{
		TargetQualified: modulePath,
		Kind:            model.EdgeImports,
		Line:            &line,
		Confidence:      extract.ConfidenceStatic,
	}); err != nil {
		return err
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		clause := n.NamedChild(i)
		if clause == nil || clause.Kind() != "export_clause" {
			continue
		}
		for j := uint(0); j < clause.NamedChildCount(); j++ {
			spec := clause.NamedChild(j)
			if spec == nil || spec.Kind() != "export_specifier" {
				continue
			}
			name := w.reexportName(spec)
			if name == "" || name == "default" {
				continue
			}
			// A re-exported name is definitionally an export of this module, so
			// it is always public — it never falls to the closed-world dead test.
			if err := w.emit.Symbol(extract.EmittedSymbol{
				Name:       name,
				Qualified:  name,
				Kind:       model.KindConstant,
				Visibility: "public",
				LineStart:  extract.Line(spec.StartPosition()),
				LineEnd:    extract.Line(spec.EndPosition()),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// reexportName extracts the exported name from an export_specifier.
// For `export { X }` returns "X". For `export { default as Y }` returns "Y".
func (w *walker) reexportName(spec *sitter.Node) string {
	count := spec.NamedChildCount()
	if count == 0 {
		return ""
	}
	last := spec.NamedChild(count - 1)
	if last == nil {
		return ""
	}
	return extract.Text(last, w.source)
}
