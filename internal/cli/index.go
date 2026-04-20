package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// Sentinel errors the command runners map to exit codes 3 and 4 per
// .doc/definition/06-mcp-and-cli.md. ErrIndexMissing fires before
// any SQL — we check the file exists and bail with a helpful hint.
// ErrIndexCorrupt fires when SQLite opens but rejects the schema.
var (
	ErrIndexMissing = errors.New("index not found")
	ErrIndexCorrupt = errors.New("index corrupt")
)

// OpenIndex opens <dir>/.sense/index.db. A missing file returns
// ErrIndexMissing; a present file that SQLite refuses returns
// ErrIndexCorrupt. Callers translate each sentinel to the correct
// exit code.
//
// A readability probe runs between stat and sqlite.Open: if the
// file exists but cannot be opened for read (permission denied,
// locked by another process), that error falls through unwrapped
// so handleIndexOpenError's default branch maps it to exit 1 — a
// permission problem is not "corrupt", and the exit-4 hint
// ("rebuild with sense scan --force") would mislead the user.
//
// The caller owns the returned Adapter and must Close it.
func OpenIndex(ctx context.Context, dir string) (*sqlite.Adapter, error) {
	if dir == "" {
		dir = "."
	}
	path := filepath.Join(dir, ".sense", "index.db")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w at %s", ErrIndexMissing, path)
		}
		return nil, fmt.Errorf("stat index: %w", err)
	}
	// Readability probe: separates permission/locking failures from
	// genuine "SQLite rejects the bytes" corruption below.
	if f, err := os.Open(path); err != nil {
		return nil, fmt.Errorf("open index %s: %w", path, err)
	} else {
		_ = f.Close()
	}
	adapter, err := sqlite.Open(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIndexCorrupt, err)
	}
	return adapter, nil
}

// collectFileIDs walks a SymbolContext and returns the unique file
// ids referenced by the subject and every edge endpoint. Used to
// batch the follow-up file-path lookup into one query.
func CollectFileIDs(sc *model.SymbolContext) []int64 {
	seen := map[int64]struct{}{sc.File.ID: {}}
	ids := []int64{sc.File.ID}
	for _, e := range sc.Outbound {
		if _, ok := seen[e.Target.FileID]; !ok {
			seen[e.Target.FileID] = struct{}{}
			ids = append(ids, e.Target.FileID)
		}
	}
	for _, e := range sc.Inbound {
		if _, ok := seen[e.Target.FileID]; !ok {
			seen[e.Target.FileID] = struct{}{}
			ids = append(ids, e.Target.FileID)
		}
	}
	return ids
}

// loadFilePaths bulk-fetches path strings for the given file ids,
// keyed for O(1) lookup in the mcpio builders. Chunked on
// SQLITE_MAX_VARIABLE_NUMBER (999) to stay safe on wide graph
// responses (a subject with hundreds of edges could otherwise hit
// the limit in one query).
func LoadFilePaths(ctx context.Context, db *sql.DB, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := strings.Repeat("?,", len(batch))
		placeholders = placeholders[:len(placeholders)-1]
		q := `SELECT id, path FROM sense_files WHERE id IN (` + placeholders + `)`
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("load file paths: %w", err)
		}
		for rows.Next() {
			var id int64
			var path string
			if err := rows.Scan(&id, &path); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan file row: %w", err)
			}
			out[id] = path
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}
