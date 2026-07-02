// Package python extracts symbols and intra-file edges from Python
// source via tree-sitter-python.
//
// Symbol kinds:
//   - class / dataclass      → KindClass
//   - def at module scope    → KindFunction
//   - def inside a class     → KindMethod
//   - UPPER_CASE = …         → KindConstant (module-level only)
//
// Intra-file edges:
//   - class B(A)             → inherits edge (B → A) when A is defined
//     in the same file; cross-file inheritance
//     is dropped for 01-03 to backfill.
//
// Calls edges:
//   - Function / method bodies are walked for `call` nodes. The target
//     is the callee's surface text — `name`, `self.foo`, `mod.fn`, etc.
//     — as written. Type inference is out of scope.
//   - `getattr(obj, "name")` with a literal string second argument is
//     emitted with confidence 0.7; non-literal `getattr` is skipped.
//
// Framework edges (see framework.go for details):
//   - Django model fields (ForeignKey, etc.) → composes edges
//   - Django URL patterns (path, re_path)    → calls edges to views
//   - FastAPI route decorators               → calls edges from routes
//   - FastAPI Depends()                      → calls edges to deps
//   - Dataclass / Pydantic field types       → composes edges
//   - Type annotations referencing classes    → composes edges
//
// Qualified-name rules (per 05-languages.md):
//   - Class:      A  or  Outer.Inner
//   - Method:     A.method  (Python has no syntactic instance/class
//     split at def-site; decorators identify
//     classmethods but we don't emit a separate
//     qualified form).
//   - Function:   f  (top-level only; nested defs are closures, skipped)
//   - Constant:   NAME  or  Outer.NAME
//
// Visibility (see visibility.go): each symbol is marked `private` (leading
// underscore, non-dunder) or `public`. The dead-code Python voice lets only
// underscore-private functions/methods fall through to `dead`.
//
// Dead-code harvest (see harvest.go): the broad mention set, the
// getattr/setattr/hasattr dispatch set, the decorator / route / Django reach
// sets, and the `__all__` export set — the facts the Python voice and the
// arbiter's soundness gate read.
//
// What is still skipped:
//   - imports (edge resolution; handled in 01-03)
package python

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
	"github.com/luuuc/sense/internal/model"
)

// Extractor is the Python implementation of extract.Extractor.
type Extractor struct{}

func (Extractor) Grammar() *sitter.Language { return grammars.Python() }
func (Extractor) Language() string          { return "python" }
func (Extractor) Extensions() []string      { return []string{".py", ".pyi"} }
func (Extractor) Tier() extract.Tier        { return extract.TierBasic }

func (Extractor) Extract(tree *sitter.Tree, source []byte, _ string, emit extract.Emitter) error {
	w := &walker{source: source, emit: emit, pkgBindings: map[string]string{}}
	w.collectModuleConstants(tree.RootNode())
	if err := w.walk(tree.RootNode(), nil); err != nil {
		return err
	}
	return emitHarvest(tree.RootNode(), source, emit)
}

func init() { extract.Register(Extractor{}) }

// ---- walker ----

type walker struct {
	source      []byte
	emit        extract.Emitter
	pkgBindings map[string]string // unqualified name → qualified name for module-level constants
}

// collectModuleConstants pre-scans the module for ALL_CAPS assignments
// at the top level so function bodies can emit references edges.
func (w *walker) collectModuleConstants(root *sitter.Node) {
	if root == nil {
		return
	}
	count := root.NamedChildCount()
	for i := uint(0); i < count; i++ {
		n := root.NamedChild(i)
		if n == nil {
			continue
		}
		// Assignments are wrapped in expression_statement at module level.
		if n.Kind() == "expression_statement" {
			if n.NamedChildCount() > 0 {
				n = n.NamedChild(0)
			}
		}
		if n == nil || n.Kind() != "assignment" {
			continue
		}
		lhs := n.ChildByFieldName("left")
		if lhs == nil || lhs.Kind() != "identifier" {
			continue
		}
		name := extract.Text(lhs, w.source)
		if isAllCaps(name) {
			w.pkgBindings[name] = name
		}
	}
}

// emitConstRefs walks a function body for identifiers that resolve to
// module-level constants and emits references edges.
func (w *walker) emitConstRefs(body *sitter.Node, sourceQualified string, params map[string]bool) error {
	if body == nil || len(w.pkgBindings) == 0 {
		return nil
	}
	seen := map[string]bool{}
	return extract.WalkNamedDescendants(body, "identifier", func(id *sitter.Node) error {
		name := extract.Text(id, w.source)
		if name == "" || params[name] || seen[name] {
			return nil
		}
		targetQ, ok := w.pkgBindings[name]
		if !ok {
			return nil
		}
		// Skip call targets: parent is `call` and this is the `function` field.
		if p := id.Parent(); p != nil && p.Kind() == "call" {
			if fn := p.ChildByFieldName("function"); fn != nil && fn.Id() == id.Id() {
				return nil
			}
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

// walk visits node and its children under the given class-name scope.
// scope is the chain of enclosing class qualified-name segments —
// e.g. ["Outer", "Inner"] inside `class Outer: class Inner: …`.
//
// Module-level functions and top-level constants live at scope=nil.
// Function bodies are NOT recursed into: nested defs are closures, not
// symbols of interest for Tier-Basic.
func (w *walker) walk(n *sitter.Node, scope []string) error {
	if n == nil {
		return nil
	}

	switch n.Kind() {
	case "class_definition":
		return w.handleClass(n, scope)
	case "function_definition":
		return w.handleFunction(n, scope)
	case "decorated_definition":
		return w.handleDecoratedDefinition(n, scope)
	case "expression_statement":
		// Assignments at module or class scope are wrapped in
		// expression_statement nodes; descend.
		return w.walkChildren(n, scope)
	case "assignment":
		if err := w.handleAssignment(n, scope); err != nil {
			return err
		}
		return nil // LHS/RHS of an assignment can't contain symbols.
	default:
		return w.walkChildren(n, scope)
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

// handleClass emits the class symbol, records inheritance edges, and
// descends into the body with the class name pushed onto the scope so
// methods and nested classes qualify correctly.
//
// Note on decorated classes: the class's LineStart is the `class …:`
// line, not the `@decorator` line above it. For Tier-Basic that's
// intentional — the symbol's location is where tree-sitter places
// the class_definition, not the outer decorated_definition. A future
// Full tier that extracts decorators can revisit this if users
// expect "jump to decorator" semantics.
func (w *walker) handleClass(n *sitter.Node, scope []string) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return w.walkChildren(n, scope)
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return w.walkChildren(n, scope)
	}

	parent := strings.Join(scope, ".")
	qualified := name
	if parent != "" {
		qualified = parent + "." + name
	}

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindClass,
		Visibility:      visibilityForName(name),
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
		Docstring:       docstringFor(n, w.source),
	}); err != nil {
		return err
	}

	// Superclass list: `class B(A, B):` — emit one inherits edge per
	// simple-name superclass. Compound expressions (e.g. `Generic[T]`
	// or `module.Base`) are skipped: we'd need type-arg or attribute
	// resolution, which is cross-file territory for 01-03.
	if sc := n.ChildByFieldName("superclasses"); sc != nil {
		line := extract.Line(sc.StartPosition())
		count := sc.NamedChildCount()
		for i := uint(0); i < count; i++ {
			arg := sc.NamedChild(i)
			if arg == nil || arg.Kind() != "identifier" {
				continue
			}
			if err := w.emit.Edge(extract.EmittedEdge{
				SourceQualified: qualified,
				TargetQualified: extract.Text(arg, w.source),
				Kind:            model.EdgeInherits,
				Line:            &line,
				Confidence:      1.0,
			}); err != nil {
				return err
			}
		}
	}

	if body := n.ChildByFieldName("body"); body != nil {
		newScope := append(slices.Clone(scope), name)
		return w.walkChildren(body, newScope)
	}
	return nil
}

// handleFunction emits a method (inside a class) or function
// (module-level) and walks the body for call expressions. The body
// walk does not emit nested defs as symbols — Tier-Basic does not
// extract closures — but calls made from within a nested closure are
// attributed to the enclosing function, which is the symbol callers
// observe.
func (w *walker) handleFunction(n *sitter.Node, scope []string) error {
	return w.emitFunctionAndWalkBody(n, scope, nil)
}

// emitFunctionAndWalkBody is the shared path for both plain and
// decorated functions. It emits the symbol, optionally processes
// decorators (FastAPI routes, Depends), and walks the body for calls.
func (w *walker) emitFunctionAndWalkBody(n *sitter.Node, scope []string, decorators []*sitter.Node) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return w.walkChildren(n, scope)
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return w.walkChildren(n, scope)
	}

	parent := strings.Join(scope, ".")
	qualified := name
	kind := model.KindFunction
	if parent != "" {
		qualified = parent + "." + name
		kind = model.KindMethod
	}

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            kind,
		Visibility:      visibilityForName(name),
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
		Docstring:       docstringFor(n, w.source),
	}); err != nil {
		return err
	}

	for _, dec := range decorators {
		if err := w.emitFastapiRouteEdge(dec, qualified); err != nil {
			return err
		}
	}
	if len(decorators) > 0 {
		if err := w.emitDependsEdges(n, qualified); err != nil {
			return err
		}
	}

	body := n.ChildByFieldName("body")
	if err := extract.WalkNamedDescendants(body, "call", func(c *sitter.Node) error {
		return w.emitCall(c, qualified)
	}); err != nil {
		return err
	}

	params := collectParams(n, w.source)
	return w.emitConstRefs(body, qualified, params)
}

// emitCall produces one calls edge. Identifier / attribute callees
// emit surface text with confidence 1.0. `getattr(obj, "name")` with a
// literal string second argument emits target = the string with
// confidence 0.7; any other callable form (subscript, lambda call,
// `f()()`) is skipped.
func (w *walker) emitCall(call *sitter.Node, source string) error {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return nil
	}
	kind := fn.Kind()
	if kind != "identifier" && kind != "attribute" {
		return nil
	}
	target := extract.Text(fn, w.source)
	if target == "" {
		return nil
	}
	line := extract.Line(call.StartPosition())

	if kind == "identifier" && target == "getattr" {
		payload, ok := literalGetattrTarget(call, w.source)
		if !ok {
			return nil
		}
		return w.emit.Edge(extract.EmittedEdge{
			SourceQualified: source,
			TargetQualified: payload,
			Kind:            model.EdgeCalls,
			Line:            &line,
			Confidence:      extract.ConfidenceDynamic,
		})
	}

	conf := 1.0
	if kind == "attribute" {
		if handled, err := w.tryEmitCeleryDispatch(fn, source, line); handled || err != nil {
			return err
		}
		conf = attrReceiverConfidence(fn, w.source)
	}

	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: source,
		TargetQualified: target,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      conf,
	})
}

// attrReceiverConfidence rates a `receiver.method` call by how well the
// receiver type is known at extraction time. A `self`/`cls` receiver or a
// Capitalized (class / constant / module-as-class) receiver stays fully
// confident; a lowercase-variable or chained receiver is an unverified
// instance call, emitted at ConfidenceUnresolved so the resolver's bare-name
// fallback does not surface it as a confident caller (a bare `x.id` binding
// to an arbitrary same-named `id`). Mirrors Ruby's resolveCallTarget, which
// already distinguishes a constant receiver from an identifier one.
func attrReceiverConfidence(attr *sitter.Node, src []byte) float64 {
	obj := attr.ChildByFieldName("object")
	if obj == nil || obj.Kind() != "identifier" {
		return extract.ConfidenceUnresolved
	}
	t := extract.Text(obj, src)
	if t == "self" || t == "cls" {
		return 1.0
	}
	if r, _ := utf8.DecodeRuneInString(t); unicode.IsUpper(r) {
		return 1.0
	}
	return extract.ConfidenceUnresolved
}

// literalGetattrTarget returns the second argument of a `getattr` call
// when it's a string literal. `getattr(obj, "name")` resolves to
// `obj.name` at runtime; for the index we emit just `"name"` and let
// the resolver match it against any symbol with that unqualified name.
// Anything but a literal string is unresolvable.
func literalGetattrTarget(call *sitter.Node, source []byte) (string, bool) {
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() < 2 {
		return "", false
	}
	second := args.NamedChild(1)
	if second == nil || second.Kind() != "string" {
		return "", false
	}
	// tree-sitter-python exposes a string's payload as a named
	// `string_content` child between the opening and closing quote
	// nodes; fish it out structurally rather than trimming quotes.
	count := second.NamedChildCount()
	for i := uint(0); i < count; i++ {
		c := second.NamedChild(i)
		if c != nil && c.Kind() == "string_content" {
			return extract.Text(c, source), true
		}
	}
	return "", false
}

// handleAssignment handles assignment nodes at module or class scope.
// It emits KindConstant for ALL_CAPS identifiers, Django model field
// composes edges, Django URL pattern edges, and type annotation edges.
func (w *walker) handleAssignment(n *sitter.Node, scope []string) error {
	if len(scope) > 0 {
		if err := w.emitDjangoModelField(n, scope); err != nil {
			return err
		}
		typeNode := n.ChildByFieldName("type")
		if typeNode != nil {
			classQualified := strings.Join(scope, ".")
			line := extract.Line(n.StartPosition())
			if err := w.emitTypeAnnotationEdge(typeNode, classQualified, line); err != nil {
				return err
			}
		}
	} else {
		if err := w.emitURLPatternEdges(n); err != nil {
			return err
		}
	}

	lhs := n.ChildByFieldName("left")
	if lhs == nil || lhs.Kind() != "identifier" {
		return nil
	}
	name := extract.Text(lhs, w.source)
	if !isAllCaps(name) {
		return nil
	}

	parent := strings.Join(scope, ".")
	qualified := name
	if parent != "" {
		qualified = parent + "." + name
	}
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindConstant,
		Visibility:      visibilityForName(name),
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	})
}

// collectParams returns parameter names from a function_definition node.
func collectParams(funcNode *sitter.Node, source []byte) map[string]bool {
	out := map[string]bool{}
	params := funcNode.ChildByFieldName("parameters")
	if params == nil {
		return out
	}
	count := params.NamedChildCount()
	for i := uint(0); i < count; i++ {
		p := params.NamedChild(i)
		if p == nil {
			continue
		}
		switch p.Kind() {
		case "identifier":
			out[extract.Text(p, source)] = true
		case "default_parameter", "typed_parameter", "typed_default_parameter":
			if name := p.ChildByFieldName("name"); name != nil {
				out[extract.Text(name, source)] = true
			}
		case "list_splat_pattern", "dictionary_splat_pattern":
			if p.NamedChildCount() > 0 {
				out[extract.Text(p.NamedChild(0), source)] = true
			}
		}
	}
	return out
}

// isAllCaps tests the Python "constant" convention: non-empty, every
// cased letter is uppercase. Digits and underscores pass through. Same
// result as the equivalent regexp but without the regex engine.
//
// strings.ToUpper on an already-uppercase ASCII name is an identity,
// so comparing for equality filters out any lowercase letters — but
// not names that contain *only* underscores and digits (`___`, `_`,
// `42`). We reject those explicitly since they're not conventional
// Python constants.
func isAllCaps(s string) bool {
	if s == "" || strings.ToUpper(s) != s {
		return false
	}
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}
