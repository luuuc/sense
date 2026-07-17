package php

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// walkCalls streams the calls edges of one function/method body. env maps
// `$var` names to resolved class types (parameters, `new` assignments);
// class is the enclosing class's qualified name ("" in a function).
//
// Receiver typing decides the confidence per decision 0003:
//   - $this / self:: / static:: / a known class name - the target class is
//     syntactic fact: ConfidenceStatic.
//   - a typed receiver ($var from env, $this->prop from the property
//     table) - type inferred from local context: ConfidenceDynamic.
//   - anything else - the bare-name law: at most ConfidenceNameCollision,
//     and no edge at all for a common name (extract.BareNameEdge).
func (w *walker) walkCalls(n *sitter.Node, src string, env map[string]string, class string) error {
	if n == nil {
		return nil
	}
	var err error
	switch n.Kind() {
	case "assignment_expression":
		w.recordAssignment(n, env)
	case "member_call_expression":
		err = w.emitMemberCall(n, src, env, class)
	case "scoped_call_expression":
		err = w.emitScopedCall(n, src, class)
	case "function_call_expression":
		err = w.emitFunctionCall(n, src)
	case "object_creation_expression":
		err = w.emitCreation(n, src, class)
	case "array_creation_expression", "string", "encapsed_string":
		err = w.emitCallableArg(n, src, env, class)
	}
	if err != nil {
		return err
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if err := w.walkCalls(n.NamedChild(i), src, env, class); err != nil {
			return err
		}
	}
	return nil
}

// recordAssignment types `$svc = new TaxService()` and
// `$g = app(Gateway::class)` into env. The creation/consumption edge
// itself is emitted when the walk reaches the right-hand node.
func (w *walker) recordAssignment(n *sitter.Node, env map[string]string) {
	left := n.ChildByFieldName("left")
	right := n.ChildByFieldName("right")
	if left == nil || right == nil || left.Kind() != "variable_name" {
		return
	}
	var typ string
	switch right.Kind() {
	case "object_creation_expression":
		typ = w.creationType(right, "")
	case "function_call_expression":
		typ = w.containerMadeType(right)
	default:
		return
	}
	name := extract.Text(left.NamedChild(0), w.source)
	if name != "" && typ != "" {
		env[name] = typ
	}
}

// emitMemberCall handles `$recv->method(...)`.
func (w *walker) emitMemberCall(n *sitter.Node, src string, env map[string]string, class string) error {
	name := extract.Text(n.ChildByFieldName("name"), w.source)
	if name == "" || name[0] == '$' {
		return nil // dynamic `$obj->$method()` - no literal target
	}
	if handled, err := w.emitContainerBinding(n, name); handled || err != nil {
		return err
	}
	if handled, err := w.emitRelation(n, name, class); handled || err != nil {
		return err
	}
	if name == "make" {
		if key := w.containerConsumptionKey(n); key != "" {
			return w.callEdge(n, src, extract.PrefixLaravelBinding+key, extract.ConfidenceConvention)
		}
	}
	if name == "middleware" {
		if handled, err := w.emitMiddlewareUse(n, src); handled || err != nil {
			return err
		}
	}
	target, conf := w.memberTarget(n.ChildByFieldName("object"), name, env, class)
	if target == "" {
		// The guard folds the name: PHP method dispatch is case-insensitive.
		var ok bool
		conf, ok = extract.BareNameEdge(strings.ToLower(name), phpCommonNames)
		if !ok {
			return nil
		}
		target = name
	}
	return w.callEdge(n, src, target, conf)
}

// memberTarget types a member call's receiver, returning "" when no type
// witness exists.
func (w *walker) memberTarget(obj *sitter.Node, name string, env map[string]string, class string) (string, float64) {
	if obj == nil {
		return "", 0
	}
	switch obj.Kind() {
	case "variable_name":
		v := extract.Text(obj.NamedChild(0), w.source)
		if v == "this" && class != "" {
			return class + `\` + name, extract.ConfidenceStatic
		}
		if typ := env[v]; typ != "" {
			return typ + `\` + name, extract.ConfidenceDynamic
		}
	case "member_access_expression":
		// `$this->prop->method()` via the typed-property table.
		inner := obj.ChildByFieldName("object")
		prop := extract.Text(obj.ChildByFieldName("name"), w.source)
		if inner != nil && inner.Kind() == "variable_name" && prop != "" &&
			extract.Text(inner.NamedChild(0), w.source) == "this" {
			if typ := w.propTypes[class][prop]; typ != "" {
				return typ + `\` + name, extract.ConfidenceDynamic
			}
		}
	case "function_call_expression":
		// `app(Gateway::class)->charge()` - the container returns the bound
		// interface, a framework-convention type witness.
		if typ := w.containerMadeType(obj); typ != "" {
			return typ + `\` + name, extract.ConfidenceDynamic
		}
	}
	return "", 0
}

// emitScopedCall handles `Class::method(...)`, `self::`/`static::` and
// `parent::` calls.
func (w *walker) emitScopedCall(n *sitter.Node, src string, class string) error {
	name := extract.Text(n.ChildByFieldName("name"), w.source)
	scope := n.ChildByFieldName("scope")
	if name == "" || scope == nil {
		return nil
	}
	// `App::bind(...)` registers through the facade exactly like
	// `$this->app->bind(...)` does through the property.
	if handled, err := w.emitContainerBinding(n, name); handled || err != nil {
		return err
	}
	// Route registrations and Event::dispatch add their dispatch edges on
	// top of the scoped call's own target.
	if err := w.emitScopedDispatch(n, src, name, scope); err != nil {
		return err
	}
	var target string
	conf := extract.ConfidenceStatic
	switch scope.Kind() {
	case "relative_scope":
		switch extract.Text(scope, w.source) {
		case "self", "static":
			if class == "" {
				return nil
			}
			target = class + `\` + name
		case "parent":
			base := w.parents[class]
			if base == "" {
				return nil
			}
			target = base + `\` + name
		}
	case "name", "qualified_name":
		if resolved := w.resolveName(extract.Text(scope, w.source)); resolved != "" {
			target = resolved + `\` + name
		}
	}
	if target == "" {
		return nil
	}
	return w.callEdge(n, src, target, conf)
}

// emitFunctionCall handles `foo(...)` / `\App\Util\helper(...)`. A plain
// function call resolves by its written name (PHP falls back to the global
// scope at runtime; the resolver's name lanes handle that) - it has no
// receiver, so the bare-name law does not apply.
func (w *walker) emitFunctionCall(n *sitter.Node, src string) error {
	fn := n.ChildByFieldName("function")
	if fn == nil {
		return nil
	}
	switch fn.Kind() {
	case "name", "qualified_name":
		target := strings.TrimPrefix(extract.Text(fn, w.source), `\`)
		if target == "" {
			return nil
		}
		// `app(X::class)` / `resolve('key')` is a container lookup: the edge
		// goes to the binding symbol, not to a helper named `app`.
		if target == "app" || target == "resolve" {
			if key := w.containerConsumptionKey(n); key != "" {
				return w.callEdge(n, src, extract.PrefixLaravelBinding+key, extract.ConfidenceConvention)
			}
		}
		// `event(new OrderShipped(...))` fires the listen chain.
		if target == "event" {
			if evt := w.creationType(argExpr(n, 0), ""); evt != "" {
				if err := w.callEdge(n, src, extract.PrefixLaravelListen+evt, extract.ConfidenceConvention); err != nil {
					return err
				}
			}
		}
		return w.callEdge(n, src, target, extract.ConfidenceStatic)
	}
	return nil // `$callable(...)` - no literal target
}

// emitCreation handles `new X(...)`: a static reference to the class.
func (w *walker) emitCreation(n *sitter.Node, src string, class string) error {
	typ := w.creationType(n, class)
	if typ == "" {
		return nil
	}
	return w.callEdge(n, src, typ, extract.ConfidenceStatic)
}

// creationType resolves the class a `new` expression instantiates.
// `new static()` / `new self()` mean the enclosing class ("" outside one).
func (w *walker) creationType(n *sitter.Node, class string) string {
	t := firstChildKind(n, "name", "qualified_name")
	if t == nil {
		return ""
	}
	written := extract.Text(t, w.source)
	switch written {
	case "static", "self":
		return class
	}
	return w.resolveName(written)
}

func (w *walker) callEdge(n *sitter.Node, src, target string, conf float64) error {
	line := extract.Line(n.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: src,
		TargetQualified: target,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      conf,
	})
}
