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
//                                 parented to the trait)
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
//       impl Money               → methods qualified `Money::display`
//       impl Formatter for Money → methods qualified `Money::format`
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
	return w.walk(tree.RootNode(), nil)
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
		inner := sib.NamedChild(0)
		if inner == nil || inner.Kind() != "attribute" {
			continue
		}
		nameNode := inner.NamedChild(0)
		if nameNode == nil || extract.Text(nameNode, w.source) != "derive" {
			continue
		}
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
					next := tt.Child(j + 1)
					if next != nil && next.Kind() == "::" {
						continue
					}
				}
				traitName := extract.Text(child, w.source)
				if traitName != "" {
					if err := fn(traitName, child); err != nil {
						return err
					}
				}
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
	parent := strings.Join(scope, "::")
	qualified := name
	if parent != "" {
		qualified = parent + "::" + name
	}
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            kind,
		Visibility:      visibility(n),
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
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
	parent := strings.Join(scope, "::")
	qualified := name
	if parent != "" {
		qualified = parent + "::" + name
	}
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindConstant,
		Visibility:      visibility(n),
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
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
	parent := strings.Join(scope, "::")
	qualified := name
	if parent != "" {
		qualified = parent + "::" + name
	}
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindFunction,
		Visibility:      visibility(n),
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}
	return extract.WalkNamedDescendants(n.ChildByFieldName("body"), "call_expression", func(c *sitter.Node) error {
		return w.emitCall(c, qualified, "")
	})
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
	parent := strings.Join(scope, "::")
	typeQualified := typeName
	if parent != "" {
		typeQualified = parent + "::" + typeName
	}

	// impl Trait for Type → emit inherits edge from Type to Trait.
	if traitNode := n.ChildByFieldName("trait"); traitNode != nil {
		traitName := unwrapTypeName(traitNode, w.source)
		if traitName != "" {
			line := extract.Line(traitNode.StartPosition())
			if err := w.emit.Edge(extract.EmittedEdge{
				SourceQualified: typeQualified,
				TargetQualified: traitName,
				Kind:            model.EdgeInherits,
				Line:            &line,
				Confidence:      1.0,
			}); err != nil {
				return err
			}
		}
	}

	body := n.ChildByFieldName("body")
	if body == nil {
		return nil
	}
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
	parent := strings.Join(scope, "::")
	qualified := name
	if parent != "" {
		qualified = parent + "::" + name
	}
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindModule,
		Visibility:      visibility(n),
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
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

var rustPrimitives = map[string]bool{
	"u8": true, "u16": true, "u32": true, "u64": true, "u128": true, "usize": true,
	"i8": true, "i16": true, "i32": true, "i64": true, "i128": true, "isize": true,
	"f32": true, "f64": true, "bool": true, "char": true, "str": true,
}

var rustStdTypes = map[string]bool{
	"String": true, "Vec": true, "HashMap": true, "HashSet": true,
	"BTreeMap": true, "BTreeSet": true, "Option": true, "Result": true,
	"Box": true, "Rc": true, "Arc": true, "Cell": true, "RefCell": true,
	"Mutex": true, "RwLock": true, "Cow": true, "Pin": true,
}

// wrapperTypes are generic std types whose inner type parameter should
// be extracted as a composition target.
var wrapperTypes = map[string]bool{
	"Vec": true, "Option": true, "Result": true, "Box": true,
	"Rc": true, "Arc": true, "Cell": true, "RefCell": true,
	"Mutex": true, "RwLock": true, "Cow": true, "Pin": true,
}

// emitFieldCompositions walks a struct's field_declaration_list and
// emits composes edges for fields with user-defined types.
func (w *walker) emitFieldCompositions(n *sitter.Node, qualified string) error {
	body := n.ChildByFieldName("body")
	if body == nil || body.Kind() != "field_declaration_list" {
		return nil
	}
	return w.emitStructFieldCompositions(body, qualified)
}

// emitEnumVariantCompositions walks enum variants with tuple or struct
// fields and emits composes edges for user-defined types.
func (w *walker) emitEnumVariantCompositions(n *sitter.Node, qualified string) error {
	body := n.ChildByFieldName("body")
	if body == nil || body.Kind() != "enum_variant_list" {
		return nil
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		variant := body.NamedChild(i)
		if variant == nil || variant.Kind() != "enum_variant" {
			continue
		}
		// Tuple variant fields
		for j := uint(0); j < variant.NamedChildCount(); j++ {
			child := variant.NamedChild(j)
			if child == nil {
				continue
			}
			switch child.Kind() {
			case "ordered_field_declaration_list":
				if err := w.emitTupleFieldCompositions(child, qualified); err != nil {
					return err
				}
			case "field_declaration_list":
				if err := w.emitStructFieldCompositions(child, qualified); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (w *walker) emitTupleFieldCompositions(list *sitter.Node, qualified string) error {
	for i := uint(0); i < list.NamedChildCount(); i++ {
		child := list.NamedChild(i)
		if child == nil {
			continue
		}
		// Tuple fields are bare type nodes directly inside the list.
		typeNode := child
		for _, target := range w.resolveComposeTargets(typeNode) {
			line := extract.Line(child.StartPosition())
			if err := w.emit.Edge(extract.EmittedEdge{
				SourceQualified: qualified,
				TargetQualified: target,
				Kind:            model.EdgeComposes,
				Line:            &line,
				Confidence:      extract.ConfidenceStatic,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *walker) emitStructFieldCompositions(list *sitter.Node, qualified string) error {
	for i := uint(0); i < list.NamedChildCount(); i++ {
		field := list.NamedChild(i)
		if field == nil || field.Kind() != "field_declaration" {
			continue
		}
		typeNode := field.ChildByFieldName("type")
		if typeNode == nil {
			continue
		}
		for _, target := range w.resolveComposeTargets(typeNode) {
			line := extract.Line(field.StartPosition())
			if err := w.emit.Edge(extract.EmittedEdge{
				SourceQualified: qualified,
				TargetQualified: target,
				Kind:            model.EdgeComposes,
				Line:            &line,
				Confidence:      extract.ConfidenceStatic,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveComposeTargets extracts user-defined type names from a type
// node, unwrapping generic wrappers like Vec<T>, Option<T>, Box<T>.
func (w *walker) resolveComposeTargets(typeNode *sitter.Node) []string {
	if typeNode == nil {
		return nil
	}
	switch typeNode.Kind() {
	case "type_identifier":
		name := extract.Text(typeNode, w.source)
		if rustPrimitives[name] || rustStdTypes[name] {
			return nil
		}
		return []string{name}
	case "scoped_type_identifier":
		nameNode := typeNode.ChildByFieldName("name")
		if nameNode == nil {
			return nil
		}
		typeName := extract.Text(nameNode, w.source)
		if rustPrimitives[typeName] || rustStdTypes[typeName] {
			return nil
		}
		return []string{typeName}
	case "generic_type":
		base := typeNode.ChildByFieldName("type")
		if base == nil {
			return nil
		}
		baseName := unwrapTypeName(base, w.source)
		if wrapperTypes[baseName] {
			args := typeNode.ChildByFieldName("type_arguments")
			if args == nil {
				return nil
			}
			return w.resolveTypeArgTargets(args)
		}
		if rustPrimitives[baseName] || rustStdTypes[baseName] {
			return nil
		}
		if baseName != "" {
			return []string{baseName}
		}
		return nil
	case "reference_type":
		inner := typeNode.ChildByFieldName("type")
		return w.resolveComposeTargets(inner)
	case "tuple_type":
		var targets []string
		for i := uint(0); i < typeNode.NamedChildCount(); i++ {
			targets = append(targets, w.resolveComposeTargets(typeNode.NamedChild(i))...)
		}
		return targets
	default:
		return nil
	}
}

func (w *walker) resolveTypeArgTargets(args *sitter.Node) []string {
	if args == nil {
		return nil
	}
	var targets []string
	for i := uint(0); i < args.NamedChildCount(); i++ {
		child := args.NamedChild(i)
		if child == nil {
			continue
		}
		targets = append(targets, w.resolveComposeTargets(child)...)
	}
	return targets
}

func unwrapTypeName(t *sitter.Node, source []byte) string {
	for t != nil {
		switch t.Kind() {
		case "type_identifier":
			return extract.Text(t, source)
		case "generic_type":
			inner := t.ChildByFieldName("type")
			if inner == nil {
				return ""
			}
			t = inner
		case "reference_type":
			inner := t.ChildByFieldName("type")
			if inner == nil {
				return ""
			}
			t = inner
		case "scoped_type_identifier":
			name := t.ChildByFieldName("name")
			if name == nil {
				return ""
			}
			return extract.Text(name, source)
		default:
			return ""
		}
	}
	return ""
}
