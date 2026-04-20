package scan

import sitter "github.com/tree-sitter/go-tree-sitter"

// ParserCache holds reusable tree-sitter parsers keyed by language.
// Callers that invoke RunIncremental repeatedly should create one
// ParserCache and pass it to each call to avoid re-allocating parsers.
type ParserCache struct {
	parsers map[string]*sitter.Parser
}

// NewParserCache creates an empty parser cache.
func NewParserCache() *ParserCache {
	return &ParserCache{parsers: make(map[string]*sitter.Parser)}
}

// Close releases all cached parsers.
func (pc *ParserCache) Close() {
	for _, p := range pc.parsers {
		p.Close()
	}
	pc.parsers = nil
}
