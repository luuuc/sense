package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func TestRenderUnreferencedHumanEmpty(t *testing.T) {
	var buf bytes.Buffer
	RenderUnreferencedHuman(&buf, mcpio.UnreferencedResponse{})
	if !strings.Contains(buf.String(), "No unreferenced symbols found.") {
		t.Errorf("expected 'No unreferenced symbols found.', got:\n%s", buf.String())
	}
}

func TestRenderUnreferencedHumanWithSymbols(t *testing.T) {
	resp := mcpio.UnreferencedResponse{
		Unreferenced: mcpio.UnreferencedSymbols{
			Dead: []mcpio.DeadEntry{
				{Qualified: "App::OldService#run", Kind: "method", File: "app/services/old_service.rb", Line: 10, Verify: "grep run"},
			},
			PossiblyDead: []mcpio.PossiblyDeadGroup{
				{
					Reason:  mcpio.ReasonInfo{Code: "ruby_public_method", Hint: "public method; a hidden caller may exist"},
					Verify:  "grep each name as a call",
					Symbols: []mcpio.PossiblyDeadSymbol{{Qualified: "App::Util#helper", Kind: "method", File: "app/util.rb", Line: 5}},
				},
			},
		},
		TotalSymbols:      200,
		DeadCount:         1,
		PossiblyDeadCount: 1,
	}
	var buf bytes.Buffer
	RenderUnreferencedHuman(&buf, resp)
	out := buf.String()

	// Dead section: header + the earned-dead symbol + its verify recipe.
	if !strings.Contains(out, "Dead (1)") {
		t.Errorf("missing dead header, got:\n%s", out)
	}
	if !strings.Contains(out, "App::OldService#run") || !strings.Contains(out, "verify: grep run") {
		t.Errorf("missing dead entry/verify, got:\n%s", out)
	}
	// Possibly-dead section: reason code, hint, the grouped symbol.
	if !strings.Contains(out, "Possibly dead (1)") {
		t.Errorf("missing possibly-dead header, got:\n%s", out)
	}
	if !strings.Contains(out, "[ruby_public_method]") || !strings.Contains(out, "App::Util#helper") {
		t.Errorf("missing possibly-dead group, got:\n%s", out)
	}
	// Footer counts.
	if !strings.Contains(out, "1 dead, 1 possibly dead, out of 200 total symbols") {
		t.Errorf("missing footer, got:\n%s", out)
	}
}

func TestParseDeadArgsDefaults(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseDeadArgs(nil, &stderr)
	if err != nil {
		t.Fatalf("parseDeadArgs: %v", err)
	}
	if opts.Language != "" {
		t.Errorf("Language = %q, want empty", opts.Language)
	}
	if opts.Domain != "" {
		t.Errorf("Domain = %q, want empty", opts.Domain)
	}
	if opts.Limit != 100 {
		t.Errorf("Limit = %d, want 100", opts.Limit)
	}
	if opts.JSON {
		t.Error("JSON should be false by default")
	}
}

func TestParseDeadArgsWithFlags(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseDeadArgs([]string{
		"--language", "go",
		"--domain", "services",
		"--limit", "50",
		"--json",
	}, &stderr)
	if err != nil {
		t.Fatalf("parseDeadArgs: %v", err)
	}
	if opts.Language != "go" {
		t.Errorf("Language = %q, want go", opts.Language)
	}
	if opts.Domain != "services" {
		t.Errorf("Domain = %q, want services", opts.Domain)
	}
	if opts.Limit != 50 {
		t.Errorf("Limit = %d, want 50", opts.Limit)
	}
	if !opts.JSON {
		t.Error("JSON should be true")
	}
}

func TestParseDeadArgsHelp(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseDeadArgs([]string{"--help"}, &stderr)
	if err == nil {
		t.Fatal("expected error from --help")
	}
}

func TestSortedLangs(t *testing.T) {
	m := map[string]mcpio.StatusLanguage{
		"ruby":       {Files: 10, Symbols: 50},
		"go":         {Files: 20, Symbols: 100},
		"python":     {Files: 5, Symbols: 30},
		"javascript": {Files: 3, Symbols: 15},
	}
	got := sortedLangs(m)
	want := []string{"go", "javascript", "python", "ruby"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, g := range got {
		if g != want[i] {
			t.Errorf("[%d] = %q, want %q", i, g, want[i])
		}
	}
}

func TestSortedLangsEmpty(t *testing.T) {
	got := sortedLangs(map[string]mcpio.StatusLanguage{})
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestRenderStatusHuman(t *testing.T) {
	lastScan := "2025-01-15T10:30:00Z"
	ageSecs := int64(3600)
	staleFiles := 2
	watching := true

	resp := mcpio.StatusResponse{
		Index: mcpio.StatusIndex{
			Path:       ".sense/index.db",
			SizeBytes:  1048576,
			Files:      42,
			Symbols:    500,
			Edges:      1200,
			Embeddings: 250,
			Coverage:   0.5,
		},
		Languages: map[string]mcpio.StatusLanguage{
			"go":   {Files: 30, Symbols: 400, Tier: "full"},
			"ruby": {Files: 12, Symbols: 100, Tier: "standard"},
		},
		Profile: &mcpio.StatusProfile{
			Tier:            "medium",
			Symbols:         500,
			PrimaryLanguage: "go",
			DynamicLanguage: false,
		},
		Freshness: mcpio.Freshness{
			LastScan:        &lastScan,
			IndexAgeSeconds: &ageSecs,
			StaleFilesSeen:  &staleFiles,
			Watching:        &watching,
		},
		Version: &mcpio.StatusVersion{
			Binary:                "v0.5.0",
			Schema:                10,
			SchemaCurrent:         true,
			EmbeddingModel:        "text-embedding-3-small",
			EmbeddingModelCurrent: true,
		},
		Lifetime: &mcpio.StatusLifetime{
			Queries:              42,
			EstimatedTokensSaved: 150000,
		},
	}

	var stdout, stderr bytes.Buffer
	cio := IO{Stdout: &stdout, Stderr: &stderr, Dir: t.TempDir()}

	renderStatusHuman(cio, resp, healthInfo{verdict: "healthy"})
	out := stdout.String()

	for _, want := range []string{
		"Index: .sense/index.db (1.0 MB)",
		"Files:      42",
		"Coverage: 50.0%",
		"Symbols:  500",
		"Edges: 1200",
		"Languages:",
		"go",
		"ruby",
		"Profile: medium (primary: go)",
		"Freshness:",
		"1 hours ago",
		"Stale files: 2",
		"Watching:    yes",
		"Schema: v10 (current)",
		"Embedding model: text-embedding-3-small (current)",
		"Lifetime: 42 queries, ~150K tokens saved",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRenderStatusHumanMinimal(t *testing.T) {
	resp := mcpio.StatusResponse{
		Index: mcpio.StatusIndex{
			Path:    ".sense/index.db",
			Symbols: 0,
		},
		Languages: map[string]mcpio.StatusLanguage{},
		Freshness: mcpio.Freshness{},
	}

	var stdout, stderr bytes.Buffer
	cio := IO{Stdout: &stdout, Stderr: &stderr, Dir: t.TempDir()}

	renderStatusHuman(cio, resp, healthInfo{verdict: "healthy"})
	out := stdout.String()

	if !strings.Contains(out, "Last scan:   unknown") {
		t.Errorf("expected 'unknown' for nil LastScan, got:\n%s", out)
	}
	if strings.Contains(out, "Watching:") {
		t.Errorf("nil Watching should be suppressed, got:\n%s", out)
	}
	// Should NOT contain Languages section since map is empty
	if strings.Contains(out, "Languages:") {
		t.Errorf("empty languages map should suppress Languages section, got:\n%s", out)
	}
}

func TestRenderStatusHumanDynamicLanguage(t *testing.T) {
	resp := mcpio.StatusResponse{
		Index: mcpio.StatusIndex{Path: ".sense/index.db"},
		Profile: &mcpio.StatusProfile{
			Tier:            "large",
			PrimaryLanguage: "ruby",
			DynamicLanguage: true,
		},
		Freshness: mcpio.Freshness{},
	}

	var stdout, stderr bytes.Buffer
	cio := IO{Stdout: &stdout, Stderr: &stderr, Dir: t.TempDir()}

	renderStatusHuman(cio, resp, healthInfo{verdict: "healthy"})
	out := stdout.String()

	if !strings.Contains(out, "Profile: large (primary: ruby, dynamic)") {
		t.Errorf("expected dynamic language annotation, got:\n%s", out)
	}
}

func TestRenderStatusHumanSchemaMismatch(t *testing.T) {
	resp := mcpio.StatusResponse{
		Index: mcpio.StatusIndex{Path: ".sense/index.db"},
		Version: &mcpio.StatusVersion{
			Schema:                5,
			SchemaCurrent:         false,
			EmbeddingModel:        "old-model",
			EmbeddingModelCurrent: false,
		},
		Freshness: mcpio.Freshness{},
	}

	var stdout, stderr bytes.Buffer
	cio := IO{Stdout: &stdout, Stderr: &stderr, Dir: t.TempDir()}

	renderStatusHuman(cio, resp, healthInfo{verdict: "healthy"})
	out := stdout.String()

	if !strings.Contains(out, "Schema: v5 (mismatch") {
		t.Errorf("expected schema mismatch, got:\n%s", out)
	}
	if !strings.Contains(out, "Embedding model: old-model (mismatch") {
		t.Errorf("expected embedding model mismatch, got:\n%s", out)
	}
}

func TestRenderInheritsNonEmpty(t *testing.T) {
	edges := []mcpio.InheritEdgeRef{
		{Symbol: "ApplicationService"},
		{Symbol: "Comparable"},
	}
	got := renderInherits(edges)
	if got != "ApplicationService, Comparable" {
		t.Errorf("renderInherits = %q", got)
	}
}

func TestRenderComposesNonEmpty(t *testing.T) {
	edges := []mcpio.ComposeEdgeRef{
		{Symbol: "Order"},
		{Symbol: "Payment"},
	}
	got := renderComposes(edges)
	if got != "Order, Payment" {
		t.Errorf("renderComposes = %q", got)
	}
}

func TestRenderIncludesNonEmpty(t *testing.T) {
	edges := []mcpio.IncludeEdgeRef{
		{Symbol: "SoftDeletable"},
		{Symbol: "Auditable"},
	}
	got := renderIncludes(edges)
	if got != "SoftDeletable, Auditable" {
		t.Errorf("renderIncludes = %q", got)
	}
}

func TestRenderImportsNonEmpty(t *testing.T) {
	edges := []mcpio.ImportEdgeRef{
		{Symbol: "fmt"},
		{Symbol: "strings"},
	}
	got := renderImports(edges)
	if got != "fmt, strings" {
		t.Errorf("renderImports = %q", got)
	}
}

func TestRenderEdgeGroupFull(t *testing.T) {
	filePtr := func(s string) *string { return &s }
	edges := mcpio.GraphEdges{
		Inherits:    []mcpio.InheritEdgeRef{{Symbol: "Base"}},
		InheritedBy: []mcpio.InheritEdgeRef{{Symbol: "PremiumCheckout"}},
		Composes:    []mcpio.ComposeEdgeRef{{Symbol: "Config"}},
		Includes:    []mcpio.IncludeEdgeRef{{Symbol: "Loggable"}},
		Imports:     []mcpio.ImportEdgeRef{{Symbol: "fmt"}},
		Calls:       []mcpio.CallEdgeRef{{Symbol: "Process", Confidence: 1.0}},
		CalledBy:    []mcpio.CallEdgeRef{{Symbol: "Main", File: filePtr("main.go"), Confidence: 0.9}},
		Tests:       []mcpio.TestEdgeRef{{File: "test.go", Confidence: 0.8}},
	}

	var buf bytes.Buffer
	label := "  %-9s %s\n"
	renderEdgeGroup(&buf, label, edges)
	out := buf.String()

	for _, want := range []string{
		"inherits  Base",
		"inherited by PremiumCheckout",
		"composes  Config",
		"includes  Loggable",
		"imports   fmt",
		"calls     Process",
		"callers   Main (0.9)",
		"tests     test.go (0.8)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRenderGraphHumanWithLayersAndTruncation(t *testing.T) {
	resp := mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{
			Name:      "CheckoutService",
			Qualified: "App::CheckoutService",
			File:      "app/services/checkout.rb",
			LineStart: 12,
			LineEnd:   85,
			Kind:      "class",
		},
		Edges: mcpio.GraphEdges{
			Calls: []mcpio.CallEdgeRef{
				{Symbol: "PaymentGateway#charge", Confidence: 1.0},
			},
		},
		Layers: []mcpio.GraphLayer{
			{
				Depth: 2,
				Edges: mcpio.GraphEdges{
					Calls: []mcpio.CallEdgeRef{
						{Symbol: "StripeAPI#post", Confidence: 0.9},
					},
				},
			},
		},
		Truncated: true,
		TestCallerSummary: &mcpio.TestCallerSummary{
			Count:    3,
			Examples: []string{"test/checkout_test.rb", "test/payment_test.rb"},
		},
	}

	var buf bytes.Buffer
	RenderGraphHuman(&buf, resp)
	out := buf.String()

	for _, want := range []string{
		"CheckoutService  (class)",
		"app/services/checkout.rb:12-85",
		"calls     PaymentGateway#charge",
		"depth 2",
		"calls     StripeAPI#post (0.9)",
		"(results truncated",
		"test cal.",
		"(3 in test/checkout_test.rb, test/payment_test.rb)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRenderSearchHumanNoResults(t *testing.T) {
	var buf bytes.Buffer
	RenderSearchHuman(&buf, mcpio.SearchResponse{})
	if !strings.Contains(buf.String(), "no results found") {
		t.Errorf("expected 'no results found', got:\n%s", buf.String())
	}
}

func TestRenderSearchHumanWithSnippet(t *testing.T) {
	resp := mcpio.SearchResponse{
		Results: []mcpio.SearchResultEntry{
			{
				Symbol:  "PaymentService#process",
				Kind:    "method",
				Score:   0.92,
				File:    "app/services/payment.rb",
				Line:    10,
				Snippet: "def process(amount)",
			},
			{
				Symbol: "RefundService#refund",
				Kind:   "method",
				Score:  0.85,
				File:   "app/services/refund.rb",
				Line:   5,
			},
		},
	}

	var buf bytes.Buffer
	RenderSearchHuman(&buf, resp)
	out := buf.String()

	for _, want := range []string{
		"PaymentService#process  (method)  0.92",
		"app/services/payment.rb:10",
		"def process(amount)",
		"RefundService#refund  (method)  0.85",
		"app/services/refund.rb:5",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\ngot:\n%s", want, out)
		}
	}
	// Second result has no snippet; should not have extra indented line
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.Contains(line, "RefundService#refund") {
			// next line should be the file line, then blank or next result
			if i+2 < len(lines) && strings.HasPrefix(lines[i+2], "  ") && !strings.Contains(lines[i+2], "(method)") {
				// If there is a snippet line it should not be empty-looking
				if strings.TrimSpace(lines[i+2]) == "" {
					t.Error("unexpected empty snippet line for result without snippet")
				}
			}
		}
	}
}

func TestRenderBlastHumanFullSections(t *testing.T) {
	resp := mcpio.BlastResponse{
		Symbol:      "App::CheckoutService",
		Risk:        "high",
		RiskFactors: []string{"hub node", "11 direct callers"},
		DirectCallers: []mcpio.BlastCaller{
			{Symbol: "OrdersController#create", File: "app/controllers/orders.rb"},
			{Symbol: "CartService#checkout", File: "app/services/cart.rb"},
		},
		IndirectCallers: []mcpio.BlastIndirect{
			{Symbol: "WebhookJob#process", Via: "OrdersController#create", Hops: 2},
		},
		AffectedSubclasses: []mcpio.BlastCaller{
			{Symbol: "PremiumCheckout", File: "app/services/premium.rb"},
		},
		AffectedViaComposition: []mcpio.BlastCaller{
			{Symbol: "OrderForm", File: "app/forms/order.rb"},
		},
		AffectedViaIncludes: []mcpio.BlastCaller{
			{Symbol: "Auditable", File: "app/concerns/auditable.rb"},
		},
		References: mcpio.BlastTierSummary{
			Count: 5,
			Examples: []mcpio.BlastCaller{
				{Symbol: "RefA", File: "a.rb"},
				{Symbol: "RefB", File: "b.rb"},
			},
		},
		AffectedTests:   []string{"test/checkout_test.rb", "test/cart_test.rb"},
		AffectedSymbols: 15,
		AffectedFiles:   8,
		TotalAffected:   15,
	}

	var buf bytes.Buffer
	RenderBlastHuman(&buf, resp)
	out := buf.String()

	for _, want := range []string{
		"App::CheckoutService  risk: high  (hub node, 11 direct callers)",
		"Direct callers (2):",
		"OrdersController#create  app/controllers/orders.rb",
		"CartService#checkout  app/services/cart.rb",
		"Indirect callers (1):",
		"WebhookJob#process  via OrdersController#create (2 hops)",
		"Affected subclasses (1):",
		"PremiumCheckout  app/services/premium.rb",
		"Affected via composition (1):",
		"OrderForm  app/forms/order.rb",
		"Affected via includes (1):",
		"Auditable  app/concerns/auditable.rb",
		"References — tier 2 (5):",
		"RefA  a.rb",
		"RefB  b.rb",
		"... and 3 more",
		"Affected tests (2):",
		"test/checkout_test.rb",
		"test/cart_test.rb",
		"Affected: 15 symbols across 8 files",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRenderBlastHumanNoRiskFactors(t *testing.T) {
	resp := mcpio.BlastResponse{
		Symbol:        "SmallHelper",
		Risk:          "low",
		TotalAffected: 0,
	}

	var buf bytes.Buffer
	RenderBlastHuman(&buf, resp)
	out := buf.String()

	if !strings.Contains(out, "SmallHelper  risk: low\n") {
		t.Errorf("expected no risk factors, got:\n%s", out)
	}
	// No sections should appear
	for _, absent := range []string{
		"Direct callers",
		"Indirect callers",
		"Affected subclasses",
		"Affected via composition",
		"Affected via includes",
		"References",
	} {
		if strings.Contains(out, absent) {
			t.Errorf("should not contain %q when empty, got:\n%s", absent, out)
		}
	}
}

func TestRenderBlastHumanTestsAffectedCount(t *testing.T) {
	resp := mcpio.BlastResponse{
		Symbol:             "BigService",
		Risk:               "medium",
		TestsAffectedCount: 42,
		TotalAffected:      50,
	}

	var buf bytes.Buffer
	RenderBlastHuman(&buf, resp)
	out := buf.String()

	if !strings.Contains(out, "Tests affected: 42") {
		t.Errorf("expected 'Tests affected: 42', got:\n%s", out)
	}
}

func TestRenderBlastHumanReferencesExact(t *testing.T) {
	resp := mcpio.BlastResponse{
		Symbol: "SmallRef",
		Risk:   "low",
		References: mcpio.BlastTierSummary{
			Count: 2,
			Examples: []mcpio.BlastCaller{
				{Symbol: "A", File: "a.rb"},
				{Symbol: "B", File: "b.rb"},
			},
		},
		TotalAffected: 2,
	}

	var buf bytes.Buffer
	RenderBlastHuman(&buf, resp)
	out := buf.String()

	if strings.Contains(out, "... and") {
		t.Errorf("should not contain '... and' when count == len(examples), got:\n%s", out)
	}
}

func TestFormatAgeAllBranches(t *testing.T) {
	tests := []struct {
		secs *int64
		want string
	}{
		{nil, "unknown"},
		{ptr(int64(30)), "30 seconds ago"},
		{ptr(int64(120)), "2 minutes ago"},
		{ptr(int64(7200)), "2 hours ago"},
		{ptr(int64(172800)), "2 days ago"},
	}
	for _, tt := range tests {
		got := formatAge(tt.secs)
		if got != tt.want {
			t.Errorf("formatAge(%v) = %q, want %q", tt.secs, got, tt.want)
		}
	}
}

func ptr(v int64) *int64 { return &v }

func TestFormatBytesAllBranches(t *testing.T) {
	tests := []struct {
		b    int64
		want string
	}{
		{500, "500 B"},
		{1536, "1.5 KB"},
		{1572864, "1.5 MB"},
		{1610612736, "1.5 GB"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.b)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.b, got, tt.want)
		}
	}
}

func TestWithConfidence(t *testing.T) {
	tests := []struct {
		label string
		c     mcpio.Confidence
		want  string
	}{
		{"Foo", 1.0, "Foo"},
		{"Foo", 0.9, "Foo (0.9)"},
		{"Bar", 0.75, "Bar (0.75)"},
	}
	for _, tt := range tests {
		got := withConfidence(tt.label, tt.c)
		if got != tt.want {
			t.Errorf("withConfidence(%q, %v) = %q, want %q", tt.label, tt.c, got, tt.want)
		}
	}
}

func TestRenderCallsEmpty(t *testing.T) {
	if got := renderCalls(nil); got != "" {
		t.Errorf("renderCalls(nil) = %q, want empty", got)
	}
}

func TestRenderTestsNonEmpty(t *testing.T) {
	edges := []mcpio.TestEdgeRef{
		{File: "test_a.rb", Confidence: 0.8},
		{File: "test_b.rb", Confidence: 1.0},
	}
	got := renderTests(edges)
	if got != "test_a.rb (0.8), test_b.rb" {
		t.Errorf("renderTests = %q", got)
	}
}

func TestRenderTestsEmpty(t *testing.T) {
	if got := renderTests(nil); got != "" {
		t.Errorf("renderTests(nil) = %q, want empty", got)
	}
}

func TestRenderInheritsEmpty(t *testing.T) {
	if got := renderInherits(nil); got != "" {
		t.Errorf("renderInherits(nil) = %q, want empty", got)
	}
}

func TestRenderComposesEmpty(t *testing.T) {
	if got := renderComposes(nil); got != "" {
		t.Errorf("renderComposes(nil) = %q, want empty", got)
	}
}

func TestRenderIncludesEmpty(t *testing.T) {
	if got := renderIncludes(nil); got != "" {
		t.Errorf("renderIncludes(nil) = %q, want empty", got)
	}
}

func TestRenderImportsEmpty(t *testing.T) {
	if got := renderImports(nil); got != "" {
		t.Errorf("renderImports(nil) = %q, want empty", got)
	}
}

func TestRunDeadHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		cio, _, stderr := newTestIO()
		if code := RunDead([]string{flag}, cio); code != ExitSuccess {
			t.Fatalf("%s: exit code = %d, want %d", flag, code, ExitSuccess)
		}
		got := stderr.String()
		for _, want := range []string{
			"usage: sense dead",
			"--language LANG",
			"--domain PATH",
			"--limit N",
			"--json",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: help missing %q\ngot:\n%s", flag, want, got)
			}
		}
	}
}

func TestRunDeadMissingIndex(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := RunDead(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitIndexMissing {
		t.Errorf("exit = %d, want %d", code, ExitIndexMissing)
	}
}

func TestRunStatusHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		cio, _, stderr := newTestIO()
		if code := RunStatus([]string{flag}, cio); code != ExitSuccess {
			t.Fatalf("%s: exit code = %d, want %d", flag, code, ExitSuccess)
		}
		got := stderr.String()
		if !strings.Contains(got, "usage: sense status") {
			t.Errorf("%s: help missing usage line\ngot:\n%s", flag, got)
		}
	}
}

// seedDeadCodeProject builds a .sense/index.db with symbols that have
// no incoming edges — dead code. Returns the tempdir.
func seedDeadCodeProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	fid, err := adapter.WriteFile(ctx, &model.File{
		Path: "app/services/old.go", Language: "go", Hash: "a1",
		Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fid2, err := adapter.WriteFile(ctx, &model.File{
		Path: "app/services/used.go", Language: "go", Hash: "a2",
		Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Dead symbols: no incoming edges
	for _, sym := range []model.Symbol{
		{FileID: fid, Name: "OldService", Qualified: "old.OldService", Kind: "class", LineStart: 1, LineEnd: 50},
		{FileID: fid, Name: "UnusedHelper", Qualified: "old.UnusedHelper", Kind: "function", LineStart: 55, LineEnd: 60},
	} {
		if _, err := adapter.WriteSymbol(ctx, &sym); err != nil {
			t.Fatal(err)
		}
	}

	// Used symbol: has an incoming edge
	usedSym := &model.Symbol{FileID: fid2, Name: "UsedService", Qualified: "used.UsedService", Kind: "class", LineStart: 1, LineEnd: 30}
	usedID, err := adapter.WriteSymbol(ctx, usedSym)
	if err != nil {
		t.Fatal(err)
	}
	callerSym := &model.Symbol{FileID: fid2, Name: "Caller", Qualified: "used.Caller", Kind: "function", LineStart: 35, LineEnd: 40}
	callerID, err := adapter.WriteSymbol(ctx, callerSym)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.WriteEdge(ctx, &model.Edge{
		SourceID: &callerID, TargetID: usedID, Kind: model.EdgeCalls,
		FileID: fid2, Confidence: 1.0,
	}); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestRunDeadHumanSuccess(t *testing.T) {
	dir := seedDeadCodeProject(t)
	var stdout, stderr bytes.Buffer
	code := RunDead(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	// Seeded data is Go, but this DB is hand-built and never scanned, so no Go
	// mention harvest ran: the soundness gate fails closed (core_no_harvest),
	// keeping the unreferenced symbols possibly_dead, never earned dead.
	if !strings.Contains(out, "Possibly dead") {
		t.Errorf("expected possibly-dead section, got:\n%s", out)
	}
	if !strings.Contains(out, "OldService") {
		t.Errorf("expected OldService in unreferenced output, got:\n%s", out)
	}
}

func TestRunDeadJSONSuccess(t *testing.T) {
	dir := seedDeadCodeProject(t)
	var stdout, stderr bytes.Buffer
	code := RunDead([]string{"--json"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	raw := bytes.TrimSpace(stdout.Bytes())
	if !json.Valid(raw) {
		t.Fatalf("stdout is not valid JSON:\n%s", raw)
	}
	var parsed mcpio.UnreferencedResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	if parsed.TotalSymbols == 0 {
		t.Error("expected non-zero total_symbols")
	}
}

func TestRunDeadWithLanguageFilter(t *testing.T) {
	dir := seedDeadCodeProject(t)
	var stdout, stderr bytes.Buffer
	code := RunDead([]string{"--language", "ruby"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	// All symbols are Go, so filtering by Ruby should find nothing.
	if !strings.Contains(stdout.String(), "No unreferenced symbols found.") {
		t.Errorf("expected no unreferenced symbols for ruby filter, got:\n%s", stdout.String())
	}
}

func TestRunDeadWithDomainFilter(t *testing.T) {
	dir := seedDeadCodeProject(t)
	var stdout, stderr bytes.Buffer
	code := RunDead([]string{"--domain", "services"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	// Domain "services" matches "app/services/old.go"
	out := stdout.String()
	if strings.Contains(out, "No unreferenced symbols found.") {
		t.Errorf("expected unreferenced symbols for services domain, got:\n%s", out)
	}
}

func TestRunStatusHumanSuccess(t *testing.T) {
	dir := seedDeadCodeProject(t) // reuse: any valid index works
	var stdout, stderr bytes.Buffer
	code := RunStatus(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Index:",
		"Files:",
		"Symbols:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRunStatusMissingIndex(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := RunStatus(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitIndexMissing {
		t.Errorf("exit = %d, want %d", code, ExitIndexMissing)
	}
}

func TestQueryLifetimeCounters(t *testing.T) {
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

	db := adapter.DB()
	// Insert lifetime metrics
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_metrics(key, value) VALUES('lifetime_queries', 42)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_metrics(key, value) VALUES('lifetime_tokens_saved', 150000)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_metrics(key, value) VALUES('lifetime_file_reads_avoided', 200)`)

	lt := queryLifetimeCounters(ctx, db)
	if lt == nil {
		t.Fatal("expected non-nil lifetime")
	}
	if lt.Queries != 42 {
		t.Errorf("Queries = %d, want 42", lt.Queries)
	}
	if lt.EstimatedTokensSaved != 150000 {
		t.Errorf("EstimatedTokensSaved = %d, want 150000", lt.EstimatedTokensSaved)
	}
	if lt.EstimatedFileReadsAvoided != 200 {
		t.Errorf("EstimatedFileReadsAvoided = %d, want 200", lt.EstimatedFileReadsAvoided)
	}
}

func TestQueryLifetimeCountersEmpty(t *testing.T) {
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

	lt := queryLifetimeCounters(ctx, adapter.DB())
	if lt != nil {
		t.Errorf("expected nil lifetime for empty metrics, got %+v", lt)
	}
}

func TestRenderConventionsHumanEmpty(t *testing.T) {
	var buf bytes.Buffer
	RenderConventionsHuman(&buf, nil)
	if !strings.Contains(buf.String(), "no conventions detected") {
		t.Errorf("expected 'no conventions detected', got:\n%s", buf.String())
	}
}

func TestRunConventionsHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		cio, _, stderr := newTestIO()
		if code := RunConventions([]string{flag}, cio); code != ExitSuccess {
			t.Fatalf("%s: exit code = %d, want %d", flag, code, ExitSuccess)
		}
		got := stderr.String()
		if !strings.Contains(got, "usage: sense conventions") {
			t.Errorf("%s: help missing usage line\ngot:\n%s", flag, got)
		}
	}
}

func TestRunDeadWithLimit(t *testing.T) {
	dir := seedDeadCodeProject(t)
	var stdout, stderr bytes.Buffer
	code := RunDead([]string{"--limit", "1"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Possibly dead") {
		t.Errorf("expected possibly-dead section, got:\n%s", out)
	}
}

func TestRunDeadCorruptIndex(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(senseDir, "index.db"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := RunDead(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitIndexCorrupt {
		t.Errorf("exit = %d, want %d", code, ExitIndexCorrupt)
	}
}

func TestRunDeadBadFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := RunDead([]string{"--nope"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: t.TempDir()})
	if code != ExitGeneralError {
		t.Errorf("exit = %d, want %d for an unknown flag", code, ExitGeneralError)
	}
}

func TestRunConventionsCorruptIndex(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(senseDir, "index.db"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := RunConventions(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitIndexCorrupt {
		t.Errorf("exit = %d, want %d", code, ExitIndexCorrupt)
	}
}

func TestRunSearchCorruptIndex(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(senseDir, "index.db"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := RunSearch([]string{"test"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitIndexCorrupt {
		t.Errorf("exit = %d, want %d", code, ExitIndexCorrupt)
	}
}

func TestRunSearchNoResults(t *testing.T) {
	dir := seedSearchProject(t)
	t.Setenv("SENSE_EMBEDDINGS", "false")
	var stdout, stderr bytes.Buffer
	code := RunSearch([]string{"zzzzxyzzy_nonexistent_query"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no results found") {
		t.Errorf("expected 'no results found', got:\n%s", stdout.String())
	}
}

func TestRenderStatusHumanProfileNoLanguage(t *testing.T) {
	resp := mcpio.StatusResponse{
		Index: mcpio.StatusIndex{Path: ".sense/index.db"},
		Profile: &mcpio.StatusProfile{
			Tier: "small",
		},
		Freshness: mcpio.Freshness{},
	}

	var stdout, stderr bytes.Buffer
	cio := IO{Stdout: &stdout, Stderr: &stderr, Dir: t.TempDir()}

	renderStatusHuman(cio, resp, healthInfo{verdict: "healthy"})
	out := stdout.String()

	if !strings.Contains(out, "Profile: small") {
		t.Errorf("expected 'Profile: small', got:\n%s", out)
	}
	// Should not contain "(primary:" when PrimaryLanguage is empty
	if strings.Contains(out, "(primary:") {
		t.Errorf("should not show primary when empty, got:\n%s", out)
	}
}
