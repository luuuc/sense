package mcpserver

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestApplyAliasesGlobalRewrite(t *testing.T) {
	args := map[string]any{"question": "how does auth work"}
	rewrites := applyAliases(args, globalAliases)

	if rewrites != 1 {
		t.Fatalf("rewrites = %d, want 1", rewrites)
	}
	if got, ok := args["query"]; !ok || got != "how does auth work" {
		t.Errorf("query not set from question alias: %v", args)
	}
	if _, lingering := args["question"]; lingering {
		t.Errorf("alias key 'question' should have been removed: %v", args)
	}
}

func TestApplyAliasesCanonicalWinsOverAlias(t *testing.T) {
	args := map[string]any{"query": "canonical", "question": "alias"}
	rewrites := applyAliases(args, globalAliases)

	if rewrites != 0 {
		t.Fatalf("rewrites = %d, want 0 (canonical present)", rewrites)
	}
	if args["query"] != "canonical" {
		t.Errorf("canonical query overwritten: %v", args["query"])
	}
	if args["question"] != "alias" {
		t.Errorf("alias key should remain when canonical wins (no rewrite happened): %v", args["question"])
	}
}

func TestApplyAliasesSymbolFromName(t *testing.T) {
	args := map[string]any{"name": "User"}
	applyAliases(args, globalAliases)
	if args["symbol"] != "User" {
		t.Errorf("symbol not set from name alias: %v", args)
	}
}

func TestApplyAliasesNilArgs(t *testing.T) {
	if got := applyAliases(nil, globalAliases); got != 0 {
		t.Errorf("nil args should yield 0 rewrites, got %d", got)
	}
}

func TestApplyAliasesFirstAltWins(t *testing.T) {
	args := map[string]any{"text": "first", "question": "second"}
	applyAliases(args, globalAliases)
	if args["query"] != "first" {
		t.Errorf("expected first alternative (text) to win, got %v", args["query"])
	}
}

func TestApplyAliasesToolScoped(t *testing.T) {
	graphArgs := map[string]any{"dir": "callers"}
	applyAliases(graphArgs, toolAliases["sense_graph"])
	if graphArgs["direction"] != "callers" {
		t.Errorf("dir → direction rewrite failed: %v", graphArgs)
	}

	blastArgs := map[string]any{"from_ref": "HEAD~1"}
	applyAliases(blastArgs, toolAliases["sense_blast"])
	if blastArgs["diff"] != "HEAD~1" {
		t.Errorf("from_ref → diff rewrite failed: %v", blastArgs)
	}
}

func TestWithAliasingRewritesBeforeHandler(t *testing.T) {
	var seen map[string]any
	handler := func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		seen = req.GetArguments()
		return mcp.NewToolResultText("ok"), nil
	}
	wrapped := withAliasing("sense_graph", handler)

	req := mcp.CallToolRequest{}
	req.Params.Name = "sense_graph"
	req.Params.Arguments = map[string]any{
		"name": "User",       // global alias → symbol
		"dir":  "callers",    // tool alias → direction
		"text": "user model", // global alias → query
	}
	if _, err := wrapped(context.Background(), req); err != nil {
		t.Fatalf("wrapped handler: %v", err)
	}

	if seen["symbol"] != "User" {
		t.Errorf("symbol not aliased into handler args: %v", seen)
	}
	if seen["direction"] != "callers" {
		t.Errorf("direction not aliased: %v", seen)
	}
	if seen["query"] != "user model" {
		t.Errorf("query not aliased: %v", seen)
	}
}

func TestWithAliasingUnknownToolUsesGlobalsOnly(t *testing.T) {
	var seen map[string]any
	handler := func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		seen = req.GetArguments()
		return mcp.NewToolResultText("ok"), nil
	}
	wrapped := withAliasing("sense_status", handler)

	req := mcp.CallToolRequest{}
	req.Params.Name = "sense_status"
	req.Params.Arguments = map[string]any{"q": "search query"}
	if _, err := wrapped(context.Background(), req); err != nil {
		t.Fatalf("wrapped handler: %v", err)
	}
	if seen["query"] != "search query" {
		t.Errorf("global alias did not apply for unknown tool: %v", seen)
	}
}
