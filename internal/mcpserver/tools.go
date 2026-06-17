package mcpserver

import (
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/luuuc/sense/internal/search"
)

// This file holds the five mcp.Tool schema definitions. They are pure
// declarations of each tool's name, description, and parameters; the matching
// handlers live in their concern files (search.go, graph.go, blast.go,
// conventions.go, status.go).

func searchTool() mcp.Tool {
	return mcp.NewTool("sense_search",
		mcp.WithDescription("Find symbols by semantic and keyword matching across all indexed code. "+
			"Use this instead of grep when the question is about concepts, functionality, or behavior — "+
			"not exact strings. Also useful for exploring architecture by searching broad concepts "+
			"(e.g., 'routing', 'middleware', 'database'). Returns ranked symbols with file locations, "+
			"kinds, and relevance scores, without reading any source files into context."),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Sense Search",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Natural language description of what you're looking for, e.g. 'how does auth work', 'payment error handling', 'user validation'"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results to return (default 10)"),
		),
		mcp.WithString("language",
			mcp.Description("Filter results to a specific language, e.g. 'ruby', 'go', 'typescript'"),
		),
		mcp.WithNumber("min_score",
			mcp.Description("Minimum relevance score threshold 0.0–1.0 (default 0.0). Raise to filter weak matches."),
		),
		mcp.WithString("mode",
			mcp.Description("Ranking mode (default 'hybrid'). 'hybrid' auto-detects whether the query is a concept or an identifier. 'semantic' forces concept ranking — use when an identifier-looking query is actually conceptual. 'keyword' forces literal ranking — use for exact identifier lookups."),
			mcp.Enum(search.ModeHybrid, search.ModeSemantic, search.ModeKeyword),
		),
	)
}

func graphTool() mcp.Tool {
	return mcp.NewTool("sense_graph",
		mcp.WithDescription("Look up the structural relationships of a symbol: callers, callees, "+
			"inheritance, composition, includes, imports, and test coverage. "+
			"Use this instead of grep or file reading when the user asks about relationships, dependencies, "+
			"callers, or how a symbol connects to the rest of the codebase. "+
			"Returns a pre-computed graph from the Sense index with no context window cost for file contents. "+
			"For symbols called through interfaces or traits, dispatch-inferred callers appear in a separate "+
			"dispatch_inferred field (confidence 0.8).\n\n"+
			"When dead_code is true, returns project-wide dead symbols (zero incoming references) instead of "+
			"per-symbol edges. The symbol, direction, and depth parameters are ignored in this mode."),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Sense Graph",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}),
		mcp.WithString("symbol",
			mcp.Description("Qualified or unqualified symbol name, e.g. 'User', 'Checkout::Order', 'HandleRequest'. Ignored when dead_code is true."),
		),
		mcp.WithString("file",
			mcp.Description("Disambiguate by file path substring when the symbol name is ambiguous (e.g. a re-opened class, or a name shared with a JS/TS component), e.g. 'app/models/status.rb'. Resolves to the single matching candidate."),
		),
		mcp.WithNumber("depth",
			mcp.Description("How many hops to traverse from the symbol (default 1)"),
		),
		mcp.WithString("direction",
			mcp.Description("Which edges to follow: 'both' (default), 'callers' (who calls this), or 'callees' (what this calls)"),
			mcp.Enum("both", "callers", "callees"),
		),
		mcp.WithBoolean("dead_code",
			mcp.Description("When true, return project-wide dead symbols instead of per-symbol edges. Symbol, direction, and depth are ignored. Test-only references are excluded by default (symbols only called from test files are reported as dead)."),
		),
		mcp.WithString("language",
			mcp.Description("Filter dead code results to a specific language, e.g. 'go', 'ruby'. Only used when dead_code is true."),
		),
		mcp.WithString("domain",
			mcp.Description("Filter dead code results to a path substring, e.g. 'services', 'models'. Only used when dead_code is true."),
		),
		mcp.WithNumber("context_lines",
			mcp.Description("Lines of source context around each call site (default 2, 0 to suppress snippets)"),
		),
		mcp.WithNumber("min_confidence",
			mcp.Description("Minimum edge confidence 0.0–1.0 for callers/callees (default 0.5). "+
				"Lower it (e.g. 0.3) to surface low-confidence callers that the default floor hides — "+
				"implicit-receiver calls to a method whose name is defined in multiple classes resolve by "+
				"bare-name fallback and are stamped 0.3. When called_by is empty but low_confidence_hidden "+
				"is non-zero, re-run with a lower min_confidence before concluding a symbol is unused."),
		),
	)
}

func blastTool() mcp.Tool {
	return mcp.NewTool("sense_blast",
		mcp.WithDescription("Compute what would break if a symbol or diff changed. "+
			"Follows the chain of callers and dependents multiple hops deep, including affected tests. "+
			"Use this instead of manually tracing callers when the user asks about impact, risk, "+
			"safe-to-change analysis, or what would break. Accepts a symbol name or a git ref for "+
			"diff-based analysis. Returns affected files, symbols, and test coverage with confidence scores."),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Sense Blast Radius",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}),
		mcp.WithString("symbol",
			mcp.Description("Symbol to analyze, e.g. 'User', 'Checkout::Order'. Mutually exclusive with diff."),
		),
		mcp.WithString("file",
			mcp.Description("Disambiguate by file path substring when the symbol name is ambiguous (e.g. a re-opened class, or a name shared with a JS/TS component), e.g. 'app/models/status.rb'. Resolves to the single matching candidate."),
		),
		mcp.WithString("diff",
			mcp.Description("Git ref for diff-based blast, e.g. 'HEAD~1', 'main..feature'. Mutually exclusive with symbol."),
		),
		mcp.WithNumber("max_hops",
			mcp.Description("How many dependency hops to follow (default 3). Higher values find more distant impacts."),
		),
		mcp.WithNumber("min_confidence",
			mcp.Description("Minimum edge confidence 0.0–1.0 (default 0.7). Lower values include weaker relationships."),
		),
		mcp.WithBoolean("include_tests",
			mcp.Description("Include affected test files in the results (default true)"),
		),
		mcp.WithNumber("context_lines",
			mcp.Description("Lines of source context around each call site (default 2, 0 to suppress snippets)"),
		),
	)
}

func statusTool() mcp.Tool {
	return mcp.NewTool("sense_status",
		mcp.WithDescription("Check Sense index health and coverage. "+
			"Returns file, symbol, edge, and embedding counts, language breakdown by tier, "+
			"index freshness, and cumulative session metrics. "+
			"Use this to verify what is indexed and whether the index is stale. "+
			"Also useful for reporting how Sense has been used in the current session."),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Sense Status",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}),
	)
}

func conventionsTool() mcp.Tool {
	return mcp.NewTool("sense_conventions",
		mcp.WithDescription("Detect project conventions and recurring patterns: inheritance hierarchies, "+
			"naming conventions, structural patterns, composition styles, and testing approaches. "+
			"Use this instead of reading multiple files to understand how existing code is structured "+
			"or what patterns to follow when writing new code. Essential for codebase orientation — "+
			"reveals the architectural patterns that define how this project is built. "+
			"Returns conventions with strength scores and instance counts, scoped by domain if specified."),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Sense Conventions",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}),
		mcp.WithString("domain",
			mcp.Description("Filter conventions by domain, e.g. 'models', 'controllers', 'services', 'test'. Matches path substrings."),
		),
		mcp.WithNumber("min_strength",
			mcp.Description("Minimum convention strength 0.0–1.0 (default 0.3). Lower to see weaker patterns, raise to see only strong, well-established ones."),
		),
	)
}
