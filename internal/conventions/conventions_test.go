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
			SourceID:   srcID,
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

