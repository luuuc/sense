package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luuuc/sense/internal/sqlite"
)

func handleSubagentStart(ctx context.Context, _ json.RawMessage, adapter *sqlite.Adapter, _ string) (any, error) {
	var count int
	if err := adapter.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_symbols`).Scan(&count); err != nil || count == 0 {
		return nil, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "IMPORTANT: This project has a Sense index (%d symbols). ", count)
	sb.WriteString("You cannot call Sense MCP tools directly, but the parent agent can. ")
	sb.WriteString("For ANY task involving codebase understanding — finding callers, tracing dependencies, exploring architecture, researching code, or understanding how code works — ")
	sb.WriteString("report back to the parent agent and recommend it use:\n")
	sb.WriteString("- sense_graph for symbol relationships (callers, callees, inheritance)\n")
	sb.WriteString("- sense_search for semantic code search\n")
	sb.WriteString("- sense_blast for impact analysis\n")
	sb.WriteString("- sense_conventions for project patterns\n")
	sb.WriteString("Do not attempt to answer codebase questions with grep or file reading — Sense is faster and more complete.")

	return &hookResponse{AdditionalContext: sb.String()}, nil
}
