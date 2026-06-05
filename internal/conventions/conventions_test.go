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
	testingConvs := findByCategory(conventions, CategoryTesting)
	if len(testingConvs) == 0 {
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
		{"zero edge counts sort by name ascending", []Example{
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

func TestDetectArchitectureLayersDeepTree(t *testing.T) {
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
		{Path: "javalin-ssl/src/main/kotlin/io/javalin/community/ssl/SslConfig.kt", Language: "kotlin", Hash: "s1", Symbols: 1, IndexedAt: now},
		{Path: "javalin-ssl/src/main/kotlin/io/javalin/community/ssl/SslPlugin.kt", Language: "kotlin", Hash: "s2", Symbols: 1, IndexedAt: now},
		{Path: "javalin-ssl/src/main/kotlin/io/javalin/community/ssl/CertLoader.kt", Language: "kotlin", Hash: "s3", Symbols: 1, IndexedAt: now},
		{Path: "javalin-core/src/main/kotlin/io/javalin/http/Handler.kt", Language: "kotlin", Hash: "c1", Symbols: 1, IndexedAt: now},
		{Path: "javalin-core/src/main/kotlin/io/javalin/http/Context.kt", Language: "kotlin", Hash: "c2", Symbols: 1, IndexedAt: now},
		{Path: "javalin-core/src/main/kotlin/io/javalin/http/Router.kt", Language: "kotlin", Hash: "c3", Symbols: 1, IndexedAt: now},
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
		file, name, qualified, kind string
	}
	symDefs := []symDef{
		{"javalin-ssl/src/main/kotlin/io/javalin/community/ssl/SslConfig.kt", "SslConfig", "SslConfig", "class"},
		{"javalin-ssl/src/main/kotlin/io/javalin/community/ssl/SslPlugin.kt", "SslPlugin", "SslPlugin", "class"},
		{"javalin-ssl/src/main/kotlin/io/javalin/community/ssl/CertLoader.kt", "CertLoader", "CertLoader", "class"},
		{"javalin-core/src/main/kotlin/io/javalin/http/Handler.kt", "Handler", "Handler", "class"},
		{"javalin-core/src/main/kotlin/io/javalin/http/Context.kt", "Context", "Context", "class"},
		{"javalin-core/src/main/kotlin/io/javalin/http/Router.kt", "Router", "Router", "class"},
	}
	symIDs := make(map[string]int64)
	for _, sd := range symDefs {
		s := &model.Symbol{
			FileID:    fileIDs[sd.file],
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

	edgeDefs := []struct{ source, target, file string }{
		{"SslConfig", "Handler", "javalin-ssl/src/main/kotlin/io/javalin/community/ssl/SslConfig.kt"},
		{"SslPlugin", "Context", "javalin-ssl/src/main/kotlin/io/javalin/community/ssl/SslPlugin.kt"},
		{"CertLoader", "Router", "javalin-ssl/src/main/kotlin/io/javalin/community/ssl/CertLoader.kt"},
	}
	for _, ed := range edgeDefs {
		srcID := symIDs[ed.source]
		tgtID := symIDs[ed.target]
		fid := fileIDs[ed.file]
		e := &model.Edge{
			SourceID:   &srcID,
			TargetID:   tgtID,
			Kind:       model.EdgeKind("calls"),
			FileID:     fid,
			Confidence: 1.0,
		}
		if _, err := adapter.WriteEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	arch := findByCategory(conventions, CategoryArchitecture)
	for _, c := range arch {
		if strings.Contains(c.Description, " ssl/") || strings.Contains(c.Description, " http/") {
			t.Errorf("deep-tree layer should use module prefix, not leaf package: %s", c.Description)
		}
	}
	foundModule := false
	for _, c := range arch {
		if strings.Contains(c.Description, "javalin-ssl/src") && strings.Contains(c.Description, "javalin-core/src") {
			foundModule = true
		}
	}
	if !foundModule {
		t.Errorf("expected javalin-ssl/src → javalin-core/src boundary, got: %v", arch)
	}
}

func TestLayerName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"app/controllers/orders_controller.rb", "controllers"},
		{"app/models/user.rb", "models"},
		{"javalin-ssl/src/main/kotlin/io/javalin/community/ssl/SslConfig.kt", "javalin-ssl/src"},
		{"javalin-core/src/main/kotlin/io/javalin/http/Handler.kt", "javalin-core/src"},
		{"packages/auth/src/index.ts", "packages/auth"},
		{"src/ProjectName/Models/User.cs", "src/ProjectName"},
		{"internal/extract/ruby/extractor.go", "internal/extract"},
		{"src/main/App.java", "main"},
		{"main.go", ""},
		{"cmd/app/main.go", "app"},
	}
	for _, tt := range tests {
		if got := layerName(tt.path); got != tt.want {
			t.Errorf("layerName(%q) = %q, want %q", tt.path, got, tt.want)
		}
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

func TestDetectTestingJavaKotlinFallback(t *testing.T) {
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
		{Path: "src/main/java/com/example/User.java", Language: "java", Hash: "j1", Symbols: 1, IndexedAt: now},
		{Path: "src/main/java/com/example/Order.java", Language: "java", Hash: "j2", Symbols: 1, IndexedAt: now},
		{Path: "src/main/java/com/example/Service.java", Language: "java", Hash: "j3", Symbols: 1, IndexedAt: now},
		{Path: "src/test/java/com/example/UserTest.java", Language: "java", Hash: "jt1", Symbols: 1, IndexedAt: now},
		{Path: "src/test/java/com/example/OrderTest.java", Language: "java", Hash: "jt2", Symbols: 1, IndexedAt: now},
		{Path: "src/test/java/com/example/ServiceTest.java", Language: "java", Hash: "jt3", Symbols: 1, IndexedAt: now},
		{Path: "src/test/kotlin/com/example/ConfigTests.kt", Language: "kotlin", Hash: "kt1", Symbols: 1, IndexedAt: now},
	}
	fileIDs := make(map[string]int64)
	for i := range files {
		id, err := adapter.WriteFile(ctx, &files[i])
		if err != nil {
			t.Fatal(err)
		}
		fileIDs[files[i].Path] = id
	}

	syms := []struct {
		file, name, qualified, kind string
	}{
		{"src/main/java/com/example/User.java", "User", "com.example.User", "class"},
		{"src/main/java/com/example/Order.java", "Order", "com.example.Order", "class"},
		{"src/main/java/com/example/Service.java", "Service", "com.example.Service", "class"},
		{"src/test/java/com/example/UserTest.java", "UserTest", "com.example.UserTest", "class"},
		{"src/test/java/com/example/OrderTest.java", "OrderTest", "com.example.OrderTest", "class"},
		{"src/test/java/com/example/ServiceTest.java", "ServiceTest", "com.example.ServiceTest", "class"},
		{"src/test/kotlin/com/example/ConfigTests.kt", "ConfigTests", "com.example.ConfigTests", "class"},
	}
	for _, sd := range syms {
		s := &model.Symbol{
			FileID:    fileIDs[sd.file],
			Name:      sd.name,
			Qualified: sd.qualified,
			Kind:      model.SymbolKind(sd.kind),
			LineStart: 1,
			LineEnd:   10,
		}
		if _, err := adapter.WriteSymbol(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	testingConvs := findByCategory(conventions, CategoryTesting)
	if len(testingConvs) == 0 {
		t.Fatal("expected testing conventions for Java/Kotlin files, got none")
	}
	found := false
	for _, c := range testingConvs {
		if strings.Contains(c.Description, "Test") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected *Test naming convention, got: %v", testingConvs)
	}
}

func setupCallbackFixture(t *testing.T) *sqlite.Adapter {
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
		{Path: "app/models/order.rb", Language: "ruby", Hash: "a1", Symbols: 1, IndexedAt: now},
		{Path: "app/models/user.rb", Language: "ruby", Hash: "a2", Symbols: 1, IndexedAt: now},
		{Path: "app/models/product.rb", Language: "ruby", Hash: "a3", Symbols: 1, IndexedAt: now},
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
		fileKey, name, qualified, kind string
	}
	symDefs := []symDef{
		{"app/models/order.rb", "Order", "Order", "class"},
		{"app/models/order.rb", "before_save", "Order.before_save", "method"},
		{"app/models/order.rb", "after_create", "Order.after_create", "method"},
		{"app/models/user.rb", "User", "User", "class"},
		{"app/models/user.rb", "before_validation", "User.before_validation", "method"},
		{"app/models/user.rb", "after_save", "User.after_save", "method"},
		{"app/models/product.rb", "Product", "Product", "class"},
		{"app/models/product.rb", "before_destroy", "Product.before_destroy", "method"},
		{"app/models/product.rb", "after_commit", "Product.after_commit", "method"},
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
	parents := map[string]string{
		"Order.before_save":      "Order",
		"Order.after_create":     "Order",
		"User.before_validation": "User",
		"User.after_save":        "User",
		"Product.before_destroy": "Product",
		"Product.after_commit":   "Product",
	}
	for childQ, parentQ := range parents {
		_, err := adapter.DB().ExecContext(ctx, `UPDATE sense_symbols SET parent_id = ? WHERE id = ?`, symIDs[parentQ], symIDs[childQ])
		if err != nil {
			t.Fatal(err)
		}
	}
	return adapter
}

func TestDetectRailsCallbacks(t *testing.T) {
	adapter := setupCallbackFixture(t)
	ctx := context.Background()

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	fw := findByCategory(conventions, CategoryFramework)
	found := false
	for _, c := range fw {
		if strings.Contains(c.Description, "Callback patterns") {
			found = true
			if c.Instances != 3 {
				t.Errorf("expected 3 classes with callbacks, got %d", c.Instances)
			}
		}
	}
	if !found {
		t.Error("expected Callback patterns convention")
	}
}

func TestDetectRailsCallbacksBelowThreshold(t *testing.T) {
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
	f := model.File{Path: "app/models/order.rb", Language: "ruby", Hash: "a1", Symbols: 1, IndexedAt: now}
	fid, err := adapter.WriteFile(ctx, &f)
	if err != nil {
		t.Fatal(err)
	}
	cls := &model.Symbol{FileID: fid, Name: "Order", Qualified: "Order", Kind: "class", LineStart: 1, LineEnd: 10}
	clsID, err := adapter.WriteSymbol(ctx, cls)
	if err != nil {
		t.Fatal(err)
	}
	cb := &model.Symbol{FileID: fid, Name: "before_save", Qualified: "Order.before_save", Kind: "method", LineStart: 2, LineEnd: 2}
	cbID, err := adapter.WriteSymbol(ctx, cb)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = adapter.DB().ExecContext(ctx, `UPDATE sense_symbols SET parent_id = ? WHERE id = ?`, clsID, cbID)

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range findByCategory(conventions, CategoryFramework) {
		if strings.Contains(c.Description, "Callback patterns") {
			t.Error("should not detect callback pattern with only 1 class")
		}
	}
}

func setupScopeFixture(t *testing.T) *sqlite.Adapter {
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
		{Path: "app/models/order.rb", Language: "ruby", Hash: "a1", Symbols: 1, IndexedAt: now},
		{Path: "app/models/user.rb", Language: "ruby", Hash: "a2", Symbols: 1, IndexedAt: now},
		{Path: "app/models/product.rb", Language: "ruby", Hash: "a3", Symbols: 1, IndexedAt: now},
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
		fileKey, name, qualified, kind string
	}
	symDefs := []symDef{
		{"app/models/order.rb", "Order", "Order", "class"},
		{"app/models/order.rb", "active", "Order.active", "method"},
		{"app/models/order.rb", "recent", "Order.recent", "method"},
		{"app/models/user.rb", "User", "User", "class"},
		{"app/models/user.rb", "verified", "User.verified", "method"},
		{"app/models/user.rb", "admins", "User.admins", "method"},
		{"app/models/product.rb", "Product", "Product", "class"},
		{"app/models/product.rb", "published", "Product.published", "method"},
		{"app/models/product.rb", "featured", "Product.featured", "method"},
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
	parents := map[string]string{
		"Order.active":      "Order",
		"Order.recent":      "Order",
		"User.verified":     "User",
		"User.admins":       "User",
		"Product.published": "Product",
		"Product.featured":  "Product",
	}
	for childQ, parentQ := range parents {
		_, err := adapter.DB().ExecContext(ctx, `UPDATE sense_symbols SET parent_id = ? WHERE id = ?`, symIDs[parentQ], symIDs[childQ])
		if err != nil {
			t.Fatal(err)
		}
	}
	// Scope detection uses positive identification: each scope symbol needs a
	// calls edge from its parent class (as emitted by emitScopeEdge).
	scopeEdges := []struct{ source, target, file string }{
		{"Order", "Order.active", "app/models/order.rb"},
		{"Order", "Order.recent", "app/models/order.rb"},
		{"User", "User.verified", "app/models/user.rb"},
		{"User", "User.admins", "app/models/user.rb"},
		{"Product", "Product.published", "app/models/product.rb"},
		{"Product", "Product.featured", "app/models/product.rb"},
	}
	for _, se := range scopeEdges {
		srcID := symIDs[se.source]
		tgtID := symIDs[se.target]
		fid := fileIDs[se.file]
		e := &model.Edge{
			SourceID:   &srcID,
			TargetID:   tgtID,
			Kind:       model.EdgeCalls,
			FileID:     fid,
			Confidence: 1.0,
		}
		if _, err := adapter.WriteEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	return adapter
}

func TestDetectScopes(t *testing.T) {
	adapter := setupScopeFixture(t)
	ctx := context.Background()

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	fw := findByCategory(conventions, CategoryFramework)
	found := false
	for _, c := range fw {
		if strings.Contains(c.Description, "Scope definitions") {
			found = true
			if c.Instances != 3 {
				t.Errorf("expected 3 classes with scopes, got %d", c.Instances)
			}
		}
	}
	if !found {
		t.Error("expected Scope definitions convention")
	}
}

func TestDetectScopesBelowThreshold(t *testing.T) {
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
	f := model.File{Path: "app/models/order.rb", Language: "ruby", Hash: "a1", Symbols: 1, IndexedAt: now}
	fid, err := adapter.WriteFile(ctx, &f)
	if err != nil {
		t.Fatal(err)
	}
	cls := &model.Symbol{FileID: fid, Name: "Order", Qualified: "Order", Kind: "class", LineStart: 1, LineEnd: 10}
	clsID, err := adapter.WriteSymbol(ctx, cls)
	if err != nil {
		t.Fatal(err)
	}
	scope := &model.Symbol{FileID: fid, Name: "active", Qualified: "Order.active", Kind: "method", LineStart: 2, LineEnd: 2}
	scopeID, err := adapter.WriteSymbol(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = adapter.DB().ExecContext(ctx, `UPDATE sense_symbols SET parent_id = ? WHERE id = ?`, clsID, scopeID)
	e := &model.Edge{SourceID: &clsID, TargetID: scopeID, Kind: model.EdgeCalls, FileID: fid, Confidence: 1.0}
	if _, err := adapter.WriteEdge(ctx, e); err != nil {
		t.Fatal(err)
	}

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range findByCategory(conventions, CategoryFramework) {
		if strings.Contains(c.Description, "Scope definitions") {
			t.Error("should not detect scope pattern with only 1 class")
		}
	}
}

func TestDetectCompositionSerializers(t *testing.T) {
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
		{Path: "app/serializers/order_serializer.rb", Language: "ruby", Hash: "a1", Symbols: 1, IndexedAt: now},
		{Path: "app/models/order.rb", Language: "ruby", Hash: "a2", Symbols: 1, IndexedAt: now},
		{Path: "app/models/user.rb", Language: "ruby", Hash: "a3", Symbols: 1, IndexedAt: now},
		{Path: "app/models/product.rb", Language: "ruby", Hash: "a4", Symbols: 1, IndexedAt: now},
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
		fileKey, name, qualified, kind string
	}
	symDefs := []symDef{
		{"app/serializers/order_serializer.rb", "OrderSerializer", "OrderSerializer", "class"},
		{"app/models/order.rb", "Order", "Order", "class"},
		{"app/models/user.rb", "User", "User", "class"},
		{"app/models/product.rb", "Product", "Product", "class"},
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

	edgeDefs := []struct{ source, target, kind, file string }{
		{"Order", "OrderSerializer", "composes", "app/models/order.rb"},
		{"User", "OrderSerializer", "composes", "app/models/user.rb"},
		{"Product", "OrderSerializer", "composes", "app/models/product.rb"},
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

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	comp := findByCategory(conventions, CategoryComposition)
	foundSerializer := false
	foundGeneric := false
	for _, c := range comp {
		if strings.Contains(c.Description, "Serializer composition") {
			foundSerializer = true
			if c.Instances != 3 {
				t.Errorf("expected 3 serializer composition instances, got %d", c.Instances)
			}
		}
		if strings.Contains(c.Description, "mix in OrderSerializer") {
			foundGeneric = true
		}
	}
	if !foundSerializer {
		t.Error("expected Serializer composition convention")
	}
	if foundGeneric {
		t.Error("serializer should not appear in generic composition convention")
	}
}

func TestDetectCallbacksAndScopesNoCollision(t *testing.T) {
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
		{Path: "app/models/order.rb", Language: "ruby", Hash: "a1", Symbols: 1, IndexedAt: now},
		{Path: "app/models/user.rb", Language: "ruby", Hash: "a2", Symbols: 1, IndexedAt: now},
		{Path: "app/models/product.rb", Language: "ruby", Hash: "a3", Symbols: 1, IndexedAt: now},
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
		fileKey, name, qualified, kind string
	}
	symDefs := []symDef{
		// Each class has callbacks AND scopes
		{"app/models/order.rb", "Order", "Order", "class"},
		{"app/models/order.rb", "before_save", "Order.before_save", "method"},
		{"app/models/order.rb", "active", "Order.active", "method"},
		{"app/models/order.rb", "recent", "Order.recent", "method"},
		{"app/models/user.rb", "User", "User", "class"},
		{"app/models/user.rb", "after_create", "User.after_create", "method"},
		{"app/models/user.rb", "verified", "User.verified", "method"},
		{"app/models/user.rb", "admins", "User.admins", "method"},
		{"app/models/product.rb", "Product", "Product", "class"},
		{"app/models/product.rb", "before_destroy", "Product.before_destroy", "method"},
		{"app/models/product.rb", "published", "Product.published", "method"},
		{"app/models/product.rb", "featured", "Product.featured", "method"},
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
	parents := map[string]string{
		"Order.before_save":      "Order",
		"Order.active":           "Order",
		"Order.recent":           "Order",
		"User.after_create":      "User",
		"User.verified":          "User",
		"User.admins":            "User",
		"Product.before_destroy": "Product",
		"Product.published":      "Product",
		"Product.featured":       "Product",
	}
	for childQ, parentQ := range parents {
		_, err := adapter.DB().ExecContext(ctx, `UPDATE sense_symbols SET parent_id = ? WHERE id = ?`, symIDs[parentQ], symIDs[childQ])
		if err != nil {
			t.Fatal(err)
		}
	}
	// Scope edges (positive identification)
	scopeEdges := []struct{ source, target, file string }{
		{"Order", "Order.active", "app/models/order.rb"},
		{"Order", "Order.recent", "app/models/order.rb"},
		{"User", "User.verified", "app/models/user.rb"},
		{"User", "User.admins", "app/models/user.rb"},
		{"Product", "Product.published", "app/models/product.rb"},
		{"Product", "Product.featured", "app/models/product.rb"},
	}
	for _, se := range scopeEdges {
		srcID := symIDs[se.source]
		tgtID := symIDs[se.target]
		fid := fileIDs[se.file]
		e := &model.Edge{SourceID: &srcID, TargetID: tgtID, Kind: model.EdgeCalls, FileID: fid, Confidence: 1.0}
		if _, err := adapter.WriteEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	conventions, _, err := Detect(ctx, adapter.DB(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	fw := findByCategory(conventions, CategoryFramework)
	var callbackConv, scopeConv *Convention
	for i, c := range fw {
		if strings.Contains(c.Description, "Callback patterns") {
			callbackConv = &fw[i]
		}
		if strings.Contains(c.Description, "Scope definitions") {
			scopeConv = &fw[i]
		}
	}
	if callbackConv == nil {
		t.Fatal("expected Callback patterns convention")
	}
	if scopeConv == nil {
		t.Fatal("expected Scope definitions convention")
	}
	if callbackConv.Instances != 3 {
		t.Errorf("callback instances = %d, want 3", callbackConv.Instances)
	}
	if scopeConv.Instances != 3 {
		t.Errorf("scope instances = %d, want 3", scopeConv.Instances)
	}
	// Verify no cross-contamination: callback names must not appear in scope examples
	for _, ex := range scopeConv.Examples {
		if model.RailsCallbackNames[ex.Name] {
			t.Errorf("scope convention contains callback name %q", ex.Name)
		}
	}
}

func TestDetectExternalDependencies(t *testing.T) {
	adapter := setupFixtureIndex(t)
	ctx := context.Background()
	db := adapter.DB()
	now := time.Now()

	// Create an "external" file that is outside the domain
	extFile := model.File{Path: "vendor/lib/tower.rb", Language: "ruby", Hash: "x1", Symbols: 1, IndexedAt: now}
	extFileID, err := adapter.WriteFile(ctx, &extFile)
	if err != nil {
		t.Fatal(err)
	}
	extSymID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: extFileID, Name: "Service", Qualified: "Tower::Service",
		Kind: "class", LineStart: 1, LineEnd: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 3 in-domain symbols reference the external type
	domainFile := model.File{Path: "app/services/a.rb", Language: "ruby", Hash: "x2", Symbols: 3, IndexedAt: now}
	domainFileID, err := adapter.WriteFile(ctx, &domainFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"A", "B", "C"} {
		srcID, err := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: domainFileID, Name: name, Qualified: "App::" + name,
			Kind: "class", LineStart: 1, LineEnd: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adapter.WriteEdge(ctx, &model.Edge{
			SourceID: &srcID, TargetID: extSymID,
			Kind: model.EdgeCalls, FileID: domainFileID, Confidence: 1.0,
		}); err != nil {
			t.Fatal(err)
		}
	}

	fileFilter, err := resolveFileFilter(ctx, db, "app/services")
	if err != nil {
		t.Fatal(err)
	}

	convs := detectExternalDependencies(ctx, db, "app/services", fileFilter)
	if len(convs) == 0 {
		t.Fatal("expected at least one external dependency convention")
	}
	found := false
	for _, c := range convs {
		if strings.Contains(c.Description, "Tower::Service") {
			found = true
			if c.Category != "external" {
				t.Errorf("category = %q, want external", c.Category)
			}
			if c.Instances < 3 {
				t.Errorf("instances = %d, want >= 3", c.Instances)
			}
		}
	}
	if !found {
		t.Error("expected external convention mentioning Tower::Service")
	}
}

func TestDetectExternalDependenciesNoDomain(t *testing.T) {
	convs := detectExternalDependencies(context.Background(), nil, "", nil)
	if len(convs) != 0 {
		t.Errorf("expected no conventions without domain, got %d", len(convs))
	}
}
