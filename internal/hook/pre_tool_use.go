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
		Pattern      string `json:"pattern"`
		Command      string `json:"command"`
		Regex        string `json:"regex"`
		SubagentType string `json:"subagent_type"`
		Prompt       string `json:"prompt"`
		Description  string `json:"description"`
	} `json:"tool_input"`
}

var explorationAgents = map[string]bool{
	"deep-explore": true,
	"Explore":      true,
}

var explorationPhrases = []string{
	"explore the codebase",
	"search the codebase",
	"understand the codebase",
	"research the codebase",
	"codebase structure",
	"codebase architecture",
	"codebase exploration",
	"codebase understanding",
	"code structure",
	"code architecture",
	"code exploration",
	"find implementation",
	"find callers",
	"find uses of",
	"find where",
	"who calls",
	"what calls",
	"callers of",
	"callees of",
	"dependencies of",
	"understand the code",
	"how does the code",
	"what would break",
	"blast radius",
	"impact of changing",
	"symbol relationship",
}

var codeExtensions = []string{
	".go", ".py", ".ts", ".tsx", ".js", ".jsx",
	".rb", ".rs", ".java", ".kt", ".scala", ".cs", ".php",
}

func handlePreToolUse(ctx context.Context, input json.RawMessage, adapter *sqlite.Adapter, _ string) (any, error) {
	var req preToolUseInput
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, err
	}

	switch req.Tool {
	case "Agent":
		return handleAgent(ctx, req, adapter)
	case "Bash":
		return handleBash(ctx, req, adapter)
	case "Grep":
		return handleGrep(ctx, req, adapter)
	case "Glob":
		return handleGlob(ctx, req, adapter)
	default:
		return nil, nil
	}
}

func handleGlob(ctx context.Context, req preToolUseInput, adapter *sqlite.Adapter) (any, error) {
	pattern := req.Input.Pattern
	if pattern == "" || !isSymbolShaped(pattern) {
		return nil, nil
	}

	symbols, err := adapter.Query(ctx, index.Filter{Name: pattern, Limit: 5})
	if err != nil || len(symbols) == 0 {
		return nil, nil
	}

	return nudge(
		fmt.Sprintf("Sense has %d indexed symbol(s) matching %q — consider sense_graph or sense_search instead of Glob.", len(symbols), pattern),
		buildContext(len(symbols), pattern, "glob"),
	), nil
}

func handleGrep(ctx context.Context, req preToolUseInput, adapter *sqlite.Adapter) (any, error) {
	pattern := extractPattern(req)
	if pattern == "" {
		return nil, nil
	}

	if !isSymbolShaped(pattern) {
		return nil, nil
	}

	symbols, err := adapter.Query(ctx, index.Filter{Name: pattern, Limit: 5})
	if err != nil || len(symbols) == 0 {
		if isMultiWordPattern(pattern) {
			ctx := fmt.Sprintf(
				"Consider sense_search query=%q for semantic code search (it's loaded and ready).", pattern)
			return nudge("Sense can do semantic code search — consider sense_search instead of grep.", ctx), nil
		}
		return nil, nil
	}

	return nudge(
		fmt.Sprintf("Sense has %d indexed symbol(s) matching %q — consider sense_graph or sense_search instead of grep.", len(symbols), pattern),
		buildContext(len(symbols), pattern, "grep"),
	), nil
}

func handleAgent(ctx context.Context, req preToolUseInput, adapter *sqlite.Adapter) (any, error) {
	isKnownExplorer := explorationAgents[req.Input.SubagentType]

	prompt := req.Input.Prompt
	if prompt == "" {
		prompt = req.Input.Description
	}
	hasExplorationIntent := prompt != "" && hasExplorationKeyword(prompt)

	if !isKnownExplorer && !hasExplorationIntent {
		return nil, nil
	}

	var count int
	if err := adapter.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_symbols`).Scan(&count); err != nil || count == 0 {
		return nil, nil
	}

	reason := fmt.Sprintf(
		"This project has a Sense index (%d symbols). The Sense MCP tools are loaded and callable now — prefer them over agents for codebase understanding:\n"+
			"- sense_graph for symbol relationships (callers, callees, inheritance)\n"+
			"- sense_search for semantic code search\n"+
			"- sense_blast for impact analysis\n"+
			"- sense_conventions for project patterns",
		count,
	)

	tip := "Sense has this project indexed — use Sense MCP tools instead of agents for codebase questions."
	if isKnownExplorer {
		tip = "Sense has this project indexed. Use Sense MCP tools instead of Explore/deep-explore agents."
	}
	return nudge(tip, reason), nil
}

func handleBash(ctx context.Context, req preToolUseInput, adapter *sqlite.Adapter) (any, error) {
	cmd := req.Input.Command

	// extractBashPattern returns a single whitespace-split token, so the
	// pattern here is never multi-word (unlike handleGrep, whose pattern comes
	// straight from the Grep tool input). Only the symbol-shaped single-token
	// case can fire; the multi-word semantic-search nudge lives in handleGrep.
	pattern := extractBashPattern(cmd)
	if pattern != "" && isSymbolShaped(pattern) {
		symbols, err := adapter.Query(ctx, index.Filter{Name: pattern, Limit: 5})
		if err == nil && len(symbols) > 0 {
			return nudge(
				fmt.Sprintf("Sense has %d indexed symbol(s) matching %q — consider sense_graph or sense_search instead of bash grep.", len(symbols), pattern),
				buildContext(len(symbols), pattern, "bash grep"),
			), nil
		}
	}

	if isExplorationCommand(cmd) {
		ctx := "Sense can answer codebase understanding questions without reading individual files — its tools are loaded and ready."
		return nudge("Sense has this project indexed — consider Sense tools instead of reading files manually.", ctx), nil
	}

	return nil, nil
}

func buildContext(count int, pattern, source string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Sense has %d indexed symbol(s) matching %q. Consider Sense MCP tools instead of %s:\n", count, pattern, source)
	fmt.Fprintf(&sb, "- sense_graph symbol=%q for callers/callees\n", pattern)
	fmt.Fprintf(&sb, "- sense_search query=%q for semantic matches\n", pattern)
	sb.WriteString("(The Sense tools are loaded and callable now.)")
	return sb.String()
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

// extractBashPattern extracts the search pattern from grep, rg, or ag
// commands. Returns "" for non-search commands so the hook is a no-op.
// Returns "" when -e/-f is used (explicit pattern flags, often regex).
// Stops at shell operators (|, ;, &&, ||) to avoid treating the next
// command's arguments as a pattern.
func extractBashPattern(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}

	base := fields[0]
	if base != "grep" && base != "rg" && base != "ag" {
		return ""
	}

	for i := 1; i < len(fields); i++ {
		arg := fields[i]

		if arg == "|" || arg == ";" || arg == "&&" || arg == "||" {
			break
		}
		if arg == "-e" || arg == "-f" {
			return ""
		}
		if strings.HasPrefix(arg, "-") {
			if needsValue(arg) && i+1 < len(fields) {
				i++
			}
			continue
		}
		return strings.Trim(arg, "'\"")
	}
	return ""
}

func needsValue(flag string) bool {
	switch flag {
	case "-m", "--max-count", "-A", "-B", "-C", "--include", "--exclude", "--exclude-dir", "-t", "--type":
		return true
	}
	return false
}

func isSymbolShaped(pattern string) bool {
	if len(pattern) < 2 {
		return false
	}
	if strings.Contains(pattern, "/") {
		return false
	}
	for _, c := range pattern {
		switch c {
		case '*', '+', '|', '(', ')', '[', ']', '{', '}', '?', '^', '$', '\\':
			return false
		}
	}
	return !hasCodeExtension(pattern)
}

func hasExplorationKeyword(prompt string) bool {
	lower := strings.ToLower(prompt)
	for _, phrase := range explorationPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func isMultiWordPattern(pattern string) bool {
	return strings.Contains(pattern, " ") && len(pattern) >= 4
}

func isExplorationCommand(cmd string) bool {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}

	switch fields[0] {
	case "find", "head", "cat", "tail", "less", "more", "wc":
		return argsHaveCodeExtension(fields)
	}
	return false
}

func argsHaveCodeExtension(fields []string) bool {
	for _, f := range fields {
		if strings.HasPrefix(f, "-") {
			continue
		}
		f = strings.Trim(f, "'\"")
		if hasCodeExtension(f) {
			return true
		}
	}
	return false
}

func hasCodeExtension(s string) bool {
	for _, ext := range codeExtensions {
		if strings.HasSuffix(s, ext) {
			return true
		}
	}
	return false
}
