package php

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// Laravel framework inference at the extractor level: container bindings
// and facade accessors, resolved from literal syntax only, by design: a
// closure binding, a computed key, or a string facade accessor emits
// nothing rather than a guess.
//
//   - `$this->app->bind(Gateway::class, StripeGateway::class)` (also
//     singleton/bindIf/singletonIf/scoped/scopedIf, member or scoped call)
//     emits the synthetic `laravel-binding:<key>` symbol plus a calls edge
//     to the concrete class. `app(X::class)` / `resolve(X::class)` /
//     `->make(X::class)` consumption sites emit a calls edge to the same
//     synthetic name, closing Consumer → binding → Concrete.
//   - `class Payments extends Facade` with `getFacadeAccessor()` returning
//     `PaymentService::class` emits an inherits edge Payments →
//     PaymentService at convention confidence: a facade is a static proxy
//     for its accessor, so proxy-IS-A rides the resolver's existing
//     ancestry walk and `Payments::charge()` binds to
//     `PaymentService\charge` with no new resolution machinery.

// containerBindMethods are the Laravel container registration methods
// whose literal (key, concrete-class) argument pair becomes a binding.
var containerBindMethods = map[string]bool{
	"bind": true, "singleton": true, "bindIf": true, "singletonIf": true,
	"scoped": true, "scopedIf": true,
}

// classConstant returns the resolved class name behind an `X::class`
// expression node, or "" for anything else (a string, a closure, a
// relative `static::class`).
func (w *walker) classConstant(arg *sitter.Node) string {
	if arg == nil || arg.Kind() != "class_constant_access_expression" {
		return ""
	}
	// The class part is the FIRST named child; the trailing `class` keyword
	// is also a `name` node, so a kind search would grab it for
	// `static::class` (whose scope is a relative_scope, not a name).
	t := arg.NamedChild(0)
	if t == nil || (t.Kind() != "name" && t.Kind() != "qualified_name") {
		return ""
	}
	written := extract.Text(t, w.source)
	switch strings.ToLower(written) {
	case "static", "self", "parent", "class":
		return ""
	}
	return w.resolveName(written)
}

// bindingKey reads a container key argument: a class constant resolves to
// its FQN, a literal string ('cache.store') to its content. Anything
// computed yields "".
func (w *walker) bindingKey(arg *sitter.Node) string {
	if key := w.classConstant(arg); key != "" {
		return key
	}
	if arg != nil && (arg.Kind() == "string" || arg.Kind() == "encapsed_string") {
		if c := firstChildKind(arg, "string_content"); c != nil {
			return extract.Text(c, w.source)
		}
	}
	return ""
}

// argExpr returns a call's i-th argument expression node, or nil.
func argExpr(call *sitter.Node, i int) *sitter.Node {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	seen := 0
	for j := uint(0); j < args.NamedChildCount(); j++ {
		a := args.NamedChild(j)
		if a == nil || a.Kind() != "argument" {
			continue
		}
		if seen == i {
			return a.NamedChild(0)
		}
		seen++
	}
	return nil
}

// emitContainerBinding recognises a bind/singleton registration call with
// a literal key and a literal concrete class, emitting the synthetic
// binding symbol (deduped per file) and its edge to the concrete class.
// handled reports whether the call was consumed; a closure or computed
// registration is NOT handled and falls back to the normal call path.
//
// The gate is the method NAME plus the literal argument pair, on any
// receiver - a deliberate name-keyed heuristic (same family as the Route
// leaf gate): a non-container method named bind taking two class
// constants is vanishingly rare, and its edge drops unresolved. Laravel's
// own route-model `$router->bind('user', Resolver::class)` rides it as a
// binding, which is semantically what it is.
func (w *walker) emitContainerBinding(call *sitter.Node, name string) (handled bool, err error) {
	if !containerBindMethods[name] {
		return false, nil
	}
	key := w.bindingKey(argExpr(call, 0))
	concrete := w.classConstant(argExpr(call, 1))
	if key == "" || concrete == "" {
		return false, nil
	}
	line := extract.Line(call.StartPosition())
	qualified := extract.PrefixLaravelBinding + key
	if !w.synthetics[qualified] {
		w.synthetics[qualified] = true
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:       key,
			Qualified:  qualified,
			Kind:       model.KindConstant,
			Visibility: "public",
			LineStart:  line,
			LineEnd:    line,
		}); err != nil {
			return true, err
		}
	}
	return true, w.emit.Edge(extract.EmittedEdge{
		SourceQualified: qualified,
		TargetQualified: concrete,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}

// containerConsumptionKey recognises an `app(...)` / `resolve(...)` /
// `->make(...)` consumption argument, returning the binding key ("" when
// the call is not a literal container lookup).
func (w *walker) containerConsumptionKey(call *sitter.Node) string {
	return w.bindingKey(argExpr(call, 0))
}

// containerMadeType returns the interface/class an `app(X::class)` or
// `resolve(X::class)` function call produces, for receiver typing. A
// string key types nothing (the concrete class is a cross-file fact the
// resolver owns).
func (w *walker) containerMadeType(call *sitter.Node) string {
	if call == nil || call.Kind() != "function_call_expression" {
		return ""
	}
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Kind() != "name" {
		return ""
	}
	name := extract.Text(fn, w.source)
	if name != "app" && name != "resolve" {
		return ""
	}
	return w.classConstant(argExpr(call, 0))
}

// facadeAccessor returns the resolved accessor class when n declares a
// Laravel facade: it extends a class whose leaf name is Facade and its
// getFacadeAccessor returns a class constant. A string accessor ('cache')
// returns "" - a smaller, true accessor map beats a larger, guessed one
// (a stale guess resolves to a wrong-but-plausible edge).
func (w *walker) facadeAccessor(n *sitter.Node, qualified string) string {
	parent := w.parents[qualified]
	if parent != "Facade" && !strings.HasSuffix(parent, `\Facade`) {
		return ""
	}
	body := n.ChildByFieldName("body")
	if body == nil {
		return ""
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		member := body.NamedChild(i)
		if member == nil || member.Kind() != "method_declaration" {
			continue
		}
		if extract.Text(member.ChildByFieldName("name"), w.source) != "getFacadeAccessor" {
			continue
		}
		return w.accessorReturn(member)
	}
	return ""
}

// accessorReturn finds the class constant a getFacadeAccessor body returns.
func (w *walker) accessorReturn(method *sitter.Node) string {
	accessor := ""
	_ = extract.WalkNamedDescendants(method, "return_statement", func(ret *sitter.Node) error {
		if accessor == "" {
			accessor = w.classConstant(ret.NamedChild(0))
		}
		return nil
	})
	return accessor
}

// concordProxyModel returns the sibling model class a Konekt-Concord model
// proxy stands for: a class named `<X>Proxy` extending ModelProxy is a
// static proxy for `<X>` in its own namespace (the concord registry binds
// them by that naming pair). Anything else returns "".
func (w *walker) concordProxyModel(qualified string) string {
	parent := w.parents[qualified]
	if parent != "ModelProxy" && !strings.HasSuffix(parent, `\ModelProxy`) {
		return ""
	}
	base := strings.TrimSuffix(qualified, "Proxy")
	if base == qualified || strings.HasSuffix(base, `\`) {
		return ""
	}
	return base
}

// emitConcordProxy emits the proxy-IS-A inherits edge for a concord model
// proxy declaration, or nothing for a non-proxy. Mirrors the facade lane:
// the proxy is a static stand-in for its model, so calls through the proxy
// ride the resolver's ancestry walk onto the model.
func (w *walker) emitConcordProxy(n *sitter.Node, qualified string) error {
	target := w.concordProxyModel(qualified)
	if target == "" {
		return nil
	}
	line := extract.Line(n.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: qualified,
		TargetQualified: target,
		Kind:            model.EdgeInherits,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}

// emitFacadeAccessor emits the proxy-IS-A inherits edge for a facade
// declaration, or nothing for a non-facade.
func (w *walker) emitFacadeAccessor(n *sitter.Node, qualified string) error {
	accessor := w.facadeAccessor(n, qualified)
	if accessor == "" {
		return nil
	}
	line := extract.Line(n.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: qualified,
		TargetQualified: accessor,
		Kind:            model.EdgeInherits,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}
