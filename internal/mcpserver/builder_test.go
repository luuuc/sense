package mcpserver

import (
	"context"
	"testing"
)

func TestBuildStatusResponse(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	resp, err := buildStatusResponse(ctx, ts.handlers.db, ts.handlers.dir, nil)
	if err != nil {
		t.Fatalf("buildStatusResponse: %v", err)
	}

	// Index counts should match seeded data
	if resp.Index.Files != 7 {
		t.Errorf("index.files = %d, want 7", resp.Index.Files)
	}
	if resp.Index.Symbols != 12 {
		t.Errorf("index.symbols = %d, want 12", resp.Index.Symbols)
	}
	if resp.Index.Edges != 8 {
		t.Errorf("index.edges = %d, want 8", resp.Index.Edges)
	}

	// Index path should be relative
	if resp.Index.Path != ".sense/index.db" {
		t.Errorf("index.path = %q, want .sense/index.db", resp.Index.Path)
	}

	// Size should be non-zero (real file on disk)
	if resp.Index.SizeBytes == 0 {
		t.Error("index.size_bytes = 0, want non-zero")
	}
}

func TestBuildStatusResponseLanguageBreakdown(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	resp, err := buildStatusResponse(ctx, ts.handlers.db, ts.handlers.dir, nil)
	if err != nil {
		t.Fatalf("buildStatusResponse: %v", err)
	}

	if resp.Languages == nil {
		t.Fatal("languages nil")
	}

	goLang, ok := resp.Languages["go"]
	if !ok {
		t.Fatal("missing 'go' in language breakdown")
	}
	if goLang.Files != 7 {
		t.Errorf("go.files = %d, want 7", goLang.Files)
	}
	if goLang.Symbols != 12 {
		t.Errorf("go.symbols = %d, want 12", goLang.Symbols)
	}
	if goLang.Tier != "full" {
		t.Errorf("go.tier = %q, want full", goLang.Tier)
	}
}

func TestBuildStatusResponseFreshness(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	resp, err := buildStatusResponse(ctx, ts.handlers.db, ts.handlers.dir, nil)
	if err != nil {
		t.Fatalf("buildStatusResponse: %v", err)
	}

	if resp.Freshness.LastScan == nil {
		t.Error("freshness.last_scan nil")
	}
}

func TestBuildStatusResponseStructure(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	resp, err := buildStatusResponse(ctx, ts.handlers.db, ts.handlers.dir, nil)
	if err != nil {
		t.Fatalf("buildStatusResponse: %v", err)
	}

	if resp.Structure == nil {
		t.Fatal("structure nil")
	}
	if len(resp.Structure.TopNamespaces) == 0 {
		t.Error("expected top namespaces")
	}
	if resp.Structure.Fingerprint == "" {
		t.Error("expected non-empty fingerprint")
	}
}

func TestBuildStatusResponseVersion(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	resp, err := buildStatusResponse(ctx, ts.handlers.db, ts.handlers.dir, nil)
	if err != nil {
		t.Fatalf("buildStatusResponse: %v", err)
	}

	if resp.Version == nil {
		t.Fatal("version nil")
	}
	if resp.Version.Binary == "" {
		t.Error("version.binary empty")
	}
	if resp.Version.EmbeddingModel == "" {
		t.Error("version.embedding_model empty")
	}
}

func TestQueryLanguageBreakdown(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	langs, err := queryLanguageBreakdown(ctx, ts.handlers.db)
	if err != nil {
		t.Fatalf("queryLanguageBreakdown: %v", err)
	}

	if len(langs) == 0 {
		t.Fatal("expected at least one language")
	}

	goLang, ok := langs["go"]
	if !ok {
		t.Fatal("missing 'go' entry")
	}
	if goLang.Files == 0 {
		t.Error("go.files = 0")
	}
	if goLang.Symbols == 0 {
		t.Error("go.symbols = 0")
	}
	if goLang.Tier == "" {
		t.Error("go.tier empty")
	}
}
