package ruby

import (
	"slices"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// handleClassOrModule emits the symbol, records inheritance (class only),
// and descends into the body with the class/module pushed onto the scope.
func (w *walker) handleClassOrModule(n *sitter.Node, scope []string, kind model.SymbolKind) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return w.walkChildren(n, scope) // malformed, keep walking
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return w.walkChildren(n, scope)
	}

	// A::B as a class name pushes both segments; the last segment is the
	// "name" (what grep will find), the full chain is the qualified name.
	segments := strings.Split(name, "::")
	newScope := append(slices.Clone(scope), segments...)
	qualified := strings.Join(newScope, "::")
	parent := strings.Join(scope, "::")

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            segments[len(segments)-1],
		Qualified:       qualified,
		Kind:            kind,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
		Docstring:       docstringFor(n, w.source),
	}); err != nil {
		return err
	}

	if kind == model.KindClass {
		if err := w.emitInheritanceEdges(n, qualified); err != nil {
			return err
		}
	}

	if body := n.ChildByFieldName("body"); body != nil {
		ivarTypes := buildInstanceVarTypeMap(body, w.source)
		w.classInstanceVars[qualified] = ivarTypes
		// Pre-pass: record each direct instance method's visibility before the
		// methods are emitted, so handleMethod can attach it. Mirrors the
		// ivar-type pre-pass above.
		w.recordBodyVisibility(body, qualified)
		if err := w.walkChildren(body, newScope); err != nil {
			return err
		}
		// Capture references that live at class-body level rather than
		// inside a method: constant RHS values, and exception/handler
		// classes named by DSLs like `rescue_from Foo` / `retry_on Bar`.
		// Method-body references are attributed to their method, so they
		// are skipped here via the nested-def guard.
		return w.collectConstRefs(body, qualified, true)
	}
	return nil
}

// emitInheritanceEdges emits the `inherits` edge for a class with a simple
// constant superclass, plus the conventional `tests` edge when that
// superclass is a test base class (e.g. `class UserTest < ActiveSupport::TestCase`).
// Target resolution to a symbol_id happens at write time — here we just
// record the target's qualified name.
func (w *walker) emitInheritanceEdges(n *sitter.Node, qualified string) error {
	sup := n.ChildByFieldName("superclass")
	if sup == nil {
		return nil
	}
	target := superclassName(sup, w.source)
	if target == "" {
		return nil
	}
	// Record the superclass so a `super` call inside one of this class's
	// methods can resolve to the parent's same-named method (the worker
	// run-method hierarchy chains through `super`).
	w.classSuperclass[qualified] = target
	line := extract.Line(sup.StartPosition())
	if err := w.emit.Edge(extract.EmittedEdge{
		SourceQualified: qualified,
		TargetQualified: target,
		Kind:            model.EdgeInherits,
		Line:            &line,
		Confidence:      1.0,
	}); err != nil {
		return err
	}
	if !isTestSuperclass(target) {
		return nil
	}
	testedClass := inferTestedClass(qualified)
	if testedClass == "" {
		return nil
	}
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: qualified,
		TargetQualified: testedClass,
		Kind:            model.EdgeTests,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}

// handleMethod emits a method symbol qualified either as Class#name
// (instance) or Class.name (singleton). For top-level methods the
// separator and parent are both empty — they become KindMethod with
// qualified=name, which matches how Ruby treats top-level defs (they
// get attached to Object at runtime, but we don't model Object here).
//
// After emitting, the body is walked for call nodes so intra-body
// calls land as calls edges.
func (w *walker) handleMethod(n *sitter.Node, scope []string, singleton bool) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}

	parent := strings.Join(scope, "::")
	var qualified, receiver string
	switch {
	case parent == "":
		// Top-level def: attaches to Object at runtime; no class-vs-instance
		// dispatch distinction to record.
		qualified = name
	case singleton:
		qualified = parent + "." + name
		receiver = extract.ReceiverSingleton
	default:
		qualified = parent + "#" + name
		receiver = extract.ReceiverInstance
	}

	// Visibility comes from the per-class pre-pass (instance methods only).
	// Anything not recorded — singleton methods, top-level defs, methods the
	// pre-pass could not classify — defaults to public, which is the safe
	// direction: a public method can never earn a `dead` verdict.
	visibility := "public"
	if v, ok := w.methodVisibility[qualified]; ok {
		visibility = v
	}

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindMethod,
		Receiver:        receiver,
		Visibility:      visibility,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
		Docstring:       docstringFor(n, w.source),
	}); err != nil {
		return err
	}
	// Include / extend / prepend calls inside a method body come through
	// here too and are emitted as regular calls — the `includes` edges
	// are produced separately at the class-body level in handleIncludeCall,
	// and a dynamic include at runtime is rare enough that a bare calls
	// edge is an accurate record of what was written.
	body := n.ChildByFieldName("body")
	localTypes := buildLocalTypeMap(body, w.source)
	w.addRescueBindings(body, localTypes)
	ivarTypes := w.classInstanceVars[strings.Join(scope, "::")]
	if err := extract.WalkNamedDescendants(body, "call", func(c *sitter.Node) error {
		if isInsideBlock(c) {
			return nil
		}
		return w.emitCall(c, qualified, scope, localTypes, ivarTypes)
	}); err != nil {
		return err
	}
	if err := w.emitBareIdentifierCalls(body, qualified, extract.ConfidenceDynamic, methodParamNames(n, w.source)); err != nil {
		return err
	}
	if err := w.emitSuperEdge(n, parent, qualified, name, singleton); err != nil {
		return err
	}
	return w.collectConstRefs(body, qualified, false)
}

// emitSuperEdge emits a calls edge from a method to its superclass's same-named
// method when the body contains a `super` call. `super` (bare or `super(args)`)
// dispatches to the parent's method of the same name — the link a worker
// subclass uses to reach an inherited run method (`CollectionRawDistributionWorker#perform`
// → `RawDistributionWorker#perform`). The target is built from the enclosing
// class's recorded superclass and the method's own name; the dispatch separator
// (`#` instance, `.` singleton) matches the method's receiver. One edge per
// method regardless of how many `super` calls appear. Emitted at convention
// confidence (0.9): super-to-direct-superclass is reliable in single
// inheritance but module prepends/MRO can intervene, so it is never 1.0. A
// method whose class has no recorded superclass (top-level, module, or no
// superclass) emits nothing rather than a guess.
func (w *walker) emitSuperEdge(methodNode *sitter.Node, parent, qualified, methodName string, singleton bool) error {
	if methodNode == nil || parent == "" {
		return nil
	}
	body := methodNode.ChildByFieldName("body")
	if body == nil {
		return nil
	}
	superclass := w.classSuperclass[parent]
	if superclass == "" {
		return nil
	}
	hasSuper := false
	if err := extract.WalkNamedDescendants(body, "super", func(s *sitter.Node) error {
		// Skip a `super` that belongs to a nested method definition inside this
		// body — its `super` dispatches to the nested method's parent, not this
		// method's. Attributing it here would be a false edge.
		if !superBelongsToMethod(s, methodNode) {
			return nil
		}
		hasSuper = true
		return nil
	}); err != nil {
		return err
	}
	if !hasSuper {
		return nil
	}
	sep := "#"
	if singleton {
		sep = "."
	}
	line := extract.Line(body.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: qualified,
		TargetQualified: superclass + sep + methodName,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}

// superBelongsToMethod reports whether a `super` node is part of methodNode
// itself, rather than a method nested inside its body. The nearest enclosing
// `method`/`singleton_method` ancestor of the super is the method it dispatches
// from; the super belongs here only when that ancestor is methodNode (compared
// by byte range — go-tree-sitter returns fresh node structs, so pointer
// identity can't be used).
func superBelongsToMethod(super, methodNode *sitter.Node) bool {
	for p := super.Parent(); p != nil; p = p.Parent() {
		if k := p.Kind(); k == "method" || k == "singleton_method" {
			return p.StartByte() == methodNode.StartByte() && p.EndByte() == methodNode.EndByte()
		}
	}
	return false
}

// isTestSuperclass returns true if the superclass name indicates a test base class.
func isTestSuperclass(name string) bool {
	return strings.Contains(name, "TestCase") || strings.Contains(name, "IntegrationTest") || strings.Contains(name, "SystemTest")
}

// inferTestedClass strips "Test" suffix from a class name to find what it tests.
// "UserTest" → "User", "Admin::DashboardControllerTest" → "Admin::DashboardController"
func inferTestedClass(qualified string) string {
	if strings.HasSuffix(qualified, "Test") {
		return strings.TrimSuffix(qualified, "Test")
	}
	return ""
}

// superclassName pulls the target class name from a `superclass` node.
// The node wraps its target (usually `constant` or `scope_resolution`).
func superclassName(sup *sitter.Node, source []byte) string {
	count := sup.NamedChildCount()
	for i := uint(0); i < count; i++ {
		c := sup.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "constant", "scope_resolution":
			return c.Utf8Text(source)
		}
	}
	return ""
}
