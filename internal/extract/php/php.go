// Package php is the dedicated Full-tier PHP extractor. It replaces the
// table-driven langspec registration with a walker that understands PHP's
// own shapes: statement-form namespaces scope the declarations that follow
// them (the known langspec limitation), `use` imports build a per-file
// alias table that qualifies inheritance and receiver types, trait `use`
// inside a class body becomes an includes edge, and method calls are
// receiver-aware - a typed receiver resolves to its class, an unresolved
// receiver falls back through the receiver/confidence law (decision 0003)
// instead of stamping a high-confidence guess.
//
// Laravel framework inference (facades, container bindings, Eloquent) is
// resolve/model-side work (internal/resolve, internal/model), not part of
// the extractor: this package stays a pure bytes-in, data-out walker.
package php

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
	"github.com/luuuc/sense/internal/model"
)

func init() { extract.Register(Extractor{}) }

// Extractor implements extract.Extractor for PHP.
type Extractor struct{}

// Grammar returns the vendored tree-sitter PHP grammar.
func (Extractor) Grammar() *sitter.Language { return grammars.PHP() }

// Language returns the language key ("php") this extractor registers under.
func (Extractor) Language() string { return "php" }

// Extensions returns the file extensions this extractor claims.
func (Extractor) Extensions() []string { return []string{".php"} }

// Tier reports PHP's support tier after the 32-01 promotion.
func (Extractor) Tier() extract.Tier { return extract.TierFull }

// HarvestsMentions reports that Extract streams the broad mention set for
// every file. PHP stays reasons-only for the dead verdict (ls_dynamic:
// reflection reaches private methods), so today the harvest adds caution
// only - but shipping it keeps the soundness gate honest ("harvested,
// nothing mentioned" vs "never harvested") per the language guide.
func (Extractor) HarvestsMentions() bool { return true }

// Extract walks one parsed PHP file and streams symbols, edges, the
// annotation harvest, and the mention harvest to emit.
func (Extractor) Extract(tree *sitter.Tree, source []byte, _ string, emit extract.Emitter) error {
	if tree == nil {
		return nil
	}
	w := &walker{
		source:     source,
		emit:       emit,
		uses:       map[string]string{},
		propTypes:  map[string]map[string]string{},
		parents:    map[string]string{},
		synthetics: map[string]bool{},
	}
	if err := w.program(tree.RootNode()); err != nil {
		return err
	}
	return w.emitMentions(tree.RootNode())
}

// walker holds one file's extraction state. A statement-form namespace
// mutates ns for the declarations that follow it; uses is the file's
// alias → fully-qualified-name import table; propTypes and parents feed
// receiver typing in calls.go.
type walker struct {
	source []byte
	emit   extract.Emitter
	ns     string
	// uses is the file's alias → fully-qualified-name table, fed by every
	// namespace_use_clause without distinguishing type/function/const use.
	// Its consumers put names in TYPE position only (inheritance, traits,
	// receiver types, scoped-call scopes), where a function/const alias can
	// never legitimately appear - keep that contract if facade resolution leans
	// harder on the table later.
	uses      map[string]string
	propTypes map[string]map[string]string // class qualified → property → resolved type
	parents   map[string]string            // class qualified → resolved extends target
	// synthetics dedupes the laravel-binding:* symbols per file (a provider
	// re-registering the same key must not emit the symbol twice).
	synthetics map[string]bool
}

// typeKinds maps a type-declaration node kind to the emitted symbol kind.
// A trait is emitted as a class (langspec parity): it is a reusable method
// bundle with no better fit in the shared kind set.
var typeKinds = map[string]model.SymbolKind{
	"class_declaration":     model.KindClass,
	"interface_declaration": model.KindInterface,
	"trait_declaration":     model.KindClass,
	"enum_declaration":      model.KindType,
}

// program walks the file's top level sequentially, so a statement-form
// `namespace App\Models;` scopes every declaration that follows it.
func (w *walker) program(root *sitter.Node) error {
	count := root.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := root.NamedChild(i)
		if child == nil {
			continue
		}
		if err := w.statement(child); err != nil {
			return err
		}
	}
	return nil
}

// statement dispatches one top-level (or block-nested) node.
func (w *walker) statement(n *sitter.Node) error {
	kind := n.Kind()
	if _, ok := typeKinds[kind]; ok {
		return w.handleType(n)
	}
	switch kind {
	case "namespace_definition":
		return w.handleNamespace(n)
	case "namespace_use_declaration":
		return w.handleUse(n)
	case "function_definition":
		return w.handleFunction(n)
	case "expression_statement":
		// Top-level code is real dispatch surface in Laravel - a routes file
		// is nothing but `Route::get(...)` statements. File-level calls
		// attribute to the namespace (mirroring file-level imports).
		return w.walkCalls(n, w.ns, map[string]string{}, "")
	default:
		// Recurse through wrappers (conditional declarations, blocks) so a
		// guarded `if (...) { class X {} }` still yields its symbols.
		count := n.NamedChildCount()
		for i := uint(0); i < count; i++ {
			child := n.NamedChild(i)
			if child == nil {
				continue
			}
			if err := w.statement(child); err != nil {
				return err
			}
		}
		return nil
	}
}

// handleNamespace emits the namespace's module symbol and scopes what it
// governs: a compound-form body walks under the namespace and restores the
// outer one; a statement form re-scopes the rest of the file.
func (w *walker) handleNamespace(n *sitter.Node) error {
	name := ""
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if child := n.NamedChild(i); child != nil && child.Kind() == "namespace_name" {
			name = extract.Text(child, w.source)
			break
		}
	}
	if name == "" {
		return nil
	}
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:       name,
		Qualified:  name,
		Kind:       model.KindModule,
		Visibility: "public",
		LineStart:  extract.Line(n.StartPosition()),
		LineEnd:    extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}
	if body := n.ChildByFieldName("body"); body != nil {
		outer := w.ns
		w.ns = name
		err := w.statement(body)
		w.ns = outer
		return err
	}
	w.ns = name
	return nil
}

// handleUse records each use-clause in the file's alias table and emits an
// imports edge per clause (`use A\B, C\D;` yields two edges - a superset of
// langspec, which kept only the first).
func (w *walker) handleUse(n *sitter.Node) error {
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		clause := n.NamedChild(i)
		if clause == nil || clause.Kind() != "namespace_use_clause" {
			continue
		}
		if err := w.handleUseClause(clause); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) handleUseClause(clause *sitter.Node) error {
	var fqn, alias string
	for i := uint(0); i < clause.NamedChildCount(); i++ {
		child := clause.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "qualified_name", "name", "namespace_name":
			if fqn == "" {
				fqn = strings.TrimPrefix(extract.Text(child, w.source), `\`)
			} else if child.Kind() == "name" {
				alias = extract.Text(child, w.source) // `as Alias`
			}
		}
	}
	if fqn == "" {
		return nil
	}
	if alias == "" {
		if idx := strings.LastIndex(fqn, `\`); idx >= 0 {
			alias = fqn[idx+1:]
		} else {
			alias = fqn
		}
	}
	w.uses[alias] = fqn
	line := extract.Line(clause.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: w.ns,
		TargetQualified: fqn,
		Kind:            model.EdgeImports,
		Line:            &line,
		Confidence:      extract.ConfidenceStatic,
	})
}

// handleType emits a class/interface/trait/enum symbol, its inheritance and
// trait-use edges, and walks its members.
func (w *walker) handleType(n *sitter.Node) error {
	name := extract.Text(n.ChildByFieldName("name"), w.source)
	if name == "" {
		return nil
	}
	qualified := w.qualify(name)
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            typeKinds[n.Kind()],
		Visibility:      "public",
		ParentQualified: w.ns,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}
	if err := w.emitAnnotated(n, name); err != nil {
		return err
	}
	if err := w.emitInheritance(n, qualified); err != nil {
		return err
	}
	if err := w.emitFacadeAccessor(n, qualified); err != nil {
		return err
	}
	if err := w.emitObservedBy(n, qualified); err != nil {
		return err
	}
	return w.walkBody(n, qualified)
}

// emitInheritance turns base_clause (extends) and class_interface_clause
// (implements) targets into inherits edges, alias/namespace-expanded. The
// first extends target is remembered for parent:: call resolution.
func (w *walker) emitInheritance(n *sitter.Node, qualified string) error {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		clause := n.NamedChild(i)
		if clause == nil {
			continue
		}
		kind := clause.Kind()
		if kind != "base_clause" && kind != "class_interface_clause" {
			continue
		}
		if err := w.emitInheritTargets(clause, qualified, kind == "base_clause"); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) emitInheritTargets(clause *sitter.Node, qualified string, isExtends bool) error {
	for i := uint(0); i < clause.NamedChildCount(); i++ {
		t := clause.NamedChild(i)
		if t == nil || (t.Kind() != "name" && t.Kind() != "qualified_name") {
			continue
		}
		target := w.resolveName(extract.Text(t, w.source))
		if target == "" {
			continue
		}
		if isExtends && w.parents[qualified] == "" {
			w.parents[qualified] = target
		}
		line := extract.Line(clause.StartPosition())
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: qualified,
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

// walkBody collects property types first (a method may precede the
// property it reads), then walks members.
func (w *walker) walkBody(n *sitter.Node, qualified string) error {
	body := n.ChildByFieldName("body")
	if body == nil {
		return nil
	}
	w.collectPropTypes(body, qualified)
	declared := declaredMethodNames(body, w.source)
	for i := uint(0); i < body.NamedChildCount(); i++ {
		member := body.NamedChild(i)
		if member == nil {
			continue
		}
		var err error
		switch member.Kind() {
		case "use_declaration":
			err = w.emitTraitUse(member, qualified)
		case "method_declaration":
			err = w.handleMethod(member, qualified, declared)
		case "property_declaration":
			err = w.emitPropertyDispatch(member, qualified)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// declaredMethodNames pre-scans a class body for its method names, so the
// scope-alias emission can tell a synthesized callable from a declared one.
func declaredMethodNames(body *sitter.Node, source []byte) map[string]bool {
	names := map[string]bool{}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		member := body.NamedChild(i)
		if member == nil || member.Kind() != "method_declaration" {
			continue
		}
		if name := extract.Text(member.ChildByFieldName("name"), source); name != "" {
			names[name] = true
		}
	}
	return names
}

// collectPropTypes records `private Logger $log;` style typed properties so
// `$this->log->info()` can resolve its receiver.
func (w *walker) collectPropTypes(body *sitter.Node, qualified string) {
	for i := uint(0); i < body.NamedChildCount(); i++ {
		member := body.NamedChild(i)
		if member == nil || member.Kind() != "property_declaration" {
			continue
		}
		typ := w.declaredType(member)
		if typ == "" {
			continue
		}
		_ = extract.WalkNamedDescendants(member, "variable_name", func(v *sitter.Node) error {
			if prop := extract.Text(v.NamedChild(0), w.source); prop != "" {
				w.setPropType(qualified, prop, typ)
			}
			return nil
		})
	}
}

func (w *walker) setPropType(class, prop, typ string) {
	if w.propTypes[class] == nil {
		w.propTypes[class] = map[string]string{}
	}
	w.propTypes[class][prop] = typ
}

// emitTraitUse turns `use Billable, Notifiable;` inside a class body into
// includes edges to the traits, alias/namespace-expanded.
func (w *walker) emitTraitUse(n *sitter.Node, qualified string) error {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		t := n.NamedChild(i)
		if t == nil || (t.Kind() != "name" && t.Kind() != "qualified_name") {
			continue
		}
		target := w.resolveName(extract.Text(t, w.source))
		if target == "" {
			continue
		}
		line := extract.Line(n.StartPosition())
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: qualified,
			TargetQualified: target,
			Kind:            model.EdgeIncludes,
			Line:            &line,
			Confidence:      extract.ConfidenceStatic,
		}); err != nil {
			return err
		}
	}
	return nil
}

// handleMethod emits a method symbol (visibility, static/instance dispatch
// kind) and walks its body for calls with the parameter type environment.
// declared is the class's pre-scanned method-name set (scope-alias guard).
func (w *walker) handleMethod(n *sitter.Node, classQualified string, declared map[string]bool) error {
	name := extract.Text(n.ChildByFieldName("name"), w.source)
	if name == "" {
		return nil
	}
	qualified := classQualified + `\` + name
	receiver := extract.ReceiverInstance
	if hasChildKind(n, "static_modifier") {
		receiver = extract.ReceiverSingleton
	}
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindMethod,
		Visibility:      w.visibility(n),
		Receiver:        receiver,
		ParentQualified: classQualified,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}
	if err := w.emitAnnotated(n, name); err != nil {
		return err
	}
	if err := w.emitScopeAlias(n, name, classQualified, declared); err != nil {
		return err
	}
	env := w.paramTypes(n, classQualified)
	return w.walkCalls(n.ChildByFieldName("body"), qualified, env, classQualified)
}

// handleFunction emits a top-level function symbol and walks its body.
func (w *walker) handleFunction(n *sitter.Node) error {
	name := extract.Text(n.ChildByFieldName("name"), w.source)
	if name == "" {
		return nil
	}
	qualified := w.qualify(name)
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindFunction,
		Visibility:      "public",
		ParentQualified: w.ns,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}
	if err := w.emitAnnotated(n, name); err != nil {
		return err
	}
	env := w.paramTypes(n, "")
	return w.walkCalls(n.ChildByFieldName("body"), qualified, env, "")
}

// paramTypes builds the `$var → resolved type` environment from typed
// parameters. A promoted constructor parameter (`private PayGateway
// $gateway`) additionally records the property type on the class.
func (w *walker) paramTypes(n *sitter.Node, classQualified string) map[string]string {
	env := map[string]string{}
	params := n.ChildByFieldName("parameters")
	if params == nil {
		return env
	}
	for i := uint(0); i < params.NamedChildCount(); i++ {
		p := params.NamedChild(i)
		if p == nil {
			continue
		}
		kind := p.Kind()
		if kind != "simple_parameter" && kind != "property_promotion_parameter" {
			continue
		}
		typ := w.declaredType(p)
		if typ == "" {
			continue
		}
		v := firstChildKind(p, "variable_name")
		if v == nil {
			continue
		}
		name := extract.Text(v.NamedChild(0), w.source)
		if name == "" {
			continue
		}
		env[name] = typ
		if kind == "property_promotion_parameter" && classQualified != "" {
			w.setPropType(classQualified, name, typ)
		}
	}
	return env
}

// declaredType reads a declaration's single class-type hint, resolved
// through the alias table and namespace. A union type is ambiguous and a
// primitive carries no class, so both yield "".
func (w *walker) declaredType(n *sitter.Node) string {
	typeNode := firstChildKind(n, "named_type", "optional_type")
	if typeNode == nil {
		return ""
	}
	if typeNode.Kind() == "optional_type" {
		typeNode = firstChildKind(typeNode, "named_type")
	}
	name := extract.Text(typeNode, w.source)
	name = strings.TrimPrefix(name, "?")
	switch strings.ToLower(name) {
	case "", "static", "self", "parent":
		return ""
	}
	return w.resolveName(name)
}

// visibility reads a member's visibility modifier; PHP defaults to public.
func (w *walker) visibility(n *sitter.Node) string {
	if v := firstChildKind(n, "visibility_modifier"); v != nil {
		return extract.Text(v, w.source)
	}
	return "public"
}

// emitAnnotated streams name when n carries a #[Attribute], feeding the
// same flat langspec_annotated set the dead-code voice read before the
// promotion - a framework may dispatch any attributed symbol with no
// source caller (ls_annotated), and that stays true off langspec.
func (w *walker) emitAnnotated(n *sitter.Node, name string) error {
	if !hasChildKind(n, "attribute_list") {
		return nil
	}
	he, ok := w.emit.(extract.LangspecHarvestEmitter)
	if !ok {
		return nil
	}
	return he.LangspecAnnotatedName(name)
}

// resolveName expands a written type/class name per PHP resolution: a
// leading backslash is fully qualified; a head matching a `use` alias
// expands through the import table; anything else is relative to the
// current namespace.
func (w *walker) resolveName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, `\`) {
		return strings.TrimPrefix(name, `\`)
	}
	head, rest, qualified := strings.Cut(name, `\`)
	if fqn, ok := w.uses[head]; ok {
		if qualified {
			return fqn + `\` + rest
		}
		return fqn
	}
	if w.ns != "" {
		return w.ns + `\` + name
	}
	return name
}

// qualify prefixes name with the current namespace.
func (w *walker) qualify(name string) string {
	if w.ns == "" {
		return name
	}
	return w.ns + `\` + name
}

// hasChildKind reports whether n has a direct named child of one of the
// given kinds.
func hasChildKind(n *sitter.Node, kinds ...string) bool {
	return firstChildKind(n, kinds...) != nil
}

// firstChildKind returns n's first direct named child whose kind is one of
// kinds, or nil. Safe on nil nodes.
func firstChildKind(n *sitter.Node, kinds ...string) *sitter.Node {
	if n == nil {
		return nil
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		for _, k := range kinds {
			if child.Kind() == k {
				return child
			}
		}
	}
	return nil
}
