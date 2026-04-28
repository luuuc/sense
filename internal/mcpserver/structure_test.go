package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func setupStructureFixture(t *testing.T) *sqlite.Adapter {
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
		// app/models — 3 files, will have 3 symbols
		{Path: "app/models/user.rb", Language: "ruby", Hash: "a1", Symbols: 1, IndexedAt: now},
		{Path: "app/models/order.rb", Language: "ruby", Hash: "a2", Symbols: 1, IndexedAt: now},
		{Path: "app/models/product.rb", Language: "ruby", Hash: "a3", Symbols: 1, IndexedAt: now},
		// app/controllers — 2 files, will have 2 symbols
		{Path: "app/controllers/orders_controller.rb", Language: "ruby", Hash: "b1", Symbols: 1, IndexedAt: now},
		{Path: "app/controllers/users_controller.rb", Language: "ruby", Hash: "b2", Symbols: 1, IndexedAt: now},
		// cmd — 1 file with main function
		{Path: "cmd/main.go", Language: "go", Hash: "c1", Symbols: 1, IndexedAt: now},
		// config — entry point file
		{Path: "config/routes.rb", Language: "ruby", Hash: "d1", Symbols: 1, IndexedAt: now},
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
		{"app/models/user.rb", "User", "App::Models::User", "class"},
		{"app/models/order.rb", "Order", "App::Models::Order", "class"},
		{"app/models/product.rb", "Product", "App::Models::Product", "class"},
		{"app/controllers/orders_controller.rb", "OrdersController", "App::Controllers::OrdersController", "class"},
		{"app/controllers/users_controller.rb", "UsersController", "App::Controllers::UsersController", "class"},
		// ApplicationRecord is the hub — all models inherit it
		{"app/models/user.rb", "ApplicationRecord", "App::ApplicationRecord", "class"},
		// main function — entry point
		{"cmd/main.go", "main", "main.main", "function"},
		// routes symbol
		{"config/routes.rb", "Routes", "App::Routes", "module"},
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
		// 3 models inherit ApplicationRecord → makes it the top hub
		{"App::Models::User", "App::ApplicationRecord", "inherits", "app/models/user.rb"},
		{"App::Models::Order", "App::ApplicationRecord", "inherits", "app/models/order.rb"},
		{"App::Models::Product", "App::ApplicationRecord", "inherits", "app/models/product.rb"},
		// controllers call User → 2 incoming edges
		{"App::Controllers::OrdersController", "App::Models::User", "calls", "app/controllers/orders_controller.rb"},
		{"App::Controllers::UsersController", "App::Models::User", "calls", "app/controllers/users_controller.rb"},
		// one controller calls Order → 1 incoming edge
		{"App::Controllers::OrdersController", "App::Models::Order", "calls", "app/controllers/orders_controller.rb"},
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

func TestTopNamespaces(t *testing.T) {
	adapter := setupStructureFixture(t)
	ctx := context.Background()

	ns, err := queryTopNamespaces(ctx, adapter.DB())
	if err != nil {
		t.Fatal(err)
	}

	if len(ns) == 0 {
		t.Fatal("expected at least one namespace")
	}

	// app/models has 4 symbols (User, Order, Product, ApplicationRecord)
	// app/controllers has 2 symbols
	// cmd has 1 symbol (main)
	// config has 1 symbol (Routes)
	if ns[0].Name != "app/models" {
		t.Errorf("expected top namespace 'app/models', got %q", ns[0].Name)
	}
	if ns[0].Kind != "directory" {
		t.Errorf("expected kind 'directory', got %q", ns[0].Kind)
	}
	if ns[0].Symbols != 4 {
		t.Errorf("expected 4 symbols in app/models, got %d", ns[0].Symbols)
	}

	if ns[1].Name != "app/controllers" {
		t.Errorf("expected second namespace 'app/controllers', got %q", ns[1].Name)
	}
	if ns[1].Symbols != 2 {
		t.Errorf("expected 2 symbols in app/controllers, got %d", ns[1].Symbols)
	}

	// All namespaces should be sorted descending by count
	for i := 1; i < len(ns); i++ {
		if ns[i].Symbols > ns[i-1].Symbols {
			t.Errorf("namespaces not sorted: %s (%d) > %s (%d)", ns[i].Name, ns[i].Symbols, ns[i-1].Name, ns[i-1].Symbols)
		}
	}
}

func TestHubSymbols(t *testing.T) {
	adapter := setupStructureFixture(t)
	ctx := context.Background()

	hubs, err := queryHubSymbols(ctx, adapter.DB())
	if err != nil {
		t.Fatal(err)
	}

	if len(hubs) == 0 {
		t.Fatal("expected at least one hub symbol")
	}

	// ApplicationRecord has 3 incoming edges (highest)
	if hubs[0].Name != "ApplicationRecord" {
		t.Errorf("expected top hub 'ApplicationRecord', got %q", hubs[0].Name)
	}
	if hubs[0].Callers != 3 {
		t.Errorf("expected 3 callers for ApplicationRecord, got %d", hubs[0].Callers)
	}
	if hubs[0].Kind != "class" {
		t.Errorf("expected kind 'class', got %q", hubs[0].Kind)
	}

	// User has 2 incoming edges (second)
	if hubs[1].Name != "User" {
		t.Errorf("expected second hub 'User', got %q", hubs[1].Name)
	}
	if hubs[1].Callers != 2 {
		t.Errorf("expected 2 callers for User, got %d", hubs[1].Callers)
	}

	// Should not exceed 5
	if len(hubs) > 5 {
		t.Errorf("expected at most 5 hubs, got %d", len(hubs))
	}
}

func TestEntryPoints(t *testing.T) {
	adapter := setupStructureFixture(t)
	ctx := context.Background()

	entries, err := queryEntryPoints(ctx, adapter.DB())
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) == 0 {
		t.Fatal("expected at least one entry point")
	}

	// Should find main function
	foundMain := false
	foundRoutes := false
	for _, ep := range entries {
		if ep.Name == "main" && ep.File == "cmd/main.go" && ep.Kind == "function" {
			foundMain = true
		}
		if ep.Name == "routes.rb" && ep.File == "config/routes.rb" && ep.Kind == "file" {
			foundRoutes = true
		}
	}
	if !foundMain {
		t.Errorf("expected main entry point, got %v", entries)
	}
	if !foundRoutes {
		t.Errorf("expected config/routes.rb entry point, got %v", entries)
	}
}

func TestFingerprint(t *testing.T) {
	resp := mcpio.StatusResponse{
		Index: mcpio.StatusIndex{
			Files:   7,
			Symbols: 8,
		},
	}
	langs := map[string]mcpio.StatusLanguage{
		"ruby": {Files: 6, Symbols: 7, Tier: "full"},
		"go":   {Files: 1, Symbols: 1, Tier: "full"},
	}
	ns := []mcpio.StatusNamespace{
		{Name: "app/models", Symbols: 4},
		{Name: "app/controllers", Symbols: 2},
		{Name: "cmd", Symbols: 1},
	}
	hubs := []mcpio.StatusHub{
		{Name: "ApplicationRecord", Callers: 3},
		{Name: "User", Callers: 2},
	}

	fp := buildFingerprint(resp, langs, ns, hubs)

	if !strings.Contains(fp, "Ruby project.") {
		t.Errorf("fingerprint should start with primary language, got %q", fp)
	}
	if !strings.Contains(fp, "7 files, 8 symbols.") {
		t.Errorf("fingerprint should contain file/symbol counts, got %q", fp)
	}
	if !strings.Contains(fp, "app/models (4)") {
		t.Errorf("fingerprint should contain top namespace, got %q", fp)
	}
	if !strings.Contains(fp, "ApplicationRecord") {
		t.Errorf("fingerprint should contain top hub, got %q", fp)
	}
}

func TestFingerprintTiedLanguages(t *testing.T) {
	resp := mcpio.StatusResponse{
		Index: mcpio.StatusIndex{Files: 2, Symbols: 10},
	}
	langs := map[string]mcpio.StatusLanguage{
		"ruby":   {Symbols: 5},
		"python": {Symbols: 5},
	}

	fp1 := buildFingerprint(resp, langs, nil, nil)
	fp2 := buildFingerprint(resp, langs, nil, nil)
	if fp1 != fp2 {
		t.Errorf("fingerprint should be deterministic on tied languages: %q vs %q", fp1, fp2)
	}
	if !strings.Contains(fp1, "Python project.") {
		t.Errorf("tied languages should pick alphabetically first, got %q", fp1)
	}
}

func TestNamespacePrefixFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"app/models/user.rb", "app/models"},
		{"app/controllers/orders_controller.rb", "app/controllers"},
		{"cmd/main.go", "cmd"},
		{"main.go", "."},
		{"src/index.ts", "src"},
		{"internal/mcpserver/server.go", "internal/mcpserver"},
	}
	for _, tt := range tests {
		got := namespacePrefixFromPath(tt.path)
		if got != tt.want {
			t.Errorf("namespacePrefixFromPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestBuildStructureIntegration(t *testing.T) {
	adapter := setupStructureFixture(t)
	ctx := context.Background()

	langs := map[string]mcpio.StatusLanguage{
		"ruby": {Files: 6, Symbols: 7, Tier: "full"},
		"go":   {Files: 1, Symbols: 1, Tier: "full"},
	}
	resp := mcpio.StatusResponse{
		Index: mcpio.StatusIndex{Files: 7, Symbols: 8},
	}

	structure, err := buildStructure(ctx, adapter.DB(), resp, langs)
	if err != nil {
		t.Fatal(err)
	}

	if structure == nil {
		t.Fatal("expected non-nil structure")
	}
	if len(structure.TopNamespaces) == 0 {
		t.Error("expected top namespaces")
	}
	if len(structure.HubSymbols) == 0 {
		t.Error("expected hub symbols")
	}
	if len(structure.EntryPoints) == 0 {
		t.Error("expected entry points")
	}
	if structure.Fingerprint == "" {
		t.Error("expected non-empty fingerprint")
	}
}
