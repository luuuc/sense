// Package cli implements the `sense` subcommands that humans, scripts
// and CI pipelines invoke. The CLI calls the same engines
// (internal/blast, internal/sqlite, future internal/search) as the MCP
// server in pitch 01-05 — both interfaces marshal through
// internal/mcpio so the documented JSON contract stays byte-for-byte
// identical across surfaces.
//
// Each subcommand is exposed as `Run<Name>(args []string, io IO) int`.
// The caller in cmd/sense/main.go passes os.Args[2:] plus a bundle of
// stdio handles and a working-directory hint; the runner parses flags,
// executes, and returns an exit code. Exit codes follow the table in
// .doc/definition/06-mcp-and-cli.md.
package cli

import (
	"flag"
	"io"
	"os"
)

// Exit codes. Pinned to .doc/definition/06-mcp-and-cli.md so scripts
// can switch on them without reading Sense source.
const (
	ExitSuccess      = 0
	ExitGeneralError = 1
	ExitSymbolIssue  = 2 // symbol not found OR ambiguous
	ExitIndexMissing = 3 // no .sense/index.db
	ExitIndexCorrupt = 4 // schema mismatch or unreadable DB
)

// IO bundles the streams a subcommand writes to. Extracted to a struct
// so tests can capture stdout/stderr without touching globals and so a
// future --quiet flag has one place to swap sinks.
//
// Stdout carries the command's payload (human output or JSON). Stderr
// carries diagnostics (disambiguation lists, errors, warnings). A
// caller redirecting `--json | jq` never sees a stray warning
// corrupting the pipe.
type IO struct {
	Stdout io.Writer
	Stderr io.Writer
	// Dir is the working directory Sense should treat as the project
	// root — where .sense/index.db lives and where git diff resolves
	// paths against. Defaults to "." at the main.go layer so runners
	// can assume Dir is always set.
	Dir string
}

// DefaultIO returns an IO wired to the process's real streams and the
// current working directory — the production default the cmd/sense
// entrypoint uses. Tests construct their own IO with bytes.Buffer
// sinks and a tempdir.
func DefaultIO() IO {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	return IO{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Dir:    wd,
	}
}

// parseInterleaved runs fs.Parse repeatedly so positional arguments
// can appear before, after, or between flags — the shell-friendly
// shape users expect (`sense graph Adapter --json` and `sense graph
// --json Adapter` must behave identically). Go's stdlib
// flag.Parse stops at the first non-flag arg, so we drain positionals
// one at a time and keep parsing until no args remain.
//
// Returned slice is the positional args in source order.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
	return positional, nil
}
