package model

// EdgeRef pairs an edge with the symbol at its non-focal endpoint, so callers
// can render a relationship without a second lookup.
type EdgeRef struct {
	Edge   Edge
	Target Symbol
}

// SymbolContext bundles a focal symbol with its enclosing file and adjacent
// edges — the aggregate shape returned by Index.ReadSymbol and the MCP
// sense.graph response in 06-mcp-and-cli.md. Named to avoid collision with
// the Index.Query search method.
type SymbolContext struct {
	Symbol   Symbol
	File     File
	Outbound []EdgeRef
	Inbound  []EdgeRef
}
