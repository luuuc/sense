package ruby

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// recorder captures emitted symbols and edges for assertions.
type recorder struct {
	symbols []extract.EmittedSymbol
	edges   []extract.EmittedEdge
}

func (r *recorder) Symbol(s extract.EmittedSymbol) error { r.symbols = append(r.symbols, s); return nil }
func (r *recorder) Edge(e extract.EmittedEdge) error     { r.edges = append(r.edges, e); return nil }

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
	if findEdge(r, "Order#process", "validate", "calls") == nil {
		t.Error("missing calls edge Order#process -> validate")
	}
	if findEdge(r, "Order#process", "customer.notify", "calls") == nil {
		t.Error("missing calls edge Order#process -> customer.notify")
	}
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
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.SourceQualified == "Service#invoke" {
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
	if findEdge(r, "Order#process", "validate_order", "calls") == nil {
		t.Error("missing calls edge from bare identifier call")
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
	// bare identifiers in statement position should produce calls edges
	if findEdge(r, "Service#process", "validate", "calls") == nil {
		t.Error("missing calls edge from bare identifier validate")
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
	if findEdge(r, "Service#process", "connection.execute", "calls") == nil {
		t.Error("missing calls edge from receiver.method call")
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
