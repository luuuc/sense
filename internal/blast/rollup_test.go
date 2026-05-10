package blast_test

import (
	"context"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// setupRollupProject creates a minimal index with parent/child symbols
// so rollup behaviour can be asserted without a full scan.
func setupRollupProject(t *testing.T) (*sqlite.Adapter, []int64, []int64) {
	t.Helper()
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	files := []model.File{
		{Path: "app/services/checkout_service.rb", Language: "ruby", Hash: "a", IndexedAt: time.Now()},
		{Path: "app/controllers/orders_controller.rb", Language: "ruby", Hash: "b", IndexedAt: time.Now()},
		{Path: "app/controllers/users_controller.rb", Language: "ruby", Hash: "c", IndexedAt: time.Now()},
		{Path: "app/jobs/webhook_job.rb", Language: "ruby", Hash: "d", IndexedAt: time.Now()},
		{Path: "app/helpers/application_helper.rb", Language: "ruby", Hash: "e", IndexedAt: time.Now()},
	}
	fids := make([]int64, len(files))
	for i := range files {
		id, werr := adapter.WriteFile(ctx, &files[i])
		if werr != nil {
			t.Fatalf("WriteFile: %v", werr)
		}
		fids[i] = id
	}

	// Write parent symbols first so we have their IDs for children.
	// 0: CheckoutService (class, subject)
	// 1: OrdersController (class)
	// 2: UsersController (class)
	// 3: WebhookJob#process (method, no parent)
	// 4: ApplicationHelper (module)
	parents := []model.Symbol{
		{FileID: fids[0], Name: "CheckoutService", Qualified: "App::Services::CheckoutService", Kind: model.KindClass, LineStart: 1, LineEnd: 100},
		{FileID: fids[1], Name: "OrdersController", Qualified: "OrdersController", Kind: model.KindClass, LineStart: 1, LineEnd: 50},
		{FileID: fids[2], Name: "UsersController", Qualified: "UsersController", Kind: model.KindClass, LineStart: 1, LineEnd: 40},
		{FileID: fids[3], Name: "process", Qualified: "WebhookJob#process", Kind: model.KindMethod, LineStart: 1, LineEnd: 10},
		{FileID: fids[4], Name: "ApplicationHelper", Qualified: "ApplicationHelper", Kind: model.KindModule, LineStart: 1, LineEnd: 30},
	}
	sids := make([]int64, len(parents)+4) // +4 for children
	for i := range parents {
		id, werr := adapter.WriteSymbol(ctx, &parents[i])
		if werr != nil {
			t.Fatalf("WriteSymbol: %v", werr)
		}
		sids[i] = id
	}

	// Write children with parent IDs set.
	children := []model.Symbol{
		{FileID: fids[1], Name: "create", Qualified: "OrdersController#create", Kind: model.KindMethod, LineStart: 10, LineEnd: 20, ParentID: &sids[1]},
		{FileID: fids[1], Name: "update", Qualified: "OrdersController#update", Kind: model.KindMethod, LineStart: 25, LineEnd: 35, ParentID: &sids[1]},
		{FileID: fids[2], Name: "show", Qualified: "UsersController#show", Kind: model.KindMethod, LineStart: 5, LineEnd: 15, ParentID: &sids[2]},
		{FileID: fids[4], Name: "format_price", Qualified: "ApplicationHelper#format_price", Kind: model.KindMethod, LineStart: 5, LineEnd: 10, ParentID: &sids[4]},
	}
	for i := range children {
		id, werr := adapter.WriteSymbol(ctx, &children[i])
		if werr != nil {
			t.Fatalf("WriteSymbol: %v", werr)
		}
		sids[len(parents)+i] = id
	}

	return adapter, fids, sids
}

func TestRollupParentsAddsParentClass(t *testing.T) {
	ctx := context.Background()
	adapter, _, sids := setupRollupProject(t)

	// Direct caller is a method; its parent class should be rolled up.
	r := blast.Result{
		Symbol:        model.Symbol{ID: sids[0], Name: "CheckoutService", Qualified: "App::Services::CheckoutService"},
		DirectCallers: []model.Symbol{{ID: sids[5], Name: "create", Qualified: "OrdersController#create", ParentID: &sids[1]}},
		TotalAffected: 1,
	}

	if err := blast.RollupParents(ctx, adapter.DB(), &r); err != nil {
		t.Fatalf("RollupParents: %v", err)
	}

	if len(r.DirectCallers) != 2 {
		t.Fatalf("expected 2 direct callers, got %d", len(r.DirectCallers))
	}
	found := false
	for _, c := range r.DirectCallers {
		if c.Name == "OrdersController" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected OrdersController in direct callers, got %+v", r.DirectCallers)
	}
	if r.TotalAffected != 2 {
		t.Errorf("expected TotalAffected=2, got %d", r.TotalAffected)
	}
}

func TestRollupParentsDeduplicatesSameParent(t *testing.T) {
	ctx := context.Background()
	adapter, _, sids := setupRollupProject(t)

	// Two methods on the same class → one parent entry.
	r := blast.Result{
		Symbol: model.Symbol{ID: sids[0], Name: "CheckoutService", Qualified: "App::Services::CheckoutService"},
		DirectCallers: []model.Symbol{
			{ID: sids[5], Name: "create", Qualified: "OrdersController#create", ParentID: &sids[1]},
			{ID: sids[6], Name: "update", Qualified: "OrdersController#update", ParentID: &sids[1]},
		},
		TotalAffected: 2,
	}

	if err := blast.RollupParents(ctx, adapter.DB(), &r); err != nil {
		t.Fatalf("RollupParents: %v", err)
	}

	if len(r.DirectCallers) != 3 {
		t.Fatalf("expected 3 direct callers (2 methods + 1 parent), got %d", len(r.DirectCallers))
	}
	parentCount := 0
	for _, c := range r.DirectCallers {
		if c.Name == "OrdersController" {
			parentCount++
		}
	}
	if parentCount != 1 {
		t.Errorf("expected 1 OrdersController entry, got %d", parentCount)
	}
	if r.TotalAffected != 3 {
		t.Errorf("expected TotalAffected=3, got %d", r.TotalAffected)
	}
}

func TestRollupParentsSkipsExistingParent(t *testing.T) {
	ctx := context.Background()
	adapter, _, sids := setupRollupProject(t)

	// Parent class is already a direct caller.
	r := blast.Result{
		Symbol: model.Symbol{ID: sids[0], Name: "CheckoutService", Qualified: "App::Services::CheckoutService"},
		DirectCallers: []model.Symbol{
			{ID: sids[1], Name: "OrdersController", Qualified: "OrdersController"},
			{ID: sids[5], Name: "create", Qualified: "OrdersController#create", ParentID: &sids[1]},
		},
		TotalAffected: 2,
	}

	if err := blast.RollupParents(ctx, adapter.DB(), &r); err != nil {
		t.Fatalf("RollupParents: %v", err)
	}

	if len(r.DirectCallers) != 2 {
		t.Fatalf("expected 2 direct callers, got %d", len(r.DirectCallers))
	}
	if r.TotalAffected != 2 {
		t.Errorf("expected TotalAffected=2, got %d", r.TotalAffected)
	}
}

func TestRollupParentsRollsUpIndirectCallers(t *testing.T) {
	ctx := context.Background()
	adapter, _, sids := setupRollupProject(t)

	// Indirect caller is a method; its parent should roll into DirectCallers.
	r := blast.Result{
		Symbol: model.Symbol{ID: sids[0], Name: "CheckoutService", Qualified: "App::Services::CheckoutService"},
		DirectCallers: []model.Symbol{
			{ID: sids[5], Name: "create", Qualified: "OrdersController#create", ParentID: &sids[1]},
		},
		IndirectCallers: []blast.CallerHop{
			{Symbol: model.Symbol{ID: sids[7], Name: "show", Qualified: "UsersController#show", ParentID: &sids[2]}},
		},
		TotalAffected: 2,
	}

	if err := blast.RollupParents(ctx, adapter.DB(), &r); err != nil {
		t.Fatalf("RollupParents: %v", err)
	}

	if len(r.DirectCallers) != 3 {
		t.Fatalf("expected 3 direct callers (create + OrdersController + UsersController), got %d", len(r.DirectCallers))
	}
	found := false
	for _, c := range r.DirectCallers {
		if c.Name == "UsersController" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected UsersController in direct callers, got %+v", r.DirectCallers)
	}
	if r.TotalAffected != 4 {
		t.Errorf("expected TotalAffected=4 (3 direct + 1 indirect), got %d", r.TotalAffected)
	}
}

func TestRollupParentsExcludesSubject(t *testing.T) {
	ctx := context.Background()
	adapter, _, sids := setupRollupProject(t)

	// Caller is the subject itself (e.g., recursive call through a method).
	r := blast.Result{
		Symbol:        model.Symbol{ID: sids[1], Name: "OrdersController", Qualified: "OrdersController"},
		DirectCallers: []model.Symbol{{ID: sids[5], Name: "create", Qualified: "OrdersController#create", ParentID: &sids[1]}},
		TotalAffected: 1,
	}

	if err := blast.RollupParents(ctx, adapter.DB(), &r); err != nil {
		t.Fatalf("RollupParents: %v", err)
	}

	// Parent (OrdersController) is the subject — should NOT be added.
	if len(r.DirectCallers) != 1 {
		t.Fatalf("expected 1 direct caller, got %d", len(r.DirectCallers))
	}
	if r.DirectCallers[0].Name != "create" {
		t.Errorf("expected only 'create', got %q", r.DirectCallers[0].Name)
	}
}

func TestRollupParentsNoParents(t *testing.T) {
	ctx := context.Background()
	adapter, _, sids := setupRollupProject(t)

	// Standalone method with no parent.
	r := blast.Result{
		Symbol:        model.Symbol{ID: sids[0], Name: "CheckoutService", Qualified: "App::Services::CheckoutService"},
		DirectCallers: []model.Symbol{{ID: sids[3], Name: "process", Qualified: "WebhookJob#process"}},
		TotalAffected: 1,
	}

	if err := blast.RollupParents(ctx, adapter.DB(), &r); err != nil {
		t.Fatalf("RollupParents: %v", err)
	}

	if len(r.DirectCallers) != 1 {
		t.Fatalf("expected 1 direct caller, got %d", len(r.DirectCallers))
	}
	if r.TotalAffected != 1 {
		t.Errorf("expected TotalAffected=1, got %d", r.TotalAffected)
	}
}

func TestRollupParentsEmptyResult(t *testing.T) {
	ctx := context.Background()
	adapter, _, _ := setupRollupProject(t)

	r := blast.Result{
		Symbol:        model.Symbol{ID: 1, Name: "X", Qualified: "X"},
		DirectCallers: nil,
		TotalAffected: 0,
	}

	if err := blast.RollupParents(ctx, adapter.DB(), &r); err != nil {
		t.Fatalf("RollupParents: %v", err)
	}

	if len(r.DirectCallers) != 0 {
		t.Fatalf("expected 0 direct callers, got %d", len(r.DirectCallers))
	}
	if r.TotalAffected != 0 {
		t.Errorf("expected TotalAffected=0, got %d", r.TotalAffected)
	}
}

func TestRollupParentsLoadSymbolsError(t *testing.T) {
	ctx := context.Background()
	adapter, _, sids := setupRollupProject(t)

	// Close the DB to force loadSymbols to fail.
	_ = adapter.DB().Close()

	r := blast.Result{
		Symbol:        model.Symbol{ID: sids[0], Name: "CheckoutService", Qualified: "App::Services::CheckoutService"},
		DirectCallers: []model.Symbol{{ID: sids[5], Name: "create", Qualified: "OrdersController#create", ParentID: &sids[1]}},
		TotalAffected: 1,
	}

	if err := blast.RollupParents(ctx, adapter.DB(), &r); err == nil {
		t.Fatal("expected error from closed DB, got nil")
	}
}

func TestRollupParentsSubjectWithMultipleMethods(t *testing.T) {
	ctx := context.Background()
	adapter, _, sids := setupRollupProject(t)

	// Subject (CheckoutService) has two of its own methods as callers.
	// Both methods have ParentID pointing back to the subject.
	// The subject must NOT be rolled into its own caller list.
	checkoutProcess := int64(9991)
	checkoutValidate := int64(9992)
	r := blast.Result{
		Symbol: model.Symbol{ID: sids[0], Name: "CheckoutService", Qualified: "App::Services::CheckoutService"},
		DirectCallers: []model.Symbol{
			{ID: checkoutProcess, Name: "process", Qualified: "CheckoutService#process", ParentID: &sids[0]},
			{ID: checkoutValidate, Name: "validate", Qualified: "CheckoutService#validate", ParentID: &sids[0]},
		},
		TotalAffected: 2,
	}

	if err := blast.RollupParents(ctx, adapter.DB(), &r); err != nil {
		t.Fatalf("RollupParents: %v", err)
	}

	// Neither method's parent (the subject) should be added.
	if len(r.DirectCallers) != 2 {
		t.Fatalf("expected 2 direct callers, got %d", len(r.DirectCallers))
	}
	for _, c := range r.DirectCallers {
		if c.Name == "CheckoutService" {
			t.Errorf("subject CheckoutService should not appear in its own caller list")
		}
	}
	if r.TotalAffected != 2 {
		t.Errorf("expected TotalAffected=2, got %d", r.TotalAffected)
	}
}
