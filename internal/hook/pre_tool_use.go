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

	// extractBashPattern finds the grep/rg/ag search even behind a `cd … &&`
	// prefix or inside a pipeline, and keeps quoted multi-word patterns intact.
	// symbolFromPattern then reduces a definition-form pattern ("func Open") to
	// the bare symbol name before the index lookup.
	symbol := symbolFromPattern(extractBashPattern(cmd))
	if symbol != "" && isSymbolShaped(symbol) {
		symbols, err := adapter.Query(ctx, index.Filter{Name: symbol, Limit: 5})
		if err == nil && len(symbols) > 0 {
			return nudge(
				fmt.Sprintf("Sense has %d indexed symbol(s) matching %q — consider sense_graph or sense_search instead of bash grep.", len(symbols), symbol),
				buildContext(len(symbols), symbol, "bash grep"),
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

// extractBashPattern extracts the search pattern from a grep, rg, or ag
// command. It locates the search even when it is not the first word — behind a
// `cd … &&`/`;` prefix or as a pipeline stage — but treats a grep that only
// consumes piped input (`… | grep`) as a filter on another command's output,
// not a code search, and skips it. Quotes are honoured, so a multi-word pattern
// like "func Open" survives intact. Returns "" for non-search commands and when
// -e/-f is used (explicit pattern flags, often regex).
func extractBashPattern(cmd string) string {
	toks := shellTokens(cmd)
	prevOp := ";" // start of the line acts as a statement boundary
	for i := 0; i < len(toks); {
		t := toks[i]
		if t.op {
			prevOp = t.val
			i++
			continue
		}
		// A search command only counts when it starts a statement; a grep right
		// after a pipe is filtering another command's output, not searching code.
		standalone := prevOp != "|"
		if standalone && isSearchBinary(baseName(t.val)) {
			j := i + 1
			var args []string
			for j < len(toks) && !toks[j].op {
				args = append(args, toks[j].val)
				j++
			}
			if p := patternFromArgs(args); p != "" {
				return p
			}
			i = j
			continue
		}
		// Skip the rest of this command's words; the next operator updates prevOp.
		j := i + 1
		for j < len(toks) && !toks[j].op {
			j++
		}
		i = j
	}
	return ""
}

// patternFromArgs returns the first non-flag argument (the search pattern) from
// a search command's arguments, or "" when an explicit -e/-f pattern flag is
// present (those are often regex, which isSymbolShaped would reject anyway).
func patternFromArgs(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-e" || arg == "-f" {
			return ""
		}
		if strings.HasPrefix(arg, "-") {
			if needsValue(arg) && i+1 < len(args) {
				i++
			}
			continue
		}
		return arg
	}
	return ""
}

func isSearchBinary(base string) bool {
	switch base {
	case "grep", "egrep", "fgrep", "rg", "ag":
		return true
	}
	return false
}

// baseName strips a leading path so /usr/bin/grep is recognised as grep.
func baseName(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// defKeywords are the definition-introducing keywords across the languages
// Sense indexes; a grep for "<keyword> Name" is a lookup of the symbol Name.
var defKeywords = map[string]bool{
	"func": true, "def": true, "class": true, "type": true,
	"interface": true, "struct": true, "module": true,
	"fn": true, "impl": true, "trait": true, "const": true, "var": true,
}

// symbolFromPattern reduces a grep pattern to the symbol name worth looking up.
// A single token is returned as-is ("ApplyBlastBudget"). A definition-form
// pattern returns the declared name ("func Open" -> "Open", "class User" ->
// "User"). Any other multi-word pattern is a literal string search, which is
// grep's job, so it returns "".
func symbolFromPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return ""
	}
	fields := strings.Fields(pattern)
	if len(fields) == 1 {
		return pattern
	}
	if len(fields) == 2 && defKeywords[fields[0]] {
		return strings.Trim(fields[1], "*()")
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

// shTok is a shell token: an operator (|, ||, &&, &, ;) or a word.
type shTok struct {
	op  bool
	val string
}

// shellTokens splits a command line into words and shell operators, honouring
// single and double quotes so a quoted pattern stays a single word. It is a
// pragmatic tokenizer, not a full shell parser: it covers the command shapes
// agents actually emit (cd prefixes, pipelines, &&/||/; chains) well enough to
// find the search command and its pattern.
//
// Deliberately NOT handled, by design: backslash escapes outside quotes,
// command substitution ($(...) and backticks), variable expansion ($VAR), and
// glob expansion. These cause at most a missed nudge, never a wrong block, so
// chasing them is not worth the complexity. Unterminated quotes are tolerated:
// the quoted run simply extends to end-of-input.
func shellTokens(cmd string) []shTok {
	var toks []shTok
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			toks = append(toks, shTok{val: b.String()})
			b.Reset()
		}
	}
	for i := 0; i < len(cmd); {
		c := cmd[i]
		switch c {
		case ' ', '\t', '\n', '\r':
			flush()
			i++
		case '\'':
			j := i + 1
			for j < len(cmd) && cmd[j] != '\'' {
				b.WriteByte(cmd[j])
				j++
			}
			i = j + 1
		case '"':
			j := i + 1
			for j < len(cmd) && cmd[j] != '"' {
				if cmd[j] == '\\' && j+1 < len(cmd) {
					b.WriteByte(cmd[j+1])
					j += 2
					continue
				}
				b.WriteByte(cmd[j])
				j++
			}
			i = j + 1
		case '|':
			flush()
			if i+1 < len(cmd) && cmd[i+1] == '|' {
				toks = append(toks, shTok{op: true, val: "||"})
				i += 2
			} else {
				toks = append(toks, shTok{op: true, val: "|"})
				i++
			}
		case '&':
			flush()
			if i+1 < len(cmd) && cmd[i+1] == '&' {
				toks = append(toks, shTok{op: true, val: "&&"})
				i += 2
			} else {
				toks = append(toks, shTok{op: true, val: "&"})
				i++
			}
		case ';':
			flush()
			toks = append(toks, shTok{op: true, val: ";"})
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	flush()
	return toks
}
