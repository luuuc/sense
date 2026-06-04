package python

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// HarvestsMentions reports that the Python extractor streams the broad mention
// set (see emitHarvest), so the scan records "python" as harvested even on a
// scan that yields zero mentions — the dead-code soundness gate then treats a
// Python symbol as proven-against-an-empty-set, not never-harvested. Without
// this opt-in every Python symbol would fail closed at the per-language gate
// (core_no_harvest) and none could earn `dead`.
func (Extractor) HarvestsMentions() bool { return true }

// emitHarvest streams the file's dead-code fact sets to the emitter when it
// accepts them, all from one set of tree walks (scan is not a hot path):
//
//   - mentions (MentionEmitter): every identifier token except a definition's
//     own name — the broad superset feeding the arbiter's soundness gate. A
//     method invoked as `obj.render()` leaves a `render` identifier mention, so a
//     same-named method stays open-world instead of falsely `dead`.
//   - dispatch (DispatchEmitter): the literal name argument of every
//     getattr/setattr/hasattr call — Python's reflective dispatch. A name reached
//     this way is invisible to the static graph, so the core voice keeps it
//     open-world (core_reflection).
//   - decorator / route / Django reach (PythonHarvestEmitter): the name of every
//     decorated symbol, the subset carrying a route decorator, and the subset
//     carrying a Django-dispatch decorator. A framework reaches each with no
//     source caller, so the voice keeps it open-world (py_decorator / py_route /
//     py_django).
//   - `__all__` exports (PythonHarvestEmitter): each name a module declares
//     public via `__all__`, so the voice raises py_all_export — the one signal
//     that overrides the underscore convention, and one the mention set misses
//     because `__all__` lists names as string literals.
//
// Each emit is best-effort — an Emitter that implements none of the extensions
// simply receives no names.
func emitHarvest(root *sitter.Node, source []byte, emit extract.Emitter) error {
	if me, ok := emit.(extract.MentionEmitter); ok {
		if err := harvestMentions(root, source, me); err != nil {
			return err
		}
	}
	if de, ok := emit.(extract.DispatchEmitter); ok {
		if err := harvestDispatch(root, source, de); err != nil {
			return err
		}
	}
	if pe, ok := emit.(extract.PythonHarvestEmitter); ok {
		if err := harvestPythonReach(root, source, pe); err != nil {
			return err
		}
	}
	return nil
}

// harvestMentions streams every bare-name identifier mention (the broad
// soundness superset) to the mention emitter.
func harvestMentions(root *sitter.Node, source []byte, me extract.MentionEmitter) error {
	for _, name := range extract.HarvestMentions(root, source, mentionWalkSpec()) {
		if err := me.MentionName(name); err != nil {
			return err
		}
	}
	return nil
}

// harvestDispatch streams the literal name argument of every
// getattr/setattr/hasattr call (Python's reflective dispatch) to the emitter.
func harvestDispatch(root *sitter.Node, source []byte, de extract.DispatchEmitter) error {
	for name := range collectReflectiveDispatch(root, source) {
		if err := de.DispatchName(name); err != nil {
			return err
		}
	}
	return nil
}

// harvestPythonReach streams the framework-reach name sets — every decorated
// symbol, the route and Django-dispatch subsets, and `__all__` exports — to the
// Python harvest emitter.
func harvestPythonReach(root *sitter.Node, source []byte, pe extract.PythonHarvestEmitter) error {
	decorated, routes, django := collectDecoratorReach(root, source)
	for name := range decorated {
		if err := pe.PythonDecoratedName(name); err != nil {
			return err
		}
	}
	for name := range routes {
		if err := pe.PythonRouteName(name); err != nil {
			return err
		}
	}
	for name := range django {
		if err := pe.PythonDjangoName(name); err != nil {
			return err
		}
	}
	for name := range collectAllExports(root, source) {
		if err := pe.PythonAllExportName(name); err != nil {
			return err
		}
	}
	return nil
}

// mentionWalkSpec is the Python grammar parameterisation of the shared mention
// harvest. A single `identifier` kind covers every mention position — a bare
// call (`helper()`), an attribute object and attribute name (`obj.render` is two
// identifiers), a keyword-argument name — because tree-sitter-python models all
// of them as `identifier`. A definition's own name token is excluded so a symbol
// is never cancelled by its own declaration.
func mentionWalkSpec() extract.MentionWalkSpec {
	return extract.MentionWalkSpec{
		NameOf: map[string]func(*sitter.Node, []byte) string{
			"identifier": extract.Text,
		},
		SkipDefinitionName: isPythonDefinitionName,
	}
}

// isPythonDefinitionName reports whether n is the `name` field of a function or
// class definition. Those tokens are excluded from the mention set so a symbol
// does not count as a mention of itself — otherwise no symbol could ever earn
// `dead`. Constant-assignment LHS names are intentionally NOT excluded: a
// constant is never a `dead` candidate (the voice raises py_constant for it), so
// leaving its name in the set only ever keeps a same-named symbol open-world (the
// safe direction).
func isPythonDefinitionName(n *sitter.Node) bool {
	p := n.Parent()
	if p == nil {
		return false
	}
	switch p.Kind() {
	case "function_definition", "class_definition":
		name := p.ChildByFieldName("name")
		return name != nil && name.Id() == n.Id()
	}
	return false
}

// reflectiveDispatchFns are the Python built-ins whose literal name argument is a
// reflective dispatch target — the analog of Ruby `send` and JS `obj["name"]`. A
// symbol reached via `getattr(obj, "render")` has no static caller edge, so the
// core voice keeps a same-named symbol open-world.
var reflectiveDispatchFns = map[string]bool{
	"getattr": true, "setattr": true, "hasattr": true,
}

// collectReflectiveDispatch returns the set of literal name arguments to
// getattr/setattr/hasattr calls (the second positional argument). A non-literal
// argument (`getattr(obj, name)`) names nothing statically and is skipped — the
// safe direction (the target stays whatever the static graph says).
func collectReflectiveDispatch(root *sitter.Node, source []byte) map[string]struct{} {
	seen := map[string]struct{}{}
	_ = extract.WalkNamedDescendants(root, "call", func(call *sitter.Node) error {
		fn := call.ChildByFieldName("function")
		if fn == nil || fn.Kind() != "identifier" || !reflectiveDispatchFns[extract.Text(fn, source)] {
			return nil
		}
		args := call.ChildByFieldName("arguments")
		if args == nil {
			return nil
		}
		pos := positionalArgs(args)
		if len(pos) < 2 || pos[1].Kind() != "string" {
			return nil
		}
		if name := stringContent(pos[1], source); name != "" {
			seen[name] = struct{}{}
		}
		return nil
	})
	return seen
}

// routeDecorators are the trailing decorator names that mark a request handler
// dispatched by a web framework's router with no source caller — Flask/Blueprint
// `@app.route`, FastAPI `@app.get`/`@router.post`/`@app.websocket`. Matched on the
// decorator's last segment so `@app.route` and `@bp.route` both qualify.
var routeDecorators = map[string]bool{
	"route": true, "websocket": true,
	"get": true, "post": true, "put": true, "delete": true,
	"patch": true, "head": true, "options": true,
}

// djangoDecorators are the trailing decorator names that mark a symbol Django's
// signal/admin machinery invokes invisibly — `@receiver` (a signal handler) and
// `@admin.register` / `@register` (admin registration). Name-based
// over-approximation is safe: it only ever keeps a symbol open-world (py_django).
var djangoDecorators = map[string]bool{
	"receiver": true, "register": true,
}

// collectDecoratorReach walks every decorated_definition and returns three sets
// of the decorated symbol's name: every decorated name, the subset whose
// decorators include a route decorator, and the subset whose decorators include a
// Django-dispatch decorator. The sets can overlap (a handler with several
// decorators); the voice reads the most specific (route, then django, then the
// generic decorated set).
func collectDecoratorReach(root *sitter.Node, source []byte) (decorated, routes, django map[string]struct{}) {
	decorated, routes, django = map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	_ = extract.WalkNamedDescendants(root, "decorated_definition", func(dd *sitter.Node) error {
		def := dd.ChildByFieldName("definition")
		if def == nil {
			return nil
		}
		name := extract.Text(def.ChildByFieldName("name"), source)
		if name == "" {
			return nil
		}
		decorated[name] = struct{}{}
		for _, dec := range collectDecorators(dd) {
			last := decoratorLastName(dec, source)
			if routeDecorators[last] {
				routes[name] = struct{}{}
			}
			if djangoDecorators[last] {
				django[name] = struct{}{}
			}
		}
		return nil
	})
	return decorated, routes, django
}

// decoratorLastName returns the trailing identifier of a decorator's callee:
// `@property` → "property", `@app.route(...)` → "route", `@admin.register` →
// "register". A decorator is `@<expr>` or `@<expr>(...)`; the expression is the
// decorator node's first named child, unwrapped one level when it is a call.
func decoratorLastName(dec *sitter.Node, source []byte) string {
	if dec.NamedChildCount() == 0 {
		return ""
	}
	expr := dec.NamedChild(0)
	if expr != nil && expr.Kind() == "call" {
		expr = expr.ChildByFieldName("function")
	}
	if expr == nil {
		return ""
	}
	switch expr.Kind() {
	case "identifier":
		return extract.Text(expr, source)
	case "attribute":
		return attrLastSegment(expr, source)
	}
	return ""
}

// collectAllExports returns the set of names a module lists in `__all__`
// (`__all__ = ["foo", "_bar"]`, including `+=` augmentations). The names are
// string literals, so the broad identifier mention set misses them — this is the
// dedicated harvest that lets the voice raise py_all_export for an
// underscore-private name a module deliberately re-exports.
func collectAllExports(root *sitter.Node, source []byte) map[string]struct{} {
	seen := map[string]struct{}{}
	collect := func(kind string) {
		_ = extract.WalkNamedDescendants(root, kind, func(n *sitter.Node) error {
			lhs := n.ChildByFieldName("left")
			if lhs == nil || lhs.Kind() != "identifier" || extract.Text(lhs, source) != "__all__" {
				return nil
			}
			addStringElements(n.ChildByFieldName("right"), source, seen)
			return nil
		})
	}
	collect("assignment")
	collect("augmented_assignment")
	return seen
}

// addStringElements adds the payload of every string element of a list/tuple/set
// literal to seen. A non-collection RHS (a name alias, a concatenation) adds
// nothing — the safe direction, since a missed `__all__` entry only risks a
// false `dead`, which the mention gate still backstops for non-string references.
func addStringElements(rhs *sitter.Node, source []byte, seen map[string]struct{}) {
	if rhs == nil {
		return
	}
	switch rhs.Kind() {
	case "list", "tuple", "set":
	default:
		return
	}
	count := rhs.NamedChildCount()
	for i := uint(0); i < count; i++ {
		el := rhs.NamedChild(i)
		if el != nil && el.Kind() == "string" {
			if name := stringContent(el, source); name != "" {
				seen[name] = struct{}{}
			}
		}
	}
}
