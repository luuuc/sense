package conventions

import (
	"strings"
	"testing"
)

// TestDetectGoMiddlewareBranches drives detectGoMiddleware through its filtering
// branches directly: a router method whose calls fan out to handler factories,
// alongside edges and targets that must be rejected, non-calls edges, calls
// from a non-router source, a non-function target, Test/Benchmark-named helpers,
// and a duplicate factory name.
func TestDetectGoMiddlewareBranches(t *testing.T) {
	use := symbolRow{id: 1, fileID: 10, name: "Use", kind: "method"}
	logger := symbolRow{id: 2, fileID: 11, name: "Logger", kind: "function"}
	recovery := symbolRow{id: 3, fileID: 12, name: "Recovery", kind: "function"}
	cors := symbolRow{id: 4, fileID: 13, name: "CORS", kind: "function"}
	testHelper := symbolRow{id: 5, fileID: 14, name: "TestServer", kind: "function"}
	benchHelper := symbolRow{id: 6, fileID: 15, name: "BenchmarkRoute", kind: "function"}
	notAFunc := symbolRow{id: 7, fileID: 16, name: "Engine", kind: "struct"}
	other := symbolRow{id: 8, fileID: 17, name: "helper", kind: "function"}
	dupLogger := symbolRow{id: 9, fileID: 18, name: "Logger", kind: "function"}

	symbols := []symbolRow{use, logger, recovery, cors, testHelper, benchHelper, notAFunc, other, dupLogger}
	symbolByID := indexSymbols(symbols)
	filePathByID := map[int64]string{
		10: "router.go", 11: "logger.go", 12: "recovery.go", 13: "cors.go",
		14: "server_test.go", 15: "bench.go", 16: "engine.go", 17: "helper.go", 18: "logger2.go",
	}

	edges := []edgeRow{
		// Router method calls three genuine factories.
		{sourceID: 1, targetID: 2, kind: "calls"},
		{sourceID: 1, targetID: 3, kind: "calls"},
		{sourceID: 1, targetID: 4, kind: "calls"},
		// Calls a Test- and Benchmark-named function, both skipped.
		{sourceID: 1, targetID: 5, kind: "calls"},
		{sourceID: 1, targetID: 6, kind: "calls"},
		// Calls a struct, not a function, skipped.
		{sourceID: 1, targetID: 7, kind: "calls"},
		// Duplicate factory name, deduped.
		{sourceID: 1, targetID: 9, kind: "calls"},
		// Non-calls edge, skipped.
		{sourceID: 1, targetID: 2, kind: "inherits"},
		// Calls edge from a non-router source, skipped.
		{sourceID: 8, targetID: 2, kind: "calls"},
	}

	out := detectGoMiddleware(symbols, edges, symbolByID, filePathByID)
	if len(out) != 1 {
		t.Fatalf("expected 1 middleware convention, got %d: %+v", len(out), out)
	}
	c := out[0]
	if c.Category != CategoryFramework {
		t.Errorf("category = %q, want %q", c.Category, CategoryFramework)
	}
	// Logger, Recovery, CORS, the duplicate Logger is collapsed, Test/Benchmark
	// and the struct target are excluded.
	if c.Instances != 3 {
		t.Errorf("instances = %d, want 3 (deduped, filtered)", c.Instances)
	}
	if !strings.Contains(c.Description, "Middleware factories") {
		t.Errorf("description = %q, missing middleware phrasing", c.Description)
	}
	for _, ex := range c.Examples {
		if ex.Name == "TestServer" || ex.Name == "BenchmarkRoute" || ex.Name == "Engine" {
			t.Errorf("example %q should have been filtered out", ex.Name)
		}
	}
}

// TestDetectGoMiddlewareBelowThreshold confirms fewer than minInstances factories
// yields no convention.
func TestDetectGoMiddlewareBelowThreshold(t *testing.T) {
	use := symbolRow{id: 1, fileID: 10, name: "Use", kind: "method"}
	logger := symbolRow{id: 2, fileID: 11, name: "Logger", kind: "function"}
	recovery := symbolRow{id: 3, fileID: 12, name: "Recovery", kind: "function"}
	symbols := []symbolRow{use, logger, recovery}
	symbolByID := indexSymbols(symbols)
	filePathByID := map[int64]string{10: "router.go", 11: "logger.go", 12: "recovery.go"}
	edges := []edgeRow{
		{sourceID: 1, targetID: 2, kind: "calls"},
		{sourceID: 1, targetID: 3, kind: "calls"},
	}
	if out := detectGoMiddleware(symbols, edges, symbolByID, filePathByID); out != nil {
		t.Errorf("expected nil for 2 factories, got %+v", out)
	}
}

// TestDetectGoMiddlewareNoRouter confirms that without any router methods there
// is nothing to detect.
func TestDetectGoMiddlewareNoRouter(t *testing.T) {
	logger := symbolRow{id: 1, fileID: 10, name: "Logger", kind: "function"}
	symbols := []symbolRow{logger}
	symbolByID := indexSymbols(symbols)
	filePathByID := map[int64]string{10: "logger.go"}
	if out := detectGoMiddleware(symbols, nil, symbolByID, filePathByID); out != nil {
		t.Errorf("expected nil with no router methods, got %+v", out)
	}
}
