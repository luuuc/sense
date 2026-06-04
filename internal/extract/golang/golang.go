// Package golang extracts Tier-Basic symbols and intra-file edges from
// Go source via tree-sitter-go. Directory is named `golang` because
// `package go` is illegal (Go reserved keyword); the exported
// Extractor type stays idiomatic.
//
// Qualification includes the package name (per 05-languages.md's
// `pkg.Type.Method` convention): Go's package boundary is always
// visible in a single file (the `package` clause), and every symbol's
// fully-qualified name in the Go ecosystem is package-prefixed. Other
// languages leave the package implicit because a single file doesn't
// always declare one (Python modules, JS files with no export, etc.).
//
// Symbol kinds emitted:
//   - struct type           → KindClass
//   - interface type        → KindInterface
//   - type alias / named    → KindType
//   - func at package scope → KindFunction  (no receiver)
//   - func with receiver    → KindMethod
//   - const NAME = …        → KindConstant  (any case; Go has no
//     all-caps convention)
//
// Calls edges:
//   - Function / method bodies are walked for call_expression nodes.
//     The target is the callee's surface text as written — a bare
//     `name`, `pkg.Func`, or `recv.Method`. Cross-file / cross-package
//     resolution is 01-03's resolver job, not the extractor's.
//
// What Tier-Basic skips:
//   - vars (package-level `var` bindings) — pitch explicitly scopes
//     to "constants". Full-tier can revisit.
//   - fields inside structs and interface method signatures.
//   - embedded types → composes edges (01-03 territory when the
//     full graph is needed).
//   - imports (01-03).
package golang

import (
	"unicode"
	"unicode/utf8"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
	"github.com/luuuc/sense/internal/model"
)

// Extractor is the Go implementation of extract.Extractor.
type Extractor struct{}

func (Extractor) Grammar() *sitter.Language { return grammars.Go() }
func (Extractor) Language() string          { return "go" }
func (Extractor) Extensions() []string      { return []string{".go"} }
func (Extractor) Tier() extract.Tier        { return extract.TierBasic }

func (Extractor) Extract(tree *sitter.Tree, source []byte, _ string, emit extract.Emitter) error {
	w := &walker{
		source:      source,
		emit:        emit,
		pkg:         packageName(tree.RootNode(), source),
		pkgBindings: map[string]string{},
	}
	if err := w.walkTopLevel(tree.RootNode()); err != nil {
		return err
	}
	return emitHarvest(tree.RootNode(), source, emit)
}

func init() { extract.Register(Extractor{}) }

// ---- walker ----

type walker struct {
	source      []byte
	emit        extract.Emitter
	pkg         string            // package name, used to prefix every qualified name
	pkgBindings map[string]string // unqualified name → qualified name for package-level consts and vars
}

// walkTopLevel iterates the direct named children of source_file in
// two passes. Pass 1 collects package-level constant names so that
// pass 2 can emit references edges when function bodies use them.
func (w *walker) walkTopLevel(root *sitter.Node) error {
	if root == nil {
		return nil
	}

	// Pass 1: collect package-level constant and variable names
	// before processing function bodies so references resolve
	// regardless of declaration order.
	count := root.NamedChildCount()
	for i := uint(0); i < count; i++ {
		n := root.NamedChild(i)
		if n == nil {
			continue
		}
		switch n.Kind() {
		case "const_declaration":
			w.collectDeclNames(n, "const_spec")
		case "var_declaration":
			w.collectDeclNames(n, "var_spec")
		}
	}

	// Pass 2: emit symbols and edges.
	for i := uint(0); i < count; i++ {
		n := root.NamedChild(i)
		if n == nil {
			continue
		}
		var err error
		switch n.Kind() {
		case "const_declaration":
			err = w.handleConstDeclaration(n)
		case "var_declaration":
			err = w.handleVarDeclaration(n)
		case "type_declaration":
			err = w.handleTypeDeclaration(n)
		case "function_declaration":
			err = w.handleFunction(n)
		case "method_declaration":
			err = w.handleMethod(n)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// collectDeclNames populates w.pkgBindings with unqualified→qualified
// mappings for every name in a const or var declaration.
func (w *walker) collectDeclNames(n *sitter.Node, specKind string) {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case specKind:
			w.collectSpecNames(child)
		case specKind + "_list":
			for j := uint(0); j < child.NamedChildCount(); j++ {
				spec := child.NamedChild(j)
				if spec != nil && spec.Kind() == specKind {
					w.collectSpecNames(spec)
				}
			}
		}
	}
}

func (w *walker) collectSpecNames(spec *sitter.Node) {
	for i := uint(0); i < spec.NamedChildCount(); i++ {
		c := spec.NamedChild(i)
		if c == nil || c.Kind() != "identifier" {
			continue
		}
		name := extract.Text(c, w.source)
		if name != "" && name != "_" {
			w.pkgBindings[name] = w.qualify(name)
		}
	}
}

// handleConstDeclaration walks const_spec children. Both the grouped
// form (`const ( A = 1; B = 2 )`) and the single form (`const A = 1`)
// produce const_spec children.
func (w *walker) handleConstDeclaration(n *sitter.Node) error {
	doc := docstringFor(n, w.source)
	for i := uint(0); i < n.NamedChildCount(); i++ {
		spec := n.NamedChild(i)
		if spec == nil || spec.Kind() != "const_spec" {
			continue
		}
		if err := w.emitConstSpec(spec, doc); err != nil {
			return err
		}
	}
	return nil
}

// emitConstSpec emits one Symbol per name in a const_spec. Specs like
// `const A, B = 1, 2` declare multiple names simultaneously — each
// becomes its own symbol and shares the declaration-level docstring.
func (w *walker) emitConstSpec(spec *sitter.Node, doc string) error {
	// `name` is a repeated field on const_spec; ChildrenByFieldName
	// needs a cursor, so iterate manually by field-name match.
	for i := uint(0); i < spec.NamedChildCount(); i++ {
		c := spec.NamedChild(i)
		if c == nil || c.Kind() != "identifier" {
			continue
		}
		name := extract.Text(c, w.source)
		// `const _ = …` is a blank declaration, not a named symbol; never emit it
		// (it would otherwise read as a zero-edge dead candidate the Go voice can't
		// vouch for, yet `staticcheck` never flags a blank).
		if name == "" || name == "_" {
			continue
		}
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:       name,
			Qualified:  w.qualify(name),
			Kind:       model.KindConstant,
			Visibility: visibility(name),
			LineStart:  extract.Line(spec.StartPosition()),
			LineEnd:    extract.Line(spec.EndPosition()),
			Docstring:  doc,
		}); err != nil {
			return err
		}
	}
	return nil
}

// handleVarDeclaration walks var_spec children and emits each
// package-level variable as a KindConstant symbol (the model has no
// separate variable kind; for dead code purposes they behave identically).
func (w *walker) handleVarDeclaration(n *sitter.Node) error {
	doc := docstringFor(n, w.source)
	for i := uint(0); i < n.NamedChildCount(); i++ {
		spec := n.NamedChild(i)
		if spec == nil || spec.Kind() != "var_spec" {
			continue
		}
		for j := uint(0); j < spec.NamedChildCount(); j++ {
			c := spec.NamedChild(j)
			if c == nil || c.Kind() != "identifier" {
				continue
			}
			name := extract.Text(c, w.source)
			// `var _ = …` (and the common `var _ Iface = (*T)(nil)` assertion) is a
			// blank declaration, not a named symbol; never emit it.
			if name == "" || name == "_" {
				continue
			}
			if err := w.emit.Symbol(extract.EmittedSymbol{
				Name:       name,
				Qualified:  w.qualify(name),
				Kind:       model.KindConstant,
				Visibility: visibility(name),
				LineStart:  extract.Line(spec.StartPosition()),
				LineEnd:    extract.Line(spec.EndPosition()),
				Docstring:  doc,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// handleTypeDeclaration walks type_spec / type_alias children. Both
// forms classify by the inner `type` field's kind.
func (w *walker) handleTypeDeclaration(n *sitter.Node) error {
	doc := docstringFor(n, w.source)
	for i := uint(0); i < n.NamedChildCount(); i++ {
		spec := n.NamedChild(i)
		if spec == nil {
			continue
		}
		switch spec.Kind() {
		case "type_spec":
			if err := w.emitTypeSpec(spec, false, doc); err != nil {
				return err
			}
		case "type_alias":
			if err := w.emitTypeSpec(spec, true, doc); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *walker) emitTypeSpec(spec *sitter.Node, isAlias bool, doc string) error {
	nameNode := spec.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}

	kind := model.KindType
	var structNode, ifaceNode *sitter.Node
	if !isAlias {
		if t := spec.ChildByFieldName("type"); t != nil {
			switch t.Kind() {
			case "struct_type":
				kind = model.KindClass
				structNode = t
			case "interface_type":
				kind = model.KindInterface
				ifaceNode = t
			}
		}
	}

	qualified := w.qualify(name)
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:       name,
		Qualified:  qualified,
		Kind:       kind,
		Visibility: visibility(name),
		LineStart:  extract.Line(spec.StartPosition()),
		LineEnd:    extract.Line(spec.EndPosition()),
		Docstring:  doc,
	}); err != nil {
		return err
	}

	if structNode != nil {
		if err := w.emitEmbeddings(structNode, qualified); err != nil {
			return err
		}
	}
	if ifaceNode != nil {
		if err := w.emitInterfaceMethods(ifaceNode, qualified); err != nil {
			return err
		}
	}
	return nil
}

// emitInterfaceMethods walks an interface_type's method_elem children
// and emits each as a KindMethod symbol parented to the interface.
func (w *walker) emitInterfaceMethods(ifaceNode *sitter.Node, ifaceQualified string) error {
	for i := uint(0); i < ifaceNode.NamedChildCount(); i++ {
		me := ifaceNode.NamedChild(i)
		if me == nil || me.Kind() != "method_elem" {
			continue
		}
		nameNode := me.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := extract.Text(nameNode, w.source)
		if name == "" {
			continue
		}
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:            name,
			Qualified:       ifaceQualified + "." + name,
			Kind:            model.KindMethod,
			Visibility:      visibility(name),
			ParentQualified: ifaceQualified,
			LineStart:       extract.Line(me.StartPosition()),
			LineEnd:         extract.Line(me.EndPosition()),
			Docstring:       docstringFor(me, w.source),
		}); err != nil {
			return err
		}
	}
	return nil
}

// emitEmbeddings walks a struct_type's field declarations and emits
// includes edges for embedded fields (fields with no explicit name).
func (w *walker) emitEmbeddings(structNode *sitter.Node, structQualified string) error {
	fdl := structNode.NamedChild(0)
	if fdl == nil || fdl.Kind() != "field_declaration_list" {
		return nil
	}
	for i := uint(0); i < fdl.NamedChildCount(); i++ {
		fd := fdl.NamedChild(i)
		if fd == nil || fd.Kind() != "field_declaration" {
			continue
		}
		if fd.ChildByFieldName("name") != nil {
			continue
		}
		typeNode := fd.ChildByFieldName("type")
		if typeNode == nil {
			continue
		}
		var target string
		switch typeNode.Kind() {
		case "type_identifier":
			target = w.qualify(extract.Text(typeNode, w.source))
		case "qualified_type":
			target = extract.Text(typeNode, w.source)
		case "generic_type":
			if base := typeNode.ChildByFieldName("type"); base != nil {
				target = w.qualify(extract.Text(base, w.source))
			}
		default:
			continue
		}
		if target == "" {
			continue
		}
		line := extract.Line(fd.StartPosition())
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: structQualified,
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

// handleFunction emits a top-level function (no receiver) and walks
// its body for call expressions.
func (w *walker) handleFunction(n *sitter.Node) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	qualified := w.qualify(name)
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:       name,
		Qualified:  qualified,
		Kind:       model.KindFunction,
		Visibility: visibility(name),
		LineStart:  extract.Line(n.StartPosition()),
		LineEnd:    extract.Line(n.EndPosition()),
		Docstring:  docstringFor(n, w.source),
	}); err != nil {
		return err
	}
	body := n.ChildByFieldName("body")
	types, locals := w.buildTypeMap(n)
	if err := extract.WalkNamedDescendants(body, "call_expression", func(c *sitter.Node) error {
		return w.emitCall(c, qualified, types, locals)
	}); err != nil {
		return err
	}
	return w.emitConstRefs(body, qualified, locals)
}

// handleMethod emits a method with receiver-type qualification and
// walks its body for call expressions. The receiver syntax is
// `func (r ReceiverType) Name(...)` or `func (r *ReceiverType) Name(...)`;
// we strip the pointer and any type parameters to get the base name used
// for intra-file resolution.
func (w *walker) handleMethod(n *sitter.Node) error {
	nameNode := n.ChildByFieldName("name")
	receiver := n.ChildByFieldName("receiver")
	if nameNode == nil || receiver == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	recvName := receiverType(receiver, w.source)
	if recvName == "" {
		return nil
	}
	parent := w.qualify(recvName)
	qualified := parent + "." + name
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindMethod,
		Visibility:      visibility(name),
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
		Docstring:       docstringFor(n, w.source),
	}); err != nil {
		return err
	}
	body := n.ChildByFieldName("body")
	types, locals := w.buildTypeMap(n)
	if err := extract.WalkNamedDescendants(body, "call_expression", func(c *sitter.Node) error {
		return w.emitCall(c, qualified, types, locals)
	}); err != nil {
		return err
	}
	return w.emitConstRefs(body, qualified, locals)
}

// emitConstRefs walks a function body for identifiers that resolve to
// package-level constants/variables and emits references edges.
func (w *walker) emitConstRefs(body *sitter.Node, sourceQualified string, locals map[string]bool) error {
	if body == nil || len(w.pkgBindings) == 0 {
		return nil
	}
	seen := map[string]bool{}
	return extract.WalkNamedDescendants(body, "identifier", func(id *sitter.Node) error {
		name := extract.Text(id, w.source)
		if name == "" || locals[name] || goBuiltins[name] || seen[name] {
			return nil
		}
		targetQ, ok := w.pkgBindings[name]
		if !ok {
			return nil
		}
		// Skip identifiers that are call targets.
		if p := id.Parent(); p != nil && p.Kind() == "call_expression" {
			if fn := p.ChildByFieldName("function"); fn != nil && fn.Id() == id.Id() {
				return nil
			}
		}
		// Skip identifiers inside selector expressions (pkg.Func, obj.Field).
		if p := id.Parent(); p != nil && p.Kind() == "selector_expression" {
			return nil
		}
		seen[name] = true
		line := extract.Line(id.StartPosition())
		return w.emit.Edge(extract.EmittedEdge{
			SourceQualified: sourceQualified,
			TargetQualified: targetQ,
			Kind:            model.EdgeReferences,
			Line:            &line,
			Confidence:      extract.ConfidenceStatic,
		})
	})
}

// goBuiltins is the set of Go predeclared identifiers that should
// never be emitted as constant references.
var goBuiltins = map[string]bool{
	"_": true, "true": true, "false": true, "nil": true, "iota": true,
	"append": true, "cap": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true,
	"make": true, "max": true, "min": true, "new": true,
	"panic": true, "print": true, "println": true, "real": true,
	"recover": true, "clear": true,
	"bool": true, "byte": true, "comparable": true, "complex64": true,
	"complex128": true, "error": true, "float32": true, "float64": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"rune": true, "string": true, "uint": true, "uint8": true, "uint16": true,
	"uint32": true, "uint64": true, "uintptr": true, "any": true,
}

// emitCall produces an EdgeCalls edge for one call_expression. When
// the callee is a selector_expression (e.g. `x.Method()`), the type
// map is consulted to resolve the receiver — if `x` has a known type,
// the target becomes `pkg.Type.Method` instead of the raw `x.Method`.
func (w *walker) emitCall(call *sitter.Node, source string, types map[string]localType, locals map[string]bool) error {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return nil
	}
	var target string
	confidence := extract.ConfidenceStatic
	switch fn.Kind() {
	case "identifier":
		target = extract.Text(fn, w.source)
	case "selector_expression":
		target, confidence = w.resolveSelector(fn, types, locals)
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

// ---- helpers ----

// visibility returns "public" for exported names (PascalCase) and
// "private" for unexported names, following Go's naming convention.
func visibility(name string) string {
	r, _ := utf8.DecodeRuneInString(name)
	if r != utf8.RuneError && unicode.IsUpper(r) {
		return "public"
	}
	return "private"
}

// qualify prepends the package name. If no package clause was found
// (unusual — a Go file almost always has one), fall back to the bare
// name rather than producing a leading-dot qualified form.
func (w *walker) qualify(name string) string {
	if w.pkg == "" {
		return name
	}
	return w.pkg + "." + name
}

// packageName reads the package clause from a source_file node. Zero
// value "" signals no package clause (malformed input or a top-level
// Go file that only has comments).
func packageName(root *sitter.Node, source []byte) string {
	if root == nil {
		return ""
	}
	for i := uint(0); i < root.NamedChildCount(); i++ {
		c := root.NamedChild(i)
		if c == nil || c.Kind() != "package_clause" {
			continue
		}
		for j := uint(0); j < c.NamedChildCount(); j++ {
			id := c.NamedChild(j)
			if id != nil && id.Kind() == "package_identifier" {
				return extract.Text(id, source)
			}
		}
	}
	return ""
}

// receiverType pulls the type name out of a method's receiver list.
// The receiver is a parameter_list containing one parameter_declaration
// whose `type` field is either a type_identifier (value receiver) or
// a pointer_type wrapping a type_identifier (pointer receiver). Type
// parameters (`Money[T]`) resolve through generic_type.
func receiverType(recv *sitter.Node, source []byte) string {
	if recv == nil {
		return ""
	}
	for i := uint(0); i < recv.NamedChildCount(); i++ {
		param := recv.NamedChild(i)
		if param == nil || param.Kind() != "parameter_declaration" {
			continue
		}
		t := param.ChildByFieldName("type")
		if t == nil {
			continue
		}
		return unwrapTypeName(t, source)
	}
	return ""
}
