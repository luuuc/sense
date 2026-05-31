package cli

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// LineRange is an inclusive [Start, End] span of 1-based line numbers in a
// file's post-diff (new) state.
type LineRange struct {
	Start int
	End   int
}

// GitDiffHunks runs `git diff -U0 <ref>` inside dir and returns, per changed
// file path, the line ranges that the diff touched in the file's new state.
// Seeding a diff-blast from these ranges scopes it to the symbols that overlap
// a change, rather than every symbol in a touched file — so a one-line edit to
// a 400-symbol routes file no longer drags all 400 into the blast.
//
// Zero context (`-U0`) keeps each hunk tight to its edited lines. Paths are the
// new-side (`+++ b/<path>`) names with the `b/` prefix stripped, matching the
// repo-relative paths stored in sense_files. Pure deletions (`+++ /dev/null`)
// contribute no ranges — the symbols are gone from the index anyway.
//
// The git invocation is hardened: git is located via LookPath for a clear
// error, the ref is passed positionally after `--end-of-options` so a ref
// starting with `-` cannot be read as an option (defence-in-depth against an
// untrusted caller passing e.g. `--upload-pack=...`; available since git
// 2.24), and git's own stderr is preserved on failure.
func GitDiffHunks(ctx context.Context, dir, ref string) (map[string][]LineRange, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("sense blast --diff requires git in PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "diff", "-U0", "--end-of-options", ref)
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
	return parseDiffHunks(stdout.String()), nil
}

// parseDiffHunks turns unified-diff text (as produced by `git diff -U0`) into
// the per-file changed line ranges. It tracks the current file from `+++`
// header lines and reads each `@@ -a,b +c,d @@` hunk's new-side span.
func parseDiffHunks(diff string) map[string][]LineRange {
	hunks := make(map[string][]LineRange)
	current := ""
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			current = newFilePath(line)
		case strings.HasPrefix(line, "@@ ") && current != "":
			if r, ok := newSideRange(line); ok {
				hunks[current] = append(hunks[current], r)
			}
		}
	}
	return hunks
}

// newFilePath extracts the repo-relative path from a `+++ b/<path>` header.
// `+++ /dev/null` (a deletion) yields "" so its hunks are skipped — the file's
// symbols no longer exist in the index. A leading `a/` or `b/` prefix is
// stripped to match the paths stored in sense_files.
func newFilePath(header string) string {
	p := strings.TrimPrefix(header, "+++ ")
	if p == "/dev/null" {
		return ""
	}
	// Trailing tab-separated metadata (rare, e.g. timestamps) is dropped.
	if i := strings.IndexByte(p, '\t'); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimPrefix(p, "b/")
	p = strings.TrimPrefix(p, "a/")
	return p
}

// newSideRange parses the new-side span from a hunk header
// `@@ -oldStart,oldCount +newStart,newCount @@`. newCount defaults to 1 when
// omitted. A pure deletion (newCount 0) is widened to the two lines bracketing
// the gap so the enclosing symbol is still caught.
func newSideRange(header string) (LineRange, bool) {
	for _, field := range strings.Fields(header) {
		if !strings.HasPrefix(field, "+") {
			continue
		}
		spec := strings.TrimPrefix(field, "+")
		startStr, countStr, hasCount := strings.Cut(spec, ",")
		start, err := strconv.Atoi(startStr)
		if err != nil {
			return LineRange{}, false
		}
		count := 1
		if hasCount {
			count, err = strconv.Atoi(countStr)
			if err != nil {
				return LineRange{}, false
			}
		}
		if start < 1 {
			start = 1
		}
		if count == 0 {
			// Deletion: the change sits between new-side lines start and start+1.
			// Cover both so a removal inside a symbol still seeds that symbol.
			return LineRange{Start: start, End: start + 1}, true
		}
		return LineRange{Start: start, End: start + count - 1}, true
	}
	return LineRange{}, false
}

// SymbolsInChangedLines returns the ids of symbols whose line span overlaps a
// changed hunk range in their file. It is the line-granular seed selector for
// diff-blast: only symbols touched by the diff are returned, not every symbol
// in a touched file.
//
// The query chunks paths on SQLITE_MAX_VARIABLE_NUMBER headroom (500). Overlap
// is computed in Go against the per-file ranges; ids are returned ascending for
// deterministic downstream ordering.
func SymbolsInChangedLines(ctx context.Context, db *sql.DB, hunks map[string][]LineRange) ([]int64, error) {
	if len(hunks) == 0 {
		return nil, nil
	}
	paths := make([]string, 0, len(hunks))
	for p := range hunks {
		paths = append(paths, p)
	}
	sort.Strings(paths)

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
		q := `SELECT s.id, f.path, s.line_start, s.line_end
		      FROM sense_symbols s
		      JOIN sense_files   f ON f.id = s.file_id
		      WHERE f.path IN (` + placeholders + `)`
		args := make([]any, len(batch))
		for i, p := range batch {
			args[i] = p
		}
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("symbols in changed lines: %w", err)
		}
		for rows.Next() {
			var (
				id        int64
				path      string
				lineStart int
				lineEnd   int
			)
			if err := rows.Scan(&id, &path, &lineStart, &lineEnd); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan symbol row: %w", err)
			}
			if overlapsAny(lineStart, lineEnd, hunks[path]) {
				ids = append(ids, id)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// overlapsAny reports whether the inclusive symbol span [symStart, symEnd]
// intersects any of the changed line ranges. A symbol with an unknown end
// (symEnd < symStart, e.g. a zero line_end) is treated as the single line
// symStart so it can still match.
func overlapsAny(symStart, symEnd int, ranges []LineRange) bool {
	if symEnd < symStart {
		symEnd = symStart
	}
	for _, r := range ranges {
		if r.Start <= symEnd && symStart <= r.End {
			return true
		}
	}
	return false
}
