package cli

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"strings"
)

// gitDiffFiles runs `git diff --name-only <ref>` inside dir and
// returns the paths it prints. One path per line, blanks skipped.
// Errors fire for: not a git repo, bad ref, git exec failure — the
// underlying stderr message is preserved so the CLI can surface
// git's actual complaint ("bad revision 'foo'") instead of a
// generic wrapper.
//
// The ref argument is passed as a positional arg to argv, not
// through a shell — no quoting concerns, no injection surface.
// `--end-of-options` precedes the ref so a ref starting with `-`
// cannot be interpreted as a git option. Defence-in-depth against
// an untrusted caller (future MCP server, git hook, CI job) passing
// e.g. `--upload-pack=...` as a "ref." Available since git 2.24.
func GitDiffFiles(ctx context.Context, dir, ref string) ([]string, error) {
	// Pre-check git availability so a missing binary produces a
	// clear "git not in PATH" error instead of an opaque
	// exec.ErrNotFound wrapped in the generic git-diff message. A
	// user on a system without git sees the cause, not the symptom.
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("sense blast --diff requires git in PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", "--end-of-options", ref)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git diff %s: %s", ref, msg)
	}
	var paths []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if p := strings.TrimSpace(line); p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// symbolsInFiles returns symbol ids for every sense_symbols row
// whose file's path is in paths. Unindexed paths (docs, YAML,
// anything Sense has no extractor for) are silently absent from
// the result — a diff that touches only Markdown produces an
// empty blast, not an error.
//
// The query chunks on SQLITE_MAX_VARIABLE_NUMBER (999) so a diff
// covering hundreds of files stays in one pass of small queries.
func SymbolsInFiles(ctx context.Context, db *sql.DB, paths []string) ([]int64, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	var ids []int64
	const chunk = 500
	for start := 0; start < len(paths); start += chunk {
		end := start + chunk
		if end > len(paths) {
			end = len(paths)
		}
		batch := paths[start:end]
		placeholders := strings.Repeat("?,", len(batch))
		placeholders = placeholders[:len(placeholders)-1]
		q := `SELECT s.id
		      FROM sense_symbols s
		      JOIN sense_files   f ON f.id = s.file_id
		      WHERE f.path IN (` + placeholders + `)
		      ORDER BY s.id`
		args := make([]any, len(batch))
		for i, p := range batch {
			args[i] = p
		}
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("symbols in files: %w", err)
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan symbol id: %w", err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return ids, nil
}
