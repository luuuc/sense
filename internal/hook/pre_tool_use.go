package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/sqlite"
)

type preToolUseInput struct {
	Tool  string `json:"tool_name"`
	Input struct {
		Pattern string `json:"pattern"`
		Command string `json:"command"`
		Regex   string `json:"regex"`
	} `json:"tool_input"`
}

func handlePreToolUse(ctx context.Context, input json.RawMessage, adapter *sqlite.Adapter, _ string) (any, error) {
	var req preToolUseInput
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, err
	}

	pattern := extractPattern(req)
	if pattern == "" || !isSymbolShaped(pattern) {
		return nil, nil
	}

	symbols, err := adapter.Query(ctx, index.Filter{Name: pattern, Limit: 5})
	if err != nil || len(symbols) == 0 {
		return nil, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Sense has %d indexed symbol(s) matching %q. Instead of grep, try:\n", len(symbols), pattern)
	fmt.Fprintf(&sb, "- sense_graph symbol=%q for callers/callees\n", pattern)
	fmt.Fprintf(&sb, "- sense_search query=%q for semantic matches\n", pattern)
	sb.WriteString("Sense results are faster and include structural context (callers, tests, blast radius).")

	return &hookResponse{AdditionalContext: sb.String()}, nil
}

func extractPattern(req preToolUseInput) string {
	if req.Input.Pattern != "" {
		return req.Input.Pattern
	}
	if req.Input.Command != "" {
		return req.Input.Command
	}
	return req.Input.Regex
}

// isSymbolShaped returns true if the pattern looks like a symbol name
// rather than a regex. Symbol-shaped: word chars, dots, colons, #.
// Regex-shaped: contains *, +, |, (, [, {, ?, ^, $.
func isSymbolShaped(pattern string) bool {
	for _, c := range pattern {
		switch c {
		case '*', '+', '|', '(', ')', '[', ']', '{', '}', '?', '^', '$', '\\':
			return false
		}
	}
	return len(pattern) >= 2
}
