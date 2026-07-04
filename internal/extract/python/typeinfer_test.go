package python

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// Receiver-type inference: a method call on a receiver whose type is provable
// inside the function scope resolves to `<Type>.<method>` at ConfidenceDynamic,
// instead of falling to the bare-name 0.5 tier. Patterns covered (and ONLY
// these — this is not a type inferencer): annotated parameters, annotated
// locals, constructor-assigned locals, and the Django `Model.objects` builder
// chain (which lives in django.go but is asserted here alongside its general
// cousins).

func TestTypedParamReceiverResolvesToTypeMethod(t *testing.T) {
	r := parse(t, `
def handle(query: Query):
    query.add_q(cond)
`)
	e := findEdge(r, "handle", "Query.add_q", "calls")
	if e == nil {
		t.Fatal("expected handle -> Query.add_q calls edge from annotated param")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("confidence = %v, want ConfidenceDynamic", e.Confidence)
	}
}

func TestTypedDefaultParamReceiverResolves(t *testing.T) {
	r := parse(t, `
def handle(query: Query = None):
    query.get_compiler(using)
`)
	if findEdge(r, "handle", "Query.get_compiler", "calls") == nil {
		t.Fatal("expected handle -> Query.get_compiler from typed default param")
	}
}

func TestAnnotatedLocalReceiverResolves(t *testing.T) {
	r := parse(t, `
def build():
    q: Query = clone()
    q.chain()
`)
	if findEdge(r, "build", "Query.chain", "calls") == nil {
		t.Fatal("expected build -> Query.chain from annotated local")
	}
}

func TestConstructorLocalReceiverResolves(t *testing.T) {
	r := parse(t, `
def check(model):
    query = Query(model=model)
    query.add_q(condition)
`)
	e := findEdge(r, "check", "Query.add_q", "calls")
	if e == nil {
		t.Fatal("expected check -> Query.add_q from constructor-assigned local")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("confidence = %v, want ConfidenceDynamic", e.Confidence)
	}
}

func TestDottedConstructorLocalUsesLeafType(t *testing.T) {
	r := parse(t, `
def check(model):
    query = sql.Query(model)
    query.get_compiler(connection)
`)
	if findEdge(r, "check", "Query.get_compiler", "calls") == nil {
		t.Fatal("expected check -> Query.get_compiler from sql.Query(...) constructor")
	}
}

func TestOptionalWrappedParamAnnotationResolves(t *testing.T) {
	r := parse(t, `
def handle(query: Optional[Query]):
    query.resolve()
`)
	if findEdge(r, "handle", "Query.resolve", "calls") == nil {
		t.Fatal("expected handle -> Query.resolve from Optional[Query] param")
	}
}

func TestUnknownReceiverStaysBareAtUnresolved(t *testing.T) {
	r := parse(t, `
def handle(query):
    query.add_q(cond)
`)
	e := findEdge(r, "handle", "query.add_q", "calls")
	if e == nil {
		t.Fatal("expected bare query.add_q edge for unannotated param")
	}
	if e.Confidence != extract.ConfidenceUnresolved {
		t.Errorf("confidence = %v, want ConfidenceUnresolved", e.Confidence)
	}
}

func TestPrimitiveAnnotationDoesNotInfer(t *testing.T) {
	r := parse(t, `
def handle(count: int):
    count.bit_length()
`)
	if findEdge(r, "handle", "int.bit_length", "calls") != nil {
		t.Fatal("primitive annotations must not produce typed-receiver targets")
	}
	if findEdge(r, "handle", "count.bit_length", "calls") == nil {
		t.Fatal("expected the bare edge to remain for a primitive-typed receiver")
	}
}

func TestSelfReceiverUnaffectedByInference(t *testing.T) {
	r := parse(t, `
class Compiler:
    def run(self):
        self.setup()
`)
	e := findEdge(r, "Compiler.run", "self.setup", "calls")
	if e == nil {
		t.Fatal("expected self.setup edge to remain the resolver's self-rewrite path")
	}
	if e.Confidence != 1.0 {
		t.Errorf("self receiver confidence = %v, want 1.0", e.Confidence)
	}
}

func TestLastAssignmentWinsInTypeMap(t *testing.T) {
	r := parse(t, `
def handle():
    q = Query(model)
    q = Raw(sql)
    q.execute()
`)
	if findEdge(r, "handle", "Raw.execute", "calls") == nil {
		t.Fatal("expected the last constructor assignment to type the receiver")
	}
}

// Django `.objects` builder chains (rule lives in django.go).

func TestObjectsChainFirstHopResolvesToQuerySet(t *testing.T) {
	r := parse(t, `
def actives():
    return Product.objects.filter(active=True)
`)
	e := findEdge(r, "actives", "QuerySet.filter", "calls")
	if e == nil {
		t.Fatal("expected actives -> QuerySet.filter from Model.objects chain")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("confidence = %v, want ConfidenceDynamic", e.Confidence)
	}
}

func TestObjectsChainContinuationResolvesEachHop(t *testing.T) {
	r := parse(t, `
def stats():
    return Product.objects.filter(active=True).annotate(n=Count("id"))
`)
	if findEdge(r, "stats", "QuerySet.filter", "calls") == nil {
		t.Fatal("expected QuerySet.filter for the chain's first hop")
	}
	if findEdge(r, "stats", "QuerySet.annotate", "calls") == nil {
		t.Fatal("expected QuerySet.annotate for the chain's second hop")
	}
}

func TestObjectsChainAssignedLocalContinues(t *testing.T) {
	r := parse(t, `
def paged():
    qs = Product.objects.filter(active=True)
    return qs.order_by("name")
`)
	if findEdge(r, "paged", "QuerySet.order_by", "calls") == nil {
		t.Fatal("expected QuerySet.order_by on a local assigned from an objects chain")
	}
}

func TestNonObjectsChainIsNotRewritten(t *testing.T) {
	r := parse(t, `
def run(conn):
    conn.cursor().execute(sql)
`)
	if findEdge(r, "run", "QuerySet.execute", "calls") != nil {
		t.Fatal("non-objects chains must not resolve to QuerySet")
	}
}

func TestLowercaseObjectsRootDoesNotFire(t *testing.T) {
	r := parse(t, `
def run(thing):
    return thing.objects.filter(x=1)
`)
	if findEdge(r, "run", "QuerySet.filter", "calls") != nil {
		t.Fatal("objects rule requires a PascalCase model root")
	}
}

func TestDottedAnnotationParamUsesLeafType(t *testing.T) {
	r := parse(t, `
def compile(query: sql.Query):
    query.get_compiler(using)
`)
	if findEdge(r, "compile", "Query.get_compiler", "calls") == nil {
		t.Fatal("expected dotted annotation sql.Query to type the receiver as Query")
	}
}

func TestNonWrapperGenericAnnotationUsesOuterType(t *testing.T) {
	r := parse(t, `
def drain(q: Registry[Task]):
    q.pop()
`)
	if findEdge(r, "drain", "Registry.pop", "calls") == nil {
		t.Fatal("expected a non-wrapper PascalCase generic to type the receiver as its outer name")
	}
}

func TestLowercaseAnnotationDoesNotInfer(t *testing.T) {
	r := parse(t, `
def run(conn: connection):
    conn.execute(sql)
`)
	if findEdge(r, "run", "connection.execute", "calls") != nil {
		t.Fatal("lowercase annotations must not produce typed targets")
	}
	if findEdge(r, "run", "conn.execute", "calls") == nil {
		t.Fatal("expected the bare edge to remain")
	}
}

func TestDottedLowercaseAnnotationLeafDoesNotInfer(t *testing.T) {
	r := parse(t, `
def run(conn: db.connection):
    conn.execute(sql)
`)
	if findEdge(r, "run", "conn.execute", "calls") == nil {
		t.Fatal("expected the bare edge for a lowercase dotted-annotation leaf")
	}
}

func TestTypedSplatParamIsSkipped(t *testing.T) {
	r := parse(t, `
def run(*args: int):
    args.count(x)
`)
	if findEdge(r, "run", "args.count", "calls") == nil {
		t.Fatal("expected the bare edge for a typed splat param")
	}
}

func TestBareWrapperAnnotationDoesNotInfer(t *testing.T) {
	r := parse(t, `
def run(x: Optional):
    x.get()
`)
	if findEdge(r, "run", "Optional.get", "calls") != nil {
		t.Fatal("a bare generic-wrapper annotation must not become a receiver type")
	}
}

func TestDottedModelObjectsRootDoesNotFire(t *testing.T) {
	r := parse(t, `
def run():
    return app.Product.objects.filter(x=1)
`)
	if findEdge(r, "run", "QuerySet.filter", "calls") != nil {
		t.Fatal("objects rule requires a bare PascalCase root, not a dotted one")
	}
}

func TestCallRootedChainWithoutAttributeDoesNotFire(t *testing.T) {
	r := parse(t, `
def run():
    return factory()().finish()
`)
	if findEdge(r, "run", "QuerySet.finish", "calls") != nil {
		t.Fatal("a call chain without an objects root must not resolve to QuerySet")
	}
}

func TestAnnotatedLocalWithUnprovableValueStillTypes(t *testing.T) {
	// The annotation proves the type even when the RHS does not.
	r := parse(t, `
def run():
    q: Query = fetch()
    q.reset()
`)
	if findEdge(r, "run", "Query.reset", "calls") == nil {
		t.Fatal("expected annotated local to type the receiver")
	}
}

func TestAliasingDoesNotPropagateType(t *testing.T) {
	r := parse(t, `
def run():
    q = Query(model)
    r = q
    r.reset()
`)
	if findEdge(r, "run", "Query.reset", "calls") != nil {
		t.Fatal("identifier aliasing is not provable; r must stay bare")
	}
}

func TestLowercaseDottedConstructorDoesNotInfer(t *testing.T) {
	r := parse(t, `
def run():
    client = http.factory(url)
    client.request(x)
`)
	if findEdge(r, "run", "client.request", "calls") == nil {
		t.Fatal("expected the bare edge for a lowercase dotted-constructor local")
	}
}

func TestLowercaseGenericOuterDoesNotInfer(t *testing.T) {
	r := parse(t, `
def run(x: registry[Task]):
    x.pop()
`)
	if findEdge(r, "run", "x.pop", "calls") == nil {
		t.Fatal("expected the bare edge for a lowercase generic annotation")
	}
}

func TestWrapperWithPrimitiveInnerDoesNotInfer(t *testing.T) {
	r := parse(t, `
def run(x: Optional[int]):
    x.bit_length()
`)
	if findEdge(r, "run", "x.bit_length", "calls") == nil {
		t.Fatal("expected the bare edge when the wrapper's inner type is a primitive")
	}
}

func TestIsQuerySetExprNilNodeIsFalse(t *testing.T) {
	// Recursion hands isQuerySetExpr the receiver of a chained call, which an
	// ERROR-node parse can leave nil; it must answer false, never crash.
	if isQuerySetExpr(nil, nil, nil) {
		t.Fatal("nil node must not be a queryset expression")
	}
}

func TestInferenceHelpersTolerateNilNodes(t *testing.T) {
	// ERROR-node trees (watch mode on half-typed source) can leave any field
	// nil; every helper must degrade to "no type", never panic. These are the
	// same guards the malformed-source suite exercises end to end; direct
	// calls pin each helper's own contract.
	if t2, ok := annotationTypeName(nil, nil); ok || t2 != "" {
		t.Fatal("nil annotation must yield no type")
	}
	if t2, ok := callResultTypeName(nil, nil, nil); ok || t2 != "" {
		t.Fatal("nil RHS must yield no type")
	}
	walkOuterAssignments(nil, nil, map[string]string{}) // must not panic
	types := map[string]string{}
	r := parse(t, "x = 1\n") // any tree; grab a non-function node
	_ = r
	collectParamTypes(mustParseRoot(t, "x = 1\n"), nil, types)
	if len(types) != 0 {
		t.Fatal("a node without parameters must collect nothing")
	}
}

// mustParseRoot parses source and returns the tree's root node for direct
// helper calls. The tree is intentionally not closed until test exit.
func mustParseRoot(t *testing.T, src string) *sitter.Node {
	t.Helper()
	ex := Extractor{}
	p := sitter.NewParser()
	t.Cleanup(p.Close)
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse([]byte(src), nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	t.Cleanup(tree.Close)
	return tree.RootNode()
}

func TestMalformedSourceDoesNotPanic(t *testing.T) {
	// Broken mid-edit source (watch mode sees these constantly): ERROR nodes
	// can leave any field nil, so inference must degrade, never crash.
	for _, src := range []string{
		"def broken(q: Query:\n    q.m(\n",
		"def f(*, ):\n    x = (\n    x.m()\n",
		"def g(a:\n",
		"qs = Product.objects.\n",
		"def h(q: ) :\n    q.m()\n",
		"def i(x: Query\n",
	} {
		_ = parse(t, src)
	}
}

func TestContainerAnnotationDoesNotTypeReceiver(t *testing.T) {
	// items is a list, NOT an Item — only Optional proves the receiver.
	r := parse(t, `
def run(items: list[Item]):
    items.append(x)
`)
	if findEdge(r, "run", "Item.append", "calls") != nil {
		t.Fatal("list[Item] must not type the receiver as Item")
	}
	if findEdge(r, "run", "items.append", "calls") == nil {
		t.Fatal("expected the bare edge for a container-annotated receiver")
	}
}

func TestUnionAnnotationDoesNotTypeReceiver(t *testing.T) {
	r := parse(t, `
def run(q: Union[Query, Raw]):
    q.execute()
`)
	if findEdge(r, "run", "Query.execute", "calls") != nil {
		t.Fatal("Union proves neither arm; must not type the receiver")
	}
}

func TestDottedWrapperAnnotationDoesNotTypeReceiver(t *testing.T) {
	// The dotted-leaf path applies the same primitive/wrapper gate as the
	// identifier path.
	r := parse(t, `
def run(x: typing.List):
    x.append(v)
`)
	if findEdge(r, "run", "List.append", "calls") != nil {
		t.Fatal("typing.List must not type the receiver as List")
	}
}

func TestDottedWrapperConstructorDoesNotTypeReceiver(t *testing.T) {
	r := parse(t, `
def run():
    x = typing.List(v)
    x.append(v)
`)
	if findEdge(r, "run", "List.append", "calls") != nil {
		t.Fatal("typing.List(...) must not type the assigned local as List")
	}
}

func TestNestedDefParamShadowsOuterType(t *testing.T) {
	// Calls inside a nested def are attributed to the enclosing function, so
	// a name the nested def's params shadow is no longer provable there.
	r := parse(t, `
def outer(q: Query):
    def inner(q):
        q.run()
`)
	if findEdge(r, "outer", "Query.run", "calls") != nil {
		t.Fatal("a nested def's shadowing param must drop the outer type")
	}
}

func TestNestedDefAssignmentDoesNotTypeOuter(t *testing.T) {
	r := parse(t, `
def outer(q):
    def inner():
        q = Raw(sql)
    q.run()
`)
	if findEdge(r, "outer", "Raw.run", "calls") != nil {
		t.Fatal("an assignment inside a nested def must not type the outer variable")
	}
}

func TestAnnotatedSelfKeepsSelfPath(t *testing.T) {
	// The resolver's self-rewrite resolves self at 1.0; an annotation must
	// not downgrade it to the 0.7 inference path.
	r := parse(t, `
class Compiler:
    def run(self: Compiler):
        self.setup()
`)
	e := findEdge(r, "Compiler.run", "self.setup", "calls")
	if e == nil {
		t.Fatal("annotated self must stay on the resolver's self-rewrite path")
	}
	if e.Confidence != 1.0 {
		t.Errorf("annotated self confidence = %v, want 1.0", e.Confidence)
	}
}

func TestTerminalChainMethodDoesNotContinueQuerySet(t *testing.T) {
	// Model.objects.get(...) returns an instance, not a QuerySet; a hop past
	// it proves nothing.
	r := parse(t, `
def run():
    User.objects.get(pk=1).save()
`)
	if findEdge(r, "run", "QuerySet.save", "calls") != nil {
		t.Fatal("a chain hop past a terminal method must not resolve to QuerySet")
	}
	if findEdge(r, "run", "QuerySet.get", "calls") == nil {
		t.Fatal("the first hop (objects.get) is still a QuerySet method")
	}
}

func TestTerminalChainAssignmentDoesNotTypeQuerySet(t *testing.T) {
	r := parse(t, `
def run():
    u = User.objects.get(pk=1)
    u.save()
`)
	if findEdge(r, "run", "QuerySet.save", "calls") != nil {
		t.Fatal("a local assigned from a terminal chain call is not a QuerySet")
	}
}

func TestUnprovableReassignmentDropsType(t *testing.T) {
	r := parse(t, `
def run():
    q = Query(model)
    q = fetch()
    q.run()
`)
	if findEdge(r, "run", "Query.run", "calls") != nil {
		t.Fatal("a reassignment to an unprovable RHS must drop the stale type")
	}
	if findEdge(r, "run", "q.run", "calls") == nil {
		t.Fatal("expected the bare edge after the unprovable reassignment")
	}
}

func TestChainedAssignmentTypesInnerTargetOnly(t *testing.T) {
	// Pins the accidental-but-acceptable asymmetry: the inner assignment's
	// target is typed, the outer stays bare (its RHS is an assignment node).
	r := parse(t, `
def run():
    x = y = Query(model)
    y.run()
    x.run()
`)
	if findEdge(r, "run", "Query.run", "calls") == nil {
		t.Fatal("expected the inner chained-assignment target to be typed")
	}
	if findEdge(r, "run", "x.run", "calls") == nil {
		t.Fatal("expected the outer chained-assignment target to stay bare")
	}
}

func TestAsyncDefTypedParamResolves(t *testing.T) {
	r := parse(t, `
async def handle(query: Query):
    query.chain()
`)
	if findEdge(r, "handle", "Query.chain", "calls") == nil {
		t.Fatal("async def must type annotated params like a plain def")
	}
}

func TestTupleUnpackingStaysBare(t *testing.T) {
	r := parse(t, `
def run():
    a, b = Query(m), Raw(sql)
    a.run()
`)
	if findEdge(r, "run", "Query.run", "calls") != nil {
		t.Fatal("tuple-unpacking targets are not collected; a must stay bare")
	}
}

func TestUnprovableAnnotationReassignmentDropsType(t *testing.T) {
	r := parse(t, `
def run():
    q = Query(model)
    q: unknown_alias = fetch()
    q.run()
`)
	if findEdge(r, "run", "Query.run", "calls") != nil {
		t.Fatal("an annotated reassignment that proves nothing must drop the stale type")
	}
}

func TestChainMethodOnUnprovenReceiverStaysBare(t *testing.T) {
	// .filter is a QuerySet builder name, but the receiver is not provably a
	// QuerySet, so the assignment proves nothing.
	r := parse(t, `
def run(thing):
    x = thing.filter(y)
    x.count()
`)
	if findEdge(r, "run", "QuerySet.count", "calls") != nil {
		t.Fatal("a builder-named call on an unproven receiver must not type the local")
	}
}

func TestWalrusStaysBare(t *testing.T) {
	// named_expression is not an assignment node; stays bare by design.
	r := parse(t, `
def run():
    if (q := Query(m)):
        q.run()
`)
	if findEdge(r, "run", "Query.run", "calls") != nil {
		t.Fatal("walrus bindings are not collected; q must stay bare")
	}
}

func TestNonIdentifierAssignmentTargetIsSkipped(t *testing.T) {
	r := parse(t, `
def run(obj):
    obj.attr = Query(model)
    obj.attr.add_q(x)
`)
	if findEdge(r, "run", "Query.add_q", "calls") != nil {
		t.Fatal("attribute-target assignments must not enter the local type map")
	}
}

// Django receiver-name convention (double-keyed): a receiver literally named
// qs/queryset calling a known QuerySet method types as QuerySet — BOTH keys
// must agree; either alone stays bare.

func TestQuerySetNamedReceiverWithQuerySetMethodResolves(t *testing.T) {
	r := parse(t, `
def run(queryset):
    return queryset.filter(active=True)
`)
	e := findEdge(r, "run", "QuerySet.filter", "calls")
	if e == nil {
		t.Fatal("expected queryset.filter to resolve via the name+method convention")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("confidence = %v, want ConfidenceDynamic", e.Confidence)
	}
}

func TestQsNamedReceiverTerminalMethodResolves(t *testing.T) {
	r := parse(t, `
def run(qs):
    return qs.get(pk=1)
`)
	if findEdge(r, "run", "QuerySet.get", "calls") == nil {
		t.Fatal("expected qs.get to resolve via the name+method convention")
	}
}

func TestQsNamedReceiverNonQuerySetMethodStaysBare(t *testing.T) {
	r := parse(t, `
def run(qs):
    qs.append(x)
`)
	if findEdge(r, "run", "QuerySet.append", "calls") != nil {
		t.Fatal("append is not a QuerySet method; the name key alone must not type")
	}
	if findEdge(r, "run", "qs.append", "calls") == nil {
		t.Fatal("expected the bare edge")
	}
}

func TestOtherNamedReceiverWithQuerySetMethodStaysBare(t *testing.T) {
	r := parse(t, `
def run(items):
    items.filter(x)
`)
	if findEdge(r, "run", "QuerySet.filter", "calls") != nil {
		t.Fatal("filter on a non-convention name must not type; the method key alone must not type")
	}
}

func TestQsNamedChainContinues(t *testing.T) {
	r := parse(t, `
def run(qs):
    return qs.filter(a=1).annotate(n=Count("id"))
`)
	if findEdge(r, "run", "QuerySet.annotate", "calls") == nil {
		t.Fatal("expected the chain to continue from a convention-named receiver")
	}
}

func TestGetQuerysetAssignmentTypesLocal(t *testing.T) {
	r := parse(t, `
def run(self):
    objs = self.get_queryset()
    return objs.exclude(hidden=True)
`)
	if findEdge(r, "run", "QuerySet.exclude", "calls") == nil {
		t.Fatal("expected a local assigned from get_queryset() to type as QuerySet")
	}
}

func TestGetQuerysetDirectChainResolves(t *testing.T) {
	r := parse(t, `
def run(self):
    return self.get_queryset().filter(active=True)
`)
	if findEdge(r, "run", "QuerySet.filter", "calls") == nil {
		t.Fatal("expected a chain rooted at get_queryset() to resolve")
	}
}

func TestChainAssignmentTypesLocal(t *testing.T) {
	r := parse(t, `
def run(self):
    clone = self._chain()
    return clone.order_by("name")
`)
	if findEdge(r, "run", "QuerySet.order_by", "calls") == nil {
		t.Fatal("expected a local assigned from _chain() to type as QuerySet")
	}
}

func TestCloneIsNotAQuerySetConvention(t *testing.T) {
	// GIS geometries and sql.Query both have _clone; it proves nothing.
	r := parse(t, `
def run(self):
    shell = self._clone(rings)
    shell.filter(x)
`)
	if findEdge(r, "run", "QuerySet.filter", "calls") != nil {
		t.Fatal("_clone must not type the local as QuerySet")
	}
}
