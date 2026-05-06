package search

import (
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	TextFallbackThreshold  = 3
	textFallbackTimeout    = time.Second
	textFallbackMaxPerFile = 3
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

// Search runs a scoped ripgrep against the given file paths relative to
// rootDir. The query is split into words; each word becomes an
// independent fixed-string pattern (OR matching). Returns nil on any
// error — the fallback never surfaces errors to callers.
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

	args := []string{
		"--no-heading",
		"--with-filename",
		"--line-number",
		"--color", "never",
		"--max-count", strconv.Itoa(textFallbackMaxPerFile),
		"--fixed-strings",
		"--ignore-case",
	}
	for _, w := range words {
		args = append(args, "-e", w)
	}
	args = append(args, paths...)

	cmd := exec.CommandContext(ctx, tf.rgPath, args...)
	cmd.Dir = rootDir
	cmd.WaitDelay = 100 * time.Millisecond
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	_ = cmd.Run()

	return parseRgOutput(stdout.String(), limit)
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
