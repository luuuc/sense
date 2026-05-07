package model

// EdgeRef pairs an edge with the symbol at its non-focal endpoint, so callers
// can render a relationship without a second lookup.
type EdgeRef struct {
	Edge   Edge
	Target Symbol
}

// SymbolContext bundles a focal symbol with its enclosing file and adjacent
// edges — the aggregate shape returned by Index.ReadSymbol and the MCP
// sense_graph response in 06-mcp-and-cli.md. Named to avoid collision with
// the Index.Query search method.
type SymbolContext struct {
	Symbol   Symbol
	File     File
	Outbound []EdgeRef
	Inbound  []EdgeRef
}

// Direction enumerates the traversal direction values accepted by
// ReadSymbolGraph and the MCP/CLI layers.
type Direction string

const (
	DirectionBoth    Direction = "both"
	DirectionCallers Direction = "callers"
	DirectionCallees Direction = "callees"
)

// GraphResult holds multi-hop graph traversal results. Root contains
// the depth-1 edges (same as ReadSymbol). Layers holds one HopEdges
// per additional hop (index 0 = depth 2, index 1 = depth 3).
type GraphResult struct {
	Root      SymbolContext
	Layers    []HopEdges
	Truncated bool
}

// HopEdges holds edges discovered at one BFS hop beyond the root.
type HopEdges struct {
	Outbound []EdgeRef
	Inbound  []EdgeRef
}
