package ruby

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

// parseRuby is a test helper that parses Ruby source and runs the extractor.
func parseRuby(t *testing.T, src string) *recorder {
	return parseRubyWithPath(t, src, "test.rb")
}

func parseRubyWithPath(t *testing.T, src, path string) *recorder {
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
	if err := ex.Extract(tree, source, path, r); err != nil {
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
	tree := p.Parse([]byte("class Foo\nend\n"), nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	c := &counter{}
	if err := ex.Extract(tree, []byte("class Foo\nend\n"), "smoke.rb", c); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if c.symbols == 0 {
		t.Error("emitted 0 symbols; expected at least the Foo class")
	}
}

func TestClassExtraction(t *testing.T) {
	r := parseRuby(t, `class Order
end
`)
	s := findSymbol(r, "Order")
	if s == nil {
		t.Fatal("missing symbol Order")
	}
	if s.Kind != "class" {
		t.Errorf("Order.Kind = %q, want class", s.Kind)
	}
}

func TestModuleExtraction(t *testing.T) {
	r := parseRuby(t, `module Services
end
`)
	s := findSymbol(r, "Services")
	if s == nil {
		t.Fatal("missing symbol Services")
	}
	if s.Kind != "module" {
		t.Errorf("Services.Kind = %q, want module", s.Kind)
	}
}

func TestNestedClassModule(t *testing.T) {
	r := parseRuby(t, `module Admin
  class Dashboard
  end
end
`)
	s := findSymbol(r, "Admin::Dashboard")
	if s == nil {
		t.Fatal("missing symbol Admin::Dashboard")
	}
	if s.ParentQualified != "Admin" {
		t.Errorf("Admin::Dashboard.Parent = %q, want Admin", s.ParentQualified)
	}
}

func TestScopeResolutionClassName(t *testing.T) {
	r := parseRuby(t, `class Admin::UsersController
end
`)
	s := findSymbol(r, "Admin::UsersController")
	if s == nil {
		t.Fatal("missing symbol Admin::UsersController")
	}
	if s.Name != "UsersController" {
		t.Errorf("Name = %q, want UsersController", s.Name)
	}
}

func TestClassInheritance(t *testing.T) {
	r := parseRuby(t, `class ApplicationRecord
end

class Order < ApplicationRecord
end
`)
	e := findEdge(r, "Order", "ApplicationRecord", "inherits")
	if e == nil {
		t.Fatal("missing inherits edge Order -> ApplicationRecord")
	}
	if e.Confidence != 1.0 {
		t.Errorf("inherits confidence = %v, want 1.0", e.Confidence)
	}
}

func TestInstanceMethod(t *testing.T) {
	r := parseRuby(t, `class Order
  def process
  end
end
`)
	s := findSymbol(r, "Order#process")
	if s == nil {
		t.Fatal("missing symbol Order#process")
	}
	if s.Kind != "method" {
		t.Errorf("Order#process.Kind = %q, want method", s.Kind)
	}
	if s.ParentQualified != "Order" {
		t.Errorf("Order#process.Parent = %q, want Order", s.ParentQualified)
	}
}

func TestSingletonMethod(t *testing.T) {
	r := parseRuby(t, `class Order
  def self.find(id)
  end
end
`)
	s := findSymbol(r, "Order.find")
	if s == nil {
		t.Fatal("missing symbol Order.find")
	}
	if s.Kind != "method" {
		t.Errorf("Order.find.Kind = %q, want method", s.Kind)
	}
}

func TestTopLevelMethod(t *testing.T) {
	r := parseRuby(t, `def helper
end
`)
	s := findSymbol(r, "helper")
	if s == nil {
		t.Fatal("missing symbol helper")
	}
	if s.Kind != "method" {
		t.Errorf("helper.Kind = %q, want method", s.Kind)
	}
	if s.ParentQualified != "" {
		t.Errorf("helper.Parent = %q, want empty", s.ParentQualified)
	}
}

func TestConstantAssignment(t *testing.T) {
	r := parseRuby(t, `class Config
  VERSION = "1.0"
end
`)
	s := findSymbol(r, "Config::VERSION")
	if s == nil {
		t.Fatal("missing symbol Config::VERSION")
	}
	if s.Kind != "constant" {
		t.Errorf("Config::VERSION.Kind = %q, want constant", s.Kind)
	}
}

func TestTopLevelConstant(t *testing.T) {
	r := parseRuby(t, `APP_NAME = "sense"
`)
	s := findSymbol(r, "APP_NAME")
	if s == nil {
		t.Fatal("missing symbol APP_NAME")
	}
}

func TestIncludeEdge(t *testing.T) {
	r := parseRuby(t, `class Order
  include Printable
end
`)
	e := findEdge(r, "Order", "Printable", "includes")
	if e == nil {
		t.Fatal("missing includes edge Order -> Printable")
	}
	if e.Confidence != 1.0 {
		t.Errorf("includes confidence = %v, want 1.0", e.Confidence)
	}
}

func TestExtendEdge(t *testing.T) {
	r := parseRuby(t, `class Order
  extend ClassMethods
end
`)
	if findEdge(r, "Order", "ClassMethods", "includes") == nil {
		t.Error("missing includes edge Order -> ClassMethods from extend")
	}
}

func TestPrependEdge(t *testing.T) {
	r := parseRuby(t, `class Order
  prepend Auditable
end
`)
	if findEdge(r, "Order", "Auditable", "includes") == nil {
		t.Error("missing includes edge Order -> Auditable from prepend")
	}
}

func TestScopeResolutionInclude(t *testing.T) {
	r := parseRuby(t, `class Order
  include Admin::Helpers
end
`)
	if findEdge(r, "Order", "Admin::Helpers", "includes") == nil {
		t.Error("missing includes edge Order -> Admin::Helpers from scope resolution")
	}
}

func TestMethodCallEdges(t *testing.T) {
	r := parseRuby(t, `class Order
  def process
    validate
    save!
    customer.notify
  end
end
`)
	t.Run("bare_identifier", func(t *testing.T) {
		// Bare identifier `validate` is emitted as `self.validate` so the
		// resolver can rewrite it to the enclosing class. Confidence is
		// dynamic because tree-sitter parses it as an identifier node.
		e := findEdge(r, "Order#process", "self.validate", "calls")
		if e == nil {
			t.Fatal("missing calls edge Order#process -> self.validate")
		}
		if e.Confidence != extract.ConfidenceDynamic {
			t.Errorf("self.validate confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
		}
	})
	t.Run("call_node_no_receiver", func(t *testing.T) {
		// `save!` has no receiver — emitted as `self.save!` with static
		// confidence because tree-sitter parses it as a call node.
		e := findEdge(r, "Order#process", "self.save!", "calls")
		if e == nil {
			t.Fatal("missing calls edge Order#process -> self.save!")
		}
		if e.Confidence != 1.0 {
			t.Errorf("self.save! confidence = %v, want 1.0", e.Confidence)
		}
	})
	t.Run("unresolved_identifier_receiver", func(t *testing.T) {
		// `customer.notify` has an identifier receiver with no local type,
		// so we fall back to the bare method name at reduced confidence.
		e := findEdge(r, "Order#process", "notify", "calls")
		if e == nil {
			t.Fatal("missing calls edge Order#process -> notify")
		}
		if e.Confidence != extract.ConfidenceUnresolved {
			t.Errorf("notify confidence = %v, want %v", e.Confidence, extract.ConfidenceUnresolved)
		}
	})
}

func TestSendDynamicDispatch(t *testing.T) {
	r := parseRuby(t, `class Service
  def invoke
    send(:process)
  end
end
`)
	e := findEdge(r, "Service#invoke", "process", "calls")
	if e == nil {
		t.Fatal("missing calls edge Service#invoke -> process from send(:process)")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("send confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestPublicSendDynamicDispatch(t *testing.T) {
	r := parseRuby(t, `class Service
  def invoke
    public_send("process")
  end
end
`)
	if findEdge(r, "Service#invoke", "process", "calls") == nil {
		t.Error("missing calls edge from public_send with string arg")
	}
}

func TestSendNonLiteralSkipped(t *testing.T) {
	r := parseRuby(t, `class Service
  def invoke
    send(method_name)
  end
end
`)
	// send() with a non-literal argument must not fabricate an edge to a
	// guessed send target. The argument `method_name` is itself a
	// receiverless call (no local binding ⇒ a method send in Ruby), so a
	// `self.method_name` edge is expected; nothing else may be emitted.
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.SourceQualified == "Service#invoke" &&
			e.TargetQualified != "self.method_name" {
			t.Errorf("unexpected calls edge from send with non-literal arg: %v", e.TargetQualified)
		}
	}
}

func TestHasManyAssociation(t *testing.T) {
	r := parseRuby(t, `class Order
  has_many :line_items
end
`)
	e := findEdge(r, "Order", "LineItem", "composes")
	if e == nil {
		t.Fatal("missing composes edge Order -> LineItem from has_many")
	}
	if e.Confidence != extract.ConfidenceConvention {
		t.Errorf("has_many confidence = %v, want %v", e.Confidence, extract.ConfidenceConvention)
	}
}

func TestBelongsToAssociation(t *testing.T) {
	r := parseRuby(t, `class Order
  belongs_to :customer
end
`)
	if findEdge(r, "Order", "Customer", "composes") == nil {
		t.Error("missing composes edge Order -> Customer from belongs_to")
	}
}

func TestHasOneAssociation(t *testing.T) {
	r := parseRuby(t, `class User
  has_one :profile
end
`)
	if findEdge(r, "User", "Profile", "composes") == nil {
		t.Error("missing composes edge User -> Profile from has_one")
	}
}

func TestHasAndBelongsToManyAssociation(t *testing.T) {
	r := parseRuby(t, `class Article
  has_and_belongs_to_many :tags
end
`)
	if findEdge(r, "Article", "Tag", "composes") == nil {
		t.Error("missing composes edge Article -> Tag from habtm")
	}
}

func TestAssociationWithClassName(t *testing.T) {
	r := parseRuby(t, `class Order
  belongs_to :author, class_name: "User"
end
`)
	if findEdge(r, "Order", "User", "composes") == nil {
		t.Error("missing composes edge Order -> User from class_name override")
	}
}

func TestAssociationWithSerializer(t *testing.T) {
	r := parseRuby(t, `class OrderSerializer
  has_many :items, serializer: ItemDetailSerializer
end
`)
	if findEdge(r, "OrderSerializer", "ItemDetailSerializer", "composes") == nil {
		t.Error("missing composes edge from serializer override")
	}
}

func TestCallbackEdges(t *testing.T) {
	r := parseRuby(t, `class Order
  before_save :validate_total
  after_create :send_notification
end
`)
	if findEdge(r, "Order", "validate_total", "calls") == nil {
		t.Error("missing calls edge Order -> validate_total from before_save")
	}
	if findEdge(r, "Order", "send_notification", "calls") == nil {
		t.Error("missing calls edge Order -> send_notification from after_create")
	}
}

func TestValidateCallbackEdge(t *testing.T) {
	// `validate :method` is a custom-validation callback: it must emit a calls
	// edge to the predicate so the method does not read as zero-edge (and thus
	// falsely dead). A block-form `validate do ... end` carries no symbol and
	// must emit no spurious edge.
	r := parseRuby(t, `class PayoutRequestForm
  validate :amount_meets_minimum_threshold
  validate do
    errors.add(:base, "x")
  end

  private

  def amount_meets_minimum_threshold; end
end
`)
	if findEdge(r, "PayoutRequestForm", "amount_meets_minimum_threshold", "calls") == nil {
		t.Error("missing calls edge PayoutRequestForm -> amount_meets_minimum_threshold from validate")
	}
}

func TestMultipleCallbackArgs(t *testing.T) {
	r := parseRuby(t, `class Order
  before_action :authenticate, :authorize
end
`)
	if findEdge(r, "Order", "authenticate", "calls") == nil {
		t.Error("missing calls edge Order -> authenticate")
	}
	if findEdge(r, "Order", "authorize", "calls") == nil {
		t.Error("missing calls edge Order -> authorize")
	}
}

func TestCallbackEdgesEmitsSymbol(t *testing.T) {
	r := parseRuby(t, `class Order
  before_save :validate_total
  after_create :send_notification
end
`)
	if s := findSymbol(r, "Order.before_save"); s == nil {
		t.Error("missing symbol Order.before_save from callback declaration")
	} else if s.Kind != "method" {
		t.Errorf("callback symbol kind = %q, want method", s.Kind)
	}
	if findSymbol(r, "Order.after_create") == nil {
		t.Error("missing symbol Order.after_create from callback declaration")
	}
}

func TestCallbackEdgesDedup(t *testing.T) {
	r := parseRuby(t, `class OrdersController
  before_action :authenticate!
  before_action :set_order
end
`)
	count := 0
	for _, s := range r.symbols {
		if s.Qualified == "OrdersController.before_action" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 before_action symbol, got %d (duplicates not suppressed)", count)
	}
}

func TestCallbackEdgesNoArgs(t *testing.T) {
	r := parseRuby(t, `class Order
  before_save
end
`)
	if findSymbol(r, "Order.before_save") != nil {
		t.Error("should not emit symbol for callback with no arguments")
	}
}

func TestScopeEdge(t *testing.T) {
	r := parseRuby(t, `class Product
  scope :active, -> { where(active: true) }
  scope :recent, -> { order(created_at: :desc) }
end
`)
	if findSymbol(r, "Product.active") == nil {
		t.Error("missing symbol Product.active from scope declaration")
	}
	if findSymbol(r, "Product.recent") == nil {
		t.Error("missing symbol Product.recent from scope declaration")
	}
	if findEdge(r, "Product", "Product.active", "calls") == nil {
		t.Error("missing calls edge Product -> Product.active from scope")
	}
	if findEdge(r, "Product", "Product.recent", "calls") == nil {
		t.Error("missing calls edge Product -> Product.recent from scope")
	}
}

func TestScopeEdgeNonSymbolArg(t *testing.T) {
	r := parseRuby(t, `class Product
  scope "not_a_symbol", -> { where(active: true) }
end
`)
	for _, s := range r.symbols {
		if s.Name == "not_a_symbol" {
			t.Error("should not emit symbol for non-symbol scope argument")
		}
	}
}

func TestScopeEdgeNoArgs(t *testing.T) {
	r := parseRuby(t, `class Product
  scope
end
`)
	if len(r.edges) > 0 {
		for _, e := range r.edges {
			if e.SourceQualified == "Product" && string(e.Kind) == "calls" {
				t.Error("should not emit calls edge for scope with no arguments")
			}
		}
	}
}

func TestRSpecDescribe(t *testing.T) {
	r := parseRuby(t, `RSpec.describe Order do
end
`)
	if findEdge(r, "OrderTest", "Order", "tests") == nil {
		t.Error("missing tests edge OrderTest -> Order from RSpec.describe")
	}
}

func TestRSpecDescribeScopeResolution(t *testing.T) {
	r := parseRuby(t, `RSpec.describe Admin::Dashboard do
end
`)
	if findEdge(r, "Admin::DashboardTest", "Admin::Dashboard", "tests") == nil {
		t.Error("missing tests edge from RSpec.describe with scope resolution")
	}
}

func TestBareDescribeNonRSpec(t *testing.T) {
	r := parseRuby(t, `describe Order do
end
`)
	if findEdge(r, "OrderTest", "Order", "tests") == nil {
		t.Error("missing tests edge from bare describe")
	}
}

func TestResourcesRoute(t *testing.T) {
	r := parseRuby(t, `resources :orders
`)
	// resources :orders emits calls edges for all RESTful actions
	actions := []string{"index", "show", "new", "create", "edit", "update", "destroy"}
	for _, action := range actions {
		target := "OrdersController#" + action
		if findEdge(r, "routes", target, "calls") == nil {
			t.Errorf("missing calls edge routes -> %s", target)
		}
	}
}

func TestSingularResource(t *testing.T) {
	r := parseRuby(t, `resource :session
`)
	// resource (singular) should not have index action
	if findEdge(r, "routes", "SessionsController#index", "calls") != nil {
		t.Error("singular resource should not have index action")
	}
	if findEdge(r, "routes", "SessionsController#show", "calls") == nil {
		t.Error("missing calls edge routes -> SessionsController#show")
	}
}

func TestNamespacedRoutes(t *testing.T) {
	r := parseRuby(t, `namespace :admin do
  resources :users
end
`)
	if findEdge(r, "routes", "Admin::UsersController#index", "calls") == nil {
		t.Error("missing calls edge routes -> Admin::UsersController#index")
	}
}

func TestVerbRoute(t *testing.T) {
	r := parseRuby(t, `get "/home", to: "pages#home"
`)
	if findEdge(r, "routes", "PagesController#home", "calls") == nil {
		t.Error("missing calls edge routes -> PagesController#home from verb route")
	}
}

func TestNamespacedVerbRoute(t *testing.T) {
	r := parseRuby(t, `namespace :admin do
  get "/dashboard", to: "dashboard#index"
end
`)
	if findEdge(r, "routes", "Admin::DashboardController#index", "calls") == nil {
		t.Error("missing calls edge for namespaced verb route")
	}
}

func TestBroadcastsTo(t *testing.T) {
	r := parseRuby(t, `class Order
  broadcasts_to :orders
end
`)
	if findEdge(r, "Order", extract.PrefixTurboChannel+"orders", "calls") == nil {
		t.Error("missing calls edge Order -> turbo-channel:orders")
	}
}

func TestBroadcastsToString(t *testing.T) {
	r := parseRuby(t, `class Order
  broadcasts_to "updates"
end
`)
	if findEdge(r, "Order", extract.PrefixTurboChannel+"updates", "calls") == nil {
		t.Error("missing calls edge Order -> turbo-channel:updates from string")
	}
}

func TestImportmapPin(t *testing.T) {
	r := parseRubyWithPath(t, `pin "application"
`, "config/importmap.rb")
	if findEdge(r, "config/importmap.rb", extract.PrefixImportmap+"application", "imports") == nil {
		t.Error("missing imports edge from pin in importmap.rb")
	}
}

func TestImportmapPinWithTo(t *testing.T) {
	r := parseRubyWithPath(t, `pin "app", to: "application.js"
`, "config/importmap.rb")
	if findEdge(r, "config/importmap.rb", extract.PrefixImportmap+"application.js", "imports") == nil {
		t.Error("missing imports edge from pin with to: override")
	}
}

func TestImportmapPinAllFrom(t *testing.T) {
	r := parseRubyWithPath(t, `pin_all_from "app/javascript/controllers"
`, "config/importmap.rb")
	if findEdge(r, "config/importmap.rb", extract.PrefixImportmap+"app/javascript/controllers", "imports") == nil {
		t.Error("missing imports edge from pin_all_from")
	}
}

func TestImportmapPinNotInImportmapFile(t *testing.T) {
	r := parseRuby(t, `pin "application"
`)
	// pin outside importmap.rb should not emit edges
	for _, e := range r.edges {
		if string(e.Kind) == "imports" {
			t.Error("pin should not emit imports edge outside importmap.rb")
		}
	}
}

func TestTestCaseInheritanceTestsEdge(t *testing.T) {
	r := parseRuby(t, `class OrderTest < ActiveSupport::TestCase
end
`)
	if findEdge(r, "OrderTest", "Order", "tests") == nil {
		t.Error("missing tests edge OrderTest -> Order from TestCase inheritance")
	}
}

func TestIntegrationTestInheritanceTestsEdge(t *testing.T) {
	r := parseRuby(t, `class CheckoutIntegrationTest < ActionDispatch::IntegrationTest
end
`)
	if findEdge(r, "CheckoutIntegrationTest", "CheckoutIntegration", "tests") == nil {
		t.Error("missing tests edge from IntegrationTest inheritance")
	}
}

func TestBareIdentifierCalls(t *testing.T) {
	r := parseRuby(t, `class Order
  def process
    validate_order
  end
end
`)
	e := findEdge(r, "Order#process", "self.validate_order", "calls")
	if e == nil {
		t.Fatal("missing calls edge from bare identifier call")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("bare identifier confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestExtractorMetadata(t *testing.T) {
	ex := Extractor{}
	if ex.Language() != "ruby" {
		t.Errorf("Language() = %q, want ruby", ex.Language())
	}
	exts := ex.Extensions()
	if len(exts) != 3 {
		t.Errorf("Extensions() = %v, want 3 extensions", exts)
	}
	if ex.Tier() != extract.TierBasic {
		t.Errorf("Tier() = %v, want TierBasic", ex.Tier())
	}
	if ex.Grammar() == nil {
		t.Error("Grammar() returned nil")
	}
	// Ruby opts into the mention harvest, so the scan records `ruby` as
	// harvested and its private methods may earn `dead`.
	if !ex.HarvestsMentions() {
		t.Error("HarvestsMentions() = false, want true (Ruby streams the mention set)")
	}
}

func TestPascalCase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"order", "Order"},
		{"line_item", "LineItem"},
		{"user_profile", "UserProfile"},
		{"", ""},
		{"a", "A"},
	}
	for _, tc := range cases {
		if got := pascalCase(tc.in); got != tc.want {
			t.Errorf("pascalCase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestInferTestedClass(t *testing.T) {
	cases := []struct{ in, want string }{
		{"UserTest", "User"},
		{"Admin::DashboardControllerTest", "Admin::DashboardController"},
		{"Order", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := inferTestedClass(tc.in); got != tc.want {
			t.Errorf("inferTestedClass(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsTestSuperclass(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"ActiveSupport::TestCase", true},
		{"ActionDispatch::IntegrationTest", true},
		{"ActionDispatch::SystemTest", true},
		{"ApplicationRecord", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isTestSuperclass(tc.in); got != tc.want {
			t.Errorf("isTestSuperclass(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestNestedResourcesBlock(t *testing.T) {
	r := parseRuby(t, `resources :orders do
  resources :items
end
`)
	if findEdge(r, "routes", "OrdersController#index", "calls") == nil {
		t.Error("missing calls edge for parent resource")
	}
	if findEdge(r, "routes", "ItemsController#index", "calls") == nil {
		t.Error("missing calls edge for nested resource")
	}
}

func TestNonConstantAssignmentSkipped(t *testing.T) {
	r := parseRuby(t, `class Foo
  x = 10
end
`)
	if findSymbol(r, "Foo::x") != nil {
		t.Error("lowercase assignment should not be emitted as constant")
	}
}

func TestSuperclassNameScopeResolution(t *testing.T) {
	r := parseRuby(t, `
class Foo < ActiveRecord::Base
end
`)
	if findEdge(r, "Foo", "ActiveRecord::Base", "inherits") == nil {
		t.Error("missing inherits edge Foo -> ActiveRecord::Base from scope resolution superclass")
	}
}

func TestSuperclassNameWithUnsupportedNode(t *testing.T) {
	// Test inheritance with a method call as superclass (should not emit inherits edge)
	r := parseRuby(t, `
class Foo < some_method()
end
`)
	// Should not crash and should not emit any inherits edges
	for _, e := range r.edges {
		if string(e.Kind) == "inherits" && e.SourceQualified == "Foo" {
			t.Error("should not emit inherits edge for method call superclass")
		}
	}
}

// Note: superclassName is thoroughly tested through integration tests
// in TestClassInheritance and TestSuperclassNameScopeResolution.
// Direct unit testing is complex due to tree-sitter CGO dependencies.

func TestModuleWithMethods(t *testing.T) {
	r := parseRuby(t, `
module Services
  module Auth
    def authenticate
      validate
    end
  end
end
`)
	if findSymbol(r, "Services") == nil {
		t.Error("missing symbol Services")
	}
	if findSymbol(r, "Services::Auth") == nil {
		t.Error("missing symbol Services::Auth")
	}
	if findSymbol(r, "Services::Auth#authenticate") == nil {
		t.Error("missing method Services::Auth#authenticate")
	}
}

func TestBareIdentifierCallsInMethodBody(t *testing.T) {
	r := parseRuby(t, `
class Service
  def process
    validate
    transform
    save
  end
end
`)
	// bare identifiers in statement position are emitted as self.name
	e := findEdge(r, "Service#process", "self.validate", "calls")
	if e == nil {
		t.Fatal("missing calls edge from bare identifier validate")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("bare identifier confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestCallbackEdgesMultiple(t *testing.T) {
	r := parseRuby(t, `
class Order
  before_save :validate_total, :update_status
end
`)
	if findEdge(r, "Order", "validate_total", "calls") == nil {
		t.Error("missing calls edge Order -> validate_total from callback")
	}
	if findEdge(r, "Order", "update_status", "calls") == nil {
		t.Error("missing calls edge Order -> update_status from callback")
	}
}

func TestDescribeWithRSpecReceiver(t *testing.T) {
	r := parseRuby(t, `
RSpec.describe User do
end
`)
	if findEdge(r, "UserTest", "User", "tests") == nil {
		t.Error("missing tests edge UserTest -> User from RSpec.describe")
	}
}

func TestDescribeWithNonRSpecReceiver(t *testing.T) {
	r := parseRuby(t, `
MyLib.describe "something" do
end
`)
	for _, e := range r.edges {
		if string(e.Kind) == "tests" {
			t.Error("should not emit tests edge for non-RSpec describe")
		}
	}
}

func TestDescribeWithStringArg(t *testing.T) {
	r := parseRuby(t, `
describe "some behavior" do
end
`)
	for _, e := range r.edges {
		if string(e.Kind) == "tests" {
			t.Error("should not emit tests edge for string arg describe")
		}
	}
}

func TestResourcesWithNestedBlock(t *testing.T) {
	r := parseRuby(t, `
resources :orders do
  resources :items
end
`)
	// Should emit calls edges for both orders and items routes.
	ordersFound := false
	itemsFound := false
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.TargetQualified == "OrdersController#index" {
			ordersFound = true
		}
		if string(e.Kind) == "calls" && e.TargetQualified == "ItemsController#index" {
			itemsFound = true
		}
	}
	if !ordersFound {
		t.Error("missing calls edge to OrdersController#index")
	}
	if !itemsFound {
		t.Error("missing calls edge to ItemsController#index")
	}
}

func TestSingularResourceActions(t *testing.T) {
	r := parseRuby(t, `
resource :session
`)
	// Singular resource should use singularResourceActions (no index).
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.TargetQualified == "SessionsController#index" {
			t.Error("singular resource should not produce index action")
		}
	}
	if findEdge(r, "routes", "SessionsController#show", "calls") == nil {
		t.Error("missing calls edge to SessionsController#show")
	}
}

func TestNamespacedRoutesWithVerbRoute(t *testing.T) {
	r := parseRuby(t, `
namespace :admin do
  get "/dashboard", to: "dashboard#index"
end
`)
	if findEdge(r, "routes", "Admin::DashboardController#index", "calls") == nil {
		t.Error("missing calls edge with namespace prefix Admin::DashboardController#index")
	}
}

func TestVerbRouteWithoutTo(t *testing.T) {
	r := parseRuby(t, `
get "/health"
`)
	// No to: keyword -> no calls edge
	for _, e := range r.edges {
		if string(e.Kind) == "calls" {
			t.Errorf("unexpected calls edge %q without to: in route", e.TargetQualified)
		}
	}
}

func TestVerbRouteToWithoutHash(t *testing.T) {
	r := parseRuby(t, `
get "/app", to: "static_pages"
`)
	for _, e := range r.edges {
		if string(e.Kind) == "calls" {
			t.Errorf("unexpected calls edge %q for to: without #", e.TargetQualified)
		}
	}
}

func TestBroadcastsToSymbolArg(t *testing.T) {
	r := parseRuby(t, `
class Message
  broadcasts_to :channel
end
`)
	found := false
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.SourceQualified == "Message" {
			found = true
		}
	}
	if !found {
		t.Error("missing calls edge from broadcasts_to symbol arg")
	}
}

func TestBroadcastsNonLiteralArg(t *testing.T) {
	r := parseRuby(t, `
class Message
  broadcasts_to some_method
end
`)
	// Non-literal arg -> no edge
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.SourceQualified == "Message" {
			t.Error("should not emit calls edge for non-literal broadcasts arg")
		}
	}
}

func TestEmitCallWithReceiverChain(t *testing.T) {
	r := parseRuby(t, `
class Service
  def process
    connection.execute("query")
    Rails.logger.info("done")
  end
end
`)
	// `connection` is an unresolved identifier receiver → bare method name.
	e := findEdge(r, "Service#process", "execute", "calls")
	if e == nil {
		t.Fatal("missing calls edge from receiver.method call")
	}
	if e.Confidence != extract.ConfidenceUnresolved {
		t.Errorf("execute confidence = %v, want %v", e.Confidence, extract.ConfidenceUnresolved)
	}
	// `Rails.logger` is a call-chain receiver → bare method name.
	e = findEdge(r, "Service#process", "info", "calls")
	if e == nil {
		t.Fatal("missing calls edge from chained receiver call")
	}
	if e.Confidence != extract.ConfidenceUnresolved {
		t.Errorf("info confidence = %v, want %v", e.Confidence, extract.ConfidenceUnresolved)
	}
}

func TestLocalTypeInference(t *testing.T) {
	r := parseRuby(t, `
class Order
  def save; end
end

class Service
  def process
    order = Order.new
    order.save
  end
end
`)
	// Local-variable assignment `order = Order.new` lets us resolve
	// `order.save` to the instance method `Order#save`.
	e := findEdge(r, "Service#process", "Order#save", "calls")
	if e == nil {
		t.Fatal("missing calls edge Service#process -> Order#save from local type inference")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("inferred edge confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestNewChainResolution(t *testing.T) {
	r := parseRuby(t, `
class TopicCreator
  def create; end
  def self.build
    TopicCreator.new.create
  end
end
`)
	// `TopicCreator.new.create` is a `.new` chain → we infer the instance method.
	e := findEdge(r, "TopicCreator.build", "TopicCreator#create", "calls")
	if e == nil {
		t.Fatal("missing calls edge from TopicCreator.new.create chain")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("chain edge confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestSelfNewChainResolution(t *testing.T) {
	r := parseRuby(t, `
class TopicCreator
  def create; end
  def self.build
    self.new.create
  end
end
`)
	// `self.new.create` inside a singleton method → instance method of the class.
	e := findEdge(r, "TopicCreator.build", "TopicCreator#create", "calls")
	if e == nil {
		t.Fatal("missing calls edge from self.new.create chain")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("chain edge confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestOperatorAssignmentLocalType(t *testing.T) {
	r := parseRuby(t, `
class Order
  def save; end
end

class Service
  def process
    order ||= Order.new
    order.save
  end
end
`)
	// `operator_assignment` (`||=`) should be handled the same as regular assignment.
	e := findEdge(r, "Service#process", "Order#save", "calls")
	if e == nil {
		t.Fatal("missing calls edge from operator-assignment local type")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("inferred edge confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestLocalTypeMapNegative(t *testing.T) {
	r := parseRuby(t, `
class Order
  def save; end
end

class Service
  def process
    order = "not an order"
    order.save
  end
end
`)
	// A non-`.new` assignment should NOT create a local-type entry, so
	// `order.save` falls back to the bare method name.
	e := findEdge(r, "Service#process", "save", "calls")
	if e == nil {
		t.Fatal("missing bare fallback edge for non-.new assignment")
	}
	if e.Confidence != extract.ConfidenceUnresolved {
		t.Errorf("bare fallback confidence = %v, want %v", e.Confidence, extract.ConfidenceUnresolved)
	}
}

func TestLocalTypeInferenceScopeResolution(t *testing.T) {
	r := parseRuby(t, `
module Admin
  class Order
    def save; end
  end
end

class Service
  def process
    order = Admin::Order.new
    order.save
  end
end
`)
	// Scope-resolution receiver `Admin::Order.new` should resolve via
	// local type inference exactly like a simple constant.
	e := findEdge(r, "Service#process", "Admin::Order#save", "calls")
	if e == nil {
		t.Fatal("missing calls edge for scope-resolution local type")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("inferred edge confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestLocalTypeReassignmentShadow(t *testing.T) {
	r := parseRuby(t, `
class Order
  def save; end
end

class Invoice
  def pay; end
end

class Service
  def process
    order = Order.new
    order = Invoice.new
    order.pay
  end
end
`)
	// Last-write-wins: `order` is reassigned to `Invoice`, so `order.pay`
	// should resolve to `Invoice#pay`, not `Order#save`.
	e := findEdge(r, "Service#process", "Invoice#pay", "calls")
	if e == nil {
		t.Fatal("missing calls edge for reassignment shadow")
	}
	if findEdge(r, "Service#process", "Order#save", "calls") != nil {
		t.Error("unexpected Order#save edge after reassignment shadow")
	}
}

func TestConstantAssignmentInScope(t *testing.T) {
	r := parseRuby(t, `
class Config
  VERSION = "1.0"
  MAX_RETRIES = 3
end
`)
	if findSymbol(r, "Config::VERSION") == nil {
		t.Error("missing constant Config::VERSION")
	}
	if findSymbol(r, "Config::MAX_RETRIES") == nil {
		t.Error("missing constant Config::MAX_RETRIES")
	}
}

func TestIncludeWithScopeResolution(t *testing.T) {
	r := parseRuby(t, `
class Service
  include Concerns::Loggable
end
`)
	if findEdge(r, "Service", "Concerns::Loggable", "includes") == nil {
		t.Error("missing includes edge Service -> Concerns::Loggable")
	}
}

func TestSingletonMethodInClass(t *testing.T) {
	r := parseRuby(t, `
class Builder
  def self.build
    create
  end
end
`)
	s := findSymbol(r, "Builder.build")
	if s == nil {
		t.Fatal("missing symbol Builder.build for singleton method")
	}
	if s.Kind != "method" {
		t.Errorf("Builder.build.Kind = %q, want method", s.Kind)
	}
}

func TestImportmapPinNotImportmapFileCoverage(t *testing.T) {
	r := parseRubyWithPath(t, `
pin "application", to: "app.js"
`, "config/routes.rb")
	// Not importmap.rb -> no imports edge
	for _, e := range r.edges {
		if string(e.Kind) == "imports" {
			t.Error("should not emit imports edge outside importmap.rb")
		}
	}
}

func TestImportmapPinInFile(t *testing.T) {
	r := parseRubyWithPath(t, `
pin "turbo-rails", to: "turbo.min.js"
pin "application"
`, "config/importmap.rb")
	found := 0
	for _, e := range r.edges {
		if string(e.Kind) == "imports" {
			found++
		}
	}
	if found < 2 {
		t.Errorf("expected at least 2 imports edges from importmap, got %d", found)
	}
}

func TestAssociationWithClassNameOverrideCoverage(t *testing.T) {
	r := parseRuby(t, `
class Order
  belongs_to :user, class_name: "Customer"
end
`)
	if findEdge(r, "Order", "Customer", "composes") == nil {
		t.Error("missing composes edge Order -> Customer from class_name override")
	}
}

func TestBroadcastsToStringArg(t *testing.T) {
	r := parseRuby(t, `
class Notification
  broadcasts_to "updates"
end
`)
	found := false
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.SourceQualified == "Notification" {
			found = true
		}
	}
	if !found {
		t.Error("missing calls edge from broadcasts_to string arg")
	}
}

func TestLiteralSendTargetSimpleSymbol(t *testing.T) {
	r := parseRuby(t, `
class Service
  def process
    send(:validate)
  end
end
`)
	if findEdge(r, "Service#process", "validate", "calls") == nil {
		t.Error("missing calls edge from send(:validate)")
	}
}

func TestLiteralSendTargetString(t *testing.T) {
	r := parseRuby(t, `
class Service
  def process
    public_send("execute")
  end
end
`)
	if findEdge(r, "Service#process", "execute", "calls") == nil {
		t.Error("missing calls edge from public_send(\"execute\")")
	}
}

func TestSendTargetNonLiteral(t *testing.T) {
	r := parseRuby(t, `
class Service
  def process
    send(method_name)
  end
end
`)
	for _, e := range r.edges {
		if e.SourceQualified == "Service#process" && string(e.Kind) == "calls" && e.TargetQualified == "method_name" {
			t.Error("should not emit calls edge for non-literal send target")
		}
	}
}

func TestSendTargetVariableSymbol(t *testing.T) {
	r := parseRuby(t, `
class Service
  def process
    callback = :after_save
    send(callback)
  end
end
`)
	e := findEdge(r, "Service#process", "after_save", "calls")
	if e == nil {
		t.Fatal("missing calls edge from variable-based send with symbol assignment")
	}
	if e.Confidence != ConfidenceHeuristicDispatch {
		t.Errorf("confidence = %v, want %v", e.Confidence, ConfidenceHeuristicDispatch)
	}
}

func TestSendTargetVariableString(t *testing.T) {
	r := parseRuby(t, `
class Service
  def process
    action = "index"
    send(action)
  end
end
`)
	e := findEdge(r, "Service#process", "index", "calls")
	if e == nil {
		t.Fatal("missing calls edge from variable-based send with string assignment")
	}
	if e.Confidence != ConfidenceHeuristicDispatch {
		t.Errorf("confidence = %v, want %v", e.Confidence, ConfidenceHeuristicDispatch)
	}
}

func TestPublicSendTargetVariable(t *testing.T) {
	r := parseRuby(t, `
class Service
  def process
    callback = :process
    public_send(callback)
  end
end
`)
	e := findEdge(r, "Service#process", "process", "calls")
	if e == nil {
		t.Fatal("missing calls edge from variable-based public_send")
	}
}

func TestSendTargetVariableUnderscoreSend(t *testing.T) {
	r := parseRuby(t, `
class Service
  def process
    callback = :after_save
    __send__(callback)
  end
end
`)
	e := findEdge(r, "Service#process", "after_save", "calls")
	if e == nil {
		t.Fatal("missing calls edge from variable-based __send__")
	}
	if e.Confidence != ConfidenceHeuristicDispatch {
		t.Errorf("confidence = %v, want %v", e.Confidence, ConfidenceHeuristicDispatch)
	}
}

func TestSendTargetVariableAssignmentAfterSend(t *testing.T) {
	r := parseRuby(t, `
class Service
  def process
    send(callback)
    callback = :should_not_use_this
  end
end
`)
	for _, e := range r.edges {
		if e.SourceQualified == "Service#process" && string(e.Kind) == "calls" {
			t.Error("should not emit calls edge when assignment appears after send")
		}
	}
}

func TestSendTargetVariableNoPatternMatch(t *testing.T) {
	r := parseRuby(t, `
class Service
  def process
    count = :something
    send(count)
  end
end
`)
	for _, e := range r.edges {
		if e.SourceQualified == "Service#process" && string(e.Kind) == "calls" && e.TargetQualified == "something" {
			t.Error("should not emit calls edge when variable name does not match method-name patterns")
		}
	}
}

func TestSendTargetVariableNonSelfReceiver(t *testing.T) {
	r := parseRuby(t, `
class Service
  def process
    callback = :after_save
    obj.send(callback)
  end
end
`)
	for _, e := range r.edges {
		if e.SourceQualified == "Service#process" && string(e.Kind) == "calls" && e.TargetQualified == "after_save" {
			t.Error("should not emit heuristic edge when receiver is not self")
		}
	}
}

func TestSendTargetVariableNoAssignment(t *testing.T) {
	r := parseRuby(t, `
class Service
  def process
    send(callback)
  end
end
`)
	// The send target stays unresolved, but `callback` has no local binding,
	// so it is itself a receiverless call — `self.callback` is expected and is
	// the only edge allowed.
	for _, e := range r.edges {
		if e.SourceQualified == "Service#process" && string(e.Kind) == "calls" &&
			e.TargetQualified != "self.callback" {
			t.Errorf("unexpected calls edge: %v", e.TargetQualified)
		}
	}
}

func TestSendTargetVariableEmptyString(t *testing.T) {
	// Edge case: string assignment with empty string content
	r := parseRuby(t, `
class Service
  def process
    callback = ""
    send(callback)
  end
end
`)
	// Should not emit edge for empty string assignment
	for _, e := range r.edges {
		if e.SourceQualified == "Service#process" && string(e.Kind) == "calls" && e.TargetQualified == "" {
			t.Error("should not emit calls edge for empty string assignment")
		}
	}
}

func TestSingularizeViaHasMany(t *testing.T) {
	r := parseRuby(t, `
class User
  has_many :orders
end
`)
	if findEdge(r, "User", "Order", "composes") == nil {
		t.Error("missing composes edge User -> Order from has_many :orders")
	}
}

func TestHasManyWithSerializerOverride(t *testing.T) {
	r := parseRuby(t, `
class Topic
  has_many :posts, serializer: PostSerializer
end
`)
	if findEdge(r, "Topic", "PostSerializer", "composes") == nil {
		t.Error("missing composes edge Topic -> PostSerializer from serializer: override")
	}
}

func TestBelongsToSimple(t *testing.T) {
	r := parseRuby(t, `
class Order
  belongs_to :customer
end
`)
	if findEdge(r, "Order", "Customer", "composes") == nil {
		t.Error("missing composes edge Order -> Customer from belongs_to")
	}
}

func TestDescribeWithScopeResolution(t *testing.T) {
	r := parseRuby(t, `
RSpec.describe Admin::Dashboard do
end
`)
	if findEdge(r, "Admin::DashboardTest", "Admin::Dashboard", "tests") == nil {
		t.Error("missing tests edge from RSpec.describe with scope_resolution")
	}
}

func TestImportmapPinAllFromDir(t *testing.T) {
	r := parseRubyWithPath(t, `
pin_all_from "app/javascript/controllers", under: "controllers"
`, "config/importmap.rb")
	found := false
	for _, e := range r.edges {
		if string(e.Kind) == "imports" {
			found = true
		}
	}
	if !found {
		t.Error("missing imports edge from pin_all_from in importmap.rb")
	}
}

func TestClassWithNamespacedName(t *testing.T) {
	r := parseRuby(t, `
class Admin::Dashboard
  def index
    render
  end
end
`)
	if findSymbol(r, "Admin::Dashboard") == nil {
		t.Error("missing symbol Admin::Dashboard")
	}
	if findSymbol(r, "Admin::Dashboard#index") == nil {
		t.Error("missing method Admin::Dashboard#index")
	}
}

func TestTestInheritance(t *testing.T) {
	r := parseRuby(t, `
class OrderTest < ActiveSupport::TestCase
  def test_something
    assert true
  end
end
`)
	if findEdge(r, "OrderTest", "Order", "tests") == nil {
		t.Error("missing tests edge OrderTest -> Order from test inheritance")
	}
}

func TestCallbackEdgesNonSymbolArgs(t *testing.T) {
	r := parseRuby(t, `
class Order
  before_save -> { update_total }
end
`)
	for _, e := range r.edges {
		if e.SourceQualified == "Order" && string(e.Kind) == "calls" {
			t.Errorf("should not emit calls edge for lambda callback arg, got target %q", e.TargetQualified)
		}
	}
}

func TestIncludeMultipleModules(t *testing.T) {
	r := parseRuby(t, `
class Service
  include Loggable, Cacheable
end
`)
	if findEdge(r, "Service", "Loggable", "includes") == nil {
		t.Error("missing includes edge Service -> Loggable")
	}
	if findEdge(r, "Service", "Cacheable", "includes") == nil {
		t.Error("missing includes edge Service -> Cacheable")
	}
}

func TestIncludeNoArgs(t *testing.T) {
	r := parseRuby(t, `
class Service
  include
end
`)
	// No args -> no edge
	for _, e := range r.edges {
		if string(e.Kind) == "includes" {
			t.Error("should not emit includes edge for include with no args")
		}
	}
}

func TestIncludeWithNonConstantArg(t *testing.T) {
	r := parseRuby(t, `
class Service
  include some_method
end
`)
	// Non-constant arg -> no edge
	for _, e := range r.edges {
		if string(e.Kind) == "includes" {
			t.Error("should not emit includes edge for include with non-constant arg")
		}
	}
}

func TestNamespacedRouteResources(t *testing.T) {
	r := parseRuby(t, `
namespace :api do
  resources :users
end
`)
	if findEdge(r, "routes", "Api::UsersController#index", "calls") == nil {
		t.Error("missing calls edge to Api::UsersController#index from namespaced resources")
	}
}

func TestHandleRouteNamespaceNoBlock(t *testing.T) {
	r := parseRuby(t, `
namespace :admin
`)
	// No block -> no nested routes, should not crash
	_ = r
}

func TestClassNoSuperclass(t *testing.T) {
	r := parseRuby(t, `
class Simple
  def run
    execute
  end
end
`)
	for _, e := range r.edges {
		if string(e.Kind) == "inherits" && e.SourceQualified == "Simple" {
			t.Errorf("unexpected inherits edge from class without superclass")
		}
	}
}

// --- error propagation ---

type failAfterN struct {
	symbolsLeft int
	edgesLeft   int
}

var errForced = &testErr{"forced"}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

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

// nameStreamEmitter also implements DispatchEmitter and MentionEmitter, failing
// the selected name-stream method. It proves the extractor surfaces a failure
// from streaming reflective-dispatch names or the broad mention set, rather than
// swallowing it — the same contract the Symbol/Edge error tests pin.
type nameStreamEmitter struct {
	failDispatch bool
	failMention  bool
}

func (nameStreamEmitter) Symbol(extract.EmittedSymbol) error { return nil }
func (nameStreamEmitter) Edge(extract.EmittedEdge) error     { return nil }
func (e nameStreamEmitter) DispatchName(string) error {
	if e.failDispatch {
		return errForced
	}
	return nil
}
func (e nameStreamEmitter) MentionName(string) error {
	if e.failMention {
		return errForced
	}
	return nil
}

func TestDispatchNameEmitErrorRuby(t *testing.T) {
	// `send(:foo)` produces a dispatch-target name; a DispatchName emit failure
	// must propagate out of Extract.
	err := parseWithEmitter(t, "class Foo\n  def go\n    send(:foo)\n  end\nend\n",
		nameStreamEmitter{failDispatch: true})
	if err == nil {
		t.Error("expected error from DispatchName emit to propagate")
	}
}

func TestMentionNameEmitErrorRuby(t *testing.T) {
	// A bare identifier produces a mention; a MentionName emit failure must
	// propagate out of Extract (dispatch streams cleanly first).
	err := parseWithEmitter(t, "class Foo\n  def go\n    helper\n  end\nend\n",
		nameStreamEmitter{failMention: true})
	if err == nil {
		t.Error("expected error from MentionName emit to propagate")
	}
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
	return ex.Extract(tree, source, "test.rb", emit)
}

// --- error propagation tests ---

func TestClassSymbolErrorRuby(t *testing.T) {
	err := parseWithEmitter(t, `class Foo
end
`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on class symbol emit")
	}
}

func TestClassMethodErrorRuby(t *testing.T) {
	err := parseWithEmitter(t, `class Foo
  def bar
  end
end
`, &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on class method symbol emit")
	}
}

func TestInheritanceEdgeErrorRuby(t *testing.T) {
	err := parseWithEmitter(t, `class Child < Parent
end
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on inheritance edge emit")
	}
}

func TestModuleSymbolErrorRuby(t *testing.T) {
	err := parseWithEmitter(t, `module Foo
end
`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on module symbol emit")
	}
}

func TestMethodCallErrorRuby(t *testing.T) {
	err := parseWithEmitter(t, `class Foo
  def bar
    helper()
  end
end
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on method call edge emit")
	}
}

func TestTestsEdgeErrorRuby(t *testing.T) {
	// A class inheriting from a test superclass emits two edges:
	// inherits + tests. Fail on the second edge to cover the
	// error return inside the isTestSuperclass block.
	err := parseWithEmitter(t, `class FooTest < ActiveSupport::TestCase
end
`, &failAfterN{symbolsLeft: 100, edgesLeft: 1})
	if err == nil {
		t.Error("expected error on tests edge emit")
	}
}

func TestEmitCallNilMethodNode(t *testing.T) {
	// `baz.()` is Ruby lambda shorthand. Tree-sitter parses it as a
	// call node with a nil method field. emitCall should tolerate this.
	r := parseRuby(t, `class Foo
  def bar
    baz.()
  end
end
`)
	// No edges expected — the call is skipped due to nil method.
	if len(r.edges) != 0 {
		t.Errorf("expected 0 edges for lambda shorthand with nil method, got %d", len(r.edges))
	}
}

func TestEmitCallSafeNavNilMethodNode(t *testing.T) {
	// `baz&.()` — safe navigation with lambda shorthand.
	r := parseRuby(t, `class Foo
  def bar
    baz&.()
  end
end
`)
	// No edges expected — the call is skipped due to nil method.
	if len(r.edges) != 0 {
		t.Errorf("expected 0 edges for safe-nav lambda shorthand with nil method, got %d", len(r.edges))
	}
}

func TestEmitCallEmptyMethodName(t *testing.T) {
	// `foo.''()` produces a call node with an empty method name.
	// Tree-sitter parses the empty string as an identifier with zero-length text.
	r := parseRuby(t, `class Foo
  def bar
    baz.''()
  end
end
`)
	// No edges expected — the call is skipped due to empty method name.
	if len(r.edges) != 0 {
		t.Errorf("expected 0 edges for empty method name, got %d", len(r.edges))
	}
}

func TestIncludeEdgeErrorRuby(t *testing.T) {
	err := parseWithEmitter(t, `class Foo
  include Comparable
end
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on include edge emit")
	}
}

func TestAssociationEdgeErrorRuby(t *testing.T) {
	err := parseWithEmitter(t, `class Order < ApplicationRecord
  has_many :items
end
`, &failAfterN{symbolsLeft: 100, edgesLeft: 1})
	if err == nil {
		t.Error("expected error on association edge emit")
	}
}

func TestRSpecTestBlockConfidence(t *testing.T) {
	r := parseRuby(t, `describe TopicCreator do
  it "creates a topic" do
    TopicCreator.create(user)
  end
end
`)
	e := findEdge(r, "TopicCreator##it_creates_a_topic", "TopicCreator.create", "calls")
	if e == nil {
		t.Fatal("missing calls edge from it block")
	}
	if e.Confidence != extract.ConfidenceTests {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceTests)
	}
}

func TestRSpecTestBlockBareIdentifierConfidence(t *testing.T) {
	r := parseRuby(t, `describe Service do
  it "processes" do
    process_data
  end
end
`)
	e := findEdge(r, "Service##it_processes", "self.process_data", "calls")
	if e == nil {
		t.Fatal("missing calls edge from bare identifier in it block")
	}
	if e.Confidence != extract.ConfidenceTests {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceTests)
	}
}

func TestRSpecNestedDescribeScope(t *testing.T) {
	r := parseRuby(t, `describe Order do
  context "when pending" do
    it "calculates" do
      Order.new
    end
  end
end
`)
	e := findEdge(r, "Order#context_when_pending##it_calculates", "Order.new", "calls")
	if e == nil {
		t.Fatal("missing calls edge from nested describe/context/it")
	}
}

func TestRSpecFileLevelFallback(t *testing.T) {
	r := parseRuby(t, `describe Product do
  it "creates a #{thing}" do
    Product.new
  end
end
`)
	found := false
	for _, e := range r.edges {
		if e.TargetQualified == "Product.new" && string(e.Kind) == "calls" {
			if strings.HasPrefix(e.SourceQualified, "test.rb#L") {
				found = true
				if e.Confidence != extract.ConfidenceTests {
					t.Errorf("fallback confidence = %v, want %v", e.Confidence, extract.ConfidenceTests)
				}
			}
		}
	}
	if !found {
		t.Fatal("missing file-level fallback edge for Product.new")
	}
}

func TestInstanceVariableResolution(t *testing.T) {
	r := parseRuby(t, `
class TopicCreator
  def create; end
end

class PostCreator
  def initialize
    @topic_creator = TopicCreator.new
  end

  def create_topic
    @topic_creator.create
  end
end
`)
	// @topic_creator.create should resolve to TopicCreator#create via
	// class-level instance-variable type map.
	e := findEdge(r, "PostCreator#create_topic", "TopicCreator#create", "calls")
	if e == nil {
		t.Fatal("missing calls edge PostCreator#create_topic -> TopicCreator#create from instance variable resolution")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("inferred edge confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestInstanceVariableOperatorAssignment(t *testing.T) {
	r := parseRuby(t, `
class Cache
  def fetch; end
end

class Service
  def initialize
    @cache ||= Cache.new
  end

  def get
    @cache.fetch
  end
end
`)
	// @cache ||= Cache.new should be captured by buildInstanceVarTypeMap.
	e := findEdge(r, "Service#get", "Cache#fetch", "calls")
	if e == nil {
		t.Fatal("missing calls edge Service#get -> Cache#fetch from operator-assignment instance variable")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("inferred edge confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestInstanceVariableUnresolvedFallback(t *testing.T) {
	r := parseRuby(t, `
class PostCreator
  def create_topic
    @unknown.create
  end
end
`)
	// @unknown is not assigned in initialize, so it falls back to bare method name.
	e := findEdge(r, "PostCreator#create_topic", "create", "calls")
	if e == nil {
		t.Fatal("missing bare fallback edge for unresolved instance variable")
	}
	if e.Confidence != extract.ConfidenceUnresolved {
		t.Errorf("bare fallback confidence = %v, want %v", e.Confidence, extract.ConfidenceUnresolved)
	}
}

func TestInstanceVariableLocalOverride(t *testing.T) {
	r := parseRuby(t, `
class TopicCreator
  def create; end
end

class CommentCreator
  def create; end
end

class PostCreator
  def initialize
    @topic_creator = TopicCreator.new
  end

  def create_topic
    topic_creator = CommentCreator.new
    topic_creator.create
    @topic_creator.create
  end
end
`)
	// Local variable (identifier receiver) resolves via localTypes.
	eLocal := findEdge(r, "PostCreator#create_topic", "CommentCreator#create", "calls")
	if eLocal == nil {
		t.Fatal("missing calls edge from local variable")
	}
	if eLocal.Confidence != extract.ConfidenceDynamic {
		t.Errorf("local edge confidence = %v, want %v", eLocal.Confidence, extract.ConfidenceDynamic)
	}
	// Instance variable (instance_variable receiver) resolves via ivarTypes.
	eIvar := findEdge(r, "PostCreator#create_topic", "TopicCreator#create", "calls")
	if eIvar == nil {
		t.Fatal("missing calls edge from instance variable")
	}
	if eIvar.Confidence != extract.ConfidenceDynamic {
		t.Errorf("ivar edge confidence = %v, want %v", eIvar.Confidence, extract.ConfidenceDynamic)
	}
}

func TestBuildInstanceVarTypeMap(t *testing.T) {
	src := []byte(`
class PostCreator
  def initialize
    @topic_creator = TopicCreator.new
    @cache ||= Cache.new
    @plain = "string"
  end
end
`)
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(Extractor{}.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	// Find the class body node
	root := tree.RootNode()
	var body *sitter.Node
	for i := uint(0); i < root.NamedChildCount(); i++ {
		c := root.NamedChild(i)
		if c != nil && c.Kind() == "class" {
			body = c.ChildByFieldName("body")
			break
		}
	}
	if body == nil {
		t.Fatal("could not find class body")
	}

	m := buildInstanceVarTypeMap(body, src)
	if m["@topic_creator"] != "TopicCreator" {
		t.Errorf("@topic_creator = %q, want TopicCreator", m["@topic_creator"])
	}
	if m["@cache"] != "Cache" {
		t.Errorf("@cache = %q, want Cache", m["@cache"])
	}
	if _, ok := m["@plain"]; ok {
		t.Error("@plain should not be in map (RHS is not .new)")
	}
}

func TestBuildInstanceVarTypeMapNoInitialize(t *testing.T) {
	src := []byte(`class PostCreator
  def create_topic
    @topic_creator.create
  end
end
`)
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(Extractor{}.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	body := tree.RootNode().NamedChild(0).ChildByFieldName("body")
	if body == nil {
		t.Fatal("could not find class body")
	}
	m := buildInstanceVarTypeMap(body, src)
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestBuildInstanceVarTypeMapEmptyInitialize(t *testing.T) {
	src := []byte(`class PostCreator
  def initialize
  end

  def create_topic
    @topic_creator.create
  end
end
`)
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(Extractor{}.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	body := tree.RootNode().NamedChild(0).ChildByFieldName("body")
	if body == nil {
		t.Fatal("could not find class body")
	}
	m := buildInstanceVarTypeMap(body, src)
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestClassWithNoBody(t *testing.T) {
	r := parseRuby(t, `class EmptyClass
end
`)
	if len(r.symbols) == 0 {
		t.Fatal("expected at least the EmptyClass symbol")
	}
	s := findSymbol(r, "EmptyClass")
	if s == nil {
		t.Fatal("missing EmptyClass symbol")
	}
}

func TestInstanceVariableScopeResolution(t *testing.T) {
	r := parseRuby(t, `
class Admin::TopicCreator
  def create; end
end

class PostCreator
  def initialize
    @topic_creator = Admin::TopicCreator.new
  end

  def create_topic
    @topic_creator.create
  end
end
`)
	e := findEdge(r, "PostCreator#create_topic", "Admin::TopicCreator#create", "calls")
	if e == nil {
		t.Fatal("missing calls edge for scope-resolution instance variable type")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("inferred edge confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestRSpecExpectWithCallInArgs(t *testing.T) {
	// expect(TopicCreator.create(...)) has no block but the argument list
	// contains a call that should still be extracted.
	r := parseRuby(t, `describe TopicCreator do
  it "works" do
    expect(TopicCreator.create(user)).to be_valid
  end
end
`)
	e := findEdge(r, "TopicCreator##it_works", "TopicCreator.create", "calls")
	if e == nil {
		t.Fatal("missing calls edge from expect() argument list")
	}
	if e.Confidence != extract.ConfidenceTests {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceTests)
	}
}

func TestRSpecDescribeWithConstantSegment(t *testing.T) {
	// describe(Order) should produce "Order" as the scope segment.
	r := parseRuby(t, `describe Order do
  it "creates" do
    Order.new
  end
end
`)
	e := findEdge(r, "Order##it_creates", "Order.new", "calls")
	if e == nil {
		t.Fatal("missing calls edge from describe with constant arg")
	}
}

func TestRSpecContextWithStringSegment(t *testing.T) {
	// context "when pending" should produce "context_when_pending" segment.
	r := parseRuby(t, `describe Order do
  context "when pending" do
    it "works" do
      Order.new
    end
  end
end
`)
	e := findEdge(r, "Order#context_when_pending##it_works", "Order.new", "calls")
	if e == nil {
		t.Fatal("missing calls edge from context with string arg")
	}
}

func TestRSpecBeforeAfterFallback(t *testing.T) {
	// before/after have no string description → file-level fallback.
	r := parseRuby(t, `describe User do
  before do
    setup
  end
  after do
    teardown
  end
end
`)
	foundSetup := false
	foundTeardown := false
	for _, e := range r.edges {
		if e.TargetQualified == "self.setup" && string(e.Kind) == "calls" {
			foundSetup = true
		}
		if e.TargetQualified == "self.teardown" && string(e.Kind) == "calls" {
			foundTeardown = true
		}
	}
	if !foundSetup {
		t.Error("missing file-level fallback edge from before block")
	}
	if !foundTeardown {
		t.Error("missing file-level fallback edge from after block")
	}
}

func TestRSpecMatcherSkipped(t *testing.T) {
	// RSpec matchers like `eq`, `be_valid`, `raise_error` should NOT emit edges.
	r := parseRuby(t, `describe Order do
  it "validates" do
    expect(order).to eq(1)
    expect(order).to be_valid
    expect { order.save }.to raise_error
  end
end
`)
	for _, e := range r.edges {
		if e.TargetQualified == "eq" || e.TargetQualified == "be_valid" || e.TargetQualified == "raise_error" {
			t.Errorf("should not emit edge for RSpec matcher %q", e.TargetQualified)
		}
	}
}

func TestRSpecLambdaShorthandCall(t *testing.T) {
	// Lambda shorthand calls like `obj.()` should not emit test call edges
	// because they have nil method nodes.
	r := parseRuby(t, `describe Service do
  it "calls lambda" do
    callback = -> { puts "hello" }
    callback.()
  end
end
`)
	// Should not crash and should not emit edges for lambda shorthand
	for _, e := range r.edges {
		if e.TargetQualified == "" || strings.Contains(e.TargetQualified, "callback") {
			t.Errorf("should not emit edge for lambda shorthand call: %q", e.TargetQualified)
		}
	}
}

func TestNonNewCallLocalType(t *testing.T) {
	// Method calls that are not `.new` should not create local type entries
	r := parseRuby(t, `class Order
  def save; end
end

class Service
  def process
    order = Order.create  # Not Order.new, should not create local type
    order.save           # Should fall back to unresolved confidence
  end
end
`)
	// The order.save call should fall back to unresolved since Order.create
	// doesn't match the .new pattern for local type inference
	e := findEdge(r, "Service#process", "save", "calls")
	if e == nil {
		t.Fatal("missing fallback edge for non-.new assignment")
	}
	if e.Confidence != extract.ConfidenceUnresolved {
		t.Errorf("non-.new assignment confidence = %v, want %v", e.Confidence, extract.ConfidenceUnresolved)
	}
}

func TestRSpecSendInTestBlock(t *testing.T) {
	// send(:method) inside a test block should emit with ConfidenceTests.
	r := parseRuby(t, `describe Service do
  it "invokes" do
    send(:process)
  end
end
`)
	e := findEdge(r, "Service##it_invokes", "process", "calls")
	if e == nil {
		t.Fatal("missing calls edge from send() inside test block")
	}
	if e.Confidence != extract.ConfidenceTests {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceTests)
	}
}

func TestRSpecLetWithSymbolArg(t *testing.T) {
	// let(:name) has no string description → file-level fallback.
	r := parseRuby(t, `describe User do
  let(:name) do
    generate_name
  end
end
`)
	found := false
	for _, e := range r.edges {
		if e.TargetQualified == "self.generate_name" && string(e.Kind) == "calls" {
			found = true
		}
	}
	if !found {
		t.Error("missing file-level fallback edge from let block")
	}
}

func TestRSpecDescribeWithScopeResolution(t *testing.T) {
	// RSpec.describe Admin::Dashboard should produce "Admin::Dashboard" segment.
	r := parseRuby(t, `RSpec.describe Admin::Dashboard do
  it "works" do
    Admin::Dashboard.new
  end
end
`)
	e := findEdge(r, "Admin::Dashboard##it_works", "Admin::Dashboard.new", "calls")
	if e == nil {
		t.Fatal("missing calls edge from describe with scope resolution")
	}
}

func TestRSpecTopLevelItBlock(t *testing.T) {
	// it block without enclosing describe should still emit edges.
	r := parseRuby(t, `it "works" do
  TopicCreator.create(user)
end
`)
	e := findEdge(r, "#it_works", "TopicCreator.create", "calls")
	if e == nil {
		t.Fatal("missing calls edge from top-level it block")
	}
}

func TestRSpecDeepNestingFallback(t *testing.T) {
	// Depth > 3 triggers file-level fallback.
	r := parseRuby(t, `describe A do
  describe "#b" do
    context "when c" do
      describe "with d" do
        it "works" do
          TopicCreator.create(user)
        end
      end
    end
  end
end
`)
	// Should fall back to file-level scope for the deepest it block.
	found := false
	for _, e := range r.edges {
		if e.TargetQualified == "TopicCreator.create" && strings.HasPrefix(e.SourceQualified, "test.rb#L") {
			found = true
		}
	}
	if !found {
		t.Fatal("missing file-level fallback edge for depth > 3 nesting")
	}
}

func TestRSpecDescribeWithScopeResolutionSegment(t *testing.T) {
	// describe with scope_resolution arg (RSpec.describe Admin::Dashboard).
	r := parseRuby(t, `RSpec.describe Admin::Dashboard do
  it "works" do
    Admin::Dashboard.new
  end
end
`)
	e := findEdge(r, "Admin::Dashboard##it_works", "Admin::Dashboard.new", "calls")
	if e == nil {
		t.Fatal("missing calls edge from describe with scope resolution arg")
	}
}

func TestRSpecDescribeWithInterpolation(t *testing.T) {
	// describe "#{name}" has interpolation → file-level fallback for describe.
	// But the nested it block still gets a proper scope.
	r := parseRuby(t, `describe "#{name}" do
  it "works" do
    Order.new
  end
end
`)
	e := findEdge(r, "#it_works", "Order.new", "calls")
	if e == nil {
		t.Fatal("missing edge from it block inside describe with interpolation")
	}
}

func TestRSpecEmptyDescriptionFallback(t *testing.T) {
	// it "" should fall back to file-level scope.
	r := parseRuby(t, `describe Order do
  it "" do
    Order.new
  end
end
`)
	found := false
	for _, e := range r.edges {
		if e.TargetQualified == "Order.new" && strings.HasPrefix(e.SourceQualified, "test.rb#L") {
			found = true
		}
	}
	if !found {
		t.Fatal("missing file-level fallback edge for empty description")
	}
}

func TestRSpecContextWithStringArg(t *testing.T) {
	// context "when active" should produce "context_when_active" segment.
	r := parseRuby(t, `describe User do
  context "when active" do
    it "works" do
      User.new
    end
  end
end
`)
	e := findEdge(r, "User#context_when_active##it_works", "User.new", "calls")
	if e == nil {
		t.Fatal("missing calls edge from context with string arg")
	}
}

func TestRSpecDescribeWithMethodString(t *testing.T) {
	// describe "#create" should preserve the # prefix in the segment.
	r := parseRuby(t, `describe TopicCreator do
  describe "#create" do
    it "works" do
      TopicCreator.create(user)
    end
  end
end
`)
	e := findEdge(r, "TopicCreator##create##it_works", "TopicCreator.create", "calls")
	if e == nil {
		t.Fatal("missing calls edge from describe with method string")
	}
}

func TestRSpecSendDynamicDispatchInTest(t *testing.T) {
	// send(:process) inside a test block should emit with ConfidenceTests.
	r := parseRuby(t, `describe Service do
  it "invokes" do
    send(:process)
  end
end
`)
	e := findEdge(r, "Service##it_invokes", "process", "calls")
	if e == nil {
		t.Fatal("missing calls edge from send() inside test block")
	}
	if e.Confidence != extract.ConfidenceTests {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceTests)
	}
}

func TestRSpecPublicSendInTest(t *testing.T) {
	// public_send("process") inside a test block.
	r := parseRuby(t, `describe Service do
  it "invokes" do
    public_send("process")
  end
end
`)
	e := findEdge(r, "Service##it_invokes", "process", "calls")
	if e == nil {
		t.Fatal("missing calls edge from public_send() inside test block")
	}
}

func TestRSpecSendNonLiteralSkippedInTest(t *testing.T) {
	// send(method_name) with non-literal arg should not emit inside test block.
	r := parseRuby(t, `describe Service do
  it "invokes" do
    send(method_name)
  end
end
`)
	for _, e := range r.edges {
		if e.SourceQualified == "Service##it_invokes" && string(e.Kind) == "calls" && e.TargetQualified == "method_name" {
			t.Error("should not emit calls edge for non-literal send target in test block")
		}
	}
}

func TestRSpecAroundBlockFallback(t *testing.T) {
	// around blocks have no string description → file-level fallback.
	r := parseRuby(t, `describe User do
  around do
    wrap_test
  end
end
`)
	found := false
	for _, e := range r.edges {
		if e.TargetQualified == "self.wrap_test" && strings.HasPrefix(e.SourceQualified, "test.rb#L") {
			found = true
		}
	}
	if !found {
		t.Fatal("missing file-level fallback edge from around block")
	}
}

func TestRSpecDescribeWithConstantNoNestedIt(t *testing.T) {
	// describe(Order) with a call directly in the describe block (no it).
	r := parseRuby(t, `describe Order do
  Order.new
end
`)
	e := findEdge(r, "Order", "Order.new", "calls")
	if e == nil {
		t.Fatal("missing calls edge from describe block without nested it")
	}
}

func TestRSpecExpectNoBlockWithCall(t *testing.T) {
	// expect(TopicCreator.create(...)) — no block, but call in arg list.
	r := parseRuby(t, `describe TopicCreator do
  it "works" do
    expect(TopicCreator.create(user)).to be_valid
  end
end
`)
	e := findEdge(r, "TopicCreator##it_works", "TopicCreator.create", "calls")
	if e == nil {
		t.Fatal("missing calls edge from expect() with call argument")
	}
}

func TestRSpecExpectNoBlockNoCall(t *testing.T) {
	// expect(topic).to be_valid — no block, no call in args.
	r := parseRuby(t, `describe TopicCreator do
  it "works" do
    expect(topic).to be_valid
  end
end
`)
	// No calls edge should be emitted for expect() with no call args.
	for _, e := range r.edges {
		if e.TargetQualified == "topic" && string(e.Kind) == "calls" {
			t.Error("should not emit edge for identifier in expect args")
		}
	}
}

func TestRSpecTopLevelExpectWithCall(t *testing.T) {
	// Top-level expect(TopicCreator.create(...)) — no describe, no it.
	r := parseRuby(t, `expect(TopicCreator.create(user)).to be_valid
`)
	// At top level, scope is empty and testScope is empty.
	// buildSyntheticSource returns "" which is used as source.
	for _, e := range r.edges {
		if e.TargetQualified == "TopicCreator.create" && string(e.Kind) == "calls" {
			if e.SourceQualified != "" {
				t.Errorf("top-level expect source should be empty, got %q", e.SourceQualified)
			}
			return
		}
	}
	t.Fatal("missing calls edge from top-level expect with call argument")
}

func TestRSpecDescribeWithNoBlockCall(t *testing.T) {
	// describe(Order) containing expect(TopicCreator.create(...)) directly
	// (no it block). The expect has no block, so handleTestBlock's no-block
	// path is used with classScope="Order" and testScope empty.
	r := parseRuby(t, `describe Order do
  expect(TopicCreator.create(user)).to be_valid
end
`)
	for _, e := range r.edges {
		if e.TargetQualified == "TopicCreator.create" && string(e.Kind) == "calls" {
			if e.SourceQualified != "Order" {
				t.Errorf("source should be Order, got %q", e.SourceQualified)
			}
			return
		}
	}
	t.Fatal("missing calls edge from expect inside describe block")
}

func TestRSpecBeforeInsideClass(t *testing.T) {
	// before block inside a class falls back to file-level scope
	// because before/after/around have no string description segment.
	r := parseRuby(t, `class OrderTest < ActiveSupport::TestCase
  before do
    setup_test
  end
end
`)
	found := false
	for _, e := range r.edges {
		if e.TargetQualified == "self.setup_test" && string(e.Kind) == "calls" {
			if !strings.HasPrefix(e.SourceQualified, "test.rb#L") {
				t.Errorf("source should be file-level fallback, got %q", e.SourceQualified)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing calls edge from before block inside class")
	}
}

func TestRSpecExpectInsideClass(t *testing.T) {
	// expect() directly inside a class body (unusual but valid AST).
	// classScope="Foo", testScope empty → buildSyntheticSource returns "Foo".
	r := parseRuby(t, `class Foo
  expect(Bar.new).to eq(1)
end
`)
	for _, e := range r.edges {
		if e.TargetQualified == "Bar.new" && string(e.Kind) == "calls" {
			if e.SourceQualified != "Foo" {
				t.Errorf("source should be Foo, got %q", e.SourceQualified)
			}
			return
		}
	}
	t.Fatal("missing calls edge from expect inside class body")
}

func TestRSpecBlockBodyNil(t *testing.T) {
	// A do_block with no body_statement (empty block).
	r := parseRuby(t, `describe Order do
  it "works" do
  end
end
`)
	// Should not crash; no edges expected.
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.SourceQualified == "Order##it_works" {
			t.Error("unexpected edge from empty it block")
		}
	}
}

func TestRSpecFileLevelScopeWithPath(t *testing.T) {
	// fileLevelScope strips directory prefix when filePath contains '/'.
	r := parseRubyWithPath(t, `before do
  setup
end
`, "spec/models/order_spec.rb")
	found := false
	for _, e := range r.edges {
		if e.TargetQualified == "self.setup" && strings.HasPrefix(e.SourceQualified, "order_spec.rb#L") {
			found = true
		}
	}
	if !found {
		t.Fatal("missing file-level fallback edge with basename")
	}
}

func TestRSpecDescribeWithIntegerArg(t *testing.T) {
	// describe(123) has an unsupported arg kind → buildTestScopeSegment returns "".
	// The describe falls back to file-level, but the nested it block has a valid scope.
	r := parseRuby(t, `describe 123 do
  it "works" do
    Order.new
  end
end
`)
	e := findEdge(r, "#it_works", "Order.new", "calls")
	if e == nil {
		t.Fatal("missing calls edge from it block inside describe with integer arg")
	}
}

func TestRSpecItWithNonStringArg(t *testing.T) {
	// it(:symbol) has a non-string arg → buildTestScopeSegment returns "".
	r := parseRuby(t, `describe Order do
  it :works do
    Order.new
  end
end
`)
	// Falls back to file-level scope.
	found := false
	for _, e := range r.edges {
		if e.TargetQualified == "Order.new" && strings.HasPrefix(e.SourceQualified, "test.rb#L") {
			found = true
		}
	}
	if !found {
		t.Fatal("missing file-level fallback edge for it with symbol arg")
	}
}

func TestRSpecPublicSendDynamicDispatchInTest(t *testing.T) {
	// public_send("process") inside a test block.
	r := parseRuby(t, `describe Service do
  it "invokes" do
    public_send("process")
  end
end
`)
	e := findEdge(r, "Service##it_invokes", "process", "calls")
	if e == nil {
		t.Fatal("missing calls edge from public_send() inside test block")
	}
	if e.Confidence != extract.ConfidenceTests {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceTests)
	}
}

func TestRSpecSendWithNonLiteralSkipped(t *testing.T) {
	// send(method_name) with non-literal arg should not emit in test block.
	r := parseRuby(t, `describe Service do
  it "invokes" do
    send(method_name)
  end
end
`)
	for _, e := range r.edges {
		if e.SourceQualified == "Service##it_invokes" && string(e.Kind) == "calls" && e.TargetQualified == "method_name" {
			t.Error("should not emit calls edge for non-literal send target in test block")
		}
	}
}

// --- coverage for existing functions below 90% ---

func TestBroadcastsToNoArgs(t *testing.T) {
	r := parseRuby(t, `
class Message
  broadcasts_to()
end
`)
	// Empty args -> no edge
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.SourceQualified == "Message" {
			t.Error("should not emit calls edge for broadcasts_to with no args")
		}
	}
}

func TestBroadcastsToEmptyString(t *testing.T) {
	r := parseRuby(t, `
class Message
  broadcasts_to ""
end
`)
	// Empty string -> no edge
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.SourceQualified == "Message" {
			t.Error("should not emit calls edge for broadcasts_to with empty string")
		}
	}
}

func TestImportmapPinNoArgs(t *testing.T) {
	r := parseRubyWithPath(t, `pin
`, "config/importmap.rb")
	for _, e := range r.edges {
		if string(e.Kind) == "imports" {
			t.Error("should not emit imports edge for pin with no args")
		}
	}
}

func TestImportmapPinNonStringArg(t *testing.T) {
	r := parseRubyWithPath(t, `pin :symbol_arg
`, "config/importmap.rb")
	for _, e := range r.edges {
		if string(e.Kind) == "imports" {
			t.Error("should not emit imports edge for pin with non-string arg")
		}
	}
}

func TestImportmapPinEmptyString(t *testing.T) {
	r := parseRubyWithPath(t, `pin ""
`, "config/importmap.rb")
	for _, e := range r.edges {
		if string(e.Kind) == "imports" {
			t.Error("should not emit imports edge for pin with empty string")
		}
	}
}

// --- coverage for existing functions below 90% ---

func TestBroadcastsToNonLiteralArg(t *testing.T) {
	r := parseRuby(t, `class Order
  broadcasts_to some_method
end
`)
	// Non-literal arg -> no edge
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.SourceQualified == "Message" {
			t.Error("should not emit calls edge for non-literal broadcasts arg")
		}
	}
}

func TestDescribeNoArgs(t *testing.T) {
	r := parseRuby(t, `describe do
end
`)
	for _, e := range r.edges {
		if string(e.Kind) == "tests" {
			t.Error("should not emit tests edge for describe with no args")
		}
	}
}

func TestDescribeWithStringArgNoEdge(t *testing.T) {
	r := parseRuby(t, `describe "some behavior" do
end
`)
	for _, e := range r.edges {
		if string(e.Kind) == "tests" {
			t.Error("should not emit tests edge for describe with string arg")
		}
	}
}

func TestImportmapPinAllFromNotImportmapFile(t *testing.T) {
	r := parseRubyWithPath(t, `pin_all_from "app/javascript/controllers"
`, "config/routes.rb")
	for _, e := range r.edges {
		if string(e.Kind) == "imports" {
			t.Error("should not emit imports edge outside importmap.rb")
		}
	}
}

func TestDescribeWithNonRSpecReceiverAndStringArg(t *testing.T) {
	r := parseRuby(t, `MyLib.describe "something" do
end
`)
	for _, e := range r.edges {
		if string(e.Kind) == "tests" {
			t.Error("should not emit tests edge for non-RSpec describe with string arg")
		}
	}
}

func TestRouteNamespaceNoBlock(t *testing.T) {
	r := parseRuby(t, `namespace :admin
`)
	// Should not crash; no edges expected from namespace without block.
	for _, e := range r.edges {
		if strings.Contains(e.TargetQualified, "Admin") {
			t.Error("should not emit edge for namespace without block")
		}
	}
}

func TestRouteNamespaceNoArgs(t *testing.T) {
	r := parseRuby(t, `namespace
`)
	// Should not crash; no edges expected from namespace without args.
	for _, e := range r.edges {
		if strings.Contains(e.TargetQualified, "Admin") {
			t.Error("should not emit edge for namespace without args")
		}
	}
}

func TestRouteNamespaceStringArg(t *testing.T) {
	r := parseRuby(t, `namespace "admin" do
  resources :users
end
`)
	// String arg should be skipped; no namespace prefix applied.
	for _, e := range r.edges {
		if strings.Contains(e.TargetQualified, "Admin") {
			t.Error("should not emit edge with namespace prefix for string arg")
		}
	}
}

func TestBuildReturnTypeMap(t *testing.T) {
	src := `class Factory
  def builder
    Builder.new
  end

  def self.create_factory
    Factory.new
  end

  def config
    return Config.new
  end

  def complex
    x = 1
    Builder.new
  end

  def plain
    "not a new call"
  end
end
`
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse([]byte(src), nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	// Find the class body node.
	root := tree.RootNode()
	var classBody *sitter.Node
	for i := uint(0); i < root.NamedChildCount(); i++ {
		child := root.NamedChild(i)
		if child.Kind() == "class" {
			classBody = child.ChildByFieldName("body")
			break
		}
	}
	if classBody == nil {
		t.Fatal("could not find class body")
	}

	retTypes := buildReturnTypeMap(classBody, []byte(src), []string{"Factory"})

	if got, want := retTypes["Factory#builder"], "Builder"; got != want {
		t.Errorf("Factory#builder = %q, want %q", got, want)
	}
	if got, want := retTypes["Factory.create_factory"], "Factory"; got != want {
		t.Errorf("Factory.create_factory = %q, want %q", got, want)
	}
	if got, want := retTypes["Factory#config"], "Config"; got != want {
		t.Errorf("Factory#config = %q, want %q", got, want)
	}
	if _, ok := retTypes["Factory#complex"]; ok {
		t.Error("Factory#complex should not be in return type map (multiple statements)")
	}
	if _, ok := retTypes["Factory#plain"]; ok {
		t.Error("Factory#plain should not be in return type map (not a .new call)")
	}

	// Test nil body
	emptyTypes := buildReturnTypeMap(nil, []byte(src), []string{"Factory"})
	if len(emptyTypes) != 0 {
		t.Errorf("nil body: expected empty map, got %d entries", len(emptyTypes))
	}

	// Test top-level method (parent == "")
	topLevelSrc := `def helper
  Helper.new
end
`
	topLevelTree := p.Parse([]byte(topLevelSrc), nil)
	if topLevelTree == nil {
		t.Fatal("Parse returned nil tree for top-level")
	}
	defer topLevelTree.Close()

	topLevelTypes := buildReturnTypeMap(topLevelTree.RootNode(), []byte(topLevelSrc), nil)
	if got, want := topLevelTypes["helper"], "Helper"; got != want {
		t.Errorf("top-level method: got %q, want %q", got, want)
	}
}

func TestChainResolution(t *testing.T) {
	r := parseRuby(t, `class Caller
  def basic_chain
    factory = Factory.new
    factory.builder.create
  end
end

class Factory
  def builder
    Builder.new
  end
end

class Builder
  def create; end
end
`)
	e := findEdge(r, "Caller#basic_chain", "Builder#create", "calls")
	if e == nil {
		t.Fatal("missing resolved chain edge Caller#basic_chain -> Builder#create")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("chain confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestInstanceVariableLocalTypeOverride(t *testing.T) {
	// When a local variable shadows an instance variable name,
	// the local variable type should take precedence.
	r := parseRuby(t, `class PostCreator
  def initialize
    @creator = TopicCreator.new
  end

  def create
    creator = CommentCreator.new
    @creator.create
  end
end

class TopicCreator
  def create; end
end

class CommentCreator
  def create; end
end
`)
	e := findEdge(r, "PostCreator#create", "TopicCreator#create", "calls")
	if e == nil {
		t.Fatal("missing calls edge for instance variable with local override")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestResolveChainReceiver(t *testing.T) {
	src := `class Service
  def builder
    Builder.new
  end
end

class Builder
  def config
    Config.new
  end
end
`
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse([]byte(src), nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	returnTypes := buildFileReturnTypeMap(tree.RootNode(), []byte(src))
	localTypes := map[string]string{"svc": "Service"}
	scope := []string{"Service"}

	// Create a minimal walker to use the method
	w := &walker{
		source:      []byte(src),
		returnTypes: returnTypes,
	}

	// Case 1: local variable receiver
	// svc.builder → "Builder"
	callSrc := `svc.builder`
	callTree := p.Parse([]byte(callSrc), nil)
	if callTree == nil {
		t.Fatal("Parse returned nil tree for call")
	}
	defer callTree.Close()
	callNode := callTree.RootNode().NamedChild(0)
	if callNode == nil || callNode.Kind() != "call" {
		t.Fatalf("expected call node, got %s", callNode.Kind())
	}
	// Use callSrc as the walker's source so node offsets match
	w.source = []byte(callSrc)
	got := w.resolveChainReceiver(callNode, scope, localTypes, 1)
	if want := "Builder"; got != want {
		t.Errorf("local var chain: got %q, want %q", got, want)
	}

	// Case 2: self receiver
	// self.builder → "Builder"
	callSrc = `self.builder`
	callTree = p.Parse([]byte(callSrc), nil)
	if callTree == nil {
		t.Fatal("Parse returned nil tree for call")
	}
	defer callTree.Close()
	callNode = callTree.RootNode().NamedChild(0)
	if callNode == nil || callNode.Kind() != "call" {
		t.Fatalf("expected call node, got %s", callNode.Kind())
	}
	w.source = []byte(callSrc)
	got = w.resolveChainReceiver(callNode, scope, localTypes, 1)
	if want := "Builder"; got != want {
		t.Errorf("self chain: got %q, want %q", got, want)
	}

	// Case 3: unresolved chain (method not in returnTypes)
	callSrc = `svc.unknown`
	callTree = p.Parse([]byte(callSrc), nil)
	if callTree == nil {
		t.Fatal("Parse returned nil tree for call")
	}
	defer callTree.Close()
	callNode = callTree.RootNode().NamedChild(0)
	if callNode == nil || callNode.Kind() != "call" {
		t.Fatalf("expected call node, got %s", callNode.Kind())
	}
	w.source = []byte(callSrc)
	got = w.resolveChainReceiver(callNode, scope, localTypes, 1)
	if got != "" {
		t.Errorf("unresolved chain: got %q, want empty", got)
	}

	// Case 4: depth limit
	callSrc = `svc.builder`
	callTree = p.Parse([]byte(callSrc), nil)
	if callTree == nil {
		t.Fatal("Parse returned nil tree for call")
	}
	defer callTree.Close()
	callNode = callTree.RootNode().NamedChild(0)
	w.source = []byte(callSrc)
	got = w.resolveChainReceiver(callNode, scope, localTypes, 4)
	if got != "" {
		t.Errorf("depth limit: got %q, want empty", got)
	}

	// Case 5: recursive chain (3 hops)
	// svc.builder.config where builder returns Builder and config returns Config
	callSrc = `svc.builder.config`
	callTree = p.Parse([]byte(callSrc), nil)
	if callTree == nil {
		t.Fatal("Parse returned nil tree for call")
	}
	defer callTree.Close()
	callNode = callTree.RootNode().NamedChild(0)
	if callNode == nil || callNode.Kind() != "call" {
		t.Fatalf("expected call node, got %s", callNode.Kind())
	}
	w.source = []byte(callSrc)
	got = w.resolveChainReceiver(callNode, scope, localTypes, 1)
	if want := "Config"; got != want {
		t.Errorf("recursive chain: got %q, want %q", got, want)
	}
}

// --- block parameter type inference ---

func TestBlockParameterEach(t *testing.T) {
	r := parseRuby(t, `
class Order
  def save; end
end

class Service
  def process
    orders = Order.new
    orders.each do |order|
      order.save
    end
  end
end
`)
	e := findEdge(r, "Service#process", "Order#save", "calls")
	if e == nil {
		t.Fatal("missing calls edge Service#process -> Order#save from block parameter inference")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
	// Verify no duplicate edges — each call should be emitted exactly once.
	// Count calls only; references edges (e.g. to the Order constant) are
	// emitted separately and are not the subject of this assertion.
	serviceCalls := 0
	for _, edge := range r.edges {
		if edge.SourceQualified == "Service#process" && string(edge.Kind) == "calls" {
			serviceCalls++
		}
	}
	if serviceCalls != 3 {
		t.Errorf("expected 3 calls edges from Service#process, got %d", serviceCalls)
	}
}

func TestBlockParameterMap(t *testing.T) {
	r := parseRuby(t, `
class User
  def name; end
end

class Service
  def process
    users = User.new
    users.map { |user| user.name }
  end
end
`)
	e := findEdge(r, "Service#process", "User#name", "calls")
	if e == nil {
		t.Fatal("missing calls edge Service#process -> User#name from block parameter inference")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestBlockParameterNestedBlocks(t *testing.T) {
	r := parseRuby(t, `
class Item
  def save; end
end

class Order
  def items
    Item.new
  end
end

class Service
  def process
    orders = Order.new
    orders.each do |order|
      order.items.each do |item|
        item.save
      end
    end
  end
end
`)
	e := findEdge(r, "Service#process", "Item#save", "calls")
	if e == nil {
		t.Fatal("missing calls edge Service#process -> Item#save from nested block parameter inference")
	}
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceDynamic)
	}
}

func TestBlockParameterNonCollectionSkipped(t *testing.T) {
	r := parseRuby(t, `
class File
  def read; end
end

class Service
  def process
    File.open("x") { |f| f.read }
  end
end
`)
	// File.open is not a collection method → no block parameter inference
	// The call inside the block falls back to bare method name
	e := findEdge(r, "Service#process", "read", "calls")
	if e == nil {
		t.Fatal("missing bare fallback edge for non-collection block")
	}
	if e.Confidence != extract.ConfidenceUnresolved {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceUnresolved)
	}
}

func TestBlockParameterFromLocalVariable(t *testing.T) {
	r := parseRuby(t, `
class Order
  def validate; end
end

class Service
  def process
    orders = Order.new
    orders.each { |order| order.validate }
  end
end
`)
	// orders = Order.new makes orders an Order
	// extractElementType("Order") returns "Order" (already singular)
	e := findEdge(r, "Service#process", "Order#validate", "calls")
	if e == nil {
		t.Fatal("missing calls edge from block parameter with local variable collection")
	}
}

func TestBlockParameterMultipleParams(t *testing.T) {
	r := parseRuby(t, `
class Order
  def save; end
end

class Service
  def process
    orders = Order.new
    orders.each { |order, index| order.save }
  end
end
`)
	// Multiple params: both get the element type
	e := findEdge(r, "Service#process", "Order#save", "calls")
	if e == nil {
		t.Fatal("missing calls edge from block with multiple parameters")
	}
}

func TestBlockParameterDestructuringSkipped(t *testing.T) {
	r := parseRuby(t, `
class Order
  def save; end
end

class Service
  def process
    orders = Order.new
    orders.each { |(id, name)| id.archive }
  end
end
`)
	// Destructuring parameter → skip block parameter inference.
	// The call inside falls back to the bare method name. (A domain method
	// is used rather than a core method like to_s, which is intentionally
	// dropped from unknown-receiver calls to avoid name-collision noise.)
	e := findEdge(r, "Service#process", "archive", "calls")
	if e == nil {
		t.Fatal("missing bare fallback edge for destructuring block parameter")
	}
	if e.Confidence != extract.ConfidenceUnresolved {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceUnresolved)
	}
}

func TestBlockParameterFromInstanceVariable(t *testing.T) {
	r := parseRuby(t, `
class Order
  def save; end
end

class Service
  def initialize
    @orders = Order.new
  end

  def process
    @orders.each { |order| order.save }
  end
end
`)
	e := findEdge(r, "Service#process", "Order#save", "calls")
	if e == nil {
		t.Fatal("missing calls edge from block parameter with instance variable collection")
	}
}

func TestBlockParameterFromChain(t *testing.T) {
	r := parseRuby(t, `
class Order
  def save; end
end

class User
  def orders
    Order.new
  end
end

class Service
  def process
    user = User.new
    user.orders.each { |order| order.save }
  end
end
`)
	e := findEdge(r, "Service#process", "Order#save", "calls")
	if e == nil {
		t.Fatal("missing calls edge from block parameter with chain receiver")
	}
}

func TestExtractBlockParams(t *testing.T) {
	src := []byte(`orders.each { |order| order.save }`)
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(Extractor{}.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	callNode := tree.RootNode().NamedChild(0)
	block := callNode.ChildByFieldName("block")
	if block == nil {
		t.Fatal("could not find block")
	}

	params := extractBlockParams(block, src)
	if len(params) != 1 || params[0] != "order" {
		t.Errorf("params = %v, want [order]", params)
	}
}

func TestExtractBlockParamsMultiple(t *testing.T) {
	src := []byte(`orders.each { |order, index| order.save }`)
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(Extractor{}.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	callNode := tree.RootNode().NamedChild(0)
	block := callNode.ChildByFieldName("block")
	params := extractBlockParams(block, src)
	if len(params) != 2 || params[0] != "order" || params[1] != "index" {
		t.Errorf("params = %v, want [order index]", params)
	}
}

func TestExtractBlockParamsDestructuring(t *testing.T) {
	src := []byte(`orders.each { |(id, name)| id.to_s }`)
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(Extractor{}.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	callNode := tree.RootNode().NamedChild(0)
	block := callNode.ChildByFieldName("block")
	params := extractBlockParams(block, src)
	if params != nil {
		t.Errorf("params = %v, want nil for destructuring", params)
	}
}

func TestExtractElementType(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Array[Order]", "Order"},
		{"Array[Admin::Order]", "Admin::Order"},
		{"orders", "Order"},
		{"users", "User"},
		{"line_items", "LineItem"},
		{"Order", "Order"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := extractElementType(tc.in); got != tc.want {
			t.Errorf("extractElementType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBlockParameterNoParams(t *testing.T) {
	// Block without parameters should still be walked, just without type augmentation
	r := parseRuby(t, `
class Order
  def save; end
end

class Service
  def process
    orders = Order.new
    orders.each { order.save }
  end
end
`)
	// order is not a block parameter, it's treated as an unresolved identifier
	// Since it's not in localTypes, it falls back to bare method name
	e := findEdge(r, "Service#process", "save", "calls")
	if e == nil {
		t.Fatal("missing bare fallback edge for block without parameters")
	}
	if e.Confidence != extract.ConfidenceUnresolved {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceUnresolved)
	}
}

func TestBlockParameterUnknownReceiverType(t *testing.T) {
	// When receiver type is unknown, block body is still walked with original types
	r := parseRuby(t, `
class Order
  def save; end
end

class Service
  def process
    orders.each { |order| order.save }
  end
end
`)
	// orders is not in localTypes, so each falls back to bare method name
	// and block parameter inference is skipped
	e := findEdge(r, "Service#process", "save", "calls")
	if e == nil {
		t.Fatal("missing bare fallback edge for unknown receiver type")
	}
	if e.Confidence != extract.ConfidenceUnresolved {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceUnresolved)
	}
}

func TestBlockParameterSplatSkipped(t *testing.T) {
	// Splat parameter should skip block parameter inference
	r := parseRuby(t, `
class Order
  def save; end
end

class Service
  def process
    orders = Order.new
    orders.each { |*args| args.first }
  end
end
`)
	// args.first is a chain call, should fall back to bare method
	e := findEdge(r, "Service#process", "first", "calls")
	if e == nil {
		t.Fatal("missing bare fallback edge for splat parameter")
	}
	if e.Confidence != extract.ConfidenceUnresolved {
		t.Errorf("confidence = %v, want %v", e.Confidence, extract.ConfidenceUnresolved)
	}
}

func TestMergeMaps(t *testing.T) {
	cases := []struct {
		base, overlay, want map[string]string
	}{
		{
			base:    map[string]string{"a": "1", "b": "2"},
			overlay: map[string]string{"b": "3", "c": "4"},
			want:    map[string]string{"a": "1", "b": "3", "c": "4"},
		},
		{
			base:    nil,
			overlay: map[string]string{"x": "y"},
			want:    map[string]string{"x": "y"},
		},
		{
			base:    map[string]string{"a": "1"},
			overlay: nil,
			want:    map[string]string{"a": "1"},
		},
		{
			base:    nil,
			overlay: nil,
			want:    map[string]string{},
		},
	}
	for _, tc := range cases {
		got := mergeMaps(tc.base, tc.overlay)
		if len(got) != len(tc.want) {
			t.Errorf("mergeMaps(%v, %v) = %v, want %v", tc.base, tc.overlay, got, tc.want)
			continue
		}
		for k, v := range tc.want {
			if got[k] != v {
				t.Errorf("mergeMaps(%v, %v)[%q] = %q, want %q", tc.base, tc.overlay, k, got[k], v)
			}
		}
	}
}

func TestConstReferenceEdgeRuby(t *testing.T) {
	r := parseRuby(t, `MAX_RETRIES = 5

class Service
  def process
    x = MAX_RETRIES
  end
end
`)
	if findEdge(r, "Service#process", "MAX_RETRIES", "references") == nil {
		t.Error("missing references edge Service#process -> MAX_RETRIES")
	}
}

func TestConstReferenceNestedRuby(t *testing.T) {
	r := parseRuby(t, `class Config
  TIMEOUT = 30

  def read
    t = TIMEOUT
  end
end
`)
	if findEdge(r, "Config#read", "Config::TIMEOUT", "references") == nil {
		t.Error("missing references edge for nested constant")
	}
}

func TestConstReferenceSkipsScopeResolutionRuby(t *testing.T) {
	r := parseRuby(t, `MAX = 10

class Svc
  def run
    x = MAX
  end
end
`)
	if findEdge(r, "Svc#run", "MAX", "references") == nil {
		t.Error("missing references edge for direct constant use")
	}
}

func TestConstReferenceDedupRuby(t *testing.T) {
	r := parseRuby(t, `LIMIT = 10

class Svc
  def run
    a = LIMIT
    b = LIMIT
  end
end
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

func TestConstReferenceFromSingletonMethodRuby(t *testing.T) {
	r := parseRuby(t, `FACTOR = 2

class Calc
  def self.double(n)
    n * FACTOR
  end
end
`)
	if findEdge(r, "Calc.double", "FACTOR", "references") == nil {
		t.Error("missing references edge from singleton method")
	}
}

func TestConstReferenceEmitsUnknownRuby(t *testing.T) {
	// A constant defined in this file resolves to its qualified name; one
	// that is not (defined cross-file, or a gem/stdlib class) is emitted
	// with its surface text so the resolver can match it against the
	// global symbol table — and drop it if it resolves to nothing. This is
	// what lets a class referenced only as `Foo.new` from another file
	// show up as referenced rather than dead.
	r := parseRuby(t, `KNOWN = 1

class Svc
  def run
    x = KNOWN
    y = UNKNOWN
  end
end
`)
	if findEdge(r, "Svc#run", "KNOWN", "references") == nil {
		t.Error("missing references edge for known constant")
	}
	e := findEdge(r, "Svc#run", "UNKNOWN", "references")
	if e == nil {
		t.Fatal("missing references edge for unknown constant (needed for cross-file resolution)")
	}
	if e.Confidence != extract.ConfidenceStatic {
		t.Errorf("UNKNOWN reference confidence = %v, want %v", e.Confidence, extract.ConfidenceStatic)
	}
}

func TestConstReferenceSkipsSuperclassRuby(t *testing.T) {
	r := parseRuby(t, `BASE = 1

class Parent
  def run
    x = BASE
  end
end

class Child < Parent
  def work
    y = BASE
  end
end
`)
	if findEdge(r, "Parent#run", "BASE", "references") == nil {
		t.Error("missing references edge for BASE in Parent.run")
	}
}

func TestClassReceiverEmitsReferenceRuby(t *testing.T) {
	// `Foo.new` must record a reference to the class itself, not just a
	// call to `new` — otherwise a class reached only via instantiation
	// looks uncalled (and dead).
	r := parseRuby(t, `class Order
  class Processor
    def run
      ProcessPaymentService.new
    end
  end
end
`)
	if findEdge(r, "Order::Processor#run", "ProcessPaymentService", "references") == nil {
		t.Error("missing references edge to class used via .new")
	}
}

func TestRescueEmitsReferenceRuby(t *testing.T) {
	r := parseRuby(t, `class Worker
  def perform
    do_work
  rescue ApiError => e
    handle(e)
  end
end
`)
	if findEdge(r, "Worker#perform", "ApiError", "references") == nil {
		t.Error("missing references edge to rescued exception class")
	}
}

func TestScopeResolutionRefEmitsReferenceRuby(t *testing.T) {
	r := parseRuby(t, `class Worker
  def perform
    x = SocialCommerce::Meta::ApiError
  end
end
`)
	if findEdge(r, "Worker#perform", "SocialCommerce::Meta::ApiError", "references") == nil {
		t.Error("missing references edge to namespaced constant")
	}
}

func TestRescueFromEmitsReferenceRuby(t *testing.T) {
	r := parseRuby(t, `class ApplicationController
  rescue_from ActiveRecord::RecordNotFound, with: :not_found
end
`)
	if findEdge(r, "ApplicationController", "ActiveRecord::RecordNotFound", "references") == nil {
		t.Error("missing references edge to rescue_from exception class")
	}
}

func TestIncludeDoesNotDoubleEmitReferenceRuby(t *testing.T) {
	// `include Foo` already yields an includes edge; it must not also
	// produce a redundant references edge.
	r := parseRuby(t, `class Widget
  include Printable
end
`)
	if findEdge(r, "Widget", "Printable", "includes") == nil {
		t.Error("missing includes edge")
	}
	if findEdge(r, "Widget", "Printable", "references") != nil {
		t.Error("include arg should not also emit a references edge")
	}
}

func TestCoreNoiseMethodDroppedRuby(t *testing.T) {
	// A ubiquitous core method on an unknown receiver must NOT emit a
	// calls edge — otherwise the resolver's unqualified fallback binds
	// `count.zero?` to an arbitrary same-named symbol. A non-core method
	// on an unknown receiver still emits its bare fallback edge.
	r := parseRuby(t, `class Order
  def process
    count.zero?
    other.archive
  end
end
`)
	if findEdge(r, "Order#process", "zero?", "calls") != nil {
		t.Error("core-noise method zero? should be dropped on unknown receiver")
	}
	if findEdge(r, "Order#process", "archive", "calls") == nil {
		t.Error("non-noise method should still emit a fallback calls edge")
	}
}

func TestCoreNoiseMethodDroppedOnLiteralReceiverRuby(t *testing.T) {
	// Receiver kinds not matched by resolveCallTarget's switch (string,
	// array, integer literals) hit the fallthrough; core-noise methods on
	// them must also be dropped, while a domain method still emits.
	r := parseRuby(t, `class Order
  def process
    "".empty?
    [].archive
  end
end
`)
	if findEdge(r, "Order#process", "empty?", "calls") != nil {
		t.Error("core-noise method empty? on a literal receiver should be dropped")
	}
	if findEdge(r, "Order#process", "archive", "calls") == nil {
		t.Error("non-noise method on a literal receiver should still emit a fallback edge")
	}
}

func TestBareHelperCallInValuePositionRuby(t *testing.T) {
	// Helpers (often injected via an included concern) are called bare and
	// parenless in value positions — as arguments, in interpolations, in
	// conditions, on the RHS of an assignment. Each must emit a self-call so
	// the resolver can bind it to the concern method, while genuine locals
	// and parameters are not mistaken for sends.
	r := parseRuby(t, `class CheckoutController
  def show(quantity)
    total = quantity * 2
    redirect_to path(currency: current_currency)
    flash[:notice] = "in #{current_locale}"
    render :ok if current_country
    @sum = total
    @cur = current_currency
  end
end
`)
	wantCalls := []string{"current_currency", "current_locale", "current_country"}
	for _, name := range wantCalls {
		if findEdge(r, "CheckoutController#show", "self."+name, "calls") == nil {
			t.Errorf("missing self.%s call edge for value-position helper", name)
		}
	}
	// `total` is a local and `quantity` a parameter — neither is a send.
	for _, local := range []string{"total", "quantity"} {
		if findEdge(r, "CheckoutController#show", "self."+local, "calls") != nil {
			t.Errorf("local/parameter %q wrongly emitted as a call", local)
		}
	}
}

func TestBareHelperCallInAssignmentRHSRuby(t *testing.T) {
	// Assignment and operator-assignment RHS are the canonical place a concern
	// helper is captured (`@currency = current_currency`, memoised
	// `@user ||= current_user`). The assignment target itself is not a send.
	r := parseRuby(t, `class OrdersController
  def show
    @currency = current_currency
    @user ||= current_user
  end
end
`)
	for _, name := range []string{"current_currency", "current_user"} {
		if findEdge(r, "OrdersController#show", "self."+name, "calls") == nil {
			t.Errorf("missing assignment-RHS helper call self.%s", name)
		}
	}
}

func TestTwoStepNamespacedNewCallResolvesRuby(t *testing.T) {
	// `service = Klass.new; service.call(...)` on a namespaced constant must
	// emit a resolved calls edge so the service-object entry point isn't
	// reported dead. Regression guard for the dead-code false-positive case.
	r := parseRuby(t, `module Checkout
  class ProcessPaymentService
    def call(order)
      true
    end
  end
end

class PaymentCallbacksController
  def create
    service = Checkout::ProcessPaymentService.new
    service.call(order)
  end
end
`)
	e := findEdge(r, "PaymentCallbacksController#create", "Checkout::ProcessPaymentService#call", "calls")
	if e == nil {
		t.Fatal("expected calls edge to Checkout::ProcessPaymentService#call")
	}
	if e.Confidence < 0.5 {
		t.Errorf("edge confidence = %v, want >= 0.5 so dead-code counts it as a caller", e.Confidence)
	}
}

func TestMethodReceiverKindRuby(t *testing.T) {
	// Instance methods carry receiver=instance, singletons receiver=singleton,
	// top-level defs carry none — feeding the resolver's dispatch-kind
	// disambiguation.
	r := parseRuby(t, `class PriceValue
  def self.zero
    new(0)
  end

  def zero?
    amount == 0
  end
end

def top_level_helper
  1
end
`)
	cases := []struct {
		qualified, want string
	}{
		{"PriceValue.zero", extract.ReceiverSingleton},
		{"PriceValue#zero?", extract.ReceiverInstance},
		{"top_level_helper", ""},
	}
	for _, c := range cases {
		s := findSymbol(r, c.qualified)
		if s == nil {
			t.Fatalf("missing symbol %q", c.qualified)
		}
		if s.Receiver != c.want {
			t.Errorf("%q Receiver = %q, want %q", c.qualified, s.Receiver, c.want)
		}
	}
}

func TestSuperclassNotEmittedAsReferenceRuby(t *testing.T) {
	// A superclass is recorded as an inherits edge, never as a references edge.
	r := parseRuby(t, `class Parent
end

class Child < Parent
  def work
    helper
  end
end
`)
	if findEdge(r, "Child", "Parent", "references") != nil {
		t.Error("superclass should be inherits, not references")
	}
	if findEdge(r, "Child", "Parent", "inherits") == nil {
		t.Error("missing inherits edge Child -> Parent")
	}
}

func TestStructuralDSLArgsNoDoubleReferenceRuby(t *testing.T) {
	// extend (includes) and has_many (composes via serializer:) already emit
	// a more specific edge; their constant args must not also emit references.
	r := parseRuby(t, `class Widget
  extend Helpers
  has_many :comments, serializer: CommentSerializer
end
`)
	if findEdge(r, "Widget", "Helpers", "includes") == nil {
		t.Error("missing includes edge for extend")
	}
	if findEdge(r, "Widget", "Helpers", "references") != nil {
		t.Error("extend arg should not also emit a references edge")
	}
	if findEdge(r, "Widget", "CommentSerializer", "references") != nil {
		t.Error("has_many serializer arg should not also emit a references edge")
	}
}

func TestPkgBindingFirstWriteWinsRuby(t *testing.T) {
	// Two classes share a trailing segment; the bare-name binding resolves to
	// the first-registered one. Exercises the already-exists guard in
	// collectConstants.
	r := parseRuby(t, `module Admin
  class Account
  end
end

module Billing
  class Account
  end
end

class Report
  def run
    x = Account
  end
end
`)
	if findEdge(r, "Report#run", "Admin::Account", "references") == nil {
		t.Error("bare Account should resolve to first-registered Admin::Account")
	}
}

// A rescue clause binds its exception variable's type, so a call on that
// variable resolves to ExceptionType#method instead of a bare-name guess.
func TestRescueBindingResolvesNamespacedException(t *testing.T) {
	r := parseRuby(t, `module SocialCommerce
  module Meta
    class ApiError < StandardError
      def retriable?
        true
      end
    end
  end
end

class BatchSyncJob
  def perform
    do_work
  rescue SocialCommerce::Meta::ApiError => e
    raise if e.retriable?
  end
end
`)
	if findEdge(r, "BatchSyncJob#perform", "SocialCommerce::Meta::ApiError#retriable?", "calls") == nil {
		t.Errorf("expected rescue var resolved to SocialCommerce::Meta::ApiError#retriable?; edges: %v", edgeTargetsFrom(r, "BatchSyncJob#perform"))
	}
	if findEdge(r, "BatchSyncJob#perform", "retriable?", "calls") != nil {
		t.Error("should not also emit a bare-name retriable? fallback once the type is known")
	}
}

// A bare exception constant resolves to its qualified name through pkgBindings.
func TestRescueBindingResolvesViaPkgBindings(t *testing.T) {
	r := parseRuby(t, `module SocialCommerce
  module Meta
    class ApiError < StandardError
      def retriable?
        true
      end
    end

    class CatalogSyncService
      def call
        do_work
      rescue ApiError => e
        raise if e.retriable?
      end
    end
  end
end
`)
	want := "SocialCommerce::Meta::ApiError#retriable?"
	if findEdge(r, "SocialCommerce::Meta::CatalogSyncService#call", want, "calls") == nil {
		t.Errorf("expected bare ApiError resolved via pkgBindings to %s; edges: %v",
			want, edgeTargetsFrom(r, "SocialCommerce::Meta::CatalogSyncService#call"))
	}
}

// A multi-type rescue leaves the variable ambiguous — no typed edge is emitted;
// the bare-name fallback is used instead.
func TestRescueBindingSkipsMultiType(t *testing.T) {
	r := parseRuby(t, `class Worker
  def run
    do_work
  rescue FooError, BarError => e
    raise if e.retriable?
  end
end
`)
	if findEdge(r, "Worker#run", "FooError#retriable?", "calls") != nil ||
		findEdge(r, "Worker#run", "BarError#retriable?", "calls") != nil {
		t.Error("multi-type rescue must not bind the variable to either type")
	}
	if findEdge(r, "Worker#run", "retriable?", "calls") == nil {
		t.Error("expected bare-name fallback edge for an ambiguous rescue variable")
	}
}

// A typeless rescue (`rescue => e`) has no type to bind.
func TestRescueBindingSkipsTypeless(t *testing.T) {
	r := parseRuby(t, `class Worker
  def run
    do_work
  rescue => e
    raise if e.retriable?
  end
end
`)
	if findEdge(r, "Worker#run", "retriable?", "calls") == nil {
		t.Error("typeless rescue should fall back to the bare method name")
	}
}

// An explicit assignment type takes precedence over a same-named rescue binding.
func TestRescueBindingDoesNotOverrideAssignment(t *testing.T) {
	r := parseRuby(t, `class Helper
  def retriable?
    false
  end
end

class FooError < StandardError
  def retriable?
    true
  end
end

class Worker
  def run
    e = Helper.new
    e.retriable?
  rescue FooError => e
    e.retriable?
  end
end
`)
	if findEdge(r, "Worker#run", "Helper#retriable?", "calls") == nil {
		t.Error("explicit e = Helper.new should keep precedence for the variable type")
	}
}

// edgeTargetsFrom lists the calls-edge targets emitted from a given source, for
// test failure diagnostics.
func edgeTargetsFrom(r *recorder, source string) []string {
	var out []string
	for _, e := range r.edges {
		if e.SourceQualified == source && e.Kind == "calls" {
			out = append(out, e.TargetQualified)
		}
	}
	return out
}

// A rescue that names a type but binds no variable (`rescue FooError`) records
// nothing and does not panic.
func TestRescueBindingNoVariable(t *testing.T) {
	r := parseRuby(t, `class Worker
  def run
    do_work
  rescue FooError
    cleanup
  end
end
`)
	if findEdge(r, "Worker#run", "FooError#retriable?", "calls") != nil {
		t.Error("a rescue without a variable binding should not produce a typed call edge")
	}
}
