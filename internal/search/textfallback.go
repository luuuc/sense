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
// rootDir. For multi-word queries, files are ranked by how many query
// terms they contain so that files matching more terms appear first.
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

// searchSingle handles single-word queries with a direct rg invocation.
func (tf *TextFallback) searchSingle(ctx context.Context, word, rootDir string, paths []string, limit int) []TextResult {
	args := []string{
		"--no-heading", "--with-filename", "--line-number",
		"--color", "never",
		"--max-count", strconv.Itoa(textFallbackMaxPerFile),
		"--fixed-strings", "--ignore-case",
		"-e", word,
	}
	args = append(args, paths...)
	return tf.run(ctx, rootDir, args, limit)
}

// searchMulti ranks files by the number of distinct query terms they
// contain, then retrieves match lines from the top files.
func (tf *TextFallback) searchMulti(ctx context.Context, words []string, rootDir string, paths []string, limit int) []TextResult {
	fileCounts := map[string]int{}
	for _, w := range words {
		args := []string{
			"--files-with-matches",
			"--color", "never",
			"--fixed-strings", "--ignore-case",
			"-e", w,
		}
		args = append(args, paths...)
		out := tf.runRaw(ctx, rootDir, args)
		for _, f := range strings.Split(out, "\n") {
			if f != "" {
				fileCounts[f]++
			}
		}
	}

	if len(fileCounts) == 0 {
		return nil
	}

	type scored struct {
		file  string
		count int
	}
	ranked := make([]scored, 0, len(fileCounts))
	for f, c := range fileCounts {
		ranked = append(ranked, scored{f, c})
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
	for i := range topN {
		topFiles[i] = ranked[i].file
	}

	args := []string{
		"--no-heading", "--with-filename", "--line-number",
		"--color", "never",
		"--max-count", strconv.Itoa(textFallbackMaxPerFile),
		"--fixed-strings", "--ignore-case",
	}
	for _, w := range words {
		args = append(args, "-e", w)
	}
	args = append(args, topFiles...)
	return tf.run(ctx, rootDir, args, limit)
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
