package dead

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func TestIsRailsControllerClass(t *testing.T) {
	yes := Symbol{Language: "ruby", Kind: "class", Name: "OrdersController"}
	if !isRailsControllerClass(yes) {
		t.Error("a *Controller class should be a router entry point")
	}
	for _, s := range []Symbol{
		{Language: "ruby", Kind: "class", Name: "OrderService"}, // not a controller
		{Language: "ruby", Kind: "module", Name: "OrdersController"}, // module, not class
		{Language: "go", Kind: "class", Name: "OrdersController"},  // not ruby
	} {
		if isRailsControllerClass(s) {
			t.Errorf("isRailsControllerClass(%+v) = true, want false", s)
		}
	}
}

func TestIsRailsControllerAction(t *testing.T) {
	concern := map[int64]struct{}{42: {}}
	pidConcern := int64(42)
	pidOther := int64(7)

	// Direct action on a controller.
	if !isRailsControllerAction(Symbol{Language: "ruby", Kind: "method", Qualified: "Account::OrdersController#create"}, nil) {
		t.Error("public method on a *Controller should be a routed action")
	}
	// Action via a concern mixed into a controller.
	if !isRailsControllerAction(Symbol{Language: "ruby", Kind: "method", Qualified: "Admin::Importable#index", ParentID: &pidConcern}, concern) {
		t.Error("method on a controller-concern should be a routed action")
	}
	for _, s := range []Symbol{
		{Language: "ruby", Kind: "method", Qualified: "OrderService#call"},                       // not a controller
		{Language: "ruby", Kind: "method", Qualified: "X::OrdersController#secret", Visibility: "private"}, // private
		{Language: "ruby", Kind: "class", Qualified: "X::OrdersController"},                       // not a method
		{Language: "go", Kind: "method", Qualified: "X::OrdersController#create"},                 // not ruby
		{Language: "ruby", Kind: "method", Qualified: "Helper#thing", ParentID: &pidOther},        // module not a controller concern
	} {
		if isRailsControllerAction(s, concern) {
			t.Errorf("isRailsControllerAction(%+v) = true, want false", s)
		}
	}
}

func TestIsStimulusController(t *testing.T) {
	for _, f := range []string{"app/javascript/controllers/modal_controller.js", "app/frontend/x_controller.ts"} {
		if !isStimulusController(Symbol{Language: "javascript", File: f}) && !isStimulusController(Symbol{Language: "typescript", File: f}) {
			t.Errorf("%s should be a Stimulus controller", f)
		}
	}
	for _, s := range []Symbol{
		{Language: "javascript", File: "app/javascript/util.js"}, // not a *_controller file
		{Language: "ruby", File: "app/controllers/foo_controller.rb"}, // ruby, not js
	} {
		if isStimulusController(s) {
			t.Errorf("isStimulusController(%+v) = true, want false", s)
		}
	}
}

func TestFrameworkNames(t *testing.T) {
	got := frameworkNames(map[string]struct{}{"Rails": {}, "Sidekiq": {}})
	if len(got) != 2 || got[0] != "Rails" || got[1] != "Sidekiq" {
		t.Errorf("frameworkNames = %v, want [Rails Sidekiq] (sorted)", got)
	}
	if len(frameworkNames(nil)) != 0 {
		t.Error("frameworkNames(nil) should be empty")
	}
}

func TestAnnotateConfidenceIncludedModuleDowngrade(t *testing.T) {
	pid := int64(99)
	out := annotateConfidence(
		[]Symbol{{Kind: "method", Name: "helper", ParentID: &pid}},
		confidenceInputs{includedModuleIDs: map[int64]struct{}{99: {}}},
	)
	if out[0].Confidence != ConfidencePossibly {
		t.Errorf("method on an included module should be possibly_dead, got %q", out[0].Confidence)
	}
}

func TestIsTestSymbolAllForms(t *testing.T) {
	for _, n := range []string{"TestX", "test_x", "BenchmarkX", "it", "describe", "specify"} {
		if !isTestSymbol(Symbol{Name: n}) {
			t.Errorf("isTestSymbol(%q) = false, want true", n)
		}
	}
	if isTestSymbol(Symbol{Name: "Process"}) {
		t.Error("isTestSymbol(\"Process\") = true, want false")
	}
}

// TestQueryHelpersErrorOnClosedDB exercises the SQL error paths: with a
// closed handle every query must surface the error rather than panic or
// return a partial result.
func TestQueryHelpersErrorOnClosedDB(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	db := a.DB()
	_ = a.Close() // close so every subsequent query fails

	checks := []func() error{
		func() error { _, e := queryTestsTargets(ctx, db); return e },
		func() error { _, e := queryInterfaceIDs(ctx, db); return e },
		func() error { _, e := queryInterfaceMethodNames(ctx, db); return e },
		func() error { _, e := queryInterfaceImplementors(ctx, db); return e },
		func() error { _, e := queryControllerConcernModuleIDs(ctx, db); return e },
		func() error { _, e := queryIncludedModuleIDs(ctx, db); return e },
		func() error { _, e := countSymbols(ctx, db, Options{}); return e },
		func() error { _, e := FindDead(ctx, db, Options{}); return e },
	}
	for i, fn := range checks {
		if fn() == nil {
			t.Errorf("check %d: expected error on closed DB, got nil", i)
		}
	}
}

// TestFindDeadControllerActionGatedOnRails proves the controller-action
// exclusion is gated on the Rails framework flag: a *Controller method in
// a project with no Rails detected is still analyzed as a normal symbol,
// not blanket-excluded by its class name.
func TestFindDeadControllerActionGatedOnRails(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	fid, err := a.WriteFile(ctx, &model.File{Path: "lib/foo_controller.rb", Language: "ruby", Hash: "h", Symbols: 1, IndexedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	cid, err := a.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "FooController", Qualified: "FooController", Kind: "class", LineStart: 1, LineEnd: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "orphan", Qualified: "FooController#orphan", Kind: "method", LineStart: 2, LineEnd: 2, ParentID: &cid}); err != nil {
		t.Fatal(err)
	}
	// No sense_meta 'frameworks' row → not a Rails project.

	result, err := FindDead(ctx, a.DB(), Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}
	var found bool
	for _, s := range result.Dead {
		if s.Qualified == "FooController#orphan" {
			found = true
		}
	}
	if !found {
		t.Error("without Rails, a *Controller method must not be auto-excluded as a routed action")
	}
}

// TestFindDeadRailsEntryPoints is the end-to-end check: routed controllers,
// their concern actions, and Stimulus callbacks are not flagged, while a
// genuinely unreferenced service method still is. It also exercises the
// includes-edge queries.
func TestFindDeadRailsEntryPoints(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	mkFile := func(path, lang string) int64 {
		id, err := a.WriteFile(ctx, &model.File{Path: path, Language: lang, Hash: path, Symbols: 1, IndexedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	mkSym := func(fid int64, name, qual, kind string, parent *int64) int64 {
		id, err := a.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: name, Qualified: qual, Kind: model.SymbolKind(kind), LineStart: 1, LineEnd: 3, ParentID: parent})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	ctrlFile := mkFile("app/controllers/orders_controller.rb", "ruby")
	concernFile := mkFile("app/controllers/concerns/importable.rb", "ruby")
	jsFile := mkFile("app/javascript/controllers/modal_controller.js", "javascript")
	svcFile := mkFile("app/services/orphan_service.rb", "ruby")

	ctrlID := mkSym(ctrlFile, "OrdersController", "OrdersController", "class", nil)
	mkSym(ctrlFile, "create", "OrdersController#create", "method", &ctrlID)
	concernID := mkSym(concernFile, "Importable", "Importable", "module", nil)
	mkSym(concernFile, "index", "Importable#index", "method", &concernID)
	mkSym(jsFile, "connect", "ModalController.connect", "method", nil)
	svcID := mkSym(svcFile, "OrphanService", "OrphanService", "class", nil)
	mkSym(svcFile, "perform", "OrphanService#perform", "method", &svcID)

	// OrdersController includes Importable.
	if _, err := a.WriteEdge(ctx, &model.Edge{SourceID: &ctrlID, TargetID: concernID, Kind: model.EdgeIncludes, FileID: ctrlFile, Confidence: 1.0}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB().ExecContext(ctx, `INSERT INTO sense_meta (key, value) VALUES ('frameworks', '["Rails"]')`); err != nil {
		t.Fatal(err)
	}

	result, err := FindDead(ctx, a.DB(), Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	dead := map[string]bool{}
	for _, s := range result.Dead {
		dead[s.Qualified] = true
	}
	for _, live := range []string{"OrdersController", "OrdersController#create", "Importable#index", "ModalController.connect"} {
		if dead[live] {
			t.Errorf("%s was flagged dead but is a framework entry point", live)
		}
	}
	if !dead["OrphanService#perform"] {
		t.Error("genuinely unreferenced OrphanService#perform should still be flagged dead")
	}
}
