package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Argument aliases for sense_* tool calls.
//
// LLM clients regularly echo phrasing from tool descriptions instead
// of the literal schema keys (`question` instead of `query`, `name`
// instead of `symbol`, …). The MCP SDK strict-validates argument
// names, so a hallucinated key trips a tool error and the agent
// retries — burning a full round-trip on what is really a synonym.
//
// Port of Context7's approach
// (sense-competitors/context7/packages/mcp/src/index.ts:285-337):
// rewrite known-hallucinated keys to their canonical form before the
// handler sees them. Aliases apply only when the canonical key is
// absent — a present canonical wins, so adding an alias here cannot
// shadow a legitimate request.
//
// The rewrite mutates request.Params.Arguments in place. Because
// GetArguments returns the same underlying map, all downstream
// GetString / GetBool / GetInt calls see the canonical key.

type aliasMap map[string][]string

var globalAliases = aliasMap{
	"query":  {"q", "text", "userQuery", "question"},
	"symbol": {"name", "identifier", "qualified"},
}

var toolAliases = map[string]aliasMap{
	"sense_graph": {
		"direction": {"dir"},
	},
	"sense_blast": {
		"diff": {"from_ref", "ref"},
	},
}

// applyAliases mutates args, copying values from any alternate key to
// the canonical key when the canonical is absent. Returns the number
// of rewrites performed — useful for tests and metrics.
func applyAliases(args map[string]any, aliases aliasMap) int {
	if args == nil {
		return 0
	}
	rewrites := 0
	for canonical, alternatives := range aliases {
		if _, present := args[canonical]; present {
			continue
		}
		for _, alt := range alternatives {
			if v, ok := args[alt]; ok {
				args[canonical] = v
				delete(args, alt)
				rewrites++
				break
			}
		}
	}
	return rewrites
}

// withAliasing wraps a tool handler so hallucinated argument keys are
// rewritten before dispatch. toolName selects the per-tool alias map
// (in addition to globalAliases).
func withAliasing(toolName string, handler server.ToolHandlerFunc) server.ToolHandlerFunc {
	perTool := toolAliases[toolName]
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if args := req.GetArguments(); args != nil {
			applyAliases(args, globalAliases)
			if perTool != nil {
				applyAliases(args, perTool)
			}
		}
		return handler(ctx, req)
	}
}
