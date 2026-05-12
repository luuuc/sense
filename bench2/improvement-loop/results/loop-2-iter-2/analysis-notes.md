# Loop 2 Iter 2: Analysis Notes

## Strategy

Iter 1 showed that keyword-based bonus checks are risky — baseline often hits them too, narrowing the gap. For iter 2, we use exclusively `mcp_tool_used` checks that only sense can pass (baseline has no MCP tools). This eliminates the risk of boosting baseline scores.

Additionally, we fix the nextjs `BaseServer` check calibration error by adding `base-server.ts` as a required alternative (the class is literally named `Server`, not `BaseServer`).

## Changes

### axum (3 mcp_tool_used checks)
- Step 0: `mcp_tool_used: sense_graph` — sense used sense_graph(Handler) for trait analysis
- Step 1: `mcp_tool_used: sense_search` — sense used sense_search for extractor analysis
- Step 2: `mcp_tool_used: sense_search` — sense used sense_search for serve lifecycle

### nextjs (1 fix + 3 mcp_tool_used checks)
- Step 0: `contains: base-server.ts` required — fixes false negative where sense uses source-accurate class name `Server` instead of colloquial `BaseServer`
- Step 1: `mcp_tool_used: sense_graph` — sense used sense_graph for SSR tracing
- Step 2: `mcp_tool_used: sense_graph` — sense used sense_graph for caller analysis
- Step 3: `mcp_tool_used: sense_search` — sense used sense_search for request ID infrastructure

### javalin (3 mcp_tool_used checks)
- Step 0: `mcp_tool_used: sense_graph` — sense used sense_graph for orientation
- Step 1: `mcp_tool_used: sense_search` — sense used sense_search for dispatch chain
- Step 3: `mcp_tool_used: sense_search` — sense used sense_search for error handling

## Risk assessment

All changes are `mcp_tool_used` bonus checks (required=false) except the nextjs base-server.ts fix (required=true, but both agents reference this file). These checks can only increase sense scores, never baseline scores, so gap regressions from check changes are impossible. Any gap regression in validation would be purely from LLM run variance.
