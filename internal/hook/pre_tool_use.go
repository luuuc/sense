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
	default:
		return handleGrepGlob(ctx, req, adapter)
	}
}

func handleGrepGlob(ctx context.Context, req preToolUseInput, adapter *sqlite.Adapter) (any, error) {
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
			return advise(fmt.Sprintf(
				"Consider sense_search query=%q for semantic code search. "+
					"Load tools first: %s", pattern, toolSearchCmd)), nil
		}
		return nil, nil
	}

	return denyOrAdvise(ctx, adapter, len(symbols), pattern, "grep"), nil
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
		"This project has a Sense index (%d symbols). Use Sense MCP tools instead of agents for codebase understanding:\n"+
			"- sense_graph for symbol relationships (callers, callees, inheritance)\n"+
			"- sense_search for semantic code search\n"+
			"- sense_blast for impact analysis\n"+
			"- sense_conventions for project patterns\n"+
			"Load tools first: %s",
		count, toolSearchCmd,
	)
	return deny(reason), nil
}

func handleBash(ctx context.Context, req preToolUseInput, adapter *sqlite.Adapter) (any, error) {
	cmd := req.Input.Command

	pattern := extractBashPattern(cmd)
	if pattern != "" && isSymbolShaped(pattern) {
		symbols, err := adapter.Query(ctx, index.Filter{Name: pattern, Limit: 5})
		if err == nil && len(symbols) > 0 {
			return denyOrAdvise(ctx, adapter, len(symbols), pattern, "bash grep"), nil
		}
		if isMultiWordPattern(pattern) {
			return advise(fmt.Sprintf(
				"Consider sense_search query=%q for semantic code search. "+
					"Load tools first: %s", pattern, toolSearchCmd)), nil
		}
	}

	if isExplorationCommand(cmd) {
		return advise(
			"Sense can answer codebase understanding questions without reading individual files. "+
				"Load tools first: "+toolSearchCmd), nil
	}

	return nil, nil
}

// denyOrAdvise blocks the tool call when the index is fresh, or adds
// advisory context when it is stale (> 24h since last scan).
func denyOrAdvise(ctx context.Context, adapter *sqlite.Adapter, count int, pattern, source string) any {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Sense has %d indexed symbol(s) matching %q. Use Sense MCP tools instead of %s:\n", count, pattern, source)
	fmt.Fprintf(&sb, "- sense_graph symbol=%q for callers/callees\n", pattern)
	fmt.Fprintf(&sb, "- sense_search query=%q for semantic matches\n", pattern)
	fmt.Fprintf(&sb, "Load tools first: %s", toolSearchCmd)

	if age := indexAge(ctx, adapter); age > staleThreshold {
		return &hookResponse{AdditionalContext: sb.String()}
	}
	return deny(sb.String())
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
