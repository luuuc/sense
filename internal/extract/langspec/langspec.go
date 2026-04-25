// Package langspec provides a table-driven generic extractor for languages
// whose AST shapes fit common patterns. A langSpec describes one language's
// node kinds; New() turns it into an extract.Extractor registered via init().
package langspec

import (
	"slices"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

type langSpec struct {
	Name      string
	Exts      []string
	Grammar   *sitter.Language
	Tier      extract.Tier
	Separator string

	FuncTypes  []string
	ClassTypes []string
	CallTypes  []string
	ImportTypes []string

	// InheritFields are field names on class nodes that hold superclass/interface
	// references (e.g., "superclass", "interfaces" for Java).
	InheritFields []string

	// InheritKinds are node kinds to search among direct children when inheritance
	// info has no field name (e.g., "base_list" for C#).
	InheritKinds []string

	NameField string

	CallNameFn func(node *sitter.Node, source []byte) string
}

type genericExtractor struct {
	spec langSpec
}

func New(spec langSpec) extract.Extractor {
	if spec.Grammar == nil {
		panic("langspec: Grammar must not be nil for language " + spec.Name)
	}
	if spec.Name == "" {
		panic("langspec: Name must not be empty")
	}
	if len(spec.Exts) == 0 {
		panic("langspec: Exts must not be empty for language " + spec.Name)
	}
	if spec.NameField == "" {
		spec.NameField = "name"
	}
	if spec.Separator == "" {
		spec.Separator = "."
	}
	return &genericExtractor{spec: spec}
}

func (g *genericExtractor) Grammar() *sitter.Language { return g.spec.Grammar }
func (g *genericExtractor) Language() string          { return g.spec.Name }
func (g *genericExtractor) Extensions() []string      { return g.spec.Exts }
func (g *genericExtractor) Tier() extract.Tier        { return g.spec.Tier }

func (g *genericExtractor) Extract(tree *sitter.Tree, source []byte, _ string, emit extract.Emitter) error {
	if tree == nil {
		return nil
	}
	w := &walker{
		spec:   &g.spec,
		source: source,
		emit:   emit,
	}
	return w.walk(tree.RootNode(), nil)
}

type walker struct {
	spec   *langSpec
	source []byte
	emit   extract.Emitter
}

func (w *walker) walk(n *sitter.Node, scope []string) error {
	if n == nil {
		return nil
	}
	kind := n.Kind()

	if w.isClass(kind) {
		return w.handleClass(n, scope)
	}
	if w.isFunc(kind) {
		return w.handleFunc(n, scope)
	}
	if w.isImport(kind) {
		return w.handleImport(n, scope)
	}

	return w.walkChildren(n, scope)
}

func (w *walker) walkChildren(n *sitter.Node, scope []string) error {
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		if err := w.walk(child, scope); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) handleClass(n *sitter.Node, scope []string) error {
	name := w.nodeName(n)
	if name == "" {
		return w.walkChildren(n, scope)
	}

	newScope := append(slices.Clone(scope), name)
	qualified := w.qualify(newScope)
	parent := w.qualify(scope)

	symKind := model.KindClass
	nk := n.Kind()
	if strings.Contains(nk, "interface") {
		symKind = model.KindInterface
	} else if strings.Contains(nk, "namespace") || strings.Contains(nk, "module") {
		symKind = model.KindModule
	} else if strings.Contains(nk, "enum") {
		symKind = model.KindType
	}

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            symKind,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}

	if len(w.spec.InheritFields) > 0 || len(w.spec.InheritKinds) > 0 {
		if err := w.emitInheritance(n, qualified); err != nil {
			return err
		}
	}

	return w.walkChildren(n, newScope)
}

func (w *walker) handleFunc(n *sitter.Node, scope []string) error {
	name := w.nodeName(n)
	if name == "" {
		return nil
	}

	qualified := w.qualify(append(slices.Clone(scope), name))
	parent := w.qualify(scope)

	kind := model.KindFunction
	if parent != "" {
		kind = model.KindMethod
	}

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            kind,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}

	return w.emitCalls(n, qualified)
}

func (w *walker) handleImport(n *sitter.Node, scope []string) error {
	text := strings.TrimSpace(extract.Text(n, w.source))
	if text == "" {
		return nil
	}

	// Try to extract a meaningful import path from the node.
	target := w.importTarget(n)
	if target == "" {
		return nil
	}

	line := extract.Line(n.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: w.qualify(scope),
		TargetQualified: target,
		Kind:            model.EdgeImports,
		Line:            &line,
		Confidence:      extract.ConfidenceStatic,
	})
}

func (w *walker) emitInheritance(n *sitter.Node, sourceQualified string) error {
	// Check field-based inheritance (e.g., Java "superclass", "interfaces").
	for _, field := range w.spec.InheritFields {
		inheritNode := n.ChildByFieldName(field)
		if inheritNode == nil {
			continue
		}
		if err := w.emitInheritTargets(inheritNode, sourceQualified); err != nil {
			return err
		}
	}
	// Check kind-based inheritance (e.g., C# "base_list").
	if len(w.spec.InheritKinds) > 0 {
		count := n.NamedChildCount()
		for i := uint(0); i < count; i++ {
			child := n.NamedChild(i)
			if child != nil && slices.Contains(w.spec.InheritKinds, child.Kind()) {
				if err := w.emitInheritTargets(child, sourceQualified); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (w *walker) emitInheritTargets(inheritNode *sitter.Node, sourceQualified string) error {
	targets := w.inheritTargets(inheritNode)
	for _, target := range targets {
		if target == "" {
			continue
		}
		line := extract.Line(inheritNode.StartPosition())
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: sourceQualified,
			TargetQualified: target,
			Kind:            model.EdgeInherits,
			Line:            &line,
			Confidence:      extract.ConfidenceStatic,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) inheritTargets(n *sitter.Node) []string {
	text := strings.TrimSpace(extract.Text(n, w.source))
	if text == "" {
		return nil
	}

	if isTypeKind(n.Kind()) {
		return []string{w.cleanTypeName(text)}
	}

	// Recursively collect type-like descendants, skipping argument
	// and modifier nodes that contain identifiers we don't want.
	var targets []string
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		if isInheritNoise(child.Kind()) {
			continue
		}
		targets = append(targets, w.inheritTargets(child)...)
	}
	if len(targets) > 0 {
		return targets
	}

	return []string{w.cleanTypeName(text)}
}

func isTypeKind(kind string) bool {
	switch kind {
	case "type_identifier", "identifier", "scoped_identifier",
		"generic_type", "scoped_type_identifier", "member_expression",
		"qualified_identifier", "simple_type", "user_type",
		"simple_identifier", "name":
		return true
	}
	return false
}

func isInheritNoise(kind string) bool {
	switch kind {
	case "access_specifier", "visibility_modifier",
		"arguments", "argument_list", "value_arguments", "call_suffix",
		"modifiers", "annotation":
		return true
	}
	return false
}

func (w *walker) cleanTypeName(text string) string {
	for _, delim := range []string{"<", "[", "("} {
		if idx := strings.Index(text, delim); idx > 0 {
			text = text[:idx]
		}
	}
	return strings.TrimSpace(text)
}

func (w *walker) emitCalls(funcNode *sitter.Node, sourceQualified string) error {
	if len(w.spec.CallTypes) == 0 {
		return nil
	}
	return w.walkForCalls(funcNode, sourceQualified)
}

func (w *walker) walkForCalls(n *sitter.Node, sourceQualified string) error {
	if n == nil {
		return nil
	}
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		if w.isFunc(child.Kind()) || w.isClass(child.Kind()) {
			continue
		}
		if w.isCall(child.Kind()) {
			if err := w.emitOneCall(child, sourceQualified); err != nil {
				return err
			}
		}
		if err := w.walkForCalls(child, sourceQualified); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) emitOneCall(n *sitter.Node, sourceQualified string) error {
	var target string

	if w.spec.CallNameFn != nil {
		target = w.spec.CallNameFn(n, w.source)
	} else {
		target = w.callTarget(n)
	}

	if target == "" {
		return nil
	}

	line := extract.Line(n.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: sourceQualified,
		TargetQualified: target,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceStatic,
	})
}

func (w *walker) callTarget(n *sitter.Node) string {
	if fn := n.ChildByFieldName("function"); fn != nil {
		return strings.TrimSpace(extract.Text(fn, w.source))
	}
	if name := n.ChildByFieldName(w.spec.NameField); name != nil {
		text := strings.TrimSpace(extract.Text(name, w.source))
		if recv := w.callReceiver(n); recv != "" {
			return recv + "." + text
		}
		return text
	}
	if method := n.ChildByFieldName("method"); method != nil {
		text := strings.TrimSpace(extract.Text(method, w.source))
		if recv := w.callReceiver(n); recv != "" {
			return recv + "." + text
		}
		return text
	}
	return ""
}

func (w *walker) callReceiver(n *sitter.Node) string {
	for _, field := range []string{"object", "scope"} {
		if r := n.ChildByFieldName(field); r != nil {
			text := strings.TrimSpace(extract.Text(r, w.source))
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func (w *walker) importTarget(n *sitter.Node) string {
	// Try specific import path fields. Skip bare identifiers — they may be
	// only the first component of a split path (e.g., Scala imports).
	for _, field := range []string{"path", "source", "module_name"} {
		child := n.ChildByFieldName(field)
		if child == nil {
			continue
		}
		ck := child.Kind()
		if ck == "identifier" || ck == "simple_identifier" {
			continue
		}
		text := strings.Trim(extract.Text(child, w.source), "\"'`")
		if text != "" {
			return text
		}
	}

	// Scan children for compound identifiers or string literals.
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		kind := child.Kind()
		if kind == "string" || kind == "string_literal" || kind == "interpreted_string_literal" {
			text := strings.Trim(extract.Text(child, w.source), "\"'`")
			if text != "" {
				return text
			}
		}
		if kind == "scoped_identifier" || kind == "dotted_name" ||
			kind == "qualified_identifier" || kind == "qualified_name" ||
			kind == "stable_identifier" || kind == "namespace_use_clause" ||
			kind == "namespace_name" || kind == "import_prefix" {
			text := extract.Text(child, w.source)
			if text != "" {
				return text
			}
		}
	}

	// Try the generic "name" field as a fallback.
	if child := n.ChildByFieldName("name"); child != nil {
		text := strings.Trim(extract.Text(child, w.source), "\"'`")
		if text != "" {
			return text
		}
	}

	// Collect bare identifier children and join — handles grammars that
	// split import paths across sibling nodes (e.g., Scala).
	var parts []string
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		kind := child.Kind()
		if kind == "identifier" || kind == "simple_identifier" {
			parts = append(parts, extract.Text(child, w.source))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, w.spec.Separator)
	}

	return ""
}

func (w *walker) nodeName(n *sitter.Node) string {
	nameNode := n.ChildByFieldName(w.spec.NameField)
	if nameNode != nil {
		return extract.Text(nameNode, w.source)
	}

	if decl := n.ChildByFieldName("declarator"); decl != nil {
		return w.extractDeclaratorName(decl)
	}

	// Fallback: scan children by kind for name-like nodes.
	// Handles grammars without field names (e.g., fwcd/tree-sitter-kotlin).
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "type_identifier", "simple_identifier", "identifier":
			return extract.Text(child, w.source)
		}
	}

	return ""
}

func (w *walker) extractDeclaratorName(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	switch n.Kind() {
	case "identifier", "type_identifier", "field_identifier":
		return extract.Text(n, w.source)
	case "pointer_declarator", "reference_declarator", "function_declarator":
		if inner := n.ChildByFieldName("declarator"); inner != nil {
			return w.extractDeclaratorName(inner)
		}
	case "parenthesized_declarator":
		if n.NamedChildCount() > 0 {
			return w.extractDeclaratorName(n.NamedChild(0))
		}
	}
	return ""
}

func (w *walker) qualify(scope []string) string {
	return strings.Join(scope, w.spec.Separator)
}

func (w *walker) isClass(kind string) bool {
	return slices.Contains(w.spec.ClassTypes, kind)
}

func (w *walker) isFunc(kind string) bool {
	return slices.Contains(w.spec.FuncTypes, kind)
}

func (w *walker) isCall(kind string) bool {
	return slices.Contains(w.spec.CallTypes, kind)
}

func (w *walker) isImport(kind string) bool {
	return slices.Contains(w.spec.ImportTypes, kind)
}
