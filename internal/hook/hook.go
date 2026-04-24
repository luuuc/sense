// Package hook implements the `sense hook` subcommand tree. Each
// handler reads Claude Code's hook JSON from stdin, queries the Sense
// index, and writes a JSON response to stdout.
//
// Handlers are silent on failure: if the index is missing, the query
// fails, or stdin is malformed, they write {} to stdout and exit 0.
// A broken hook must never block the user's workflow.
package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/luuuc/sense/internal/sqlite"
)

// Run dispatches to the named hook handler. It returns an exit code
// (always 0 — hooks must not fail the host tool).
func Run(name string, dir string, stdin io.Reader, stdout io.Writer) int {
	switch name {
	case "pre-tool-use":
		silentRun(dir, stdin, stdout, handlePreToolUse)
	case "pre-compact":
		silentRun(dir, stdin, stdout, handlePreCompact)
	case "subagent-start":
		silentRun(dir, stdin, stdout, handleSubagentStart)
	case "session-start":
		silentRun(dir, stdin, stdout, handleSessionStart)
	default:
		fmt.Fprintf(os.Stderr, "sense hook: unknown hook %q\n", name)
		writeEmpty(stdout)
	}
	return 0
}

// handlerFunc is the signature for individual hook handlers. They
// receive the parsed stdin, a read-only index adapter, and the
// project root. They return the JSON response to write to stdout.
type handlerFunc func(ctx context.Context, input json.RawMessage, adapter *sqlite.Adapter, dir string) (any, error)

// silentRun is the shared wrapper: read stdin, open index, call
// handler, write response. Any error at any stage writes {} and
// returns quietly.
func silentRun(dir string, stdin io.Reader, stdout io.Writer, fn handlerFunc) {
	ctx := context.Background()

	input, err := io.ReadAll(stdin)
	if err != nil {
		writeEmpty(stdout)
		return
	}

	dbPath := indexPath(dir)
	if _, err := os.Stat(dbPath); err != nil {
		writeEmpty(stdout)
		return
	}

	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		writeEmpty(stdout)
		return
	}
	defer func() { _ = adapter.Close() }()

	result, err := fn(ctx, json.RawMessage(input), adapter, dir)
	if err != nil || result == nil {
		writeEmpty(stdout)
		return
	}

	data, err := json.Marshal(result)
	if err != nil {
		writeEmpty(stdout)
		return
	}
	_, _ = stdout.Write(data)
	_, _ = io.WriteString(stdout, "\n")
}

func writeEmpty(w io.Writer) {
	_, _ = io.WriteString(w, "{}\n")
}

func indexPath(dir string) string {
	if env := os.Getenv("SENSE_DIR"); env != "" {
		return filepath.Join(env, "index.db")
	}
	return filepath.Join(dir, ".sense", "index.db")
}
