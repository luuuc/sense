package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/luuuc/sense/internal/sqlite"
)

func formatScanAge(lastScan string, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, lastScan)
	if err != nil {
		return "unknown"
	}
	age := now.Sub(t).Truncate(time.Minute)
	if age < time.Minute {
		return "just now"
	}
	return fmt.Sprintf("%s ago", age)
}

func handleSessionStart(ctx context.Context, _ json.RawMessage, adapter *sqlite.Adapter, dir string) (any, error) {
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
	if err := row.Scan(&lastScan); err != nil || lastScan == "" {
		lastScan = "unknown"
	} else {
		lastScan = formatScanAge(lastScan, time.Now())
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Sense index: %d symbols, %d edges, %d languages (%s). Last scan: %s.\n", symbolCount, edgeCount, len(langs), strings.Join(langs, ", "), lastScan)
	fmt.Fprintf(&sb, "REQUIRED: Your FIRST tool call MUST be %s to load Sense tools.\n", toolSearchCmd)
	sb.WriteString("Use Sense MCP tools for ALL codebase understanding — do not use grep, glob, Read, Bash, or agents before loading Sense.")

	summaryPath := filepath.Join(dir, ".sense", "summary.md")
	if summary, err := os.ReadFile(summaryPath); err == nil && len(bytes.TrimSpace(summary)) > 0 {
		sb.WriteString("\n\n--- Codebase Summary ---\n")
		sb.Write(summary)
		if !bytes.HasSuffix(summary, []byte("\n")) {
			sb.WriteByte('\n')
		}
		sb.WriteString("--- End Summary ---")
	}

	return &messageResponse{Message: sb.String()}, nil
}
