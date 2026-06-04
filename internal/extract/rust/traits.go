package rust

// traits.go isolates the trait and derive machinery: the pre-collection
// of trait method sets and type→trait mappings (so self.method() calls
// can resolve through implemented traits), the #[derive(...)] attribute
// walk, impl-block handling (methods + the Type→Trait inherits edge), and
// trait-method symbol emission. The core symbol walk lives in rust.go and
// composition resolution in compose.go.

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

func (w *walker) collectTraitMethods(n *sitter.Node, scope []string) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	traitName := extract.Text(nameNode, w.source)
	if traitName == "" {
		return
	}
	if len(scope) > 0 {
		traitName = strings.Join(scope, "::") + "::" + traitName
	}
	methods := map[string]bool{}
	body := n.ChildByFieldName("body")
	if body == nil {
		return
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() != "function_signature_item" && child.Kind() != "function_item" {
			continue
		}
		mn := child.ChildByFieldName("name")
		if mn != nil {
			methods[extract.Text(mn, w.source)] = true
		}
	}
	w.traitMethods[traitName] = methods
}

func (w *walker) collectImplTraits(n *sitter.Node, scope []string) {
	implType := n.ChildByFieldName("type")
	traitNode := n.ChildByFieldName("trait")
	if implType == nil || traitNode == nil {
		return
	}
	typeName := unwrapTypeName(implType, w.source)
	traitName := unwrapTypeName(traitNode, w.source)
	if typeName == "" || traitName == "" {
		return
	}
	if len(scope) > 0 {
		prefix := strings.Join(scope, "::")
		typeName = prefix + "::" + typeName
		traitName = prefix + "::" + traitName
	}
	w.typeTraits[typeName] = append(w.typeTraits[typeName], traitName)
}

func (w *walker) collectDeriveTraits(n *sitter.Node, scope []string) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	typeName := extract.Text(nameNode, w.source)
	if typeName == "" {
		return
	}
	if len(scope) > 0 {
		typeName = strings.Join(scope, "::") + "::" + typeName
	}
	_ = w.forEachDerivedTrait(n, func(traitName string, _ *sitter.Node) error {
		w.typeTraits[typeName] = append(w.typeTraits[typeName], traitName)
		return nil
	})
}

// forEachDerivedTrait walks #[derive(...)] attributes preceding a node
// and calls fn for each derived trait name. The node argument to fn is
// the identifier node (for line information).
func (w *walker) forEachDerivedTrait(n *sitter.Node, fn func(traitName string, ident *sitter.Node) error) error {
	for sib := n.PrevNamedSibling(); sib != nil; sib = sib.PrevNamedSibling() {
		if sib.Kind() != "attribute_item" {
			break
		}
		inner := deriveAttribute(sib, w.source)
		if inner == nil {
			continue
		}
		if err := w.forEachDeriveToken(inner, fn); err != nil {
			return err
		}
	}
	return nil
}

// deriveAttribute returns the `attribute` node of an attribute_item when
// it is a #[derive(...)] attribute, or nil for any other attribute.
func deriveAttribute(sib *sitter.Node, source []byte) *sitter.Node {
	inner := sib.NamedChild(0)
	if inner == nil || inner.Kind() != "attribute" {
		return nil
	}
	nameNode := inner.NamedChild(0)
	if nameNode == nil || extract.Text(nameNode, source) != "derive" {
		return nil
	}
	return inner
}

// forEachDeriveToken calls fn for each trait identifier in a derive
// attribute's token_tree(s), skipping path-qualified segments (the `Foo`
// in `Foo::Bar`).
func (w *walker) forEachDeriveToken(inner *sitter.Node, fn func(traitName string, ident *sitter.Node) error) error {
	for i := uint(0); i < inner.NamedChildCount(); i++ {
		tt := inner.NamedChild(i)
		if tt == nil || tt.Kind() != "token_tree" {
			continue
		}
		count := tt.ChildCount()
		for j := uint(0); j < count; j++ {
			child := tt.Child(j)
			if child == nil || child.Kind() != "identifier" {
				continue
			}
			if j+1 < count {
				if next := tt.Child(j + 1); next != nil && next.Kind() == "::" {
					continue
				}
			}
			traitName := extract.Text(child, w.source)
			if traitName == "" {
				continue
			}
			if err := fn(traitName, child); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveTraitMethod checks if a method name on a given type can be
// resolved to a specific trait method. Returns the qualified trait
// method name (e.g. "Processor::process") or "" if not resolvable.
// When multiple traits declare the same method, resolution is ambiguous
// and returns "" — the caller falls back to inherent method resolution.
func (w *walker) resolveTraitMethod(typeName, methodName string) string {
	var match string
	for _, traitName := range w.typeTraits[typeName] {
		if methods, ok := w.traitMethods[traitName]; ok && methods[methodName] {
			if match != "" {
				return ""
			}
			match = traitName + "::" + methodName
		}
	}
	return match
}

// handleImpl walks an `impl Type { … }` or `impl Trait for Type { … }`
// block. Functions inside become methods qualified through Type; if a
// trait is present, an inherits edge (Type → Trait) captures the
// trait implementation.
func (w *walker) handleImpl(n *sitter.Node, scope []string) error {
	implType := n.ChildByFieldName("type")
	if implType == nil {
		return nil
	}
	typeName := unwrapTypeName(implType, w.source)
	if typeName == "" {
		return nil
	}
	typeQualified, _ := qualify(scope, typeName)

	if err := w.emitImplTraitEdge(n, typeQualified); err != nil {
		return err
	}

	body := n.ChildByFieldName("body")
	if body == nil {
		return nil
	}
	return w.emitImplMethods(body, typeQualified)
}

// emitImplTraitEdge emits the inherits edge for `impl Trait for Type`
// (Type → Trait). A bare `impl Type` block has no trait field and emits
// nothing.
func (w *walker) emitImplTraitEdge(n *sitter.Node, typeQualified string) error {
	traitNode := n.ChildByFieldName("trait")
	if traitNode == nil {
		return nil
	}
	traitName := unwrapTypeName(traitNode, w.source)
	if traitName == "" {
		return nil
	}
	line := extract.Line(traitNode.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: typeQualified,
		TargetQualified: traitName,
		Kind:            model.EdgeInherits,
		Line:            &line,
		Confidence:      1.0,
	})
}

// emitImplMethods emits a method symbol and its calls edges for each
// function_item in an impl block body, qualified through the impl's type.
func (w *walker) emitImplMethods(body *sitter.Node, typeQualified string) error {
	for i := uint(0); i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		if child == nil || child.Kind() != "function_item" {
			continue
		}
		nameNode := child.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := extract.Text(nameNode, w.source)
		if name == "" {
			continue
		}
		methodQualified := typeQualified + "::" + name
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:            name,
			Qualified:       methodQualified,
			Kind:            model.KindMethod,
			Visibility:      visibility(child),
			ParentQualified: typeQualified,
			LineStart:       extract.Line(child.StartPosition()),
			LineEnd:         extract.Line(child.EndPosition()),
			Docstring:       docstringFor(child, w.source),
		}); err != nil {
			return err
		}
		if err := extract.WalkNamedDescendants(child.ChildByFieldName("body"), "call_expression", func(c *sitter.Node) error {
			return w.emitCall(c, methodQualified, typeQualified)
		}); err != nil {
			return err
		}
	}
	return nil
}

// emitTraitMethods walks a trait_item's declaration_list and emits
// each function_signature_item as a KindMethod symbol parented to the
// trait. Trait methods inherit their visibility from the trait itself —
// a `pub trait`'s methods are public even without their own `pub`.
func (w *walker) emitTraitMethods(traitNode *sitter.Node, traitQualified string) error {
	traitVis := visibility(traitNode)
	body := traitNode.ChildByFieldName("body")
	if body == nil {
		return nil
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() != "function_signature_item" && child.Kind() != "function_item" {
			continue
		}
		nameNode := child.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := extract.Text(nameNode, w.source)
		if name == "" {
			continue
		}
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:            name,
			Qualified:       traitQualified + "::" + name,
			Kind:            model.KindMethod,
			Visibility:      traitVis,
			ParentQualified: traitQualified,
			LineStart:       extract.Line(child.StartPosition()),
			LineEnd:         extract.Line(child.EndPosition()),
			Docstring:       docstringFor(child, w.source),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) emitDerives(n *sitter.Node, qualified string) error {
	return w.forEachDerivedTrait(n, func(traitName string, ident *sitter.Node) error {
		line := extract.Line(ident.StartPosition())
		return w.emit.Edge(extract.EmittedEdge{
			SourceQualified: qualified,
			TargetQualified: traitName,
			Kind:            model.EdgeInherits,
			Line:            &line,
			Confidence:      extract.ConfidenceStatic,
		})
	})
}
