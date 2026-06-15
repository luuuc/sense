package hook

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/luuuc/sense/internal/freshen"
	"github.com/luuuc/sense/internal/sqlite"
)

// reconcileTimeout bounds the session-start catch-up so a slow re-index can
// never blow the hook's overall timeout.
const reconcileTimeout = 4 * time.Second

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

func checkFreshness(ctx context.Context, db *sql.DB, dir string) string {
	rows, err := db.QueryContext(ctx, `SELECT path, indexed_at FROM sense_files`)
	if err != nil {
		return ""
	}
	defer func() { _ = rows.Close() }()

	var stale, deleted int
	for rows.Next() {
		var path, indexedAtStr string
		if err := rows.Scan(&path, &indexedAtStr); err != nil {
			continue
		}
		indexedAt, err := time.Parse(time.RFC3339Nano, indexedAtStr)
		if err != nil {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, path))
		if err != nil {
			deleted++
			continue
		}
		if info.ModTime().After(indexedAt) {
			stale++
		}
	}
	if rows.Err() != nil {
		return ""
	}
	if stale == 0 && deleted == 0 {
		return "Index is current."
	}
	var parts []string
	if stale > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", stale))
	}
	if deleted > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", deleted))
	}
	return fmt.Sprintf("Index is stale (%s since last scan).", strings.Join(parts, ", "))
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

	// Catch up on drift that accumulated while no watcher was running (the
	// editor was closed, edits or a pull happened in between), so the first
	// query sees a current index. Respect the single-writer lock: if a
	// `sense mcp` server is already watching this repo, it owns indexing and
	// we skip — its watcher and read-repair keep the index fresh.
	if release, ok := freshen.AcquireWriterLock(dir); ok {
		rctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
		_, _, _ = freshen.ReconcileDrift(rctx, adapter, dir)
		cancel()
		release()
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

	var lastScanStr string
	row := db.QueryRowContext(ctx, `SELECT MAX(indexed_at) FROM sense_files`)
	if err := row.Scan(&lastScanStr); err != nil || lastScanStr == "" {
		lastScanStr = ""
	}

	now := time.Now()
	scanAge := "unknown"
	freshness := ""
	if lastScanStr != "" {
		scanAge = formatScanAge(lastScanStr, now)
		freshness = checkFreshness(ctx, db, dir)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Sense index: %d symbols, %d edges, %d languages (%s). Last scan: %s.", symbolCount, edgeCount, len(langs), strings.Join(langs, ", "), scanAge)
	if freshness != "" {
		fmt.Fprintf(&sb, " %s", freshness)
	}
	sb.WriteByte('\n')
	// A light, task-layer hint — NOT an imperative gate. The Sense tools are
	// pre-loaded (alwaysLoad in .mcp.json), so there is no ToolSearch hop to
	// require and nothing to "load first". We just point at the resolved code
	// map and let the model reach for it; grep/ls stay available for the cases
	// where they genuinely fit.
	sb.WriteString("A resolved code map for this repo is already loaded: sense_search, sense_graph, sense_blast, sense_conventions are callable now (no setup call needed).\n")
	sb.WriteString("For structural questions — who calls X, what breaks if X changes, how symbols relate, what conventions to follow — these are faster and more complete than grep/glob/file-walking.\n")
	sb.WriteString("Index health was verified at session start, so there is no need to call sense_status.")

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
