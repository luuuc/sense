package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/luuuc/sense/internal/sqlite"
)

func handleSessionStart(ctx context.Context, _ json.RawMessage, adapter *sqlite.Adapter, _ string) (any, error) {
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

	rows, err := db.QueryContext(ctx, `SELECT DISTINCT language FROM sense_files WHERE language != '' ORDER BY language`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var langs []string
	for rows.Next() {
		var lang string
		if err := rows.Scan(&lang); err != nil {
			return nil, err
		}
		langs = append(langs, lang)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var lastScan string
	row := db.QueryRowContext(ctx, `SELECT MAX(indexed_at) FROM sense_files`)
	if err := row.Scan(&lastScan); err == nil && lastScan != "" {
		if t, err := time.Parse(time.RFC3339Nano, lastScan); err == nil {
			age := time.Since(t).Truncate(time.Minute)
			if age < time.Minute {
				lastScan = "just now"
			} else {
				lastScan = fmt.Sprintf("%s ago", age)
			}
		}
	} else {
		lastScan = "unknown"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Sense index: %d symbols, %d edges, %d languages (%s). Last scan: %s.\n", symbolCount, edgeCount, len(langs), strings.Join(langs, ", "), lastScan)
	fmt.Fprintf(&sb, "REQUIRED: Run %s now to load Sense tools.\n", toolSearchCmd)
	sb.WriteString("Use Sense MCP tools for ALL structural questions — do not use grep, glob, or exploration agents.")

	return &messageResponse{Message: sb.String()}, nil
}
