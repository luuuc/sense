package php

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// String-dispatch resolution: the Laravel wiring that
// names its targets in literals instead of code - callable arrays and
// `'Controller@method'` strings, route registrations, the event `$listen`
// map, and middleware aliases. All literal-only, like the container rules:
// anything computed emits nothing.
//
//   - `[OrderController::class, 'index']` or `'OrderController@index'` in
//     ANY argument position is a PHP callable reference → calls edge to the
//     method at convention confidence.
//   - `Route::get('/x', PingController::class)` (invokable controller) →
//     `PingController\__invoke`; `Route::resource('x', C::class)` → C.
//   - `$listen = [Event::class => [Listener::class, ...]]` emits the
//     synthetic `laravel-listen:<Event>` symbol plus edges to each
//     listener's `handle`; `Event::dispatch(...)` and `event(new Event)`
//     consumption sites edge to the same synthetic - DispatchSite →
//     laravel-listen:Event → Listener\handle is a connected chain.
//   - `$middlewareAliases = ['auth' => Authenticate::class]` emits
//     `laravel-middleware:auth` → `Authenticate\handle`;
//     `->middleware('auth')` sites edge to the synthetic.

// routeVerbHandlerArg maps a Route facade verb to its handler-argument
// index; routeControllerArg maps the class-argument registrations.
var routeVerbHandlerArg = map[string]int{
	"get": 1, "post": 1, "put": 1, "patch": 1, "delete": 1, "options": 1,
	"any": 1, "fallback": 0, "match": 2,
}

var routeControllerArg = map[string]int{"resource": 1, "apiResource": 1}

// isMethodIdent reports whether s is a plain method identifier.
func isMethodIdent(s string) bool {
	if s == "" || (!isIdentStart(rune(s[0]))) {
		return false
	}
	for _, r := range s {
		if !isIdentStart(r) && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func isIdentStart(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// stringLiteral reads a plain string literal's content ("" for anything
// interpolated or non-string).
func (w *walker) stringLiteral(n *sitter.Node) string {
	if n == nil || (n.Kind() != "string" && n.Kind() != "encapsed_string") {
		return ""
	}
	if n.NamedChildCount() != 1 {
		return "" // interpolation or empty
	}
	c := n.NamedChild(0)
	if c == nil || c.Kind() != "string_content" {
		return ""
	}
	return extract.Text(c, w.source)
}

// singleInit unwraps an array_element_initializer holding a single value
// (no `key =>`), returning that value node.
func singleInit(n *sitter.Node) *sitter.Node {
	if n == nil || n.Kind() != "array_element_initializer" || n.NamedChildCount() != 1 {
		return nil
	}
	return n.NamedChild(0)
}

// callableFromArray resolves a two-element callable array literal
// (`[X::class, 'm']`, `[$this, 'm']`, `[$typedVar, 'm']`) to its method's
// qualified name, or "".
func (w *walker) callableFromArray(arr *sitter.Node, env map[string]string, class string) string {
	if arr.NamedChildCount() != 2 {
		return ""
	}
	recvNode := singleInit(arr.NamedChild(0))
	method := w.stringLiteral(singleInit(arr.NamedChild(1)))
	if recvNode == nil || !isMethodIdent(method) {
		return ""
	}
	recv := ""
	switch recvNode.Kind() {
	case "class_constant_access_expression":
		recv = w.classConstant(recvNode)
	case "variable_name":
		v := extract.Text(recvNode.NamedChild(0), w.source)
		if v == "this" {
			recv = class
		} else {
			recv = env[v]
		}
	}
	if recv == "" {
		return ""
	}
	return recv + `\` + method
}

// callableFromString resolves a `'Controller@method'` literal to the
// method's qualified name, or "". The class segment must be
// StudlyCase-ish (PSR class names lead uppercase), which is what keeps an
// email-shaped string (`'user@host'`) from becoming an edge.
func (w *walker) callableFromString(content string) string {
	classPart, method, found := strings.Cut(content, "@")
	if !found || classPart == "" || !isMethodIdent(method) {
		return ""
	}
	segs := strings.Split(strings.TrimPrefix(classPart, `\`), `\`)
	last := segs[len(segs)-1]
	if last == "" || last[0] < 'A' || last[0] > 'Z' {
		return ""
	}
	for _, seg := range segs {
		if !isMethodIdent(seg) {
			return ""
		}
	}
	return w.resolveName(classPart) + `\` + method
}

// emitCallableArg emits the calls edge for a callable literal sitting in an
// argument position; any other array/string is left alone.
func (w *walker) emitCallableArg(n *sitter.Node, src string, env map[string]string, class string) error {
	parent := n.Parent()
	if parent == nil || parent.Kind() != "argument" {
		return nil
	}
	target := ""
	if n.Kind() == "array_creation_expression" {
		target = w.callableFromArray(n, env, class)
	} else if content := w.stringLiteral(n); strings.Contains(content, "@") {
		target = w.callableFromString(content)
	}
	if target == "" {
		return nil
	}
	return w.callEdge(n, src, target, extract.ConfidenceConvention)
}

// emitScopedDispatch adds the Laravel-specific edges a scoped call implies
// beyond its own target: route registrations with a bare controller class,
// and `Event::dispatch(...)` consumption of the listen chain.
func (w *walker) emitScopedDispatch(n *sitter.Node, src, name string, scope *sitter.Node) error {
	if scope.Kind() != "name" && scope.Kind() != "qualified_name" {
		return nil
	}
	scopeText := extract.Text(scope, w.source)
	if leafName(scopeText) == "Route" {
		return w.emitRouteRegistration(n, src, name)
	}
	if name == "dispatch" {
		if target := w.resolveName(scopeText); target != "" {
			// `X::dispatch()` is one spelling for two dispatch families:
			// an event fires its $listen chain (the synthetic), a queued
			// job runs its own handle(). Emit both; whichever target the
			// index does not hold drops at write time.
			if err := w.callEdge(n, src, extract.PrefixLaravelListen+target, extract.ConfidenceConvention); err != nil {
				return err
			}
			return w.callEdge(n, src, target+`\handle`, extract.ConfidenceConvention)
		}
	}
	if name == "observe" {
		return w.emitObserveCall(n, src)
	}
	return nil
}

// emitRouteRegistration handles the bare-class handler forms the generic
// callable rule cannot see: an invokable controller and resource routes.
func (w *walker) emitRouteRegistration(n *sitter.Node, src, verb string) error {
	if i, ok := routeControllerArg[verb]; ok {
		if controller := w.classConstant(argExpr(n, i)); controller != "" {
			return w.callEdge(n, src, controller, extract.ConfidenceConvention)
		}
		return nil
	}
	i, ok := routeVerbHandlerArg[verb]
	if !ok {
		return nil
	}
	if controller := w.classConstant(argExpr(n, i)); controller != "" {
		return w.callEdge(n, src, controller+`\__invoke`, extract.ConfidenceConvention)
	}
	return nil
}

// emitMiddlewareUse handles `->middleware('auth')` / `->middleware(['a',
// 'b'])` consumption, edging each alias's synthetic. handled reports
// whether the call was consumed.
func (w *walker) emitMiddlewareUse(n *sitter.Node, src string) (bool, error) {
	arg := argExpr(n, 0)
	if alias := w.stringLiteral(arg); alias != "" {
		return true, w.callEdge(n, src, extract.PrefixLaravelMiddleware+alias, extract.ConfidenceConvention)
	}
	if arg == nil || arg.Kind() != "array_creation_expression" {
		return false, nil
	}
	handled := false
	for i := uint(0); i < arg.NamedChildCount(); i++ {
		alias := w.stringLiteral(singleInit(arg.NamedChild(i)))
		if alias == "" {
			continue
		}
		handled = true
		if err := w.callEdge(n, src, extract.PrefixLaravelMiddleware+alias, extract.ConfidenceConvention); err != nil {
			return true, err
		}
	}
	return handled, nil
}

// emitPropertyDispatch parses the wiring-map properties of a class body:
// `$listen` (events) and `$middlewareAliases` / `$routeMiddleware`.
func (w *walker) emitPropertyDispatch(member *sitter.Node, _ string) error {
	v := firstChildKind(member, "property_element")
	if v == nil {
		return nil
	}
	name := extract.Text(v.ChildByFieldName("name"), w.source)
	arr := firstChildKind(v, "array_creation_expression")
	if arr == nil {
		return nil
	}
	switch name {
	case "$listen":
		return w.emitListenMap(arr)
	case "$middlewareAliases", "$routeMiddleware":
		return w.emitMiddlewareAliases(arr)
	}
	return nil
}

// emitListenMap emits the laravel-listen:<Event> synthetic (deduped) plus
// an edge to each listener's handle method.
func (w *walker) emitListenMap(arr *sitter.Node) error {
	for i := uint(0); i < arr.NamedChildCount(); i++ {
		pair := arr.NamedChild(i)
		if pair == nil || pair.Kind() != "array_element_initializer" || pair.NamedChildCount() != 2 {
			continue
		}
		event := w.classConstant(pair.NamedChild(0))
		if event == "" {
			continue
		}
		for _, listener := range w.listenerClasses(pair.NamedChild(1)) {
			if err := w.emitSyntheticEdge(pair, extract.PrefixLaravelListen+event, event, listener+`\handle`); err != nil {
				return err
			}
		}
	}
	return nil
}

// listenerClasses reads the listener side of one $listen pair: a class
// constant, or an array of them.
func (w *walker) listenerClasses(n *sitter.Node) []string {
	if c := w.classConstant(n); c != "" {
		return []string{c}
	}
	if n == nil || n.Kind() != "array_creation_expression" {
		return nil
	}
	var out []string
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if c := w.classConstant(singleInit(n.NamedChild(i))); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// emitMiddlewareAliases emits laravel-middleware:<alias> → Handler\handle
// for each literal alias pair.
func (w *walker) emitMiddlewareAliases(arr *sitter.Node) error {
	for i := uint(0); i < arr.NamedChildCount(); i++ {
		pair := arr.NamedChild(i)
		if pair == nil || pair.Kind() != "array_element_initializer" || pair.NamedChildCount() != 2 {
			continue
		}
		alias := w.stringLiteral(pair.NamedChild(0))
		handler := w.classConstant(pair.NamedChild(1))
		if alias == "" || handler == "" {
			continue
		}
		if err := w.emitSyntheticEdge(pair, extract.PrefixLaravelMiddleware+alias, alias, handler+`\handle`); err != nil {
			return err
		}
	}
	return nil
}

// emitSyntheticEdge emits a synthetic dispatch symbol (deduped per file)
// plus its calls edge to target - the shared tail of the listen and
// middleware maps, mirroring emitContainerBinding.
func (w *walker) emitSyntheticEdge(at *sitter.Node, qualified, name, target string) error {
	line := extract.Line(at.StartPosition())
	if !w.synthetics[qualified] {
		w.synthetics[qualified] = true
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:       name,
			Qualified:  qualified,
			Kind:       model.KindConstant,
			Visibility: "public",
			LineStart:  line,
			LineEnd:    line,
		}); err != nil {
			return err
		}
	}
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: qualified,
		TargetQualified: target,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}

// leafName returns the last `\`-segment of a written name.
func leafName(s string) string {
	if i := strings.LastIndex(s, `\`); i >= 0 {
		return s[i+1:]
	}
	return s
}
