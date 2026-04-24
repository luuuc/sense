package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luuuc/sense/internal/sqlite"
)

func handlePreCompact(ctx context.Context, _ json.RawMessage, adapter *sqlite.Adapter, _ string) (any, error) {
	db := adapter.DB()

	var symbolCount, edgeCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_symbols`).Scan(&symbolCount); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_edges`).Scan(&edgeCount); err != nil {
		return nil, err
	}

	if symbolCount == 0 {
		return nil, nil
	}

	hubs, err := topHubs(ctx, adapter, 5)
	if err != nil {
		return nil, err
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Sense Index Summary\nThis project has a Sense index with %d symbols and %d edges.\n", symbolCount, edgeCount)

	if len(hubs) > 0 {
		sb.WriteString("Key structural hubs:\n")
		for _, h := range hubs {
			fmt.Fprintf(&sb, "- %s (%d callers, %d callees)\n", h.name, h.inbound, h.outbound)
		}
	}

	sb.WriteString("\nUse Sense MCP tools (sense_graph, sense_search, sense_blast, sense_conventions) instead of grep/glob for structural questions.")

	return &messageResponse{Message: sb.String()}, nil
}

type hub struct {
	name     string
	inbound  int
	outbound int
}

func topHubs(ctx context.Context, adapter *sqlite.Adapter, limit int) ([]hub, error) {
	db := adapter.DB()

	const q = `
		SELECT s.qualified,
		       COALESCE(i.cnt, 0) AS inbound,
		       COALESCE(o.cnt, 0) AS outbound,
		       COALESCE(i.cnt, 0) + COALESCE(o.cnt, 0) AS total
		FROM sense_symbols s
		LEFT JOIN (
			SELECT target_id, COUNT(*) AS cnt FROM sense_edges GROUP BY target_id
		) i ON i.target_id = s.id
		LEFT JOIN (
			SELECT source_id, COUNT(*) AS cnt FROM sense_edges GROUP BY source_id
		) o ON o.source_id = s.id
		WHERE total > 0
		ORDER BY total DESC
		LIMIT ?`

	rows, err := db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var hubs []hub
	for rows.Next() {
		var h hub
		var total int
		if err := rows.Scan(&h.name, &h.inbound, &h.outbound, &total); err != nil {
			return nil, err
		}
		hubs = append(hubs, h)
	}
	return hubs, rows.Err()
}
