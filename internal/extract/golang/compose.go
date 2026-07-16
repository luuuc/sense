package golang

// compose.go emits composition edges: a named struct field's declared type is
// a has-a fact recorded as a composes edge to each user-defined type the
// field type names. Wrapper types (pointers, slices, arrays, maps, channels,
// parentheses, generics) unwrap to the types inside them; predeclared types,
// the declaration's own type parameters, function types, and inline type
// literals compose no edge. Embedded fields are emitEmbeddings' territory
// (includes edges) — this file walks the exact complement.

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// goPredeclaredTypes is the Go spec's predeclared type identifier set — a
// closed set frozen by the language.
var goPredeclaredTypes = map[string]bool{
	"bool": true, "byte": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"rune": true, "string": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "any": true, "comparable": true,
}

// emitFieldCompositions walks a struct_type's field declarations and emits
// composes edges for named fields. typeParams holds the enclosing
// declaration's type-parameter names: they are local to the declaration and
// would false-bind to same-named real types if emitted.
func (w *walker) emitFieldCompositions(structNode *sitter.Node, structQualified string, typeParams map[string]bool) error {
	fdl := structNode.NamedChild(0)
	if fdl == nil || fdl.Kind() != "field_declaration_list" {
		return nil
	}
	for i := uint(0); i < fdl.NamedChildCount(); i++ {
		fd := fdl.NamedChild(i)
		if fd == nil || fd.Kind() != "field_declaration" {
			continue
		}
		if fd.ChildByFieldName("name") == nil {
			continue
		}
		line := extract.Line(fd.StartPosition())
		for _, target := range w.composeTargets(fd.ChildByFieldName("type"), typeParams) {
			if err := w.emit.Edge(extract.EmittedEdge{
				SourceQualified:  structQualified,
				TargetQualified:  target.qualified,
				Kind:             model.EdgeComposes,
				Line:             &line,
				Confidence:       extract.ConfidenceStatic,
				TargetImportPath: target.importPath,
				TargetInPackage:  target.inPackage,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// composeTargets extracts the user-defined type names a field type expression
// holds. A generic base recurses through this same switch so a qualified base
// (pkg.Registry[T]) keeps its package prefix. Function types and inline
// struct/interface literals hold behavior or their own fields, not a named
// has-a target — they and every other unlisted shape compose nothing.
// Qualified types carry their import-path annotation so the resolver's path
// lane can refuse the same-basename shadow bind the literal text invites.
func (w *walker) composeTargets(typeNode *sitter.Node, typeParams map[string]bool) []emitTarget {
	if typeNode == nil {
		return nil
	}
	switch typeNode.Kind() {
	case "type_identifier":
		name := extract.Text(typeNode, w.source)
		if name == "" || goPredeclaredTypes[name] || typeParams[name] {
			return nil
		}
		return []emitTarget{{qualified: w.qualify(name)}}
	case "qualified_type":
		return []emitTarget{w.qualifiedTypeTarget(typeNode)}
	case "pointer_type", "parenthesized_type":
		return w.composeTargets(typeNode.NamedChild(0), typeParams)
	case "slice_type", "array_type":
		return w.composeTargets(typeNode.ChildByFieldName("element"), typeParams)
	case "map_type":
		targets := w.composeTargets(typeNode.ChildByFieldName("key"), typeParams)
		return append(targets, w.composeTargets(typeNode.ChildByFieldName("value"), typeParams)...)
	case "channel_type":
		return w.composeTargets(typeNode.ChildByFieldName("value"), typeParams)
	case "generic_type":
		targets := w.composeTargets(typeNode.ChildByFieldName("type"), typeParams)
		return append(targets, w.composeTypeArgTargets(typeNode.ChildByFieldName("type_arguments"), typeParams)...)
	default:
		return nil
	}
}

// composeTypeArgTargets extracts compose targets from a generic type's
// argument list. Each argument arrives wrapped in a type_elem node whose
// children are the type expressions themselves.
func (w *walker) composeTypeArgTargets(args *sitter.Node, typeParams map[string]bool) []emitTarget {
	if args == nil {
		return nil
	}
	var targets []emitTarget
	for i := uint(0); i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if arg == nil || arg.Kind() != "type_elem" {
			continue
		}
		for j := uint(0); j < arg.NamedChildCount(); j++ {
			targets = append(targets, w.composeTargets(arg.NamedChild(j), typeParams)...)
		}
	}
	return targets
}

// typeParamNames collects a type declaration's type-parameter names from its
// type_parameter_list (nil for non-generic declarations).
func typeParamNames(spec *sitter.Node, source []byte) map[string]bool {
	tpl := spec.ChildByFieldName("type_parameters")
	if tpl == nil {
		return nil
	}
	params := map[string]bool{}
	for i := uint(0); i < tpl.NamedChildCount(); i++ {
		pd := tpl.NamedChild(i)
		if pd == nil || pd.Kind() != "type_parameter_declaration" {
			continue
		}
		for j := uint(0); j < pd.NamedChildCount(); j++ {
			ch := pd.NamedChild(j)
			if ch != nil && ch.Kind() == "identifier" {
				params[extract.Text(ch, source)] = true
			}
		}
	}
	return params
}
