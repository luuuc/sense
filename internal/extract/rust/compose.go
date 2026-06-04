package rust

// compose.go resolves composition edges (struct/enum field types → the
// user-defined types they name) and the type-name unwrapping shared with
// impl handling. Wrapper generics (Vec<T>, Option<T>, Box<T>, …) unwrap to
// their argument; primitives and std types compose no edge.

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

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
		return composeTargetForName(extract.Text(typeNode, w.source))
	case "scoped_type_identifier":
		nameNode := typeNode.ChildByFieldName("name")
		if nameNode == nil {
			return nil
		}
		return composeTargetForName(extract.Text(nameNode, w.source))
	case "generic_type":
		return w.resolveGenericComposeTargets(typeNode)
	case "reference_type":
		return w.resolveComposeTargets(typeNode.ChildByFieldName("type"))
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

// composeTargetForName returns a single-element target slice for a
// user-defined type name, or nil for primitives, std types, and the empty
// name (the cases that compose no edge).
func composeTargetForName(name string) []string {
	if name == "" || rustPrimitives[name] || rustStdTypes[name] {
		return nil
	}
	return []string{name}
}

// resolveGenericComposeTargets resolves the compose targets of a
// generic_type: a wrapper (Vec/Option/Box/…) descends into its type
// arguments; any other generic base composes to the base type itself.
func (w *walker) resolveGenericComposeTargets(typeNode *sitter.Node) []string {
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
	return composeTargetForName(baseName)
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
