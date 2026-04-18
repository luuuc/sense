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
// Qualified-name rules (per 05-languages.md, "pkg.module.Class.method"):
//   Class/Interface/Enum/Type: Name or Outer.Inner
//   Method:                    Class.method
//   Function / Const:          Name (no further qualification in Tier-Basic)
//
// What Tier-Basic skips:
//   - class fields (public_field_definition)
//   - import/export edges (01-03)
//   - JSX component usage (Full tier)
//   - default-export-names-from-filename (Full tier)
//   - let / var bindings — only `const` counts
package tsjs

import (
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
func (TypeScript) Extract(tree *sitter.Tree, source []byte, _ string, emit extract.Emitter) error {
	return extractAll(tree, source, emit)
}

// TSX handles .tsx files via the TSX grammar (same extractor logic —
// JSX elements appear as expressions in bodies we don't descend into).
type TSX struct{}

func (TSX) Grammar() *sitter.Language { return grammars.TSX() }
func (TSX) Language() string          { return "tsx" }
func (TSX) Extensions() []string      { return []string{".tsx"} }
func (TSX) Tier() extract.Tier        { return extract.TierBasic }
func (TSX) Extract(tree *sitter.Tree, source []byte, _ string, emit extract.Emitter) error {
	return extractAll(tree, source, emit)
}

// JavaScript handles .js/.mjs/.cjs/.jsx files. The JS grammar parses
// JSX, so .jsx uses this extractor too — no separate JSX grammar.
type JavaScript struct{}

func (JavaScript) Grammar() *sitter.Language { return grammars.JavaScript() }
func (JavaScript) Language() string           { return "javascript" }
func (JavaScript) Extensions() []string       { return []string{".js", ".jsx", ".mjs", ".cjs"} }
func (JavaScript) Tier() extract.Tier         { return extract.TierBasic }
func (JavaScript) Extract(tree *sitter.Tree, source []byte, _ string, emit extract.Emitter) error {
	return extractAll(tree, source, emit)
}

func init() {
	extract.Register(TypeScript{})
	extract.Register(TSX{})
	extract.Register(JavaScript{})
}

func extractAll(tree *sitter.Tree, source []byte, emit extract.Emitter) error {
	w := &walker{source: source, emit: emit}
	return w.walk(tree.RootNode(), nil)
}

// ---- walker ----

type walker struct {
	source []byte
	emit   extract.Emitter
}

func (w *walker) walk(n *sitter.Node, scope []string) error {
	if n == nil {
		return nil
	}
	switch n.Kind() {
	case "export_statement":
		// `export <decl>` unwraps to its declaration child.
		if d := n.ChildByFieldName("declaration"); d != nil {
			return w.walk(d, scope)
		}
		return w.walkChildren(n, scope)
	case "class_declaration":
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
		Kind:            model.KindClass,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}

	// class_heritage is an unnamed-field child carrying extends + implements.
	if err := w.emitHeritageEdges(n, qualified); err != nil {
		return err
	}

	// Descend into body for methods. Class body lives under the `body` field.
	if body := n.ChildByFieldName("body"); body != nil {
		newScope := append(slices.Clone(scope), name)
		count := body.NamedChildCount()
		for i := uint(0); i < count; i++ {
			child := body.NamedChild(i)
			if child == nil {
				continue
			}
			if child.Kind() == "method_definition" {
				if err := w.handleMethod(child, newScope); err != nil {
					return err
				}
			}
			// public_field_definition, private_field_definition, and
			// friends are intentionally skipped in Tier-Basic.
		}
	}
	return nil
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

// handleMethod emits a method symbol using the enclosing class scope.
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
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindMethod,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	})
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

// handleTypeAlias emits a KindType symbol. Tier-Basic records the
// name + location; the alias's RHS isn't emitted as separate edges
// (that's a structural-edge job for Full tier).
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
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindType,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	})
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

// handleFunction emits a top-level or scoped function. JS/TS have no
// syntactic "method defined outside a class" idiom, so this always
// produces KindFunction — methods come from class bodies.
func (w *walker) handleFunction(n *sitter.Node, scope []string) error {
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
		Kind:            model.KindFunction,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	})
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
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            kind,
		ParentQualified: parent,
		LineStart:       extract.Line(decl.StartPosition()),
		LineEnd:         extract.Line(decl.EndPosition()),
	})
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
