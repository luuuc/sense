package python

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// This file holds type-annotation composes extraction: turning a class
// attribute's annotation (`order: Order`, `items: list[Item]`, `owner:
// Optional[User]`) into a composes edge to the referenced class. It serves
// Django models, Pydantic/dataclass models, and FastAPI signatures alike — the
// PascalCase convention, not a framework marker, decides what counts as a class.

// pythonPrimitives is the set of built-in type names that should NOT
// produce composes edges from type annotations.
var pythonPrimitives = map[string]bool{
	"int": true, "str": true, "float": true, "bool": true,
	"bytes": true, "None": true, "object": true, "type": true,
	"complex": true,
}

// pythonGenericWrappers are type constructors where we unwrap one level
// to find the inner class reference. Limited to the types that commonly
// appear in Django, FastAPI, and Pydantic codebases.
var pythonGenericWrappers = map[string]bool{
	"Optional": true, "Union": true,
	"list": true, "List": true,
	"set": true, "Set": true,
	"tuple": true, "Tuple": true,
	"dict": true, "Dict": true,
	"Sequence": true, "Type": true,
	"ClassVar": true,
}

// emitTypeAnnotationEdge extracts a composes edge from a type annotation node,
// dispatching on the node kind. It handles plain identifiers, generic types
// (Optional[X], list[X], Union[X, Y], dict[str, X]), dotted attributes, and the
// `type` wrapper node; primitives are skipped.
func (w *walker) emitTypeAnnotationEdge(typeNode *sitter.Node, ownerQualified string, line int) error {
	if typeNode == nil {
		return nil
	}
	switch typeNode.Kind() {
	case "type":
		if typeNode.NamedChildCount() > 0 {
			return w.emitTypeAnnotationEdge(typeNode.NamedChild(0), ownerQualified, line)
		}
	case "identifier":
		return w.emitIdentifierAnnotation(typeNode, ownerQualified, line)
	case "generic_type":
		return w.emitGenericAnnotation(typeNode, ownerQualified, line)
	case "attribute":
		return w.emitAttributeAnnotation(typeNode, ownerQualified, line)
	}
	return nil
}

// emitIdentifierAnnotation emits a composes edge for a bare class annotation,
// skipping primitives, generic wrappers, and non-PascalCase (non-class) names.
func (w *walker) emitIdentifierAnnotation(typeNode *sitter.Node, ownerQualified string, line int) error {
	name := extract.Text(typeNode, w.source)
	if name == "" || pythonPrimitives[name] || pythonGenericWrappers[name] {
		return nil
	}
	if !isPascalCase(name) {
		return nil
	}
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: ownerQualified,
		TargetQualified: name,
		Kind:            model.EdgeComposes,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}

// emitGenericAnnotation handles a generic annotation: a known wrapper
// (Optional[X], list[X], …) unwraps to its type parameters, while any other
// PascalCase outer name composes directly.
func (w *walker) emitGenericAnnotation(typeNode *sitter.Node, ownerQualified string, line int) error {
	outerNode := typeNode.ChildByFieldName("type")
	if outerNode == nil {
		if typeNode.NamedChildCount() > 0 {
			outerNode = typeNode.NamedChild(0)
		}
	}
	if outerNode == nil {
		return nil
	}
	outerName := extract.Text(outerNode, w.source)
	if pythonGenericWrappers[outerName] {
		return w.emitTypeParamEdges(typeNode, ownerQualified, line)
	}
	if isPascalCase(outerName) && !pythonPrimitives[outerName] {
		return w.emit.Edge(extract.EmittedEdge{
			SourceQualified: ownerQualified,
			TargetQualified: outerName,
			Kind:            model.EdgeComposes,
			Line:            &line,
			Confidence:      extract.ConfidenceConvention,
		})
	}
	return nil
}

// emitAttributeAnnotation emits a composes edge for a dotted annotation
// (`module.Class`), using the full attribute text as the target.
func (w *walker) emitAttributeAnnotation(typeNode *sitter.Node, ownerQualified string, line int) error {
	name := extract.Text(typeNode, w.source)
	if name == "" {
		return nil
	}
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: ownerQualified,
		TargetQualified: name,
		Kind:            model.EdgeComposes,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}

// emitTypeParamEdges unwraps one level of generic type parameters and
// emits composes edges for each inner type that references a class.
func (w *walker) emitTypeParamEdges(genericType *sitter.Node, ownerQualified string, line int) error {
	count := genericType.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := genericType.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() == "type_parameter" {
			innerCount := child.NamedChildCount()
			for j := uint(0); j < innerCount; j++ {
				inner := child.NamedChild(j)
				if inner != nil {
					if err := w.emitTypeAnnotationEdge(inner, ownerQualified, line); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
