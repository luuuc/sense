package tsjs

// frameworks.go isolates framework-idiom extraction from the
// language-core walk. Today that is Stimulus: controller classes named by
// file-path convention (app/javascript/controllers/**_controller.js) and
// their static `targets` / `outlets` declarations, which emit target
// symbols and outlet calls edges. Keeping it separate means the core
// symbol/edge walk in tsjs.go stays free of Rails-specific convention.

import (
	"path/filepath"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// ---- Stimulus controller inference ----

// handleStimulusClass handles anonymous (or oddly-named) default-export classes
// in Stimulus controller files. Uses the convention-derived name from the file path.
func (w *walker) handleStimulusClass(n *sitter.Node, _ []string) error {
	qualified := w.stimulusName

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:      qualified,
		Qualified: qualified,
		Kind:      model.KindClass,
		LineStart: extract.Line(n.StartPosition()),
		LineEnd:   extract.Line(n.EndPosition()),
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
	segments[last] += "Controller"
	return strings.Join(segments, "::")
}
