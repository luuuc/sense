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

	FuncTypes   []string
	ClassTypes  []string
	CallTypes   []string
	ImportTypes []string

	// InheritFields are field names on class nodes that hold superclass/interface
	// references (e.g., "superclass", "interfaces" for Java).
	InheritFields []string

	// InheritKinds are node kinds to search among direct children when inheritance
	// info has no field name (e.g., "base_list" for C#).
	InheritKinds []string

	NameField string

	CallNameFn func(node *sitter.Node, source []byte) string

	// VisibilityFn computes a func/class node's visibility ("public", "private",
	// "protected", "internal", or a grammar-specific token like "package") from
	// its access modifiers, or "" when the grammar offers no signal. Set per
	// grammar; nil leaves Visibility unset. The dead-code langspec voice treats
	// any non-"private" value (including unset "") as not-file-local and raises a
	// hand, so the only direction that risks a false `dead` is mislabeling a
	// reachable symbol "private" — which the per-grammar fns never do.
	VisibilityFn func(n *sitter.Node, source []byte) string

	// AnnotationKinds are the node kinds that represent an annotation/attribute on
	// a declaration (Java "marker_annotation"/"annotation", C#/PHP "attribute_list",
	// Kotlin/Scala "annotation"). Their presence as a direct child — or one level
	// inside a "modifiers" wrapper — marks the symbol annotated, which the langspec
	// voice reads (ls_annotated) because a framework's DI/reflection/test runner may
	// dispatch any annotated symbol with no source caller.
	AnnotationKinds []string

	// MentionKinds are the node kinds whose text is a bare-name mention for the
	// dead-code soundness harvest (e.g. "identifier", "type_identifier"). A
	// non-empty list opts this grammar into mention harvesting, which is the
	// prerequisite for any of its symbols to earn `dead`: the soundness gate proves
	// a name unreachable only against the harvested mention set. An empty list
	// means the language never harvests, so its symbols fail closed (core_no_harvest)
	// — the honest default for a language with no validated `dead` tier.
	MentionKinds []string
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
	if err := w.walk(tree.RootNode(), nil); err != nil {
		return err
	}
	return w.emitMentions(tree.RootNode())
}

// HarvestsMentions reports whether this grammar streams the broad mention set
// (see walker.emitMentions). It is true exactly when MentionKinds is configured,
// so the scan records the language as harvested and the dead-code soundness gate
// can reason about it. A grammar with no MentionKinds never harvests, so its
// symbols fail closed (core_no_harvest) rather than earn `dead` off an absent set.
func (g *genericExtractor) HarvestsMentions() bool { return len(g.spec.MentionKinds) > 0 }

// emitMentions streams every bare-name mention in the tree to the emitter when it
// is a MentionEmitter and this grammar opted in via MentionKinds. The project-wide
// union feeds the dead-code arbiter's soundness gate: a candidate earns `dead`
// only when its name is absent from this set, so a live-but-unbindable reference
// (an inherited bare call, a chain receiver, a reflectively-named string) still
// leaves a textual mention and keeps the symbol open-world. A grammar with no
// MentionKinds emits nothing.
func (w *walker) emitMentions(root *sitter.Node) error {
	if len(w.spec.MentionKinds) == 0 {
		return nil
	}
	me, ok := w.emit.(extract.MentionEmitter)
	if !ok {
		return nil
	}
	for _, name := range extract.HarvestMentions(root, w.source, w.mentionWalkSpec()) {
		if err := me.MentionName(name); err != nil {
			return err
		}
	}
	return nil
}

// mentionWalkSpec parameterises the shared mention harvest for this grammar: the
// configured MentionKinds carry a bare-name mention (read via extract.Text), and a
// definition's own name token is excluded so a symbol is never cancelled by its
// own declaration (otherwise no symbol could ever earn `dead`).
func (w *walker) mentionWalkSpec() extract.MentionWalkSpec {
	nameOf := make(map[string]func(*sitter.Node, []byte) string, len(w.spec.MentionKinds))
	for _, kind := range w.spec.MentionKinds {
		nameOf[kind] = extract.Text
	}
	return extract.MentionWalkSpec{
		NameOf:             nameOf,
		SkipDefinitionName: w.isDefinitionName,
	}
}

// isDefinitionName reports whether n is the NameField child of a func/class
// declaration — the symbol's own name token, excluded from the mention set. It is
// the generic analog of the Ruby/Python definition-name skip; it covers the
// grammars whose name is a direct NameField child (Java/C#/Kotlin/Scala/PHP). A
// grammar whose name hides inside a declarator (C/C++) is not matched here, which
// is harmless: those languages have no validated `dead` tier, so their mention
// gate is never the deciding factor.
func (w *walker) isDefinitionName(n *sitter.Node) bool {
	p := n.Parent()
	if p == nil {
		return false
	}
	if !w.isFunc(p.Kind()) && !w.isClass(p.Kind()) {
		return false
	}
	name := p.ChildByFieldName(w.spec.NameField)
	return name != nil && name.Equals(*n)
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
	switch {
	case strings.Contains(nk, "interface"):
		symKind = model.KindInterface
	case strings.Contains(nk, "namespace") || strings.Contains(nk, "module"):
		symKind = model.KindModule
	case strings.Contains(nk, "enum"):
		symKind = model.KindType
	}

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            symKind,
		Visibility:      w.visibility(n),
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}

	if err := w.emitAnnotation(n, name); err != nil {
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
		Visibility:      w.visibility(n),
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}

	if err := w.emitAnnotation(n, name); err != nil {
		return err
	}

	return w.emitCalls(n, qualified)
}

func (w *walker) handleImport(n *sitter.Node, scope []string) error {
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

// importTarget resolves an import node to its target path by trying ordered,
// grammar-agnostic strategies and returning the first non-empty result. Each
// strategy is a generic probe — a field name, a set of child node kinds — never a
// per-grammar special case, so all seven standard-tier grammars share one path.
func (w *walker) importTarget(n *sitter.Node) string {
	if t := w.importFromPathField(n); t != "" {
		return t
	}
	if t := w.importFromChildLiteral(n); t != "" {
		return t
	}
	if t := w.importFromNameField(n); t != "" {
		return t
	}
	return w.importFromBareIdentifiers(n)
}

// importFromPathField reads an explicit import-path field (path/source/
// module_name). A bare identifier is skipped: it may be only the first component
// of a split path (e.g., Scala), which importFromBareIdentifiers joins instead.
func (w *walker) importFromPathField(n *sitter.Node) string {
	for _, field := range []string{"path", "source", "module_name"} {
		child := n.ChildByFieldName(field)
		if child == nil {
			continue
		}
		if ck := child.Kind(); ck == "identifier" || ck == "simple_identifier" {
			continue
		}
		if text := strings.Trim(extract.Text(child, w.source), "\"'`"); text != "" {
			return text
		}
	}
	return ""
}

// importFromChildLiteral scans named children for a string literal or a compound
// identifier (scoped/dotted/qualified name), returning the first in child order
// so a path appearing before a sibling token wins.
func (w *walker) importFromChildLiteral(n *sitter.Node) string {
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "string", "string_literal", "interpreted_string_literal":
			if text := strings.Trim(extract.Text(child, w.source), "\"'`"); text != "" {
				return text
			}
		case "scoped_identifier", "dotted_name", "qualified_identifier",
			"qualified_name", "stable_identifier", "namespace_use_clause",
			"namespace_name", "import_prefix":
			if text := extract.Text(child, w.source); text != "" {
				return text
			}
		}
	}
	return ""
}

// importFromNameField reads the generic "name" field as a fallback for grammars
// that expose the import path there.
func (w *walker) importFromNameField(n *sitter.Node) string {
	child := n.ChildByFieldName("name")
	if child == nil {
		return ""
	}
	return strings.Trim(extract.Text(child, w.source), "\"'`")
}

// importFromBareIdentifiers joins bare identifier children with the grammar's
// separator, handling grammars that split an import path across sibling nodes
// (e.g., Scala's `import a.b.c`).
func (w *walker) importFromBareIdentifiers(n *sitter.Node) string {
	var parts []string
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		if k := child.Kind(); k == "identifier" || k == "simple_identifier" {
			parts = append(parts, extract.Text(child, w.source))
		}
	}
	return strings.Join(parts, w.spec.Separator)
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

// visibility returns the access visibility of a func/class node via the
// grammar's VisibilityFn, or "" when the grammar configures none.
func (w *walker) visibility(n *sitter.Node) string {
	if w.spec.VisibilityFn == nil {
		return ""
	}
	return w.spec.VisibilityFn(n, w.source)
}

// emitAnnotation streams name to the LangspecHarvestEmitter when n carries an
// annotation/attribute (per AnnotationKinds) and the emitter accepts it. A
// framework's DI/reflection/test runner may dispatch any annotated symbol with no
// source caller, so the langspec voice keeps such a name open-world (ls_annotated).
// An Emitter that does not implement the extension, or a grammar with no
// AnnotationKinds, is a no-op.
func (w *walker) emitAnnotation(n *sitter.Node, name string) error {
	if name == "" || len(w.spec.AnnotationKinds) == 0 || !w.hasAnnotation(n) {
		return nil
	}
	he, ok := w.emit.(extract.LangspecHarvestEmitter)
	if !ok {
		return nil
	}
	return he.LangspecAnnotatedName(name)
}

// hasAnnotation reports whether n carries an annotation/attribute, checking its
// direct children and one level inside a "modifiers" wrapper (Java/Kotlin/Scala
// hold annotations there; C#/PHP carry an attribute_list as a direct child).
func (w *walker) hasAnnotation(n *sitter.Node) bool {
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		if slices.Contains(w.spec.AnnotationKinds, child.Kind()) {
			return true
		}
		if child.Kind() == "modifiers" {
			mc := child.NamedChildCount()
			for j := uint(0); j < mc; j++ {
				gc := child.NamedChild(j)
				if gc != nil && slices.Contains(w.spec.AnnotationKinds, gc.Kind()) {
					return true
				}
			}
		}
	}
	return false
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
