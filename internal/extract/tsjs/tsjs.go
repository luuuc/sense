// Package tsjs extracts Tier-Basic symbols and intra-file edges from
// TypeScript, TSX, and JavaScript source. One walker, three grammars:
// the TS grammar is a strict superset of the JS grammar (plus TSX
// variant), so a single switch over node kinds covers all three —
// unknown TS-only kinds simply don't appear in JS trees.
//
// Symbol kinds:
//   - class                    → KindClass
//   - interface (TS only)      → KindInterface
//   - enum (TS only)           → KindClass (no enum kind in model)
//   - type alias (TS only)     → KindType
//   - function declaration     → KindFunction
//   - method inside a class    → KindMethod
//   - const NAME = <arrow/fn>  → KindFunction (named function expression)
//   - const NAME = class …     → KindClass (class expression)
//   - other const NAME = value → KindConstant
//
// Intra-file edges:
//   - class B extends A        → inherits (B → A) when A is local
//   - class B implements I     → inherits (B → I) when I is local
//   - interface B extends A    → inherits (B → A) when A is local
//
// Calls edges:
//   - Method / function / arrow-function bodies are walked for
//     `call_expression` nodes. The target is the callee's surface
//     text — `foo`, `obj.bar`, `a.b.c` — as written, with one rewrite:
//     a leading `this.` is stripped so `this.helper()` resolves against
//     the enclosing class's members. Tagged template invocations
//     (`` tag`literal` ``) parse as `call_expression` in tree-sitter and
//     are emitted as regular calls. `new X()` is a `new_expression`,
//     not a call_expression; constructors aren't emitted in Tier-Basic.
//     Subscript / dynamic callees (`obj[k]()`, `f()()`) are skipped.
//   - JSX elements with PascalCase tags emit calls edges to the component
//     name (`<UserProfile>` → calls UserProfile). Lowercase tags (HTML)
//     and fragments (`<>`, `<React.Fragment>`) are skipped. Namespaced
//     tags emit the full surface text (`<Form.Input>` → calls Form.Input).
//
// Qualified-name rules (per 05-languages.md, "pkg.module.Class.method"):
//   Class/Interface/Enum/Type: Name or Outer.Inner
//   Method:                    Class.method
//   Function / Const:          Name (no further qualification in Tier-Basic)
//
// What Tier-Basic skips:
//   - class fields (public_field_definition)
//   - let / var bindings — only `const` counts
package tsjs

import (
	"path/filepath"
	"slices"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
	"github.com/luuuc/sense/internal/model"
)

// TypeScript handles .ts files via the TypeScript grammar.
type TypeScript struct{}

func (TypeScript) Grammar() *sitter.Language { return grammars.TypeScript() }
func (TypeScript) Language() string          { return "typescript" }
func (TypeScript) Extensions() []string      { return []string{".ts"} }
func (TypeScript) Tier() extract.Tier        { return extract.TierBasic }
func (TypeScript) Extract(tree *sitter.Tree, source []byte, filePath string, emit extract.Emitter) error {
	return extractAll(tree, source, filePath, emit)
}

// TSX handles .tsx files via the TSX grammar (same extractor logic —
// JSX elements appear as expressions in bodies we don't descend into).
type TSX struct{}

func (TSX) Grammar() *sitter.Language { return grammars.TSX() }
func (TSX) Language() string          { return "tsx" }
func (TSX) Extensions() []string      { return []string{".tsx"} }
func (TSX) Tier() extract.Tier        { return extract.TierBasic }
func (TSX) Extract(tree *sitter.Tree, source []byte, filePath string, emit extract.Emitter) error {
	return extractAll(tree, source, filePath, emit)
}

// JavaScript handles .js/.mjs/.cjs/.jsx files. The JS grammar parses
// JSX, so .jsx uses this extractor too — no separate JSX grammar.
type JavaScript struct{}

func (JavaScript) Grammar() *sitter.Language { return grammars.JavaScript() }
func (JavaScript) Language() string           { return "javascript" }
func (JavaScript) Extensions() []string       { return []string{".js", ".jsx", ".mjs", ".cjs"} }
func (JavaScript) Tier() extract.Tier         { return extract.TierBasic }
func (JavaScript) Extract(tree *sitter.Tree, source []byte, filePath string, emit extract.Emitter) error {
	return extractAll(tree, source, filePath, emit)
}

func init() {
	extract.Register(TypeScript{})
	extract.Register(TSX{})
	extract.Register(JavaScript{})
}

func extractAll(tree *sitter.Tree, source []byte, filePath string, emit extract.Emitter) error {
	w := &walker{
		source:       source,
		emit:         emit,
		filePath:     filePath,
		stimulusName: inferStimulusController(filePath),
	}
	if err := w.walk(tree.RootNode(), nil); err != nil {
		return err
	}
	return w.walkDynamicImports(tree.RootNode())
}

// ---- walker ----

type walker struct {
	source       []byte
	emit         extract.Emitter
	filePath     string
	stimulusName string // non-empty if this file is a Stimulus controller
}

func (w *walker) walk(n *sitter.Node, scope []string) error {
	if n == nil {
		return nil
	}
	switch n.Kind() {
	case "export_statement":
		if d := n.ChildByFieldName("declaration"); d != nil {
			if hasDefaultKeyword(n, w.source) {
				if handled, err := w.handleDefaultExport(d, scope); handled || err != nil {
					return err
				}
			}
			return w.walk(d, scope)
		}
		if hasDefaultKeyword(n, w.source) {
			for i := uint(0); i < n.NamedChildCount(); i++ {
				child := n.NamedChild(i)
				if child == nil {
					continue
				}
				if handled, err := w.handleDefaultExport(child, scope); handled || err != nil {
					return err
				}
			}
		}
		if modulePath := reexportSource(n, w.source); modulePath != "" {
			return w.handleReexport(n, modulePath)
		}
		return w.walkChildren(n, scope)
	case "class_declaration", "class":
		return w.handleClass(n, scope)
	case "abstract_class_declaration":
		return w.handleClass(n, scope)
	case "interface_declaration":
		return w.handleInterface(n, scope)
	case "type_alias_declaration":
		return w.handleTypeAlias(n, scope)
	case "enum_declaration":
		return w.handleEnum(n, scope)
	case "function_declaration":
		return w.handleFunction(n, scope)
	case "lexical_declaration":
		return w.handleLexicalDeclaration(n, scope)
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

// handleClass emits a class symbol, records extends/implements edges,
// and descends into the body to pick up methods. Class fields
// (public_field_definition) are skipped in Tier-Basic.
func (w *walker) handleClass(n *sitter.Node, scope []string) error {
	nameNode := n.ChildByFieldName("name")
	name := extract.Text(nameNode, w.source)

	if name == "" && w.stimulusName != "" {
		return w.handleStimulusClass(n, scope)
	}
	if name == "" {
		return w.walkChildren(n, scope)
	}
	return w.emitClassWithBody(n, name, scope)
}

// emitClassWithBody emits a class symbol, heritage edges, and descends
// into methods. Shared by handleClass and handleDefaultExport.
func (w *walker) emitClassWithBody(n *sitter.Node, name string, scope []string) error {
	parent := strings.Join(scope, ".")
	qualified := name
	if parent != "" {
		qualified = parent + "." + name
	}

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindClass,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}

	if err := w.emitHeritageEdges(n, qualified); err != nil {
		return err
	}

	return w.walkClassBody(n, append(slices.Clone(scope), name), qualified)
}

// emitHeritageEdges walks the class_heritage child (if any) and emits
// inherits edges for each extends/implements entry with a simple
// identifier target.
//
// The two grammars shape this differently:
//
//   TypeScript: class_heritage → extends_clause (value: identifier)
//               class_heritage → implements_clause (type_identifier*)
//   JavaScript: class_heritage → identifier (direct child, no clause)
//
// We handle both: clause children are walked with emitHeritageTargets,
// bare identifiers are converted into a one-target edge in place.
// Compound expressions like `extends Generic<T>` resolve via
// resolveHeritageName's generic_type case. Member expressions like
// `mod.Base` are skipped — cross-file territory for 01-03.
func (w *walker) emitHeritageEdges(classNode *sitter.Node, source string) error {
	for i := uint(0); i < classNode.NamedChildCount(); i++ {
		heritage := classNode.NamedChild(i)
		if heritage == nil || heritage.Kind() != "class_heritage" {
			continue
		}
		for j := uint(0); j < heritage.NamedChildCount(); j++ {
			clause := heritage.NamedChild(j)
			if clause == nil {
				continue
			}
			line := extract.Line(clause.StartPosition())
			switch clause.Kind() {
			case "extends_clause", "implements_clause":
				if err := w.emitHeritageTargets(clause, source, line); err != nil {
					return err
				}
			case "identifier", "type_identifier":
				// JS shape: extends-target is a direct child.
				target := extract.Text(clause, w.source)
				if target == "" {
					continue
				}
				if err := w.emit.Edge(extract.EmittedEdge{
					SourceQualified: source,
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
	return nil
}

// emitHeritageTargets walks a single extends/implements clause and
// emits inherits edges for simple-identifier target names.
func (w *walker) emitHeritageTargets(clause *sitter.Node, source string, line int) error {
	for i := uint(0); i < clause.NamedChildCount(); i++ {
		ref := clause.NamedChild(i)
		if ref == nil {
			continue
		}
		// The target name can sit in the `value` field (extends on
		// class_declaration) or be a direct type_identifier child
		// (implements clauses, interface extends). Try the field first,
		// then fall back to the clause's direct children.
		target := resolveHeritageName(ref, w.source)
		if target == "" {
			continue
		}
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: source,
			TargetQualified: target,
			Kind:            model.EdgeInherits,
			Line:            &line,
			Confidence:      1.0,
		}); err != nil {
			return err
		}
	}
	return nil
}

// resolveHeritageName extracts a simple target name from the node a
// heritage clause references. Accepts plain identifiers and
// type_identifiers; rejects member expressions (cross-file) and
// type parameters (can't resolve in Tier-Basic).
func resolveHeritageName(n *sitter.Node, source []byte) string {
	switch n.Kind() {
	case "identifier", "type_identifier":
		return extract.Text(n, source)
	case "generic_type":
		// `Base<T>` — the name lives in the `name` field.
		if name := n.ChildByFieldName("name"); name != nil {
			return extract.Text(name, source)
		}
	}
	return ""
}

// handleMethod emits a method symbol using the enclosing class scope
// and walks the body for call expressions.
func (w *walker) handleMethod(n *sitter.Node, scope []string) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	parent := strings.Join(scope, ".")
	qualified := name
	if parent != "" {
		qualified = parent + "." + name
	}
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindMethod,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}
	return w.walkBodyEdges(n.ChildByFieldName("body"), qualified)
}

// handleInterface emits an interface symbol and inherits edges for
// each `extends` target. Unlike classes, interfaces don't have a
// class_heritage wrapper — extends_clauses (plural) appear as direct
// children of the interface_declaration.
func (w *walker) handleInterface(n *sitter.Node, scope []string) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return w.walkChildren(n, scope)
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return w.walkChildren(n, scope)
	}
	parent := strings.Join(scope, ".")
	qualified := name
	if parent != "" {
		qualified = parent + "." + name
	}

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindInterface,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}

	for i := uint(0); i < n.NamedChildCount(); i++ {
		child := n.NamedChild(i)
		if child == nil || child.Kind() != "extends_type_clause" {
			continue
		}
		line := extract.Line(child.StartPosition())
		if err := w.emitHeritageTargets(child, qualified, line); err != nil {
			return err
		}
	}
	return nil
}

// handleTypeAlias emits a KindType symbol and composes edges for
// intersection types (A & B → composes edges to A and B).
func (w *walker) handleTypeAlias(n *sitter.Node, scope []string) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	parent := strings.Join(scope, ".")
	qualified := name
	if parent != "" {
		qualified = parent + "." + name
	}
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindType,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}
	if valueNode := n.ChildByFieldName("value"); valueNode != nil {
		return w.emitIntersectionEdges(valueNode, qualified)
	}
	return nil
}

// emitIntersectionEdges recursively walks an intersection_type node and
// emits composes edges for each identifiable type name.
func (w *walker) emitIntersectionEdges(n *sitter.Node, source string) error {
	if n.Kind() != "intersection_type" {
		return nil
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "type_identifier":
			if err := w.emitComposesEdge(source, extract.Text(child, w.source), child); err != nil {
				return err
			}
		case "generic_type":
			if err := w.emitComposesEdge(source, resolveHeritageName(child, w.source), child); err != nil {
				return err
			}
		case "intersection_type":
			if err := w.emitIntersectionEdges(child, source); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *walker) emitComposesEdge(source, target string, n *sitter.Node) error {
	if target == "" {
		return nil
	}
	line := extract.Line(n.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: source,
		TargetQualified: target,
		Kind:            model.EdgeComposes,
		Line:            &line,
		Confidence:      extract.ConfidenceStatic,
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

// handleEnum emits an enum as KindClass (the data model has no enum
// kind; classes are the closest structural neighbour — both declare a
// type with members). Individual enum members are not emitted as
// separate symbols in Tier-Basic.
func (w *walker) handleEnum(n *sitter.Node, scope []string) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	parent := strings.Join(scope, ".")
	qualified := name
	if parent != "" {
		qualified = parent + "." + name
	}
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindClass,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	})
}

// handleFunction emits a top-level or scoped function and walks the
// body for calls. JS/TS have no syntactic "method defined outside a
// class" idiom, so this always produces KindFunction — methods come
// from class bodies.
func (w *walker) handleFunction(n *sitter.Node, scope []string) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	return w.emitFunctionWithBody(n, name, scope)
}

// emitFunctionWithBody emits a function symbol and walks the body for
// edges. Shared by handleFunction and handleDefaultExport.
func (w *walker) emitFunctionWithBody(n *sitter.Node, name string, scope []string) error {
	parent := strings.Join(scope, ".")
	qualified := name
	if parent != "" {
		qualified = parent + "." + name
	}
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindFunction,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}
	return w.walkBodyEdges(n.ChildByFieldName("body"), qualified)
}

// handleLexicalDeclaration walks the variable_declarator children of
// a `const …` (let/var are skipped). Each declarator may be:
//   - an arrow_function or function expression → KindFunction
//   - a class expression → KindClass
//   - otherwise → KindConstant
//
// Only top-level lexical_declarations yield symbols — nested ones
// inside function bodies don't get walked because we never descend
// into function/method bodies.
func (w *walker) handleLexicalDeclaration(n *sitter.Node, scope []string) error {
	// `const` declarations have their kind token as a non-named child;
	// inspect the leading tokens. lexical_declaration's first unnamed
	// child is the "const" / "let" / "var" keyword.
	if !isConstDeclaration(n, w.source) {
		return nil
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		decl := n.NamedChild(i)
		if decl == nil || decl.Kind() != "variable_declarator" {
			continue
		}
		if err := w.handleVariableDeclarator(decl, scope); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) handleVariableDeclarator(decl *sitter.Node, scope []string) error {
	nameNode := decl.ChildByFieldName("name")
	if nameNode == nil || nameNode.Kind() != "identifier" {
		return nil // destructuring patterns skipped in Tier-Basic
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	valueNode := decl.ChildByFieldName("value")
	kind := model.KindConstant
	if valueNode != nil {
		switch valueNode.Kind() {
		case "arrow_function", "function_expression", "function", "generator_function":
			kind = model.KindFunction
		case "class", "class_expression":
			kind = model.KindClass
		}
	}

	parent := strings.Join(scope, ".")
	qualified := name
	if parent != "" {
		qualified = parent + "." + name
	}
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            kind,
		ParentQualified: parent,
		LineStart:       extract.Line(decl.StartPosition()),
		LineEnd:         extract.Line(decl.EndPosition()),
	}); err != nil {
		return err
	}
	// Only walk bodies for function-valued consts. A class expression
	// bound to a const doesn't get its methods emitted here — that
	// gap predates this card and remains a known limitation.
	if kind == model.KindFunction && valueNode != nil {
		return w.walkBodyEdges(valueNode.ChildByFieldName("body"), qualified)
	}
	return nil
}

// walkBodyEdges walks a function/method body for call expressions and
// JSX component usage, emitting edges for both.
func (w *walker) walkBodyEdges(body *sitter.Node, sourceQualified string) error {
	if err := extract.WalkNamedDescendants(body, "call_expression", func(c *sitter.Node) error {
		return w.emitCall(c, sourceQualified)
	}); err != nil {
		return err
	}
	if err := extract.WalkNamedDescendants(body, "jsx_opening_element", func(c *sitter.Node) error {
		return w.emitJSXComponent(c, sourceQualified)
	}); err != nil {
		return err
	}
	return extract.WalkNamedDescendants(body, "jsx_self_closing_element", func(c *sitter.Node) error {
		return w.emitJSXComponent(c, sourceQualified)
	})
}

// emitJSXComponent emits a calls edge for a PascalCase JSX element.
// Lowercase tags (HTML elements) and fragments are skipped.
func (w *walker) emitJSXComponent(n *sitter.Node, sourceQualified string) error {
	var tag string
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "identifier", "member_expression":
			tag = extract.Text(child, w.source)
		}
		if tag != "" {
			break
		}
	}
	if tag == "" || (tag[0] >= 'a' && tag[0] <= 'z') {
		return nil
	}
	if tag == "React.Fragment" {
		return nil
	}
	line := extract.Line(n.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: sourceQualified,
		TargetQualified: tag,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceStatic,
	})
}

// emitCall produces one calls edge. Identifier and member_expression
// callees emit surface text with confidence 1.0; a leading `this.` is
// stripped so intra-class calls (`this.helper()`) resolve against the
// enclosing class's members via the qualified-name resolver. Other
// callee shapes (subscript, inner-call `f()()`, tagged templates) are
// skipped — they're either dynamic or not `call_expression` at all.
func (w *walker) emitCall(call *sitter.Node, source string) error {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return nil
	}
	var target string
	switch fn.Kind() {
	case "identifier", "member_expression":
		target = extract.Text(fn, w.source)
	default:
		return nil
	}
	target = strings.TrimPrefix(target, "this.")
	if target == "" {
		return nil
	}
	line := extract.Line(call.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: source,
		TargetQualified: target,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      1.0,
	})
}

// handleDefaultExport handles anonymous default exports by synthesizing
// a symbol name from the filename. Returns (true, err) if the node was
// handled, (false, nil) if it should fall through to normal processing.
func (w *walker) handleDefaultExport(n *sitter.Node, scope []string) (bool, error) {
	nameNode := n.ChildByFieldName("name")
	if nameNode != nil && extract.Text(nameNode, w.source) != "" {
		return false, nil
	}
	defName := w.fileBasedName()
	if defName == "" {
		return false, nil
	}
	switch n.Kind() {
	case "class", "class_expression":
		if w.stimulusName != "" {
			return false, nil
		}
		return true, w.emitClassWithBody(n, defName, scope)
	case "function_expression", "arrow_function":
		return true, w.emitFunctionWithBody(n, defName, scope)
	}
	return false, nil
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
			if err := w.emit.Symbol(extract.EmittedSymbol{
				Name:      name,
				Qualified: name,
				Kind:      model.KindConstant,
				LineStart: extract.Line(spec.StartPosition()),
				LineEnd:   extract.Line(spec.EndPosition()),
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

// hasDefaultKeyword checks if an export_statement node contains the
// "default" keyword token.
func hasDefaultKeyword(n *sitter.Node, source []byte) bool {
	count := n.ChildCount()
	for i := uint(0); i < count; i++ {
		c := n.Child(i)
		if c == nil || c.IsNamed() {
			continue
		}
		if c.Utf8Text(source) == "default" {
			return true
		}
	}
	return false
}

// fileBasedName derives a PascalCase symbol name from the file path.
// Returns "" if the path is empty or the file is an index file.
func (w *walker) fileBasedName() string {
	if w.filePath == "" {
		return ""
	}
	base := filepath.Base(w.filePath)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	if name == "" || name == "index" {
		return ""
	}
	if idx := strings.Index(name, "."); idx > 0 {
		name = name[:idx]
	}
	name = strings.ReplaceAll(name, "-", "_")
	return snakeToPascal(name)
}

// isConstDeclaration returns true when the lexical_declaration begins
// with the `const` keyword. `let` and `var` are skipped — they're
// reassignable, so not "constants" for structural purposes.
func isConstDeclaration(n *sitter.Node, source []byte) bool {
	// Scan unnamed children for the leading keyword.
	count := n.ChildCount()
	for i := uint(0); i < count; i++ {
		c := n.Child(i)
		if c == nil || c.IsNamed() {
			continue
		}
		if c.Utf8Text(source) == "const" {
			return true
		}
		// The first unnamed child is the declaration keyword; stop on
		// the first unnamed token regardless of value.
		return false
	}
	return false
}

// walkClassBody traverses a class body for methods and (in Stimulus controllers)
// static field declarations. Shared by handleClass and handleStimulusClass.
func (w *walker) walkClassBody(n *sitter.Node, methodScope []string, classQualified string) error {
	body := n.ChildByFieldName("body")
	if body == nil {
		return nil
	}
	count := body.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := body.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "method_definition":
			if err := w.handleMethod(child, methodScope); err != nil {
				return err
			}
		case "field_definition":
			if w.stimulusName != "" {
				if err := w.handleStimulusField(child, classQualified); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ---- Stimulus controller inference ----

// handleStimulusClass handles anonymous (or oddly-named) default-export classes
// in Stimulus controller files. Uses the convention-derived name from the file path.
func (w *walker) handleStimulusClass(n *sitter.Node, scope []string) error {
	qualified := w.stimulusName

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:       qualified,
		Qualified:  qualified,
		Kind:       model.KindClass,
		LineStart:  extract.Line(n.StartPosition()),
		LineEnd:    extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}

	if err := w.emitHeritageEdges(n, qualified); err != nil {
		return err
	}

	return w.walkClassBody(n, []string{qualified}, qualified)
}

// handleStimulusField extracts static targets and outlets declarations from
// Stimulus controller classes. Emits symbols for target declarations and
// edges for outlet declarations.
func (w *walker) handleStimulusField(n *sitter.Node, classQualified string) error {
	nameNode := n.ChildByFieldName("property")
	if nameNode == nil {
		return nil
	}
	fieldName := extract.Text(nameNode, w.source)

	switch fieldName {
	case "targets":
		return w.emitStimulusTargets(n, classQualified)
	case "outlets":
		return w.emitStimulusOutlets(n, classQualified)
	}
	return nil
}

func (w *walker) emitStimulusTargets(n *sitter.Node, classQualified string) error {
	for _, name := range extractStringArray(n, w.source) {
		line := extract.Line(n.StartPosition())
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:            "target:" + name,
			Qualified:       classQualified + ".target:" + name,
			Kind:            model.KindConstant,
			ParentQualified: classQualified,
			LineStart:       line,
			LineEnd:         line,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) emitStimulusOutlets(n *sitter.Node, classQualified string) error {
	for _, name := range extractStringArray(n, w.source) {
		target := extract.StimulusControllerQualified(name)
		line := extract.Line(n.StartPosition())
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: classQualified,
			TargetQualified: target,
			Kind:            model.EdgeCalls,
			Line:            &line,
			Confidence:      extract.ConfidenceConvention,
		}); err != nil {
			return err
		}
	}
	return nil
}

// extractStringArray extracts string values from a field_definition's array value.
// Handles: static targets = ["output", "name"]
func extractStringArray(fieldDef *sitter.Node, source []byte) []string {
	// Find the array child (value of the field).
	var arr *sitter.Node
	count := fieldDef.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := fieldDef.NamedChild(i)
		if child != nil && child.Kind() == "array" {
			arr = child
			break
		}
	}
	if arr == nil {
		return nil
	}

	var result []string
	arrCount := arr.NamedChildCount()
	for i := uint(0); i < arrCount; i++ {
		child := arr.NamedChild(i)
		if child == nil || child.Kind() != "string" {
			continue
		}
		// String node contains string_fragment child with the actual text.
		frag := child.NamedChild(0)
		if frag != nil {
			result = append(result, extract.Text(frag, source))
		}
	}
	return result
}


// inferStimulusController derives a Stimulus controller qualified name from a
// file path. Returns "" if the file doesn't match the Stimulus convention.
//
// Convention: **/controllers/**_controller.{js,ts,jsx,tsx}
// Examples:
//
//	"app/javascript/controllers/checkout_controller.js" → "CheckoutController"
//	"app/javascript/controllers/admin/users_controller.ts" → "Admin::UsersController"
func inferStimulusController(filePath string) string {
	if filePath == "" {
		return ""
	}

	// Normalize separators for matching.
	normalized := filepath.ToSlash(filePath)

	// Find the controllers/ directory segment.
	const marker = "/controllers/"
	idx := strings.LastIndex(normalized, marker)
	if idx < 0 {
		return ""
	}
	rest := normalized[idx+len(marker):]

	// Strip extension and _controller suffix.
	ext := filepath.Ext(rest)
	switch ext {
	case ".js", ".ts", ".jsx", ".tsx", ".mjs":
	default:
		return ""
	}
	rest = strings.TrimSuffix(rest, ext)
	if !strings.HasSuffix(rest, "_controller") {
		return ""
	}
	rest = strings.TrimSuffix(rest, "_controller")

	// Split into path segments: "admin/users" → ["admin", "users"]
	segments := strings.Split(rest, "/")
	for i, seg := range segments {
		segments[i] = snakeToPascal(seg)
	}
	last := len(segments) - 1
	segments[last] = segments[last] + "Controller"
	return strings.Join(segments, "::")
}

func snakeToPascal(s string) string {
	words := strings.Split(s, "_")
	var b strings.Builder
	for _, w := range words {
		if w == "" {
			continue
		}
		b.WriteString(strings.ToUpper(w[:1]))
		b.WriteString(w[1:])
	}
	return b.String()
}
