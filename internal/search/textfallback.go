package search

import (
	"bytes"
	"context"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	textFallbackTimeout    = time.Second
	textFallbackMaxPerFile = 1
)

// TextResult is a single ripgrep match from the text fallback tier.
type TextResult struct {
	File  string
	Line  int
	Match string
}

// TextFallback shells out to ripgrep for text-level queries that the
// structural search engine can't answer. Degrades silently when rg is
// absent or fails.
type TextFallback struct {
	rgPath string
}

// NewTextFallback probes PATH for rg once. If absent, Available returns
// false and Search always returns nil.
func NewTextFallback() *TextFallback {
	p, err := exec.LookPath("rg")
	if err != nil {
		return &TextFallback{}
	}
	return &TextFallback{rgPath: p}
}

func (tf *TextFallback) Available() bool {
	return tf.rgPath != ""
}

// Search runs a scoped ripgrep against the given paths relative to
// rootDir. For multi-word queries, files are ranked by total match
// count so that files matching more terms appear first.
//
// NOTE: paths are passed as positional args to rg. On very large repos
// the argument list may approach ARG_MAX (typically 256 KB on Linux).
// If this becomes an issue, switch to passing paths via a temporary
// file with rg's --file flag or pipe them via stdin.
//
// Returns nil on any error — the fallback never surfaces errors.
func (tf *TextFallback) Search(ctx context.Context, query, rootDir string, paths []string, limit int) []TextResult {
	if !tf.Available() || len(paths) == 0 {
		return nil
	}

	words := strings.Fields(query)
	if len(words) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 10
	}

	ctx, cancel := context.WithTimeout(ctx, textFallbackTimeout)
	defer cancel()

	if len(words) == 1 {
		return tf.searchSingle(ctx, words[0], rootDir, paths, limit)
	}
	return tf.searchMulti(ctx, words, rootDir, paths, limit)
}

func (tf *TextFallback) searchSingle(ctx context.Context, word, rootDir string, paths []string, limit int) []TextResult {
	args := []string{
		"--no-heading", "--with-filename", "--line-number",
		"--color", "never",
		"--max-count", strconv.Itoa(textFallbackMaxPerFile),
		"--max-filesize", "100K",
		"--fixed-strings", "--ignore-case",
		"-e", word,
	}
	args = append(args, paths...)
	return tf.run(ctx, rootDir, args, limit)
}

// searchMulti ranks files by total match count across all query terms
// (2 rg invocations instead of N+1), then retrieves match lines from
// the top files.
func (tf *TextFallback) searchMulti(ctx context.Context, words []string, rootDir string, paths []string, limit int) []TextResult {
	args := []string{
		"--count",
		"--color", "never",
		"--max-filesize", "100K",
		"--fixed-strings", "--ignore-case",
	}
	for _, w := range words {
		args = append(args, "-e", w)
	}
	args = append(args, paths...)
	out := tf.runRaw(ctx, rootDir, args)

	type scored struct {
		file  string
		count int
	}
	var ranked []scored
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		idx := strings.LastIndexByte(line, ':')
		if idx < 0 {
			continue
		}
		count, err := strconv.Atoi(line[idx+1:])
		if err != nil || count == 0 {
			continue
		}
		ranked = append(ranked, scored{file: line[:idx], count: count})
	}

	if len(ranked) == 0 {
		return nil
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].count != ranked[j].count {
			return ranked[i].count > ranked[j].count
		}
		return ranked[i].file < ranked[j].file
	})

	topN := limit
	if topN > len(ranked) {
		topN = len(ranked)
	}
	topFiles := make([]string, topN)
	fileRank := make(map[string]int, topN)
	for i := range topN {
		topFiles[i] = ranked[i].file
		fileRank[ranked[i].file] = i
	}

	args = []string{
		"--no-heading", "--with-filename", "--line-number",
		"--color", "never",
		"--max-count", strconv.Itoa(textFallbackMaxPerFile),
		"--max-filesize", "100K",
		"--fixed-strings", "--ignore-case",
		"--sort", "none",
	}
	for _, w := range words {
		args = append(args, "-e", w)
	}
	args = append(args, topFiles...)
	results := tf.run(ctx, rootDir, args, limit)

	sort.Slice(results, func(i, j int) bool {
		ri, rj := fileRank[results[i].File], fileRank[results[j].File]
		if ri != rj {
			return ri < rj
		}
		return results[i].Line < results[j].Line
	})
	return results
}

func (tf *TextFallback) run(ctx context.Context, rootDir string, args []string, limit int) []TextResult {
	out := tf.runRaw(ctx, rootDir, args)
	return parseRgOutput(out, limit)
}

func (tf *TextFallback) runRaw(ctx context.Context, rootDir string, args []string) string {
	cmd := exec.CommandContext(ctx, tf.rgPath, args...)
	cmd.Dir = rootDir
	cmd.WaitDelay = 100 * time.Millisecond
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run()
	return stdout.String()
}

func parseRgOutput(output string, limit int) []TextResult {
	var results []TextResult
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		first := strings.IndexByte(line, ':')
		if first < 0 {
			continue
		}
		rest := line[first+1:]
		second := strings.IndexByte(rest, ':')
		if second < 0 {
			continue
		}
		lineNum, err := strconv.Atoi(rest[:second])
		if err != nil {
			continue
		}
		results = append(results, TextResult{
			File:  line[:first],
			Line:  lineNum,
			Match: strings.TrimSpace(rest[second+1:]),
		})
		if len(results) >= limit {
			break
		}
	}
	return results
}
