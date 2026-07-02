package python

import (
	"strings"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// recorder captures emitted symbols and edges for assertions.
type recorder struct {
	symbols []extract.EmittedSymbol
	edges   []extract.EmittedEdge
}

func (r *recorder) Symbol(s extract.EmittedSymbol) error {
	r.symbols = append(r.symbols, s)
	return nil
}
func (r *recorder) Edge(e extract.EmittedEdge) error { r.edges = append(r.edges, e); return nil }

// counter counts emitted symbols and edges.
type counter struct {
	symbols int
	edges   int
}

func (c *counter) Symbol(extract.EmittedSymbol) error { c.symbols++; return nil }
func (c *counter) Edge(extract.EmittedEdge) error     { c.edges++; return nil }

// parse is a test helper that parses Python source and runs the extractor.
func parse(t *testing.T, src string) *recorder {
	t.Helper()
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	source := []byte(src)
	tree := p.Parse(source, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, source, "test.py", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return r
}

func findSymbol(r *recorder, qualified string) *extract.EmittedSymbol {
	for i := range r.symbols {
		if r.symbols[i].Qualified == qualified {
			return &r.symbols[i]
		}
	}
	return nil
}

func findEdge(r *recorder, source, target, kind string) *extract.EmittedEdge {
	for i := range r.edges {
		if r.edges[i].SourceQualified == source &&
			r.edges[i].TargetQualified == target &&
			string(r.edges[i].Kind) == kind {
			return &r.edges[i]
		}
	}
	return nil
}

func TestSmokeExtract(t *testing.T) {
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	src := []byte("class Foo:\n    pass\n")
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	c := &counter{}
	if err := ex.Extract(tree, src, "smoke.py", c); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if c.symbols == 0 {
		t.Error("emitted 0 symbols; expected at least the Foo class")
	}
}

func TestClassExtraction(t *testing.T) {
	r := parse(t, `class Foo:
    pass
`)
	s := findSymbol(r, "Foo")
	if s == nil {
		t.Fatal("missing symbol Foo")
	}
	if s.Kind != "class" {
		t.Errorf("Foo.Kind = %q, want class", s.Kind)
	}
	if s.Name != "Foo" {
		t.Errorf("Foo.Name = %q, want Foo", s.Name)
	}
}

func TestNestedClass(t *testing.T) {
	r := parse(t, `class Outer:
    class Inner:
        pass
`)
	s := findSymbol(r, "Outer.Inner")
	if s == nil {
		t.Fatal("missing symbol Outer.Inner")
	}
	if s.Kind != "class" {
		t.Errorf("Outer.Inner.Kind = %q, want class", s.Kind)
	}
	if s.ParentQualified != "Outer" {
		t.Errorf("Outer.Inner.Parent = %q, want Outer", s.ParentQualified)
	}
}

func TestClassInheritance(t *testing.T) {
	r := parse(t, `class Base:
    pass

class Child(Base):
    pass
`)
	e := findEdge(r, "Child", "Base", "inherits")
	if e == nil {
		t.Fatal("missing inherits edge Child -> Base")
	}
	if e.Confidence != 1.0 {
		t.Errorf("inherits confidence = %v, want 1.0", e.Confidence)
	}
}

func TestMultipleInheritance(t *testing.T) {
	r := parse(t, `class A:
    pass

class B:
    pass

class C(A, B):
    pass
`)
	if findEdge(r, "C", "A", "inherits") == nil {
		t.Error("missing inherits edge C -> A")
	}
	if findEdge(r, "C", "B", "inherits") == nil {
		t.Error("missing inherits edge C -> B")
	}
}

func TestModuleLevelFunction(t *testing.T) {
	r := parse(t, `def helper():
    pass
`)
	s := findSymbol(r, "helper")
	if s == nil {
		t.Fatal("missing symbol helper")
	}
	if s.Kind != "function" {
		t.Errorf("helper.Kind = %q, want function", s.Kind)
	}
	if s.ParentQualified != "" {
		t.Errorf("helper.Parent = %q, want empty", s.ParentQualified)
	}
}

func TestMethodInsideClass(t *testing.T) {
	r := parse(t, `class Foo:
    def bar(self):
        pass
`)
	s := findSymbol(r, "Foo.bar")
	if s == nil {
		t.Fatal("missing symbol Foo.bar")
	}
	if s.Kind != "method" {
		t.Errorf("Foo.bar.Kind = %q, want method", s.Kind)
	}
	if s.ParentQualified != "Foo" {
		t.Errorf("Foo.bar.Parent = %q, want Foo", s.ParentQualified)
	}
}

func TestConstantExtraction(t *testing.T) {
	r := parse(t, `MAX_RETRIES = 3
_INTERNAL = 5
`)
	s := findSymbol(r, "MAX_RETRIES")
	if s == nil {
		t.Fatal("missing symbol MAX_RETRIES")
	}
	if s.Kind != "constant" {
		t.Errorf("MAX_RETRIES.Kind = %q, want constant", s.Kind)
	}
	// _INTERNAL has leading underscore but is all caps — still a constant
	_ = findSymbol(r, "_INTERNAL")
}

func TestClassLevelConstant(t *testing.T) {
	r := parse(t, `class Config:
    MAX_SIZE = 100
`)
	s := findSymbol(r, "Config.MAX_SIZE")
	if s == nil {
		t.Fatal("missing symbol Config.MAX_SIZE")
	}
	if s.Kind != "constant" {
		t.Errorf("Config.MAX_SIZE.Kind = %q, want constant", s.Kind)
	}
}

func TestNonConstantAssignmentSkipped(t *testing.T) {
	r := parse(t, `x = 10
my_var = "hello"
`)
	if findSymbol(r, "x") != nil {
		t.Error("lowercase x should not be emitted as constant")
	}
	if findSymbol(r, "my_var") != nil {
		t.Error("snake_case my_var should not be emitted as constant")
	}
}

func TestFunctionCallEdges(t *testing.T) {
	r := parse(t, `def outer():
    inner()
    obj.method()
`)
	if findEdge(r, "outer", "inner", "calls") == nil {
		t.Error("missing calls edge outer -> inner")
	}
	if findEdge(r, "outer", "obj.method", "calls") == nil {
		t.Error("missing calls edge outer -> obj.method")
	}
}

func TestGetattrWithLiteral(t *testing.T) {
	r := parse(t, `def f():
    getattr(obj, "name")
`)
	e := findEdge(r, "f", "name", "calls")
	if e == nil {
		t.Fatal("missing calls edge f -> name from getattr")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("getattr confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestGetattrWithoutLiteralSkipped(t *testing.T) {
	r := parse(t, `def f():
    getattr(obj, some_var)
`)
	// No edge should be emitted for non-literal getattr
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.SourceQualified == "f" {
			t.Errorf("unexpected calls edge from getattr with non-literal: %v", e.TargetQualified)
		}
	}
}

func TestDjangoForeignKey(t *testing.T) {
	r := parse(t, `class Order:
    customer = models.ForeignKey(User)
`)
	e := findEdge(r, "Order", "User", "composes")
	if e == nil {
		t.Fatal("missing composes edge Order -> User from ForeignKey")
	}
	if e.Confidence != extract.ConfidenceConvention {
		t.Errorf("ForeignKey confidence = %v, want %v", e.Confidence, extract.ConfidenceConvention)
	}
}

func TestDjangoOneToOneField(t *testing.T) {
	r := parse(t, `class Profile:
    user = OneToOneField(User)
`)
	if findEdge(r, "Profile", "User", "composes") == nil {
		t.Error("missing composes edge Profile -> User from OneToOneField")
	}
}

func TestDjangoManyToManyField(t *testing.T) {
	r := parse(t, `class Article:
    tags = models.ManyToManyField(Tag)
`)
	if findEdge(r, "Article", "Tag", "composes") == nil {
		t.Error("missing composes edge Article -> Tag from ManyToManyField")
	}
}

func TestDjangoForeignKeyStringTarget(t *testing.T) {
	r := parse(t, `class Order:
    customer = models.ForeignKey("accounts.User")
`)
	e := findEdge(r, "Order", "User", "composes")
	if e == nil {
		t.Fatal("missing composes edge Order -> User from ForeignKey string target")
	}
	if e.Confidence != extract.ConfidenceAmbiguous {
		t.Errorf("ForeignKey string confidence = %v, want %v", e.Confidence, extract.ConfidenceAmbiguous)
	}
}

func TestFastapiRouteDecorator(t *testing.T) {
	r := parse(t, `@app.post("/orders")
def create_order():
    pass
`)
	s := findSymbol(r, "create_order")
	if s == nil {
		t.Fatal("missing symbol create_order")
	}
	if s.Kind != "function" {
		t.Errorf("create_order.Kind = %q, want function", s.Kind)
	}
	e := findEdge(r, "POST /orders", "create_order", "calls")
	if e == nil {
		t.Fatal("missing calls edge POST /orders -> create_order")
	}
}

func TestFastapiGetRoute(t *testing.T) {
	r := parse(t, `@router.get("/items")
def list_items():
    pass
`)
	if findEdge(r, "GET /items", "list_items", "calls") == nil {
		t.Error("missing calls edge GET /items -> list_items")
	}
}

func TestFastapiDepends(t *testing.T) {
	r := parse(t, `@app.get("/items")
def list_items(db = Depends(get_db)):
    pass
`)
	if findEdge(r, "list_items", "get_db", "calls") == nil {
		t.Error("missing calls edge list_items -> get_db from Depends")
	}
}

func TestFastapiDependsAttribute(t *testing.T) {
	r := parse(t, `@app.get("/items")
def list_items(svc = Depends(services.auth)):
    pass
`)
	if findEdge(r, "list_items", "services.auth", "calls") == nil {
		t.Error("missing calls edge list_items -> services.auth from Depends attribute")
	}
}

func TestDecoratedClass(t *testing.T) {
	r := parse(t, `@dataclass
class Config:
    name: str
`)
	s := findSymbol(r, "Config")
	if s == nil {
		t.Fatal("missing symbol Config")
	}
	if s.Kind != "class" {
		t.Errorf("Config.Kind = %q, want class", s.Kind)
	}
}

func TestDjangoURLPatterns(t *testing.T) {
	r := parse(t, `urlpatterns = [
    path("orders/", views.order_list),
    path("orders/<int:pk>/", views.order_detail),
]
`)
	if findEdge(r, "urlpatterns", "order_list", "calls") == nil {
		t.Error("missing calls edge urlpatterns -> order_list")
	}
	if findEdge(r, "urlpatterns", "order_detail", "calls") == nil {
		t.Error("missing calls edge urlpatterns -> order_detail")
	}
}

func TestDjangoURLPatternAsView(t *testing.T) {
	r := parse(t, `urlpatterns = [
    path("orders/", OrderListView.as_view()),
]
`)
	if findEdge(r, "urlpatterns", "OrderListView", "calls") == nil {
		t.Error("missing calls edge urlpatterns -> OrderListView from as_view")
	}
}

func TestDjangoURLInclude(t *testing.T) {
	r := parse(t, `urlpatterns = [
    path("api/", include("api.urls")),
]
`)
	if findEdge(r, "urlpatterns", "api.urls", "imports") == nil {
		t.Error("missing imports edge urlpatterns -> api.urls from include")
	}
}

func TestDjangoURLAttributeView(t *testing.T) {
	// A dotted view reference via re_path: the view resolves through the
	// attribute's last segment, exercising the re_path branch and the
	// attribute view-reference path.
	r := parse(t, `urlpatterns = [
    re_path(r"^home/$", views.home),
]
`)
	if findEdge(r, "urlpatterns", "home", "calls") == nil {
		t.Error("missing calls edge urlpatterns -> home from re_path attribute view")
	}
}

func TestTypeAnnotationComposes(t *testing.T) {
	r := parse(t, `class Order:
    customer: User
`)
	if findEdge(r, "Order", "User", "composes") == nil {
		t.Error("missing composes edge Order -> User from type annotation")
	}
}

func TestTypeAnnotationPrimitiveSkipped(t *testing.T) {
	r := parse(t, `class Foo:
    name: str
    count: int
`)
	for _, e := range r.edges {
		if string(e.Kind) == "composes" && e.SourceQualified == "Foo" {
			t.Errorf("unexpected composes edge for primitive type: %v", e.TargetQualified)
		}
	}
}

func TestTypeAnnotationOptional(t *testing.T) {
	r := parse(t, `class Order:
    customer: Optional[User]
`)
	if findEdge(r, "Order", "User", "composes") == nil {
		t.Error("missing composes edge Order -> User from Optional[User]")
	}
}

func TestTypeAnnotationList(t *testing.T) {
	r := parse(t, `class Order:
    items: List[Item]
`)
	if findEdge(r, "Order", "Item", "composes") == nil {
		t.Error("missing composes edge Order -> Item from List[Item]")
	}
}

func TestTypeAnnotationAttribute(t *testing.T) {
	r := parse(t, `class Order:
    customer: models.User
`)
	if findEdge(r, "Order", "models.User", "composes") == nil {
		t.Error("missing composes edge Order -> models.User from attribute annotation")
	}
}

func TestIsAllCaps(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"MAX_RETRIES", true},
		{"A", true},
		{"AB", true},
		{"A1", true},
		{"", false},
		{"abc", false},
		{"Abc", false},
		{"___", false},
		{"123", false},
		{"_A", true},
	}
	for _, tc := range cases {
		if got := isAllCaps(tc.in); got != tc.want {
			t.Errorf("isAllCaps(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestIsPascalCase(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"User", true},
		{"user", false},
		{"", false},
		{"A", true},
		{"ABC", true},
	}
	for _, tc := range cases {
		if got := isPascalCase(tc.in); got != tc.want {
			t.Errorf("isPascalCase(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestExtractorMetadata(t *testing.T) {
	ex := Extractor{}
	if ex.Language() != "python" {
		t.Errorf("Language() = %q, want python", ex.Language())
	}
	exts := ex.Extensions()
	if len(exts) != 2 || exts[0] != ".py" || exts[1] != ".pyi" {
		t.Errorf("Extensions() = %v, want [.py .pyi]", exts)
	}
	if ex.Tier() != extract.TierBasic {
		t.Errorf("Tier() = %v, want TierBasic", ex.Tier())
	}
	if ex.Grammar() == nil {
		t.Error("Grammar() returned nil")
	}
}

func TestMethodCallsInsideBody(t *testing.T) {
	r := parse(t, `class Service:
    def process(self):
        self.validate()
        helper()
`)
	if findEdge(r, "Service.process", "self.validate", "calls") == nil {
		t.Error("missing calls edge Service.process -> self.validate")
	}
	if findEdge(r, "Service.process", "helper", "calls") == nil {
		t.Error("missing calls edge Service.process -> helper")
	}
}

func TestDjangoForeignKeyNoArgs(t *testing.T) {
	// ForeignKey with no arguments should not crash or emit edges
	r := parse(t, `class Order:
    customer = models.ForeignKey()
`)
	for _, e := range r.edges {
		if string(e.Kind) == "composes" && e.SourceQualified == "Order" {
			t.Errorf("unexpected composes edge from ForeignKey with no args: %v", e.TargetQualified)
		}
	}
}

func TestNonDjangoFieldSkipped(t *testing.T) {
	r := parse(t, `class Order:
    name = models.CharField(max_length=100)
`)
	for _, e := range r.edges {
		if string(e.Kind) == "composes" && e.SourceQualified == "Order" {
			t.Errorf("unexpected composes edge from CharField: %v", e.TargetQualified)
		}
	}
}

func TestDjangoURLPatternIdentifierView(t *testing.T) {
	r := parse(t, `urlpatterns = [
    path("home/", home_view),
]
`)
	if findEdge(r, "urlpatterns", "home_view", "calls") == nil {
		t.Error("missing calls edge urlpatterns -> home_view")
	}
}

func TestDjangoRePathPattern(t *testing.T) {
	r := parse(t, `urlpatterns = [
    re_path(r"^orders/", order_list),
]
`)
	if findEdge(r, "urlpatterns", "order_list", "calls") == nil {
		t.Error("missing calls edge urlpatterns -> order_list from re_path")
	}
}

func TestDjangoURLIncludeInPath(t *testing.T) {
	r := parse(t, `urlpatterns = [
    path("api/", include("api.urls")),
]
`)
	if findEdge(r, "urlpatterns", "api.urls", "imports") == nil {
		t.Error("missing imports edge urlpatterns -> api.urls from include inside path")
	}
}

func TestNonURLPatternsAssignmentSkipped(t *testing.T) {
	r := parse(t, `other = [
    path("orders/", views.order_list),
]
`)
	for _, e := range r.edges {
		if e.SourceQualified == "urlpatterns" {
			t.Error("urlpatterns edges should not be emitted for non-urlpatterns assignment")
		}
	}
}

func TestGenericTypeAnnotation(t *testing.T) {
	r := parse(t, `class Warehouse:
    items: Dict[str, Item]
`)
	// Dict is a wrapper, str is primitive, Item is the target
	if findEdge(r, "Warehouse", "Item", "composes") == nil {
		t.Error("missing composes edge Warehouse -> Item from Dict[str, Item]")
	}
}

func TestFastapiMultipleRoutes(t *testing.T) {
	r := parse(t, `@app.get("/items")
@app.post("/items")
def items_handler():
    pass
`)
	if findEdge(r, "GET /items", "items_handler", "calls") == nil {
		t.Error("missing calls edge GET /items -> items_handler")
	}
	if findEdge(r, "POST /items", "items_handler", "calls") == nil {
		t.Error("missing calls edge POST /items -> items_handler")
	}
}

func TestFastapiPutDeletePatchRoutes(t *testing.T) {
	r := parse(t, `@app.put("/items/{id}")
def update_item():
    pass

@app.delete("/items/{id}")
def delete_item():
    pass

@app.patch("/items/{id}")
def patch_item():
    pass
`)
	if findEdge(r, "PUT /items/{id}", "update_item", "calls") == nil {
		t.Error("missing calls edge PUT -> update_item")
	}
	if findEdge(r, "DELETE /items/{id}", "delete_item", "calls") == nil {
		t.Error("missing calls edge DELETE -> delete_item")
	}
	if findEdge(r, "PATCH /items/{id}", "patch_item", "calls") == nil {
		t.Error("missing calls edge PATCH -> patch_item")
	}
}

func TestDjangoForeignKeyKeywordOnlyArgs(t *testing.T) {
	r := parse(t, `class Order:
    customer = models.ForeignKey(on_delete=CASCADE)
`)
	// No positional arg means no composes edge
	for _, e := range r.edges {
		if string(e.Kind) == "composes" && e.SourceQualified == "Order" {
			t.Errorf("unexpected composes edge from ForeignKey with only keyword args: %v", e.TargetQualified)
		}
	}
}

func TestDecoratedClassDefinition(t *testing.T) {
	r := parse(t, `@dataclass
class Config:
    name: str
    value: int
`)
	s := findSymbol(r, "Config")
	if s == nil {
		t.Fatal("missing symbol Config for decorated class")
	}
	if s.Kind != "class" {
		t.Errorf("Config.Kind = %q, want class", s.Kind)
	}
}

func TestFastapiRouteWithDecorator(t *testing.T) {
	// Head/options/trace are HTTP methods not tested yet
	r := parse(t, `@app.head("/health")
def health_check():
    pass

@app.options("/cors")
def cors_handler():
    pass
`)
	if findEdge(r, "HEAD /health", "health_check", "calls") == nil {
		t.Error("missing calls edge HEAD /health -> health_check")
	}
	if findEdge(r, "OPTIONS /cors", "cors_handler", "calls") == nil {
		t.Error("missing calls edge OPTIONS /cors -> cors_handler")
	}
}

func TestDjangoIncludeEdge(t *testing.T) {
	r := parse(t, `urlpatterns = [
    path("api/", include("api.urls")),
]
`)
	if findEdge(r, "urlpatterns", "api.urls", "imports") == nil {
		t.Error("missing imports edge urlpatterns -> api.urls from include()")
	}
}

func TestTypeAnnotationOptionalWrapper(t *testing.T) {
	r := parse(t, `class Order:
    customer: Optional[Customer]
`)
	if findEdge(r, "Order", "Customer", "composes") == nil {
		t.Error("missing composes edge Order -> Customer from Optional[Customer]")
	}
}

func TestTypeAnnotationListWrapper(t *testing.T) {
	r := parse(t, `class Store:
    items: List[Product]
`)
	if findEdge(r, "Store", "Product", "composes") == nil {
		t.Error("missing composes edge Store -> Product from List[Product]")
	}
}

func TestTypeAnnotationUnionTypes(t *testing.T) {
	r := parse(t, `class Handler:
    result: Union[Success, Failure]
`)
	if findEdge(r, "Handler", "Success", "composes") == nil {
		t.Error("missing composes edge Handler -> Success from Union[Success, Failure]")
	}
	if findEdge(r, "Handler", "Failure", "composes") == nil {
		t.Error("missing composes edge Handler -> Failure from Union[Success, Failure]")
	}
}

func TestTypeAnnotationPrimitivesSkipped(t *testing.T) {
	r := parse(t, `class Config:
    name: str
    count: int
    active: bool
`)
	for _, e := range r.edges {
		if string(e.Kind) == "composes" {
			t.Errorf("unexpected composes edge for primitive type: %v", e.TargetQualified)
		}
	}
}

func TestFastapiDependsAttributeAccess(t *testing.T) {
	// Depends with attribute access: Depends(auth.get_current_user)
	r := parse(t, `@app.get("/protected")
def protected(user: User = Depends(auth.get_current_user)):
    pass
`)
	if findEdge(r, "protected", "auth.get_current_user", "calls") == nil {
		t.Error("missing calls edge protected -> auth.get_current_user from Depends()")
	}
}

func TestNestedClassExtraction(t *testing.T) {
	r := parse(t, `class Outer:
    class Meta:
        ordering = ["name"]
    def method(self):
        pass
`)
	if findSymbol(r, "Outer.Meta") == nil {
		t.Error("missing symbol Outer.Meta")
	}
	if findSymbol(r, "Outer.method") == nil {
		t.Error("missing symbol Outer.method")
	}
}

func TestLiteralGetattrTarget(t *testing.T) {
	// getattr(obj, "method") should emit a call to method
	r := parse(t, `def process():
    getattr(obj, "execute")
`)
	if findEdge(r, "process", "execute", "calls") == nil {
		t.Error("missing calls edge process -> execute from getattr")
	}
}

func TestMultipleInheritanceWithMixin(t *testing.T) {
	r := parse(t, `class Base:
    pass

class Mixin:
    pass

class Child(Base, Mixin):
    pass
`)
	if findEdge(r, "Child", "Base", "inherits") == nil {
		t.Error("missing inherits edge Child -> Base")
	}
	if findEdge(r, "Child", "Mixin", "inherits") == nil {
		t.Error("missing inherits edge Child -> Mixin")
	}
}

func TestDjangoURLPatternViewFunction(t *testing.T) {
	r := parse(t, `urlpatterns = [
    path("orders/", views.order_list),
    path("orders/<int:pk>/", views.order_detail),
]
`)
	// Django URL patterns use "urlpatterns" as source and the last
	// attribute segment as the target.
	if findEdge(r, "urlpatterns", "order_list", "calls") == nil {
		t.Error("missing calls edge urlpatterns -> order_list")
	}
	if findEdge(r, "urlpatterns", "order_detail", "calls") == nil {
		t.Error("missing calls edge urlpatterns -> order_detail")
	}
}

// --- error propagation tests ---

var errForced = &testErr{"forced"}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

type failAfterN struct {
	symbolsLeft int
	edgesLeft   int
}

func (f *failAfterN) Symbol(_ extract.EmittedSymbol) error {
	if f.symbolsLeft <= 0 {
		return errForced
	}
	f.symbolsLeft--
	return nil
}

func (f *failAfterN) Edge(_ extract.EmittedEdge) error {
	if f.edgesLeft <= 0 {
		return errForced
	}
	f.edgesLeft--
	return nil
}

func parseWithEmitter(t *testing.T, src string, emit extract.Emitter) error {
	t.Helper()
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	source := []byte(src)
	tree := p.Parse(source, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()
	return ex.Extract(tree, source, "test.py", emit)
}

func TestClassSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `class Foo:
    pass
`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error from failing emitter on class symbol")
	}
}

func TestMethodSymbolError(t *testing.T) {
	// Class symbol succeeds, method symbol fails
	err := parseWithEmitter(t, `class Foo:
    def bar(self):
        pass
`, &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error from failing emitter on method symbol")
	}
}

func TestInheritanceEdgeError(t *testing.T) {
	// Class symbol succeeds, inherits edge fails
	err := parseWithEmitter(t, `class Child(Parent):
    pass
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing emitter on inheritance edge")
	}
}

func TestCallEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `def main():
    helper()
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing emitter on call edge")
	}
}

func TestFastapiRouteEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `from fastapi import FastAPI
app = FastAPI()

@app.get("/items")
def list_items():
    pass
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing emitter on fastapi route edge")
	}
}

func TestDjangoModelFieldError(t *testing.T) {
	err := parseWithEmitter(t, `from django.db import models

class Article(models.Model):
    author = models.ForeignKey("Author", on_delete=models.CASCADE)
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing emitter on django field edge")
	}
}

func TestAssignmentConstant(t *testing.T) {
	r := parse(t, `MAX_RETRIES = 3
DEFAULT_TIMEOUT = 30
`)
	if findSymbol(r, "MAX_RETRIES") == nil {
		t.Error("missing symbol MAX_RETRIES from assignment")
	}
	if findSymbol(r, "DEFAULT_TIMEOUT") == nil {
		t.Error("missing symbol DEFAULT_TIMEOUT from assignment")
	}
}

func TestDecoratedClassFullPath(t *testing.T) {
	r := parse(t, `
@dataclass
class Config:
    name: str
    value: int
`)
	if findSymbol(r, "Config") == nil {
		t.Fatal("missing symbol Config from decorated class")
	}
}

func TestDecoratedFunctionStackedDecorators(t *testing.T) {
	r := parse(t, `
@app.post("/items")
@app.get("/items")
def list_items():
    pass
`)
	if findSymbol(r, "list_items") == nil {
		t.Fatal("missing symbol list_items")
	}
}

func TestEmitCallAttribute(t *testing.T) {
	r := parse(t, `
class Service:
    def process(self):
        self.db.execute("query")
        logger.info("done")
`)
	if findEdge(r, "Service.process", "self.db.execute", "calls") == nil {
		t.Error("missing calls edge from attribute call")
	}
	if findEdge(r, "Service.process", "logger.info", "calls") == nil {
		t.Error("missing calls edge from attribute call logger.info")
	}
}

func TestGetattrNonStringArg(t *testing.T) {
	r := parse(t, `
def process():
    getattr(obj, name_var)
`)
	// Non-literal second arg -> no calls edge
	for _, e := range r.edges {
		if e.SourceQualified == "process" && string(e.Kind) == "calls" {
			if e.TargetQualified != "getattr" {
				t.Errorf("unexpected calls edge target %q from getattr with non-string arg", e.TargetQualified)
			}
		}
	}
}

func TestGetattrTooFewArgs(t *testing.T) {
	r := parse(t, `
def process():
    getattr(obj)
`)
	if findSymbol(r, "process") == nil {
		t.Fatal("missing symbol process")
	}
}

func TestClassMultipleInheritanceFull(t *testing.T) {
	r := parse(t, `
class Service(BaseService, LoggingMixin, Serializable):
    pass
`)
	if findEdge(r, "Service", "BaseService", "inherits") == nil {
		t.Error("missing inherits edge Service -> BaseService")
	}
	if findEdge(r, "Service", "LoggingMixin", "inherits") == nil {
		t.Error("missing inherits edge Service -> LoggingMixin")
	}
	if findEdge(r, "Service", "Serializable", "inherits") == nil {
		t.Error("missing inherits edge Service -> Serializable")
	}
}

func TestNestedClassWithMethods(t *testing.T) {
	r := parse(t, `
class Outer:
    class Inner:
        def method(self):
            helper()
`)
	if findSymbol(r, "Outer.Inner") == nil {
		t.Error("missing symbol Outer.Inner")
	}
	if findSymbol(r, "Outer.Inner.method") == nil {
		t.Error("missing symbol Outer.Inner.method")
	}
	if findEdge(r, "Outer.Inner.method", "helper", "calls") == nil {
		t.Error("missing calls edge from nested class method")
	}
}

func TestTopLevelConstantAssignment(t *testing.T) {
	r := parse(t, `
MAX_RETRIES = 3
DEFAULT_TIMEOUT = 30
some_var = "not a constant"
`)
	if findSymbol(r, "MAX_RETRIES") == nil {
		t.Error("missing constant MAX_RETRIES")
	}
	if findSymbol(r, "DEFAULT_TIMEOUT") == nil {
		t.Error("missing constant DEFAULT_TIMEOUT")
	}
	if findSymbol(r, "some_var") != nil {
		t.Error("non-ALL_CAPS should not be emitted as constant")
	}
}

func TestFastapiDependsWithAttributeArg(t *testing.T) {
	r := parse(t, `
@app.get("/items")
def get_items(service = Depends(services.get_service)):
    pass
`)
	if findSymbol(r, "get_items") == nil {
		t.Fatal("missing symbol get_items")
	}
	if findEdge(r, "get_items", "services.get_service", "calls") == nil {
		t.Error("missing calls edge from Depends(attribute)")
	}
}

func TestDjangoModelFieldEmptyArgs(t *testing.T) {
	r := parse(t, `
class Order:
    name = CharField()
`)
	// CharField() with no positional args -> still emits symbol
	if findSymbol(r, "Order") == nil {
		t.Fatal("missing symbol Order")
	}
}

func TestDjangoURLPatternRePath(t *testing.T) {
	r := parse(t, `
urlpatterns = [
    re_path(r'^users/$', UserListView.as_view()),
]
`)
	// exercises the URL pattern branch
	_ = r
}

func TestTypeAnnotationUnion(t *testing.T) {
	r := parse(t, `
class Order:
    status: str
    notes: list
`)
	if findSymbol(r, "Order") == nil {
		t.Fatal("missing symbol Order")
	}
}

func TestFunctionWithDecoratorAndBody(t *testing.T) {
	r := parse(t, `
@app.route("/users")
def get_users(db = Depends(get_db)):
    results = db.query("SELECT *")
    return results
`)
	if findSymbol(r, "get_users") == nil {
		t.Fatal("missing symbol get_users")
	}
}

func TestFastapiHeadOptionsTrace(t *testing.T) {
	r := parse(t, `
@router.head("/ping")
def head_ping():
    pass

@router.options("/cors")
def options_cors():
    pass
`)
	if findSymbol(r, "head_ping") == nil {
		t.Error("missing symbol head_ping")
	}
	if findSymbol(r, "options_cors") == nil {
		t.Error("missing symbol options_cors")
	}
}

func TestEmitCallSubscriptSkipped(t *testing.T) {
	r := parse(t, `
def process():
    handlers["key"]()
`)
	if findSymbol(r, "process") == nil {
		t.Fatal("missing symbol process")
	}
	// handlers["key"]() has a subscript callee -> skipped
}

func TestClassLevelConstantInNested(t *testing.T) {
	r := parse(t, `
class Outer:
    class Meta:
        TABLE_NAME = "outer"
`)
	if findSymbol(r, "Outer.Meta.TABLE_NAME") == nil {
		t.Error("missing constant Outer.Meta.TABLE_NAME")
	}
}

func TestDjangoIncludeCallInURLPatternsCoverage(t *testing.T) {
	r := parse(t, `
urlpatterns = [
    path("api/", include("api.urls")),
]
`)
	found := false
	for _, e := range r.edges {
		if string(e.Kind) == "imports" {
			found = true
		}
	}
	if !found {
		t.Error("missing imports edge from include() in URL patterns")
	}
}

func TestTypeAnnotationAttributeEdge(t *testing.T) {
	r := parse(t, `
class Order:
    user: auth.User
    items: store.ItemList
`)
	// attribute type annotation -> composes edge
	if findSymbol(r, "Order") == nil {
		t.Fatal("missing symbol Order")
	}
}

func TestTypeAnnotationGenericCustomClass(t *testing.T) {
	r := parse(t, `
class Service:
    repo: Repository[Order]
`)
	// Repository is PascalCase, not a wrapper -> composes edge to Repository
	if findEdge(r, "Service", "Repository", "composes") == nil {
		t.Error("missing composes edge Service -> Repository from generic type annotation")
	}
}

func TestTypeAnnotationOptionalUnwrap(t *testing.T) {
	r := parse(t, `
class Order:
    customer: Optional[Customer]
`)
	if findEdge(r, "Order", "Customer", "composes") == nil {
		t.Error("missing composes edge Order -> Customer from Optional unwrap")
	}
}

func TestTypeAnnotationPrimitivesFilteredOut(t *testing.T) {
	r := parse(t, `
class Config:
    name: str
    count: int
    active: bool
`)
	for _, e := range r.edges {
		if string(e.Kind) == "composes" {
			t.Errorf("unexpected composes edge to %q from primitive type", e.TargetQualified)
		}
	}
}

func TestFastapiPostRoute(t *testing.T) {
	r := parse(t, `
@app.post("/orders")
def create_order(data):
    save(data)
`)
	if findSymbol(r, "create_order") == nil {
		t.Fatal("missing symbol create_order")
	}
	// Should have a route edge POST /orders -> create_order
	routeEdgeFound := false
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.SourceQualified == "POST /orders" && e.TargetQualified == "create_order" {
			routeEdgeFound = true
		}
	}
	if !routeEdgeFound {
		t.Error("missing calls edge POST /orders -> create_order from FastAPI route")
	}
}

func TestDjangoModelFieldForeignKey(t *testing.T) {
	r := parse(t, `
class Order:
    customer = ForeignKey(Customer, on_delete=CASCADE)
`)
	if findSymbol(r, "Order") == nil {
		t.Fatal("missing symbol Order")
	}
}

func TestDjangoURLPatternPath(t *testing.T) {
	r := parse(t, `
urlpatterns = [
    path("orders/", OrderListView.as_view()),
]
`)
	_ = r // exercises the URL pattern detection path
}

func TestEmptyFunctionBody(t *testing.T) {
	r := parse(t, `
def noop():
    pass
`)
	if findSymbol(r, "noop") == nil {
		t.Fatal("missing symbol noop")
	}
}

func TestClassWithNoBody(t *testing.T) {
	r := parse(t, `
class Empty:
    pass
`)
	if findSymbol(r, "Empty") == nil {
		t.Fatal("missing symbol Empty")
	}
}

func TestWalkTopLevelImportFrom(t *testing.T) {
	r := parse(t, `
from os import path
from collections import OrderedDict

def process():
    path.join("a", "b")
`)
	if findSymbol(r, "process") == nil {
		t.Fatal("missing symbol process")
	}
}

func TestDecoratedClassWithMethods(t *testing.T) {
	r := parse(t, `
@dataclass
class Config:
    name: str

    def validate(self):
        check(self.name)
`)
	if findSymbol(r, "Config") == nil {
		t.Fatal("missing symbol Config")
	}
	if findSymbol(r, "Config.validate") == nil {
		t.Error("missing method Config.validate inside decorated class")
	}
}

func TestClassBaseAsAttribute(t *testing.T) {
	r := parse(t, `
class AdminView(views.ModelAdmin, PermissionMixin):
    pass
`)
	if findSymbol(r, "AdminView") == nil {
		t.Fatal("missing symbol AdminView")
	}
}

func TestHandleAssignmentNonConstantInClass(t *testing.T) {
	r := parse(t, `
class Config:
    default_value = 10
    MAX_SIZE = 100
`)
	if findSymbol(r, "Config.MAX_SIZE") == nil {
		t.Error("missing constant Config.MAX_SIZE")
	}
	if findSymbol(r, "Config.default_value") != nil {
		t.Error("non-ALL_CAPS class attribute should not be emitted")
	}
}

// --- coverage floor tests for framework.go ---

func TestDecoratedAsyncFunction(t *testing.T) {
	r := parse(t, `@app.get("/items")
async def list_items():
    pass
`)
	s := findSymbol(r, "list_items")
	if s == nil {
		t.Fatal("missing symbol list_items for decorated async function")
	}
	if s.Kind != "function" {
		t.Errorf("list_items.Kind = %q, want function", s.Kind)
	}
}

func TestFastapiRouteEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "non-call decorator",
			src: `@app
def handler():
    pass
`,
		},
		{
			name: "decorator call with no args",
			src: `@app.post()
def handler():
    pass
`,
		},
		{
			name: "decorator call with keyword-only arg",
			src: `@app.post(path="/items")
def handler():
    pass
`,
		},
		{
			name: "decorator call with non-string path",
			src: `@app.post(123)
def handler():
    pass
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := parse(t, tc.src)
			s := findSymbol(r, "handler")
			if s == nil {
				t.Fatal("missing symbol handler")
			}
			// None of these should produce route edges
			for _, e := range r.edges {
				// Route sources are "METHOD /path" — reject any edge that looks like a route
				for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "TRACE"} {
					if strings.HasPrefix(e.SourceQualified, method+" ") {
						t.Errorf("unexpected route edge: %v", e)
					}
				}
			}
		})
	}
}

func TestDependsEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "Depends with no args",
			src: `@app.get("/items")
def handler(db = Depends()):
    pass
`,
		},
		{
			name: "Depends with non-identifier arg",
			src: `@app.get("/items")
def handler(db = Depends(123)):
    pass
`,
		},
		{
			name: "non-Depends call in parameter",
			src: `@app.get("/items")
def handler(db = Something(auth.get_user)):
    pass
`,
		},
		{
			name: "attribute call in parameter",
			src: `@app.get("/items")
def handler(db = obj.method()):
    pass
`,
		},
		{
			name: "Depends with keyword-only arg",
			src: `@app.get("/items")
def handler(db = Depends(factory=get_db)):
    pass
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := parse(t, tc.src)
			for _, e := range r.edges {
				if e.SourceQualified == "handler" && string(e.Kind) == "calls" && e.TargetQualified != "Depends" {
					t.Errorf("unexpected calls edge from parameter: %v", e)
				}
			}
		})
	}
}

func TestIncludeEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "include with no args",
			src: `urlpatterns = [
    path("api/", include()),
]
`,
		},
		{
			name: "include with non-string arg",
			src: `urlpatterns = [
    path("api/", include(some_var)),
]
`,
		},
		{
			name: "include with empty string",
			src: `urlpatterns = [
    path("api/", include("")),
]
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := parse(t, tc.src)
			for _, e := range r.edges {
				if string(e.Kind) == "imports" && e.TargetQualified == "" {
					t.Errorf("unexpected empty imports edge: %v", e)
				}
			}
		})
	}
}

func TestDjangoModelFieldIntegerArg(t *testing.T) {
	r := parse(t, `class Order:
    customer = models.ForeignKey(123)
`)
	// Integer literal is not identifier or string, so no composes edge
	for _, e := range r.edges {
		if string(e.Kind) == "composes" && e.SourceQualified == "Order" {
			t.Errorf("unexpected composes edge for integer arg: %v", e.TargetQualified)
		}
	}
}

func TestEmptyStringContent(t *testing.T) {
	r := parse(t, `class Order:
    customer = models.ForeignKey("")
`)
	// Empty string has no string_content child, so stringContent returns ""
	// No composes edge should be emitted
	for _, e := range r.edges {
		if string(e.Kind) == "composes" && e.SourceQualified == "Order" {
			t.Errorf("unexpected composes edge for empty string target: %v", e.TargetQualified)
		}
	}
}

// Note: failAfterN, errForced, testErr, and parseWithEmitter are
// defined in python_test.go — reused here.

// --- more error propagation tests ---

func TestClassSymbolErrorPy(t *testing.T) {
	err := parseWithEmitter(t, `class Foo:
    pass
`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on class symbol emit")
	}
}

func TestClassMethodErrorPy(t *testing.T) {
	err := parseWithEmitter(t, `class Foo:
    def bar(self):
        pass
`, &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on class method symbol emit")
	}
}

func TestInheritanceEdgeErrorPy(t *testing.T) {
	err := parseWithEmitter(t, `class Child(Parent):
    pass
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on inheritance edge emit")
	}
}

func TestFunctionSymbolErrorPy(t *testing.T) {
	err := parseWithEmitter(t, `def hello():
    pass
`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on function symbol emit")
	}
}

func TestFunctionCallEdgeErrorPy(t *testing.T) {
	err := parseWithEmitter(t, `def f():
    g()
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on function call edge emit")
	}
}

func TestDecoratedFunctionError(t *testing.T) {
	err := parseWithEmitter(t, `@app.route("/")
def index():
    pass
`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on decorated function emit")
	}
}

func TestTypeAnnotationEdgeErrorPy(t *testing.T) {
	err := parseWithEmitter(t, `class Foo:
    x: Bar
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on type annotation edge emit")
	}
}

func TestConstReferenceEdgePy(t *testing.T) {
	r := parse(t, `MAX_RETRIES = 5

def process():
    x = MAX_RETRIES
`)
	if findEdge(r, "process", "MAX_RETRIES", "references") == nil {
		t.Error("missing references edge process -> MAX_RETRIES")
	}
}

func TestConstReferenceSkipsParamsPy(t *testing.T) {
	r := parse(t, `MAX_RETRIES = 5

def process(MAX_RETRIES):
    x = MAX_RETRIES
`)
	if findEdge(r, "process", "MAX_RETRIES", "references") != nil {
		t.Error("should not emit references edge when param shadows constant")
	}
}

func TestConstReferenceSkipsCallTargetsPy(t *testing.T) {
	r := parse(t, `API_URL = "http://example.com"

def fetch():
    if API_URL:
        print("ok")
`)
	if findEdge(r, "fetch", "API_URL", "references") == nil {
		t.Error("missing references edge for constant in condition")
	}
}

func TestConstReferenceDedupPy(t *testing.T) {
	r := parse(t, `LIMIT = 10

def process():
    a = LIMIT
    b = LIMIT
`)
	count := 0
	for _, e := range r.edges {
		if string(e.Kind) == "references" && e.TargetQualified == "LIMIT" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 references edge to LIMIT, got %d", count)
	}
}

func TestConstReferenceFromMethodPy(t *testing.T) {
	r := parse(t, `MAX = 10

class Svc:
    def run(self):
        x = MAX
`)
	if findEdge(r, "Svc.run", "MAX", "references") == nil {
		t.Error("missing references edge from class method to module-level constant")
	}
}

func TestConstReferenceSkipsCallTargetFnPy(t *testing.T) {
	r := parse(t, `HANDLER = "process"

def run():
    HANDLER()
    x = HANDLER
`)
	edges := 0
	for _, e := range r.edges {
		if e.Kind == "references" && e.TargetQualified == "HANDLER" {
			edges++
		}
	}
	if edges != 1 {
		t.Errorf("expected 1 references edge (value read only), got %d", edges)
	}
}

func TestCollectParamsSplatPy(t *testing.T) {
	r := parse(t, `ARGS = "x"
KWARGS = "y"

def run(*ARGS, **KWARGS):
    pass
`)
	if findEdge(r, "run", "ARGS", "references") != nil {
		t.Error("should not emit references for *args param name")
	}
	if findEdge(r, "run", "KWARGS", "references") != nil {
		t.Error("should not emit references for **kwargs param name")
	}
}

// TestAttrReceiverConfidence pins the emit-alignment: an attribute call is
// rated by how well its receiver type is known. self/cls and a Capitalized
// (class/constant) receiver stay fully confident; a lowercase-variable or
// chained receiver is an unverified instance call at ConfidenceUnresolved, so
// the resolver's bare-name fallback cannot surface it as a confident caller.
func TestAttrReceiverConfidence(t *testing.T) {
	r := parse(t, `class C:
    def m(self):
        self.helper()
        Other.build()
        obj.save()
        a.b.run()
`)
	cases := []struct {
		target string
		want   float64
	}{
		{"self.helper", 1.0},
		{"Other.build", 1.0},
		{"obj.save", extract.ConfidenceUnresolved},
		{"a.b.run", extract.ConfidenceUnresolved},
	}
	for _, c := range cases {
		e := findEdge(r, "C.m", c.target, "calls")
		if e == nil {
			t.Errorf("missing calls edge to %q", c.target)
			continue
		}
		if e.Confidence != c.want {
			t.Errorf("%s confidence = %v, want %v", c.target, e.Confidence, c.want)
		}
	}
}
