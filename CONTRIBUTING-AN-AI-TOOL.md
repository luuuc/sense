# Adding (or tuning) an AI tool integration

This guide walks you through teaching `sense setup` about a new AI coding tool,
or changing what an existing one is told. It is written to be followed literally,
by an AI agent or a human, with no prior knowledge of the codebase.

`sense setup` writes integration files into a project so an AI tool discovers the
Sense MCP server and prefers it over grep and file-walking. Each supported tool
gets an **MCP server entry** (so the tool can call Sense), an **instructions
file** (so the model knows to), and optionally **hooks, skills, and agents**.

There are two tasks this guide covers, in rising order of effort:

- **Tuning what tools are told.** A one-place edit to the shared guidance, or to
  one tool's files. No new tool. Start here if the integration exists and you
  only want to improve the prompt.
- **Adding a new tool.** A new per-tool file plus one registry line. The registry
  is the single source of truth, so there are no `switch` statements to hunt
  down.

Everything lives in [`internal/setup/`](internal/setup/).

---

## Architecture in one screen

The set of tools Sense configures is a **registry**
([`internal/setup/registry.go`](internal/setup/registry.go)): a slice of `tool`
values, each pairing a detector with a configurer.

```go
type tool struct {
    id          Tool
    displayName string
    detect      func() DetectResult        // is this tool installed?
    configure   func(root string) (*ToolResult, error) // write its files
    currentEnv  []string                   // env vars that mean "running inside this tool now"
}
```

`currentEnv` feeds `DetectCurrent`, which `sense scan` uses on first run to
configure only the tool the user is currently inside. It is optional: a tool with
no live-session env var still detects and configures normally, it just never wins
`DetectCurrent` (which falls back to Claude Code).

Every public entry point loops over that slice, so the registry is the only place
the tool list is enumerated:

- `AllTools()` and `DetectAll()` iterate the registry.
- `Detect(t)` and `configureTool(root, t)` call `lookup(t)`, then the matching
  field.
- `ParseTools("a,b")` validates each name against the registry.
- `Tool.DisplayName()` returns `lookup(t).displayName`.

Each tool's detector, configurer, and file writers live in **one file named for
the tool** ([`claude.go`](internal/setup/claude.go),
[`cursor.go`](internal/setup/cursor.go), [`codex.go`](internal/setup/codex.go),
[`opencode.go`](internal/setup/opencode.go)). Read `claude.go` first: it is the
fullest example (MCP entry, settings and hooks, guidance, skills, agents).
`cursor.go` is the smallest (MCP entry plus one guidance file).

The shared pieces a tool composes from:

| Piece | Where | What it does |
|---|---|---|
| `Tool` constant | [`detect.go`](internal/setup/detect.go) | the tool's id, also its `--tools` name |
| `guidanceMarkdown` | [`marker.go`](internal/setup/marker.go) | **the single guidance source** every instructions file gets |
| `writeMarkerFile` | [`marker.go`](internal/setup/marker.go) | idempotent merge of a marker-delimited section into a Markdown file |
| `readJSONFile` / `writeJSONFile` | [`merge.go`](internal/setup/merge.go) | read, deep-merge, and write a JSON config without clobbering other tools' entries |
| `mergeHooks` / `mergePermissions` | [`merge.go`](internal/setup/merge.go) | Claude-style settings merges |
| `skills` / `agents` | [`skills.go`](internal/setup/skills.go), [`agents.go`](internal/setup/agents.go) | the shared skill and agent file contents |
| `mcpio.ServerInstructions` | [`internal/mcpio/types.go`](internal/mcpio/types.go) | the MCP protocol-level instruction string |

**The two guidance surfaces, and why there are exactly two.** There is one
in-repo prompt (`guidanceMarkdown`, the Markdown a model reads from
CLAUDE.md, .cursorrules, or AGENTS.md) and one protocol-level prompt
(`mcpio.ServerInstructions`, the `serverInstructions` string an MCP client
receives at connect time). They live in different packages on purpose:
`ServerInstructions` is sent by the running `sense mcp` server too, so it cannot
live in `setup`. Keep them aligned in spirit, each edited in its own one place.
There is no third copy. If you find yourself pasting guidance into a per-tool
file, stop and reference `guidanceMarkdown` instead.

**The idempotency contract.** Running `sense setup` twice must produce the same
files. JSON configs are deep-merged (an existing `sense` entry is replaced, other
servers untouched), Markdown sections are replaced between `<!-- sense:start -->`
and `<!-- sense:end -->` markers, and skills and agents are overwritten. Your
`configure` must hold this line; the tests below enforce it.

### Before you start

```bash
git clone https://github.com/luuuc/sense.git
cd sense
./scripts/fetch-deps.sh --local   # ONNX runtime + model, required once
make build
```

---

## Tuning what tools are told (no new tool)

- **Change the guidance every tool receives** (the table of tools, the MUST-NOT
  rules, when not to use Sense): edit `guidanceMarkdown` in
  [`marker.go`](internal/setup/marker.go). One edit, every tool updated on the
  next `sense setup`. Keep it **tool-agnostic**: do not name one tool's UI. A
  test (`TestGuidanceMarkdownContent`) fails if a tool-specific phrase leaks in,
  and another (`TestGuidanceMarkdownIsSingleSource`) asserts every instructions
  file carries this exact block.
- **Change the MCP protocol instructions**: edit `mcpio.ServerInstructions` in
  [`internal/mcpio/types.go`](internal/mcpio/types.go).
- **Change a skill or agent's content**: edit the `skills` or `agents` slices in
  [`skills.go`](internal/setup/skills.go) or [`agents.go`](internal/setup/agents.go).
- **Change only one tool's files** (for example, add a hook to Claude Code): edit
  that tool's `configure` function in its file.

Then regenerate and eyeball the output:

```bash
go test ./internal/setup/
go build -o /tmp/sense . && (cd "$(mktemp -d)" && /tmp/sense setup --tools claude-code && cat CLAUDE.md)
```

---

## Adding a new tool

The running example is **Aider**. Substitute your tool's real name and config
paths. The one thing you must research up front is **how your tool consumes an
MCP server and project instructions**, because that decides which files
`configure` writes. The three existing tools already cover the common shapes;
find which yours matches.

### Step 1. Research the tool's config surface

Answer three questions from the tool's own documentation before writing code:

1. **How does it register an MCP server?** A JSON file with an `mcpServers`
   object (Claude Code's `.mcp.json`, Cursor's `.cursor/mcp.json`)? A different
   key or schema (OpenCode's `opencode.json` uses an `mcp` key with
   `{type, command, enabled}`)? A non-JSON format? If the tool has no MCP
   support, it cannot use Sense's tools and is not a candidate.
2. **Where does it read project instructions?** A Markdown file the model is
   given each session (`CLAUDE.md`, `.cursorrules`, `AGENTS.md`). Many tools now
   read `AGENTS.md`; reuse it if so.
3. **Does it support hooks, skills, or agents?** Optional. Claude Code is the only
   current tool wiring hooks; skills are written for Claude Code and OpenCode.

If two of your answers match an existing tool, your `configure` is mostly a copy
of that tool's writers with different paths.

### Step 2. Add the `Tool` constant

In [`internal/setup/detect.go`](internal/setup/detect.go), add a constant. The
string value **is** the `--tools` flag name, so make it the canonical lowercase
id:

```go
const (
    ToolClaudeCode Tool = "claude-code"
    // ...
    ToolAider      Tool = "aider"
)
```

`ParseTools`, the `--tools` help text, and the error message all derive from the
registry, so this is the only constant you add.

### Step 3. Create the tool's file

Create `internal/setup/aider.go` holding **the detector and the configurer**,
mirroring [`cursor.go`](internal/setup/cursor.go) (the smallest example) or
[`claude.go`](internal/setup/claude.go) (the fullest):

```go
package setup

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
)

// detectAider looks for evidence that Aider is installed. The signals run
// most-specific first: a session env var, the binary on PATH, a config dir.
func detectAider() DetectResult {
    r := DetectResult{Tool: ToolAider}
    if _, err := exec.LookPath("aider"); err == nil {
        r.Found = true
        r.Evidence = "aider on PATH"
        return r
    }
    if home, err := os.UserHomeDir(); err == nil {
        if _, err := os.Stat(filepath.Join(home, ".aider")); err == nil {
            r.Found = true
            r.Evidence = "~/.aider/ directory"
            return r
        }
    }
    return r
}

// configureAider writes Aider's MCP config and the AGENTS.md guidance it reads.
// Each writer returns (wrote bool, err); append the relative path on success so
// the run summary lists it.
func configureAider(root string) (*ToolResult, error) {
    tr := &ToolResult{Tool: ToolAider}

    if wrote, err := writeMCPJSON(root); err != nil {       // reuse the .mcp.json writer if the schema matches
        return nil, fmt.Errorf("write .mcp.json: %w", err)
    } else if wrote {
        tr.Files = append(tr.Files, ".mcp.json")
    }

    if wrote, err := writeAgentsMD(root); err != nil {      // reuse the AGENTS.md writer (guidanceMarkdown)
        return nil, fmt.Errorf("write AGENTS.md: %w", err)
    } else if wrote {
        tr.Files = append(tr.Files, "AGENTS.md")
    }

    return tr, nil
}
```

**Reuse before you write.** If your tool's MCP schema matches `.mcp.json`, call
`writeMCPJSON`. If it reads `AGENTS.md`, call `writeAgentsMD`. Only write a new
`writeAiderJSON` when the schema genuinely differs (as `writeOpencodeJSON` does
for OpenCode's `mcp`-keyed shape). When you do, always go through
`readJSONFile` and `writeJSONFile` so you deep-merge rather than clobber a config
the user already has.

**Guidance is never inlined.** A guidance file is always
`writeMarkerFile(path, guidanceMarkdown)`. Do not paste the guidance text into
your file; the single-source test will fail and the next person editing the
prompt will miss your copy.

### Step 4. Register the tool

Add one line to `registry()` in
[`internal/setup/registry.go`](internal/setup/registry.go), in display order:

```go
func registry() []tool {
    return []tool{
        {id: ToolClaudeCode, displayName: "Claude Code", detect: detectClaudeCode, configure: configureClaudeCode, currentEnv: []string{"CLAUDE_CODE"}},
        // ...
        {id: ToolAider, displayName: "Aider", detect: detectAider, configure: configureAider, currentEnv: []string{"AIDER"}},
    }
}
```

That is the whole wiring. `AllTools`, `DetectAll`, `Detect`, `configureTool`,
`ParseTools`, `DisplayName`, and `DetectCurrent` now all include Aider with no
further edits. Set `currentEnv` to the env var(s) the tool sets in its own
sessions (omit the field if it has none); that is what makes the
"one file plus one registry line" promise literally true, including for
`sense scan`'s current-tool first-run path.

### Step 5. Add tests

The setup tests are plain Go tests in
[`internal/setup/setup_test.go`](internal/setup/setup_test.go) (no golden files).
Add these, mirroring the `Cursor` and `Opencode` cases:

1. **Creates files.** Run `Run(root, buf, &Options{Tools: []Tool{ToolAider}})`,
   then assert each expected path exists.
2. **Idempotent.** Run it twice; assert the instructions file is byte-identical
   and contains exactly one `markerStart`.
3. **Preserves user content.** Pre-write the instructions file with a user
   heading, run setup, assert the heading survives and the Sense section is
   appended once (see `TestOpencodeAgentsMDMergeOverUserContent`).
4. **MCP merge preserves other servers.** Pre-write the JSON config with a
   non-Sense server, run setup, assert both entries exist (see
   `TestMCPJSONPreservesExistingServers`).
5. **Error paths.** Every `return nil, err` branch in your writers needs a test
   that triggers it (make the target path a directory, or pre-write invalid
   JSON). The existing `*Error` tests show the technique; you need these to clear
   the coverage floor.

```bash
go test ./internal/setup/
```

### Step 6. Verify the whole path

```bash
make ci      # build + cover (per-file ≥92% line AND func) + lint
make smoke
```

The per-file coverage gate (see [`CONTRIBUTING.md`](CONTRIBUTING.md)) applies to
`aider.go` automatically: it is a new production file, gated with no allow-list
to opt into. Cover every writer branch, including the error returns.

---

## Reference: the existing tools

| Tool | MCP config | Instructions | Extras |
|---|---|---|---|
| Claude Code | `.mcp.json` (`mcpServers`, with `serverInstructions`) | `CLAUDE.md` | `.claude/settings.json` hooks and perms, skills, agents |
| Cursor | `.cursor/mcp.json` (`mcpServers`) | `.cursorrules` | none |
| Codex CLI | `.codex/config.toml` (`[mcp_servers.sense]`; Codex ignores `.mcp.json`) | `AGENTS.md` | also writes `.mcp.json` for shared-repo consistency |
| OpenCode | `opencode.json` (`mcp` key, `{type, command, enabled}`) | `AGENTS.md` | skills as `.opencode/skills/<name>/SKILL.md` |

---

## When you get stuck

If a step here does not match what you see in the repo, the **doc** is wrong, not
you. Open an issue or PR against this file. This guide is meant to be followable
end-to-end with zero gaps; a gap a newcomer hits is a bug in the guide.
