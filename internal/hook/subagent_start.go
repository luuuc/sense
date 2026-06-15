package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luuuc/sense/internal/sqlite"
)

func handleSubagentStart(ctx context.Context, _ json.RawMessage, adapter *sqlite.Adapter, _ string) (any, error) {
	var symbolCount int
	if err := adapter.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_symbols`).Scan(&symbolCount); err != nil || symbolCount == 0 {
		return nil, nil
	}

	var edgeCount int
	if err := adapter.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_edges`).Scan(&edgeCount); err != nil {
		edgeCount = 0
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "This project has a Sense index (%d symbols, %d edges).\n\n", symbolCount, edgeCount)
	sb.WriteString("The Sense tools are loaded and callable now — prefer them over grep/find/file-walking for structural questions:\n")
	sb.WriteString("- Who calls X? What does X depend on? → sense_graph\n")
	sb.WriteString("- Find code related to a concept → sense_search\n")
	sb.WriteString("- What breaks if I change X? → sense_blast\n")
	sb.WriteString("- What patterns does this project follow? → sense_conventions\n")

	return &hookResponse{AdditionalContext: sb.String()}, nil
}
