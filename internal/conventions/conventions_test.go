package conventions

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func setupFixtureIndex(t *testing.T) *sqlite.Adapter {
	t.Helper()
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	now := time.Now()

	// Files
	files := []model.File{
		{Path: "app/services/checkout_service.rb", Language: "ruby", Hash: "a1", Symbols: 1, IndexedAt: now},
		{Path: "app/services/payment_service.rb", Language: "ruby", Hash: "a2", Symbols: 1, IndexedAt: now},
		{Path: "app/services/shipping_service.rb", Language: "ruby", Hash: "a3", Symbols: 1, IndexedAt: now},
		{Path: "app/services/refund_service.rb", Language: "ruby", Hash: "a4", Symbols: 1, IndexedAt: now},
		{Path: "app/controllers/orders_controller.rb", Language: "ruby", Hash: "b1", Symbols: 1, IndexedAt: now},
		{Path: "app/controllers/users_controller.rb", Language: "ruby", Hash: "b2", Symbols: 1, IndexedAt: now},
		{Path: "app/controllers/admin_controller.rb", Language: "ruby", Hash: "b3", Symbols: 1, IndexedAt: now},
		{Path: "app/controllers/sessions_controller.rb", Language: "ruby", Hash: "b4", Symbols: 1, IndexedAt: now},
		{Path: "app/models/order.rb", Language: "ruby", Hash: "c1", Symbols: 1, IndexedAt: now},
		{Path: "app/models/user.rb", Language: "ruby", Hash: "c2", Symbols: 1, IndexedAt: now},
		{Path: "app/models/product.rb", Language: "ruby", Hash: "c3", Symbols: 1, IndexedAt: now},
		{Path: "test/services/checkout_service_test.rb", Language: "ruby", Hash: "d1", Symbols: 1, IndexedAt: now},
		{Path: "test/services/payment_service_test.rb", Language: "ruby", Hash: "d2", Symbols: 1, IndexedAt: now},
		{Path: "test/services/shipping_service_test.rb", Language: "ruby", Hash: "d3", Symbols: 1, IndexedAt: now},
		{Path: "test/services/refund_service_test.rb", Language: "ruby", Hash: "d4", Symbols: 1, IndexedAt: now},
		{Path: "test/controllers/orders_controller_test.rb", Language: "ruby", Hash: "d5", Symbols: 1, IndexedAt: now},
		// Below threshold: only 2 service objects inheriting a different base
		{Path: "app/jobs/send_email_job.rb", Language: "ruby", Hash: "e1", Symbols: 1, IndexedAt: now},
		{Path: "app/jobs/process_order_job.rb", Language: "ruby", Hash: "e2", Symbols: 1, IndexedAt: now},
	}
	fileIDs := make(map[string]int64)
	for i := range files {
		id, err := adapter.WriteFile(ctx, &files[i])
		if err != nil {
			t.Fatal(err)
		}
		fileIDs[files[i].Path] = id
	}

	// Symbols
	type symDef struct {
		fileKey   string
		name      string
		qualified string
		kind      string
	}
	symDefs := []symDef{
		// Services (inheriting ApplicationService)
		{"app/services/checkout_service.rb", "CheckoutService", "App::Services::CheckoutService", "class"},
		{"app/services/payment_service.rb", "PaymentService", "App::Services::PaymentService", "class"},
		{"app/services/shipping_service.rb", "ShippingService", "App::Services::ShippingService", "class"},
		{"app/services/refund_service.rb", "RefundService", "App::Services::RefundService", "class"},
		// The base class
		{"app/services/checkout_service.rb", "ApplicationService", "App::ApplicationService", "class"},
		// Controllers (inheriting ApplicationController, including Authentication)
		{"app/controllers/orders_controller.rb", "OrdersController", "App::Controllers::OrdersController", "class"},
		{"app/controllers/users_controller.rb", "UsersController", "App::Controllers::UsersController", "class"},
		{"app/controllers/admin_controller.rb", "AdminController", "App::Controllers::AdminController", "class"},
		{"app/controllers/sessions_controller.rb", "SessionsController", "App::Controllers::SessionsController", "class"},
		// Controller base
		{"app/controllers/orders_controller.rb", "ApplicationController", "App::ApplicationController", "class"},
		// Authentication module
		{"app/controllers/orders_controller.rb", "Authentication", "App::Concerns::Authentication", "module"},
		// Models
		{"app/models/order.rb", "Order", "App::Models::Order", "class"},
		{"app/models/user.rb", "User", "App::Models::User", "class"},
		{"app/models/product.rb", "Product", "App::Models::Product", "class"},
		// Test symbols
		{"test/services/checkout_service_test.rb", "CheckoutServiceTest", "CheckoutServiceTest", "class"},
		{"test/services/payment_service_test.rb", "PaymentServiceTest", "PaymentServiceTest", "class"},
		{"test/services/shipping_service_test.rb", "ShippingServiceTest", "ShippingServiceTest", "class"},
		{"test/services/refund_service_test.rb", "RefundServiceTest", "RefundServiceTest", "class"},
		{"test/controllers/orders_controller_test.rb", "OrdersControllerTest", "OrdersControllerTest", "class"},
		// Jobs (only 2 — below threshold for separate convention)
		{"app/jobs/send_email_job.rb", "SendEmailJob", "App::Jobs::SendEmailJob", "class"},
		{"app/jobs/process_order_job.rb", "ProcessOrderJob", "App::Jobs::ProcessOrderJob", "class"},
		// A different base class for jobs (only 2 inherit it → below threshold)
		{"app/jobs/send_email_job.rb", "ApplicationJob", "App::ApplicationJob", "class"},
	}

	symIDs := make(map[string]int64)
	for _, sd := range symDefs {
		fid := fileIDs[sd.fileKey]
		s := &model.Symbol{
			FileID:    fid,
			Name:      sd.name,
			Qualified: sd.qualified,
			Kind:      model.SymbolKind(sd.kind),
			LineStart: 1,
			LineEnd:   10,
		}
		id, err := adapter.WriteSymbol(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		symIDs[sd.qualified] = id
	}

	// Edges
	type edgeDef struct {
		source string
		target string
		kind   string
		file   string
	}
	edgeDefs := []edgeDef{
		// 4 services inherit ApplicationService
		{"App::Services::CheckoutService", "App::ApplicationService", "inherits", "app/services/checkout_service.rb"},
		{"App::Services::PaymentService", "App::ApplicationService", "inherits", "app/services/payment_service.rb"},
		{"App::Services::ShippingService", "App::ApplicationService", "inherits", "app/services/shipping_service.rb"},
		{"App::Services::RefundService", "App::ApplicationService", "inherits", "app/services/refund_service.rb"},
		// 4 controllers inherit ApplicationController
		{"App::Controllers::OrdersController", "App::ApplicationController", "inherits", "app/controllers/orders_controller.rb"},
		{"App::Controllers::UsersController", "App::ApplicationController", "inherits", "app/controllers/users_controller.rb"},
		{"App::Controllers::AdminController", "App::ApplicationController", "inherits", "app/controllers/admin_controller.rb"},
		{"App::Controllers::SessionsController", "App::ApplicationController", "inherits", "app/controllers/sessions_controller.rb"},
		// 4 controllers include Authentication
		{"App::Controllers::OrdersController", "App::Concerns::Authentication", "includes", "app/controllers/orders_controller.rb"},
		{"App::Controllers::UsersController", "App::Concerns::Authentication", "includes", "app/controllers/users_controller.rb"},
		{"App::Controllers::AdminController", "App::Concerns::Authentication", "includes", "app/controllers/admin_controller.rb"},
		{"App::Controllers::SessionsController", "App::Concerns::Authentication", "includes", "app/controllers/sessions_controller.rb"},
		// Tests edges
		{"CheckoutServiceTest", "App::Services::CheckoutService", "tests", "test/services/checkout_service_test.rb"},
		{"PaymentServiceTest", "App::Services::PaymentService", "tests", "test/services/payment_service_test.rb"},
		{"ShippingServiceTest", "App::Services::ShippingService", "tests", "test/services/shipping_service_test.rb"},
		{"RefundServiceTest", "App::Services::RefundService", "tests", "test/services/refund_service_test.rb"},
		{"OrdersControllerTest", "App::Controllers::OrdersController", "tests", "test/controllers/orders_controller_test.rb"},
		// Jobs: only 2 inherit ApplicationJob — below threshold
		{"App::Jobs::SendEmailJob", "App::ApplicationJob", "inherits", "app/jobs/send_email_job.rb"},
		{"App::Jobs::ProcessOrderJob", "App::ApplicationJob", "inherits", "app/jobs/process_order_job.rb"},
	}
	for _, ed := range edgeDefs {
		srcID, ok := symIDs[ed.source]
		if !ok {
			t.Fatalf("symbol not found: %s", ed.source)
		}
		tgtID, ok := symIDs[ed.target]
		if !ok {
			t.Fatalf("symbol not found: %s", ed.target)
		}
		fid := fileIDs[ed.file]
		e := &model.Edge{
			SourceID:   &srcID,
			TargetID:   tgtID,
			Kind:       model.EdgeKind(ed.kind),
			FileID:     fid,
			Confidence: 1.0,
		}
		if _, err := adapter.WriteEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	return adapter
}

func TestDetectAllCategories(t *testing.T) {
	adapter := setupFixtureIndex(t)
	ctx := context.Background()

	conventions, symbolCount, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if symbolCount == 0 {
		t.Fatal("expected non-zero symbol count")
	}

	// Check inheritance category present
	found := findByCategory(conventions, CategoryInheritance)
	if len(found) == 0 {
		t.Error("expected inheritance conventions")
	}
	// ApplicationService should be detected (4 instances)
	hasAppService := false
	for _, c := range found {
		if c.Instances >= 4 && strings.Contains(c.Description, "ApplicationService") {
			hasAppService = true
		}
	}
	if !hasAppService {
		t.Errorf("expected ApplicationService inheritance convention, got: %v", found)
	}

	// Jobs with only 2 instances should NOT appear
	for _, c := range found {
		if strings.Contains(c.Description, "ApplicationJob") {
			t.Errorf("ApplicationJob should be below threshold (2 < 3), but was detected: %v", c)
		}
	}

	// Check composition category
	comp := findByCategory(conventions, CategoryComposition)
	if len(comp) == 0 {
		t.Error("expected composition conventions")
	}
	hasAuth := false
	for _, c := range comp {
		if strings.Contains(c.Description, "Authentication") {
			hasAuth = true
		}
	}
	if !hasAuth {
		t.Errorf("expected Authentication composition convention, got: %v", comp)
	}

	// Check testing category
	testing_ := findByCategory(conventions, CategoryTesting)
	if len(testing_) == 0 {
		t.Error("expected testing conventions")
	}

	// Check naming category
	naming := findByCategory(conventions, CategoryNaming)
	if len(naming) == 0 {
		t.Error("expected naming conventions")
	}
}

func TestDetectMinStrength(t *testing.T) {
	adapter := setupFixtureIndex(t)
	ctx := context.Background()

	all, _, err := Detect(ctx, adapter.DB(), Options{MinStrength: 0})
	if err != nil {
		t.Fatal(err)
	}

	strong, _, err := Detect(ctx, adapter.DB(), Options{MinStrength: 0.9})
	if err != nil {
		t.Fatal(err)
	}

	// Fixture is designed so that some conventions are below 0.9 (e.g.
	// structure conventions where 4 classes share a dir but total class
	// count is higher). If this assertion fails, the fixture needs a
	// convention with strength < 0.9.
	var belowThreshold int
	for _, c := range all {
		if c.Strength < 0.9 {
			belowThreshold++
		}
	}
	if belowThreshold == 0 {
		t.Fatal("fixture must contain at least one convention with strength < 0.9 to exercise min_strength filtering")
	}

	if len(strong) >= len(all) {
		t.Errorf("min_strength filter should reduce results: all=%d strong=%d", len(all), len(strong))
	}
	if len(strong) != len(all)-belowThreshold {
		t.Errorf("expected %d strong conventions, got %d", len(all)-belowThreshold, len(strong))
	}
	for _, c := range strong {
		if c.Strength < 0.9 {
			t.Errorf("convention below min_strength: %v", c)
		}
	}
}

func TestDetectDomainFilter(t *testing.T) {
	adapter := setupFixtureIndex(t)
	ctx := context.Background()

	conventions, _, err := Detect(ctx, adapter.DB(), Options{Domain: "services"})
	if err != nil {
		t.Fatal(err)
	}

	// Should find inheritance for services, not controllers
	for _, c := range conventions {
		if c.Category == CategoryInheritance && strings.Contains(c.Description, "ApplicationController") {
			t.Error("domain=services should not detect controller inheritance")
		}
	}
}

func TestDetectThresholdEnforcement(t *testing.T) {
	adapter := setupFixtureIndex(t)
	ctx := context.Background()

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range conventions {
		if c.Instances < minInstances {
			t.Errorf("convention below threshold: %v", c)
		}
	}
}

func TestDetectStrengthScoring(t *testing.T) {
	adapter := setupFixtureIndex(t)
	ctx := context.Background()

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range conventions {
		if c.Strength < 0 || c.Strength > 1.0 {
			t.Errorf("strength out of range: %v", c)
		}
		expected := float64(c.Instances) / float64(c.Total)
		if c.Strength != expected {
			t.Errorf("strength mismatch for %q: got %f, want %f", c.Description, c.Strength, expected)
		}
	}
}

func TestDetectDefaultOptionsShowLowStrength(t *testing.T) {
	adapter := setupFixtureIndex(t)
	ctx := context.Background()

	// Zero-value Options (the CLI/MCP default) must not hide conventions.
	all, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	filtered, _, err := Detect(ctx, adapter.DB(), Options{MinStrength: 0.5})
	if err != nil {
		t.Fatal(err)
	}

	var belowHalf int
	for _, c := range all {
		if c.Strength < 0.5 {
			belowHalf++
		}
	}
	if belowHalf == 0 {
		t.Fatal("fixture must contain at least one convention with strength < 0.5 to exercise this regression test")
	}
	if len(all) <= len(filtered) {
		t.Errorf("zero-value Options should return more conventions than MinStrength=0.5: got all=%d filtered=%d", len(all), len(filtered))
	}
}

func TestDetectEmptyIndex(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	conventions, symbolCount, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if symbolCount != 0 {
		t.Errorf("expected 0 symbols, got %d", symbolCount)
	}
	if len(conventions) != 0 {
		t.Errorf("expected no conventions, got %d", len(conventions))
	}
}

func TestPickRepresentatives(t *testing.T) {
	tests := []struct {
		name     string
		examples []Example
		max      int
		want     []string
	}{
		{"empty", nil, 3, nil},
		{"one", []Example{{Name: "A", Path: "a"}}, 3, []string{"A"}},
		{"two", []Example{{Name: "A", Path: "a"}, {Name: "B", Path: "b"}}, 3, []string{"A", "B"}},
		{"exactly three", []Example{
			{Name: "A", Path: "a"}, {Name: "B", Path: "b"}, {Name: "C", Path: "c"},
		}, 3, []string{"A", "B", "C"}},
		{"picks by edge count descending", []Example{
			{Name: "A", Path: "a", EdgeCount: 2},
			{Name: "B", Path: "b", EdgeCount: 10},
			{Name: "C", Path: "c", EdgeCount: 5},
			{Name: "D", Path: "d", EdgeCount: 1},
		}, 3, []string{"B", "C", "A"}},
		{"zero edge counts preserves input order", []Example{
			{Name: "A", Path: "a"}, {Name: "B", Path: "b"}, {Name: "C", Path: "c"},
			{Name: "D", Path: "d"}, {Name: "E", Path: "e"}, {Name: "F", Path: "f"},
		}, 3, []string{"A", "B", "C"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PickRepresentatives(tt.examples, tt.max)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestHasMatchingExample(t *testing.T) {
	examples := []Example{
		{Name: "Order", Path: "app/models/order.rb"},
		{Name: "User", Path: "app/models/user.rb"},
	}
	if !hasMatchingExample(examples, "models") {
		t.Error("expected match for domain 'models'")
	}
	if hasMatchingExample(examples, "controllers") {
		t.Error("expected no match for domain 'controllers'")
	}
	if hasMatchingExample(nil, "models") {
		t.Error("expected no match for nil examples")
	}
}

func TestDomainFilterTighteningExcludesNonMatching(t *testing.T) {
	adapter := setupFixtureIndex(t)
	ctx := context.Background()

	all, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	services, _, err := Detect(ctx, adapter.DB(), Options{Domain: "services"})
	if err != nil {
		t.Fatal(err)
	}

	if len(services) >= len(all) {
		t.Errorf("domain filter should reduce conventions: all=%d services=%d", len(all), len(services))
	}

	for _, c := range services {
		matched := false
		for _, e := range c.Examples {
			if strings.Contains(e.Path, "services") {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("convention %q has no examples matching domain 'services': examples=%v", c.Description, c.Examples)
		}
	}
}

func TestExamplesPopulated(t *testing.T) {
	adapter := setupFixtureIndex(t)
	ctx := context.Background()

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range conventions {
		if len(c.Examples) == 0 {
			t.Errorf("convention %q has no examples", c.Description)
		}
		if len(c.Examples) != c.Instances {
			t.Errorf("convention %q: len(Examples)=%d != Instances=%d", c.Description, len(c.Examples), c.Instances)
		}
	}
}

func TestDescriptionsContainInstanceNames(t *testing.T) {
	adapter := setupFixtureIndex(t)
	ctx := context.Background()

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range conventions {
		if c.Category == CategoryTesting {
			continue
		}
		hasName := false
		for _, ex := range c.Examples {
			if strings.Contains(c.Description, ex.Name) {
				hasName = true
				break
			}
		}
		if !hasName {
			t.Errorf("convention description %q does not contain any instance name from examples %v",
				c.Description, c.Examples)
		}
	}

	inh := findByCategory(conventions, CategoryInheritance)
	for _, c := range inh {
		if !strings.Contains(c.Description, "extend") {
			t.Errorf("inheritance convention should use 'extend' format, got %q", c.Description)
		}
		if !strings.Contains(c.Description, "base class") {
			t.Errorf("inheritance convention should describe base class pattern, got %q", c.Description)
		}
	}

	struc := findByCategory(conventions, CategoryStructure)
	for _, c := range struc {
		if !strings.Contains(c.Description, "grouped in") {
			t.Errorf("structure convention should use 'grouped in' format, got %q", c.Description)
		}
	}
}

func findByCategory(conventions []Convention, cat Category) []Convention {
	var out []Convention
	for _, c := range conventions {
		if c.Category == cat {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Strength > out[j].Strength
	})
	return out
}

func setupDesignPatternsFixture(t *testing.T) *sqlite.Adapter {
	t.Helper()
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	now := time.Now()

	files := []model.File{
		{Path: "app/services/checkout_service.rb", Language: "ruby", Hash: "a1", Symbols: 1, IndexedAt: now},
		{Path: "app/services/payment_service.rb", Language: "ruby", Hash: "a2", Symbols: 1, IndexedAt: now},
		{Path: "app/services/refund_service.rb", Language: "ruby", Hash: "a3", Symbols: 1, IndexedAt: now},
		{Path: "app/models/concerns/trackable.rb", Language: "ruby", Hash: "b1", Symbols: 1, IndexedAt: now},
		{Path: "app/models/order.rb", Language: "ruby", Hash: "b2", Symbols: 1, IndexedAt: now},
		{Path: "app/models/user.rb", Language: "ruby", Hash: "b3", Symbols: 1, IndexedAt: now},
		{Path: "app/models/product.rb", Language: "ruby", Hash: "b4", Symbols: 1, IndexedAt: now},
		{Path: "app/controllers/orders_controller.rb", Language: "ruby", Hash: "c1", Symbols: 1, IndexedAt: now},
		{Path: "app/controllers/users_controller.rb", Language: "ruby", Hash: "c2", Symbols: 1, IndexedAt: now},
		{Path: "app/controllers/admin_controller.rb", Language: "ruby", Hash: "c3", Symbols: 1, IndexedAt: now},
		{Path: "src/hooks/useAuth.ts", Language: "typescript", Hash: "d1", Symbols: 1, IndexedAt: now},
		{Path: "src/hooks/useCart.ts", Language: "typescript", Hash: "d2", Symbols: 1, IndexedAt: now},
		{Path: "src/hooks/useUser.ts", Language: "typescript", Hash: "d3", Symbols: 1, IndexedAt: now},
	}
	fileIDs := make(map[string]int64)
	for i := range files {
		id, err := adapter.WriteFile(ctx, &files[i])
		if err != nil {
			t.Fatal(err)
		}
		fileIDs[files[i].Path] = id
	}

	type symDef struct {
		fileKey   string
		name      string
		qualified string
		kind      string
		parentQ   string
	}
	symDefs := []symDef{
		// Service objects with single call method
		{"app/services/checkout_service.rb", "CheckoutService", "CheckoutService", "class", ""},
		{"app/services/checkout_service.rb", "call", "CheckoutService#call", "method", "CheckoutService"},
		{"app/services/payment_service.rb", "PaymentService", "PaymentService", "class", ""},
		{"app/services/payment_service.rb", "call", "PaymentService#call", "method", "PaymentService"},
		{"app/services/refund_service.rb", "RefundService", "RefundService", "class", ""},
		{"app/services/refund_service.rb", "execute", "RefundService#execute", "method", "RefundService"},
		// Concern module
		{"app/models/concerns/trackable.rb", "Trackable", "Trackable", "module", ""},
		// Models that include the concern
		{"app/models/order.rb", "Order", "Order", "class", ""},
		{"app/models/user.rb", "User", "User", "class", ""},
		{"app/models/product.rb", "Product", "Product", "class", ""},
		// Controllers
		{"app/controllers/orders_controller.rb", "OrdersController", "OrdersController", "class", ""},
		{"app/controllers/users_controller.rb", "UsersController", "UsersController", "class", ""},
		{"app/controllers/admin_controller.rb", "AdminController", "AdminController", "class", ""},
		// React hooks
		{"src/hooks/useAuth.ts", "useAuth", "useAuth", "function", ""},
		{"src/hooks/useCart.ts", "useCart", "useCart", "function", ""},
		{"src/hooks/useUser.ts", "useUser", "useUser", "function", ""},
	}

	symIDs := make(map[string]int64)
	parentQualified := make(map[string]string)
	for _, sd := range symDefs {
		if sd.parentQ != "" {
			parentQualified[sd.qualified] = sd.parentQ
		}
	}
	// First pass: create all symbols without parent links
	for _, sd := range symDefs {
		fid := fileIDs[sd.fileKey]
		s := &model.Symbol{
			FileID:    fid,
			Name:      sd.name,
			Qualified: sd.qualified,
			Kind:      model.SymbolKind(sd.kind),
			LineStart: 1,
			LineEnd:   10,
		}
		id, err := adapter.WriteSymbol(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		symIDs[sd.qualified] = id
	}
	// Second pass: set parent_id for methods
	for childQ, parentQ := range parentQualified {
		childID := symIDs[childQ]
		parentID := symIDs[parentQ]
		_, err := adapter.DB().ExecContext(ctx, `UPDATE sense_symbols SET parent_id = ? WHERE id = ?`, parentID, childID)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Edges: models include Trackable concern
	type edgeDef struct {
		source, target, kind, file string
	}
	edgeDefs := []edgeDef{
		{"Order", "Trackable", "includes", "app/models/order.rb"},
		{"User", "Trackable", "includes", "app/models/user.rb"},
		{"Product", "Trackable", "includes", "app/models/product.rb"},
		// Controllers call models (layer boundary)
		{"OrdersController", "Order", "calls", "app/controllers/orders_controller.rb"},
		{"UsersController", "User", "calls", "app/controllers/users_controller.rb"},
		{"AdminController", "Order", "calls", "app/controllers/admin_controller.rb"},
	}
	for _, ed := range edgeDefs {
		srcID := symIDs[ed.source]
		tgtID := symIDs[ed.target]
		fid := fileIDs[ed.file]
		e := &model.Edge{
			SourceID:   &srcID,
			TargetID:   tgtID,
			Kind:       model.EdgeKind(ed.kind),
			FileID:     fid,
			Confidence: 1.0,
		}
		if _, err := adapter.WriteEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	return adapter
}

func TestDetectDesignPatterns(t *testing.T) {
	adapter := setupDesignPatternsFixture(t)
	ctx := context.Background()

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	dp := findByCategory(conventions, CategoryDesignPattern)
	if len(dp) == 0 {
		t.Fatal("expected design_pattern conventions")
	}
	found := false
	for _, c := range dp {
		if strings.Contains(c.Description, "Service object") {
			found = true
			if c.Instances != 3 {
				t.Errorf("expected 3 service objects, got %d", c.Instances)
			}
		}
	}
	if !found {
		t.Errorf("expected Service object pattern, got: %v", dp)
	}
}

func TestDetectFrameworkIdiomsConcerns(t *testing.T) {
	adapter := setupDesignPatternsFixture(t)
	ctx := context.Background()

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	fw := findByCategory(conventions, CategoryFramework)
	hasConcern := false
	hasHook := false
	for _, c := range fw {
		if strings.Contains(c.Description, "Concern") && strings.Contains(c.Description, "Trackable") {
			hasConcern = true
		}
		if strings.Contains(c.Description, "hook") {
			hasHook = true
		}
	}
	if !hasConcern {
		t.Error("expected Trackable concern detection")
	}
	if !hasHook {
		t.Error("expected React hook pattern detection")
	}
}

func TestDetectArchitectureLayers(t *testing.T) {
	adapter := setupDesignPatternsFixture(t)
	ctx := context.Background()

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	arch := findByCategory(conventions, CategoryArchitecture)
	if len(arch) == 0 {
		t.Fatal("expected architecture conventions")
	}
	found := false
	for _, c := range arch {
		if strings.Contains(c.Description, "controllers") && strings.Contains(c.Description, "models") {
			found = true
			if c.Strength <= 0 || c.Strength > 1.0 {
				t.Errorf("architecture strength should be in (0, 1.0], got %f", c.Strength)
			}
		}
	}
	if !found {
		t.Errorf("expected controllers→models layer boundary, got: %v", arch)
	}
}

func TestDetectDesignPatternsEmpty(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range conventions {
		if c.Category == CategoryDesignPattern || c.Category == CategoryFramework || c.Category == CategoryArchitecture {
			t.Errorf("expected no new-category conventions on empty index, got: %v", c)
		}
	}
}

func setupGoFrameworkFixture(t *testing.T) *sqlite.Adapter {
	t.Helper()
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	now := time.Now()
	files := []model.File{
		{Path: "gin.go", Language: "go", Hash: "g1", Symbols: 1, IndexedAt: now},
		{Path: "routergroup.go", Language: "go", Hash: "g2", Symbols: 1, IndexedAt: now},
		{Path: "auth.go", Language: "go", Hash: "g3", Symbols: 1, IndexedAt: now},
		{Path: "logger.go", Language: "go", Hash: "g4", Symbols: 1, IndexedAt: now},
		{Path: "recovery.go", Language: "go", Hash: "g5", Symbols: 1, IndexedAt: now},
		{Path: "cors.go", Language: "go", Hash: "g6", Symbols: 1, IndexedAt: now},
	}
	fileIDs := make(map[string]int64)
	for i := range files {
		id, err := adapter.WriteFile(ctx, &files[i])
		if err != nil {
			t.Fatal(err)
		}
		fileIDs[files[i].Path] = id
	}

	type symDef struct {
		fileKey   string
		name      string
		qualified string
		kind      string
	}
	symDefs := []symDef{
		// Type aliases (kind="type" in Go = alias/newtype)
		{"gin.go", "HandlerFunc", "gin.HandlerFunc", "type"},
		{"gin.go", "HandlersChain", "gin.HandlersChain", "type"},
		{"gin.go", "Params", "gin.Params", "type"},
		{"gin.go", "H", "gin.H", "type"},
		// Structs
		{"gin.go", "Engine", "gin.Engine", "class"},
		{"routergroup.go", "RouterGroup", "gin.RouterGroup", "class"},
		// Router method
		{"routergroup.go", "Use", "gin.RouterGroup.Use", "method"},
		// Middleware factories (functions)
		{"logger.go", "Logger", "gin.Logger", "function"},
		{"recovery.go", "Recovery", "gin.Recovery", "function"},
		{"auth.go", "BasicAuth", "gin.BasicAuth", "function"},
		{"cors.go", "CORS", "gin.CORS", "function"},
	}

	symIDs := make(map[string]int64)
	for _, sd := range symDefs {
		fid := fileIDs[sd.fileKey]
		s := &model.Symbol{
			FileID:    fid,
			Name:      sd.name,
			Qualified: sd.qualified,
			Kind:      model.SymbolKind(sd.kind),
			LineStart: 1,
			LineEnd:   10,
		}
		id, err := adapter.WriteSymbol(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		symIDs[sd.qualified] = id
	}

	type edgeDef struct {
		source string
		target string
		kind   string
		file   string
	}
	edgeDefs := []edgeDef{
		// Router method calls middleware factories
		{"gin.RouterGroup.Use", "gin.Logger", "calls", "routergroup.go"},
		{"gin.RouterGroup.Use", "gin.Recovery", "calls", "routergroup.go"},
		{"gin.RouterGroup.Use", "gin.BasicAuth", "calls", "routergroup.go"},
		{"gin.RouterGroup.Use", "gin.CORS", "calls", "routergroup.go"},
	}
	for _, ed := range edgeDefs {
		srcID := symIDs[ed.source]
		tgtID := symIDs[ed.target]
		fid := fileIDs[ed.file]
		e := &model.Edge{
			SourceID:   &srcID,
			TargetID:   tgtID,
			Kind:       model.EdgeKind(ed.kind),
			FileID:     fid,
			Confidence: 1.0,
		}
		if _, err := adapter.WriteEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	return adapter
}

func TestDetectGoTypeAliases(t *testing.T) {
	adapter := setupGoFrameworkFixture(t)
	ctx := context.Background()

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, c := range conventions {
		if strings.Contains(c.Description, "Type aliases") {
			found = true
			if c.Instances < 2 {
				t.Errorf("type alias convention should have at least 2 instances, got %d", c.Instances)
			}
			if c.Category != CategoryStructure {
				t.Errorf("type alias convention category = %q, want %q", c.Category, CategoryStructure)
			}
			break
		}
	}
	if !found {
		t.Error("expected Type aliases convention to be detected")
	}
}

func TestDetectGoMiddlewareFactories(t *testing.T) {
	adapter := setupGoFrameworkFixture(t)
	ctx := context.Background()

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, c := range conventions {
		if strings.Contains(c.Description, "Middleware factories") {
			found = true
			if c.Instances < 3 {
				t.Errorf("middleware factory convention should have at least 3 instances, got %d", c.Instances)
			}
			if c.Category != CategoryFramework {
				t.Errorf("middleware factory convention category = %q, want %q", c.Category, CategoryFramework)
			}
			break
		}
	}
	if !found {
		t.Error("expected Middleware factories convention to be detected")
	}
}

