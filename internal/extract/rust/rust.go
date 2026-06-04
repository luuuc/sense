// Package rust extracts symbols and intra-file edges from Rust source
// via tree-sitter-rust.
//
// Symbol kinds:
//   - struct_item               → KindClass
//   - enum_item                 → KindClass
//   - trait_item                → KindInterface
//   - type_item (`type X = Y`)  → KindType
//   - const_item / static_item  → KindConstant
//   - function_item at module   → KindFunction
//   - function_item in impl     → KindMethod with parent = impl type
//   - mod_item                  → KindModule
//   - function_signature_item   → KindMethod (trait method signatures,
//     parented to the trait)
//
// Visibility:
//   - `pub` → "public"; `pub(crate)`, `pub(super)`, `pub(in path)` and
//     no modifier → "private". Rust's export boundary is the crate, so
//     restricted-pub variants are not truly public API.
//
// Depth edges:
//   - `impl Trait for Type`     → inherits (Type → Trait), confidence 1.0
//   - `#[derive(Trait)]`        → inherits (Type → Trait), confidence 1.0
//   - struct field types        → composes (Struct → FieldType), confidence 1.0
//   - call_expression           → calls, confidence 1.0
//
// Known limitation — cross-module impl: `impl other_mod::Trait for Type`
// resolves the trait name via unwrapTypeName, which returns the trailing
// segment only (e.g. "Trait", not "other_mod::Trait"). Trait method
// resolution therefore misses impls of traits defined in sibling modules.
//
// Qualified-name rules:
//   - Rust uses `::` for path separators. Module-scoped items carry
//     the module chain: `inner::helper`.
//   - Impl methods qualify through the impl's type:
//     impl Money               → methods qualified `Money::display`
//     impl Formatter for Money → methods qualified `Money::format`
package rust

import (
	"slices"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
	"github.com/luuuc/sense/internal/model"
)

// Extractor is the Rust implementation of extract.Extractor.
type Extractor struct{}

func (Extractor) Grammar() *sitter.Language { return grammars.Rust() }
func (Extractor) Language() string          { return "rust" }
func (Extractor) Extensions() []string      { return []string{".rs"} }
func (Extractor) Tier() extract.Tier        { return extract.TierBasic }

func (Extractor) Extract(tree *sitter.Tree, source []byte, _ string, emit extract.Emitter) error {
	w := &walker{
		source:       source,
		emit:         emit,
		traitMethods: map[string]map[string]bool{},
		typeTraits:   map[string][]string{},
	}
	w.preCollect(tree.RootNode(), nil)
	if err := w.walk(tree.RootNode(), nil); err != nil {
		return err
	}
	return emitHarvest(tree.RootNode(), source, emit)
}

func init() { extract.Register(Extractor{}) }

// ---- walker ----

type walker struct {
	source []byte
	emit   extract.Emitter

	// traitMethods maps trait names to their declared method names.
	// Built by preCollect before the main walk.
	traitMethods map[string]map[string]bool

	// typeTraits maps type names to the set of traits they implement
	// (from explicit `impl Trait for Type` and `#[derive(...)]`).
	typeTraits map[string][]string
}

// preCollect scans the tree to build trait method sets and type→trait
// mappings before the main walk. This enables resolving self.method()
// calls through trait implementations.
func (w *walker) preCollect(n *sitter.Node, scope []string) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "source_file", "declaration_list":
		for i := uint(0); i < n.NamedChildCount(); i++ {
			w.preCollect(n.NamedChild(i), scope)
		}
	case "trait_item":
		w.collectTraitMethods(n, scope)
	case "impl_item":
		w.collectImplTraits(n, scope)
	case "struct_item", "enum_item":
		w.collectDeriveTraits(n, scope)
	case "mod_item":
		nameNode := n.ChildByFieldName("name")
		if nameNode == nil {
			return
		}
		name := extract.Text(nameNode, w.source)
		if name == "" {
			return
		}
		if body := n.ChildByFieldName("body"); body != nil {
			w.preCollect(body, append(slices.Clone(scope), name))
		}
	}
}

func (w *walker) walk(n *sitter.Node, scope []string) error {
	if n == nil {
		return nil
	}
	switch n.Kind() {
	case "source_file", "declaration_list":
		return w.walkChildren(n, scope)
	case "struct_item":
		return w.handleTypeDef(n, scope, model.KindClass)
	case "enum_item":
		return w.handleTypeDef(n, scope, model.KindClass)
	case "trait_item":
		return w.handleTypeDef(n, scope, model.KindInterface)
	case "type_item":
		return w.handleTypeDef(n, scope, model.KindType)
	case "const_item", "static_item":
		return w.handleConstItem(n, scope)
	case "function_item":
		return w.handleFunction(n, scope)
	case "impl_item":
		return w.handleImpl(n, scope)
	case "mod_item":
		return w.handleMod(n, scope)
	default:
		return nil
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

// qualify builds a symbol's qualified name and parent-qualified name from
// the enclosing module scope. With an empty scope the qualified name is
// the bare name and the parent is empty; otherwise the scope segments join
// with Rust's "::" path separator. Every symbol-emitting handler funnels
// through here so the path-qualification rule lives in one place.
func qualify(scope []string, name string) (qualified, parent string) {
	parent = strings.Join(scope, "::")
	if parent == "" {
		return name, ""
	}
	return parent + "::" + name, parent
}

// handleTypeDef emits a struct / enum / trait / type-alias symbol.
func (w *walker) handleTypeDef(n *sitter.Node, scope []string, kind model.SymbolKind) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	qualified, parent := qualify(scope, name)
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            kind,
		Visibility:      visibility(n),
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
		Docstring:       docstringFor(n, w.source),
	}); err != nil {
		return err
	}

	if kind == model.KindInterface {
		if err := w.emitTraitMethods(n, qualified); err != nil {
			return err
		}
	}
	if kind == model.KindClass {
		if err := w.emitDerives(n, qualified); err != nil {
			return err
		}
		if n.Kind() == "struct_item" {
			if err := w.emitFieldCompositions(n, qualified); err != nil {
				return err
			}
		}
		if n.Kind() == "enum_item" {
			if err := w.emitEnumVariantCompositions(n, qualified); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *walker) handleConstItem(n *sitter.Node, scope []string) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	qualified, parent := qualify(scope, name)
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindConstant,
		Visibility:      visibility(n),
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
		Docstring:       docstringFor(n, w.source),
	})
}

func (w *walker) handleFunction(n *sitter.Node, scope []string) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	qualified, parent := qualify(scope, name)
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindFunction,
		Visibility:      visibility(n),
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
		Docstring:       docstringFor(n, w.source),
	}); err != nil {
		return err
	}
	return extract.WalkNamedDescendants(n.ChildByFieldName("body"), "call_expression", func(c *sitter.Node) error {
		return w.emitCall(c, qualified, "")
	})
}

// emitCall produces a calls edge for a call_expression. When implType
// is non-empty and the callee is self.method(), the method is resolved
// through the type's trait implementations (confidence 0.9) or as an
// inherent method on the type (confidence 0.9). Unresolvable calls
// fall back to surface text at confidence 1.0.
func (w *walker) emitCall(call *sitter.Node, source, implType string) error {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return nil
	}
	var target string
	confidence := extract.ConfidenceStatic

	switch fn.Kind() {
	case "identifier":
		target = extract.Text(fn, w.source)
	case "field_expression":
		target, confidence = w.resolveFieldCall(fn, implType)
	case "scoped_identifier":
		target = extract.Text(fn, w.source)
	default:
		return nil
	}
	if target == "" {
		return nil
	}
	line := extract.Line(call.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: source,
		TargetQualified: target,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      confidence,
	})
}

// resolveFieldCall resolves a field_expression callee (e.g. self.method).
// When the receiver is `self` and we're inside an impl block, the call
// is resolved through trait methods or as an inherent method.
func (w *walker) resolveFieldCall(fn *sitter.Node, implType string) (string, float64) {
	text := extract.Text(fn, w.source)
	if implType == "" {
		return text, extract.ConfidenceStatic
	}

	// Parse field_expression: value.field
	value := fn.ChildByFieldName("value")
	field := fn.ChildByFieldName("field")
	if value == nil || field == nil {
		return text, extract.ConfidenceStatic
	}

	// Only resolve `self.method()` — other receivers need type inference.
	if value.Kind() != "self" {
		return text, extract.ConfidenceStatic
	}

	methodName := extract.Text(field, w.source)
	if methodName == "" {
		return text, extract.ConfidenceStatic
	}

	if resolved := w.resolveTraitMethod(implType, methodName); resolved != "" {
		return resolved, extract.ConfidenceConvention
	}

	// Inherent method on the type. The method may be defined in another
	// impl block or even another file, so the qualified name is a best
	// guess — confidence 0.9 reflects this.
	return implType + "::" + methodName, extract.ConfidenceConvention
}

func (w *walker) handleMod(n *sitter.Node, scope []string) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	qualified, parent := qualify(scope, name)
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindModule,
		Visibility:      visibility(n),
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
		Docstring:       docstringFor(n, w.source),
	}); err != nil {
		return err
	}
	if body := n.ChildByFieldName("body"); body != nil {
		return w.walkChildren(body, append(slices.Clone(scope), name))
	}
	return nil
}

// visibility checks for a visibility_modifier child. `pub` (with no
// restriction) → "public"; everything else (absent, pub(crate),
// pub(super), pub(in path)) → "private".
func visibility(n *sitter.Node) string {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		child := n.NamedChild(i)
		if child != nil && child.Kind() == "visibility_modifier" {
			if child.NamedChildCount() == 0 {
				return "public"
			}
			return "private"
		}
	}
	return "private"
}
